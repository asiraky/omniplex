package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// registered pairs a provider instance with the adapter that serves its
// driver. Adapters stay singletons in code — one codex adapter serves every
// codex instance — and ad is nil when the driver is unknown to this build,
// which presents as unavailable rather than failing anything.
type registered struct {
	inst provider.Instance
	ad   adapter.Adapter
}

// Manager owns the set of live actors and the provider-instance registry.
type Manager struct {
	store *store.Store
	// drivers maps adapter id to its singleton implementation.
	drivers     map[string]adapter.Adapter
	driverOrder []string
	// instances is keyed by instance id, never by driver: sessions and the
	// wire protocol route on instance ids.
	instances     map[string]registered
	instanceOrder []string
	secrets       *provider.SecretStore
	logf          func(string, ...any)

	mu     sync.RWMutex
	actors map[string]*Actor
	// lifecycle serialises resume, close, and delete for a session. Without it,
	// Close could remove an actor before it had marked the row closed and a
	// concurrent Get could spawn a second writer in that window.
	lifecycle sync.Mutex
	// leases serialises the check-then-claim of a checkout when a session is
	// created. Whether a directory is free is read from the session table, so
	// two concurrent creates would otherwise both see it free and both take
	// it.
	leases sync.Mutex

	// attachments is where a prompt's images are stored, so deleting a
	// session takes its pictures with it. Nil in tests and in a server built
	// without the feature.
	attachments AttachmentPurger

	probeMu sync.Mutex
	probes  map[string]probeResult

	// modelsMu guards the per-instance model cache. Asking a harness what it
	// offers costs a process start, so the answer is cached and refreshed off
	// the request path: a listing never waits on one.
	modelsMu   sync.Mutex
	models     map[string]modelResult
	refreshing map[string]bool
	// modelGen invalidates listings already in flight. A recheck that cleared
	// the cache must not be overwritten seconds later by an answer read before
	// the user installed whatever they were rechecking for.
	modelGen int

	// Broadcast of session-list changes, so presenters can refresh the sidebar.
	listMu  sync.Mutex
	listSub map[string]chan struct{}
	// Broadcast of harness changes — a model list that arrived after the
	// welcome frame — so a picker opened later shows the live catalogue
	// without the user reconnecting.
	harnessSub map[string]chan struct{}
	// Broadcast of label-definition changes, so every paired device sees a
	// created, renamed, reordered, or deleted label without reconnecting.
	labelSub map[string]chan struct{}
}

// probeTTL bounds how stale a readiness answer may be.
const probeTTL = 30 * time.Second

// modelTTL bounds how stale a model list may be. Catalogues change on the
// harness's release cadence, not by the minute, and a user who has just
// installed or upgraded one can force the question with a recheck.
const modelTTL = 30 * time.Minute

// modelRefreshTimeout bounds one background listing. The adapters apply their
// own deadlines; this is the backstop for one that does not return at all.
const modelRefreshTimeout = 90 * time.Second

type probeResult struct {
	result adapter.Availability
	at     time.Time
}

// modelResult is one instance's last model listing. A failed attempt is cached
// too, with no list: without that, every connection would retry a harness that
// cannot answer and pay the process start each time.
type modelResult struct {
	list []adapter.ModelMeta
	at   time.Time
}

func NewManager(st *store.Store, logf func(string, ...any), ads ...adapter.Adapter) *Manager {
	m := &Manager{
		store:      st,
		drivers:    map[string]adapter.Adapter{},
		instances:  map[string]registered{},
		logf:       logf,
		actors:     map[string]*Actor{},
		probes:     map[string]probeResult{},
		models:     map[string]modelResult{},
		refreshing: map[string]bool{},
		listSub:    map[string]chan struct{}{},
		harnessSub: map[string]chan struct{}{},
		labelSub:   map[string]chan struct{}{},
	}
	for _, ad := range ads {
		m.drivers[ad.ID()] = ad
		m.driverOrder = append(m.driverOrder, ad.ID())
		// The default instance: same id as the driver, ambient environment.
		// This is why one account per harness looks exactly like today.
		m.register(registered{inst: provider.Default(ad.ID(), ad.Meta().Name), ad: ad})
	}
	return m
}

// AttachmentPurger removes the images a session accumulated. Narrow on
// purpose: the manager has no business knowing anything else about them.
type AttachmentPurger interface {
	PurgeSession(sessionID string) error
}

// SetAttachments tells the manager where prompt images live. A session that is
// deleted takes them with it; without this they would outlive it on disk.
func (m *Manager) SetAttachments(p AttachmentPurger) { m.attachments = p }

// purgeAttachments is best effort: a picture left behind must never be the
// reason a session cannot be deleted.
func (m *Manager) purgeAttachments(id string) {
	if m.attachments == nil {
		return
	}
	if err := m.attachments.PurgeSession(id); err != nil {
		m.logf("purge attachments for %s: %v", id, err)
	}
}

// register adds or replaces one instance, preserving order on replacement so a
// configured entry that overrides a default keeps the default's position.
func (m *Manager) register(reg registered) {
	if _, exists := m.instances[reg.inst.ID]; !exists {
		m.instanceOrder = append(m.instanceOrder, reg.inst.ID)
	}
	m.instances[reg.inst.ID] = reg
}

// ConfigureInstances installs the operator's configured provider instances,
// on top of the defaults synthesised per adapter. An instance naming a driver
// this build does not have is registered anyway and presents as unavailable —
// a config written on another branch must never brick startup. Call this
// before serving; it is not synchronised against concurrent reads.
func (m *Manager) ConfigureInstances(instances []provider.Instance, secrets *provider.SecretStore) {
	m.secrets = secrets
	seen := map[string]bool{}
	for _, inst := range instances {
		// A configured entry may override the same driver's default instance,
		// but never an instance of a *different* driver: {"id":"codex",
		// "driver":"claude"} would silently delete the Codex default and break
		// every session on it. Duplicate ids within the config are a mistake
		// too; the first entry wins.
		if existing, ok := m.instances[inst.ID]; ok && (existing.inst.Driver != inst.Driver || seen[inst.ID]) {
			m.logf("provider instance %q collides with an existing %q instance; entry skipped", inst.ID, existing.inst.Driver)
			continue
		}
		seen[inst.ID] = true
		ad, known := m.drivers[inst.Driver]
		if !known {
			m.logf("provider instance %q names driver %q, which this build does not have; listing it as unavailable", inst.ID, inst.Driver)
		}
		m.register(registered{inst: inst, ad: ad})
	}
}

// envFor materialises an instance's credential overlay: plain values from the
// config, sensitive values from the secret store, at spawn (or probe) time.
// A missing secret is an error, never a silent fall-through to the ambient
// account.
func (m *Manager) envFor(inst provider.Instance) (map[string]string, error) {
	return inst.EnvOverlay(m.secrets)
}

// instanceFor resolves the instance a session runs under. A session created
// before instances existed has no ProviderInstance and resolves to the default
// instance for its harness — that is the whole migration. A session whose
// instance has since vanished, or whose instance now names a different driver,
// is refused legibly: resuming a work-account session against a personal
// account would silently produce a different agent identity.
func (m *Manager) instanceFor(meta store.SessionMeta) (registered, error) {
	id := meta.ProviderInstance
	if id == "" {
		id = meta.Harness
	}
	reg, ok := m.instances[id]
	if !ok {
		return registered{}, fmt.Errorf("unknown provider instance %q", id)
	}
	if reg.inst.Driver != meta.Harness {
		return registered{}, fmt.Errorf("provider instance %q now runs driver %q, but this session was created on %q", id, reg.inst.Driver, meta.Harness)
	}
	if reg.ad == nil {
		return registered{}, fmt.Errorf("no %q driver in this build", reg.inst.Driver)
	}
	return reg, nil
}

// cleanupAdapter is the lenient sibling of instanceFor, for workspace cleanup
// and pending restores: those paths never spawn a harness, so a session whose
// instance has been removed from the config must still be cleanable — anything
// stricter strands it in "cleaning" forever. The adapter may be nil.
func (m *Manager) cleanupAdapter(meta store.SessionMeta) (adapter.Adapter, map[string]string) {
	if reg, err := m.instanceFor(meta); err == nil {
		if env, envErr := m.envFor(reg.inst); envErr == nil {
			return reg.ad, env
		}
		return reg.ad, nil
	}
	return m.drivers[meta.Harness], nil
}

// resolveInstance turns a create request into an instance. The instance id
// wins when given; otherwise the harness id names its default instance.
func (m *Manager) resolveInstance(instanceID, harness string) (registered, error) {
	id := instanceID
	if id == "" {
		id = harness
	}
	reg, ok := m.instances[id]
	if !ok {
		return registered{}, fmt.Errorf("unknown harness %q", id)
	}
	if harness != "" && reg.inst.Driver != harness {
		return registered{}, fmt.Errorf("provider instance %q runs driver %q, not %q", id, reg.inst.Driver, harness)
	}
	if !reg.inst.Enabled {
		return registered{}, fmt.Errorf("provider instance %q is disabled", id)
	}
	if reg.ad == nil {
		return registered{}, fmt.Errorf("%s is not available: no %q driver in this build", reg.inst.DisplayName, reg.inst.Driver)
	}
	return reg, nil
}

// InstanceMeta is one provider instance as presented to a UI: the routing key,
// the driver that supplies its mark and accent, and per-instance health and
// models. Credential env never appears here — nothing about an instance's
// environment is client-bound.
type InstanceMeta struct {
	ID           string               `json:"id"`
	Driver       string               `json:"driver"`
	DisplayName  string               `json:"displayName"`
	Enabled      bool                 `json:"enabled"`
	Availability adapter.Availability `json:"availability"`
	Models       []adapter.ModelMeta  `json:"models"`
}

// Harness is one registered harness plus its current readiness, as presented
// to a UI. Everything here comes from the adapter; the core adds nothing and
// interprets nothing. The driver-level fields mirror the default instance so
// existing clients keep working; Instances carries every account.
type Harness struct {
	adapter.HarnessMeta
	Models          []adapter.ModelMeta          `json:"models"`
	PermissionModes []adapter.PermissionModeMeta `json:"permissionModes"`
	Availability    adapter.Availability         `json:"availability"`
	Instances       []InstanceMeta               `json:"instances"`
}

// Harnesses lists every registered harness, available or not, each with its
// provider instances. An unavailable harness is listed with the reason it
// cannot start, because a silently missing harness reads as a bug — and the
// same goes for an instance whose driver this build has never heard of.
func (m *Manager) Harnesses(ctx context.Context) []Harness {
	out := make([]Harness, 0, len(m.driverOrder))
	seenDrivers := map[string]bool{}
	for _, id := range m.driverOrder {
		ad := m.drivers[id]
		seenDrivers[id] = true
		h := Harness{
			HarnessMeta:     ad.Meta(),
			Models:          ad.Models(),
			PermissionModes: ad.PermissionModes(),
			Instances:       m.instancesOf(ctx, id),
		}
		// The driver-level availability and models mirror the default
		// instance, which is what today's UI renders; one instance being
		// unhealthy (or offering different models) must not speak for the
		// others, so each instance also reports its own.
		for _, inst := range h.Instances {
			if inst.ID == id {
				h.Availability = inst.Availability
				if len(inst.Models) > 0 {
					h.Models = inst.Models
				}
			}
		}
		out = append(out, h)
	}
	// Instances whose driver is unknown to this build still have to be
	// visible: they load, present as unavailable, and lose nothing.
	for _, id := range m.instanceOrder {
		reg := m.instances[id]
		if seenDrivers[reg.inst.Driver] {
			continue
		}
		seenDrivers[reg.inst.Driver] = true
		out = append(out, Harness{
			HarnessMeta:     adapter.HarnessMeta{ID: reg.inst.Driver, Name: reg.inst.Driver},
			Models:          []adapter.ModelMeta{},
			PermissionModes: []adapter.PermissionModeMeta{},
			Availability:    m.availability(ctx, reg),
			Instances:       m.instancesOf(ctx, reg.inst.Driver),
		})
	}
	return out
}

// instancesOf lists every instance of one driver, in registration order, with
// independent availability and models.
func (m *Manager) instancesOf(ctx context.Context, driver string) []InstanceMeta {
	var out []InstanceMeta
	for _, id := range m.instanceOrder {
		reg := m.instances[id]
		if reg.inst.Driver != driver {
			continue
		}
		im := InstanceMeta{
			ID:           reg.inst.ID,
			Driver:       reg.inst.Driver,
			DisplayName:  reg.inst.DisplayName,
			Enabled:      reg.inst.Enabled,
			Availability: m.availability(ctx, reg),
		}
		im.Models = m.modelsFor(reg, im.Availability)
		out = append(out, im)
	}
	return out
}

// availability caches a probe result briefly, per instance, so that listing
// harnesses on every connection does not re-run process lookups, while a user
// who installs a harness still sees it appear without restarting.
func (m *Manager) availability(ctx context.Context, reg registered) adapter.Availability {
	if reg.ad == nil {
		return adapter.Unavailable("This build has no " + strconv.Quote(reg.inst.Driver) + " driver. The configuration is kept and will work on a build that has it.")
	}
	if !reg.inst.Enabled {
		return adapter.Unavailable(reg.inst.DisplayName + " is disabled.")
	}

	m.probeMu.Lock()
	cached, ok := m.probes[reg.inst.ID]
	m.probeMu.Unlock()
	if ok && time.Since(cached.at) < probeTTL {
		return cached.result
	}

	env, err := m.envFor(reg.inst)
	if err != nil {
		// Refusing to guess: probing (or spawning) with the ambient credential
		// in place of a missing secret would report the wrong account's health.
		return adapter.Unavailable(err.Error())
	}
	result := reg.ad.Probe(ctx, env)

	m.probeMu.Lock()
	m.probes[reg.inst.ID] = probeResult{result: result, at: time.Now()}
	m.probeMu.Unlock()
	return result
}

// RecheckHarnesses drops cached probes so the next listing re-examines the
// system. A UI calls this after telling the user to install something.
func (m *Manager) RecheckHarnesses() {
	m.probeMu.Lock()
	m.probes = map[string]probeResult{}
	m.probeMu.Unlock()
	// Model lists go with them: a user who has just installed or upgraded a
	// harness is asking about its catalogue as much as its readiness.
	m.modelsMu.Lock()
	m.models = map[string]modelResult{}
	m.modelGen++
	m.modelsMu.Unlock()
	m.notifyList()
}

// expireModelsForTest ages every cached listing past its TTL. Tests use it to
// reach the refresh path without waiting out modelTTL.
func (m *Manager) expireModelsForTest() {
	m.modelsMu.Lock()
	defer m.modelsMu.Unlock()
	for id, result := range m.models {
		result.at = time.Now().Add(-2 * modelTTL)
		m.models[id] = result
	}
}

// modelsFor serves an instance's model list from cache, falling back to the
// adapter's built-in list, and refreshes in the background when the cache is
// missing or stale. It never blocks: asking a harness costs a process start,
// and this runs on every connection and every session-list push.
func (m *Manager) modelsFor(reg registered, avail adapter.Availability) []adapter.ModelMeta {
	if reg.ad == nil {
		return nil
	}
	m.modelsMu.Lock()
	cached, ok := m.models[reg.inst.ID]
	stale := !ok || time.Since(cached.at) > modelTTL
	// Only one refresh per instance is in flight; the rest of the callers keep
	// serving what is cached.
	start := stale && !m.refreshing[reg.inst.ID] && avail.OK() && reg.inst.Enabled
	gen := m.modelGen
	if start {
		m.refreshing[reg.inst.ID] = true
	}
	m.modelsMu.Unlock()

	if start {
		go m.refreshModels(reg, gen)
	}
	if len(cached.list) > 0 {
		return cached.list
	}
	// Nothing learned yet — or the harness could not be asked. The adapter's
	// own list is small and possibly dated, which is better than a picker with
	// nothing in it.
	return reg.ad.Models()
}

// refreshModels asks one instance's harness for its catalogue and caches the
// answer, telling connected clients when it changed. A failure is cached too,
// so a harness that cannot answer is not re-asked on every listing.
func (m *Manager) refreshModels(reg registered, gen int) {
	ctx, cancel := context.WithTimeout(context.Background(), modelRefreshTimeout)
	defer cancel()

	var list []adapter.ModelMeta
	env, err := m.envFor(reg.inst)
	if err == nil {
		list, err = reg.ad.ListModels(ctx, env)
	}
	if err != nil {
		m.logf("models: %s: %v", reg.inst.ID, err)
	}

	m.modelsMu.Lock()
	defer m.modelsMu.Unlock()
	delete(m.refreshing, reg.inst.ID)
	// A recheck since this listing started means the answer predates whatever
	// the user just changed. Drop it and ask again straight away, so that one
	// click of "Check again" is enough: waiting for the next listing would
	// leave the user staring at the fallback with no way to force the issue.
	if gen != m.modelGen {
		if !m.refreshing[reg.inst.ID] {
			m.refreshing[reg.inst.ID] = true
			current := m.modelGen
			go m.refreshModels(reg, current)
		}
		return
	}
	previous := m.models[reg.inst.ID]
	if err != nil {
		// A harness that could not answer this time has not withdrawn what it
		// said last time. Keeping the last good list means a blip does not
		// silently downgrade a live catalogue to the built-in fallback; only
		// the timestamp moves, so the retry waits out the TTL.
		m.models[reg.inst.ID] = modelResult{list: previous.list, at: time.Now()}
		return
	}
	m.models[reg.inst.ID] = modelResult{list: list, at: time.Now()}
	if !sameModels(previous.list, list) && len(list) > 0 {
		m.notifyHarnesses()
	}
}

func sameModels(a, b []adapter.ModelMeta) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Create starts a new session on the named harness, under the named provider
// instance (empty means the harness's default instance).
func (m *Manager) Create(ctx context.Context, harness, instance, cwd, model, mode string) (*Actor, error) {
	reg, err := m.resolveInstance(instance, harness)
	if err != nil {
		return nil, err
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return nil, err
	}

	// Probe fresh here rather than trusting the cache: the answer decides
	// whether we are about to spawn a process, and it may have changed.
	if avail := reg.ad.Probe(ctx, env); !avail.OK() {
		m.probeMu.Lock()
		m.probes[reg.inst.ID] = probeResult{result: avail, at: time.Now()}
		m.probeMu.Unlock()
		return nil, fmt.Errorf("%s is not available: %s", reg.inst.DisplayName, avail.Reason)
	}

	meta := store.SessionMeta{
		ID:               uuid.NewString(),
		Cwd:              cwd,
		Harness:          reg.inst.Driver,
		ProviderInstance: reg.inst.ID,
		CreatedAt:        proto.NowMillis(),
		UpdatedAt:        proto.NowMillis(),
		Phase:            "idle",
	}
	if err := m.store.CreateSession(ctx, meta); err != nil {
		return nil, err
	}

	a, err := Start(ctx, m.store, reg.ad, meta, model, mode, env, m.logf)
	if err != nil {
		_ = m.store.DeleteSession(ctx, meta.ID)
		return nil, err
	}

	m.adopt(a)
	m.notifyList()

	return a, nil
}

type CreateProjectOptions struct {
	ProjectID string
	Harness   string
	// Instance names the provider instance; empty means the harness's default.
	Instance  string
	Model     string
	Mode      string
	Effort    string
	Branch    string
	Workspace string
	// BaseRef is the ref a new worktree is branched from, chosen per session.
	// Empty defers to the project's default base branch.
	BaseRef string
	// WorkspacePath attaches the session to a checkout that already exists
	// instead of provisioning one. It is the minority case, so it overrides
	// Workspace rather than being another value of it.
	WorkspacePath string
}

// CreateProject persists and returns an attachable session immediately. Its
// workspace is prepared in the background; no harness exists before readiness.
func (m *Manager) CreateProject(ctx context.Context, o CreateProjectOptions) (*Actor, error) {
	p, err := m.store.Project(ctx, o.ProjectID)
	if err != nil {
		return nil, err
	}
	if o.Harness == "" {
		o.Harness = p.Config.Defaults.Harness
	}
	if o.Model == "" {
		o.Model = p.Config.Defaults.Model
	}
	if o.Mode == "" {
		o.Mode = p.Config.Defaults.Mode
	}
	if o.Effort == "" {
		o.Effort = p.Config.Defaults.Effort
	}
	if o.Workspace == "" {
		o.Workspace = p.Config.Defaults.Workspace
	}
	if o.Workspace == "" {
		o.Workspace = "local"
	}
	// Attaching is what produces a borrowed lease; it is not something a
	// client may ask for by name. Anything else unrecognised would fall
	// through every guard below and land a harness in the project root with
	// the hooks still attached, so it is refused rather than interpreted.
	if o.Workspace != "local" && o.Workspace != "managed" {
		return nil, fmt.Errorf("unknown workspace mode %q", o.Workspace)
	}
	reg, err := m.resolveInstance(o.Instance, o.Harness)
	if err != nil {
		return nil, err
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return nil, err
	}
	if avail := reg.ad.Probe(ctx, env); !avail.OK() {
		return nil, fmt.Errorf("%s is not available: %s", reg.inst.DisplayName, avail.Reason)
	}

	// Two websocket commands are two goroutines, and creating a session reads
	// the workspace list before writing a row that changes it. Serialising the
	// pair keeps the busy flags a session is created against consistent with
	// what was on disk a moment earlier.
	m.leases.Lock()
	defer m.leases.Unlock()

	cwd := p.Root
	switch {
	case strings.TrimSpace(o.WorkspacePath) != "":
		w, resolveErr := m.ResolveWorkspace(ctx, p.ID, o.WorkspacePath)
		if resolveErr != nil {
			return nil, resolveErr
		}
		// Sharing a checkout is fine; moving into one that is being deleted is
		// not. Teardown runs outside this lock, so without this a session
		// could attach to a directory that is seconds from being removed.
		if holder, ok := m.cleaningSessionIn(ctx, w.Path); ok {
			return nil, fmt.Errorf("%s is being cleaned up by %q", filepath.Base(w.Path), holder)
		}
		// omniplex did not create this checkout, so it must never destroy it: the
		// borrowed mode skips both hooks and the managed worktree teardown.
		cwd, o.Workspace = w.Path, "borrowed"
		if o.Branch == "" {
			o.Branch = w.Branch
		}
	case o.Workspace == "local":
		// The main checkout, which omniplex did not create and must not clean up.
		// Resolving it as a workspace still reuses the attach guard — the path
		// has to be a checkout Git reports for this project — but a checkout
		// another session already holds is no longer refused. The presenter
		// warns; the user decides.
		w, resolveErr := m.ResolveWorkspace(ctx, p.ID, p.Root)
		if resolveErr != nil {
			return nil, resolveErr
		}
		// No branch is created: the session is on whatever the checkout is
		// already on, and reporting that is more honest than reporting a name
		// nothing acted upon.
		cwd, o.Branch = w.Path, w.Branch
	}

	// Hooks belong to provisioning. A local session runs in the directory the
	// user already works in and a borrowed one in a checkout somebody else
	// made; neither has anything to prepare, so neither resolves a hook —
	// which also means a hook that has since been deleted cannot block the
	// one mode that would never have run it.
	var provision, deprovision string
	if o.Workspace == "managed" {
		if provision, err = project.ResolveHook(p.Root, p.Config.Workspace.Provision); err != nil && p.Config.Workspace.Provision != "" {
			return nil, fmt.Errorf("provision hook: %w", err)
		}
		if deprovision, err = project.ResolveHook(p.Root, p.Config.Workspace.Deprovision); err != nil && p.Config.Workspace.Deprovision != "" {
			return nil, fmt.Errorf("deprovision hook: %w", err)
		}
	}

	meta := store.SessionMeta{ID: uuid.NewString(), Cwd: cwd, Harness: reg.inst.Driver, ProviderInstance: reg.inst.ID, CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis(), Phase: "creating", ProjectID: p.ID, Branch: o.Branch, Model: o.Model, Mode: o.Mode, Effort: o.Effort, WorkspaceMode: o.Workspace, BaseRef: o.BaseRef, ProvisionScript: relHook(p.Root, provision), DeprovisionScript: relHook(p.Root, deprovision)}
	if err := m.store.CreateSession(ctx, meta); err != nil {
		return nil, err
	}
	a := StartPending(m.store, reg.ad, meta, env, m.logf)
	m.adopt(a)
	m.notifyList()
	go m.provision(meta, p, a)
	return a, nil
}

func relHook(root, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return rel
}

func (m *Manager) AddProject(ctx context.Context, root string) (project.Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return project.Project{}, err
	}
	cfg, err := project.Load(abs)
	if err != nil {
		return project.Project{}, err
	}
	now := proto.NowMillis()
	p := project.Project{ID: uuid.NewString(), Root: abs, Config: cfg, CreatedAt: now, UpdatedAt: now}
	if err := m.store.PutProject(ctx, p); err != nil {
		return p, err
	}
	m.notifyList()
	return p, nil
}

func (m *Manager) SaveProject(ctx context.Context, id string, cfg project.Config) (project.Project, error) {
	p, err := m.store.Project(ctx, id)
	if err != nil {
		return p, err
	}
	cfg, err = project.Save(p.Root, cfg)
	if err != nil {
		return p, err
	}
	p.Config = cfg
	p.UpdatedAt = proto.NowMillis()
	if err := m.store.PutProject(ctx, p); err != nil {
		return p, err
	}
	m.notifyList()
	return p, nil
}

// ReloadProjects re-reads each project's on-disk config, which is the source
// of truth; the copy in the database is a cache that a pull can make stale. A
// project whose file has gone is left as it is: a missing file means the
// checkout moved or is mid-checkout, not that its settings were cleared.
func (m *Manager) ReloadProjects(ctx context.Context) error {
	projects, err := m.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, p := range projects {
		if _, statErr := os.Stat(filepath.Join(p.Root, project.ConfigPath)); statErr != nil {
			continue
		}
		cfg, loadErr := project.Load(p.Root)
		if loadErr != nil {
			m.logf("project %s: %v", p.Config.Name, loadErr)
			continue
		}
		if reflect.DeepEqual(cfg, p.Config) {
			continue
		}
		p.Config, p.UpdatedAt = cfg, proto.NowMillis()
		if err := m.store.PutProject(ctx, p); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		m.notifyList()
	}
	return nil
}

func (m *Manager) Projects(ctx context.Context) ([]project.Project, error) {
	return m.store.ListProjects(ctx)
}

func (m *Manager) RetryProvision(ctx context.Context, id string) error {
	a, ok := m.Peek(id)
	if !ok {
		var err error
		a, err = m.Get(ctx, id)
		if err != nil {
			return err
		}
	}
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if meta.Phase != "provision_failed" {
		return fmt.Errorf("session is not awaiting provisioning")
	}
	p, err := m.store.Project(ctx, meta.ProjectID)
	if err != nil {
		return err
	}
	if err := m.store.SetPhase(ctx, id, "provisioning"); err != nil {
		return err
	}
	go m.provision(meta, p, a)
	return nil
}

func (m *Manager) Cleanup(ctx context.Context, id string) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if meta.ProjectID == "" {
		return m.closeLocked(ctx, id, "closed by user")
	}
	if meta.Phase == "cleaning" {
		return errors.New("workspace cleanup is already running")
	}
	if meta.Phase == "closed" {
		return nil
	}
	p, err := m.store.Project(ctx, meta.ProjectID)
	if err != nil {
		return err
	}
	// Stop the harness first. A fresh, process-less actor keeps cleanup output attachable.
	if live, ok := m.Peek(id); ok {
		live.Dispose("cleaning workspace")
	}
	_ = m.store.SetPhase(ctx, id, "cleaning")
	ad, env := m.cleanupAdapter(meta)
	a, err := RestorePending(ctx, m.store, ad, meta, env, m.logf)
	if err != nil {
		return err
	}
	m.adopt(a)
	// "Clean up workspace" asks for exactly that, so a lease omniplex provisioned is
	// released; a checkout it merely borrowed still is not omniplex's to delete.
	go m.cleanup(meta, p, a, false, meta.WorkspaceMode == "managed")
	return nil
}

// Get returns an actor with its harness active. Commands use this path; a
// process restored for viewing is activated here on first real use.
func (m *Manager) Get(ctx context.Context, id string) (*Actor, error) {
	return m.get(ctx, id, true)
}

// View returns the attach/state surface without starting a provider process.
// Opening a transcript is a read and should stay cheap after a server restart.
func (m *Manager) View(ctx context.Context, id string) (*Actor, error) {
	return m.get(ctx, id, false)
}

func (m *Manager) get(ctx context.Context, id string, activate bool) (*Actor, error) {
	m.lifecycle.Lock()
	locked := true
	defer func() {
		if locked {
			m.lifecycle.Unlock()
		}
	}()
	release := func() {
		m.lifecycle.Unlock()
		locked = false
	}

	m.mu.RLock()
	a, ok := m.actors[id]
	m.mu.RUnlock()
	if ok {
		meta, err := m.store.Session(ctx, id)
		if err != nil {
			return nil, err
		}
		release()
		return m.finishGet(ctx, a, meta, activate)
	}

	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if a, ok := m.actors[id]; ok { // another caller won the race
		m.mu.Unlock()
		return a, nil
	}
	m.mu.Unlock()

	if meta.Phase == "closed" {
		a, err = RestoreClosed(ctx, m.store, meta, m.logf)
	} else if meta.Phase == "creating" || meta.Phase == "provisioning" || meta.Phase == "provision_failed" || meta.Phase == "cleaning" || meta.Phase == "cleanup_failed" {
		// Lenient on purpose: a pending session must stay attachable (and
		// cleanable) even if its instance has gone from the config. Activation
		// is where a missing adapter is refused.
		ad, env := m.cleanupAdapter(meta)
		a, err = RestorePending(ctx, m.store, ad, meta, env, m.logf)
		if err == nil && (meta.Phase == "creating" || meta.Phase == "provisioning") {
			_ = m.store.SetPhase(ctx, meta.ID, "provision_failed")
			_ = a.Emit(ctx, proto.Emit(proto.WorkspaceFailed, proto.WorkspaceFailedPayload{Hook: "provision", Error: "server restarted while provisioning; retry is safe"}))
		}
		if err == nil && meta.Phase == "cleaning" {
			_ = m.store.SetPhase(ctx, meta.ID, "cleanup_failed")
			_ = a.Emit(ctx, proto.Emit(proto.WorkspaceCleanupFailed, proto.WorkspaceFailedPayload{Hook: "deprovision", Error: "server restarted while cleaning up; retry is safe"}))
		}
		if err == nil {
			m.notifyList()
		}
	} else {
		reg, regErr := m.instanceFor(meta)
		if regErr != nil {
			return nil, regErr
		}
		// Resume reuses the instance the session was created under: its env is
		// re-materialised, so the same account backs the same conversation. A
		// missing secret refuses the resume rather than falling through to the
		// ambient account.
		env, envErr := m.envFor(reg.inst)
		if envErr != nil {
			return nil, envErr
		}
		a, err = RestoreIdle(ctx, m.store, reg.ad, meta, env, m.logf)
	}
	if err != nil {
		return nil, err
	}
	m.adopt(a)
	release()
	return m.finishGet(ctx, a, meta, activate)
}

func (m *Manager) finishGet(ctx context.Context, a *Actor, meta store.SessionMeta, activate bool) (*Actor, error) {
	if !activate || (meta.Phase != "idle" && meta.Phase != "turn") {
		return a, nil
	}
	// Process startup is intentionally outside the global lifecycle lock. The
	// actor serializes duplicate activations for this session; unrelated reads
	// must remain instant while a provider takes seconds to start.
	if err := a.ActivateResume(ctx); err != nil {
		return nil, err
	}
	if meta.Phase == "turn" {
		go m.recoverActor(a)
	}
	return a, nil
}

func (m *Manager) recoverActor(a *Actor) {
	if err := a.Recover(context.Background()); err != nil {
		m.logf("continue interrupted turn on %s: %v", a.ID, err)
	}
}

// Peek returns the live actor if one exists, without starting a harness.
func (m *Manager) Peek(id string) (*Actor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.actors[id]
	return a, ok
}

// adopt registers a live actor and arranges for it to be forgotten when its
// harness exits, so the next attach resumes the session cleanly.
func (m *Manager) adopt(a *Actor) {
	m.mu.Lock()
	m.actors[a.ID] = a
	m.mu.Unlock()
	a.mu.Lock()
	a.onExit = m.forgetFn(a.ID, a)
	a.onPhase = m.notifyList
	a.mu.Unlock()
	select {
	case <-a.quit:
		m.mu.Lock()
		if m.actors[a.ID] == a {
			delete(m.actors, a.ID)
		}
		m.mu.Unlock()
	default:
	}
}

func (m *Manager) forgetFn(id string, a *Actor) func() {
	return func() {
		m.mu.Lock()
		if cur, ok := m.actors[id]; ok && cur == a {
			delete(m.actors, id)
		}
		m.mu.Unlock()
		m.notifyList()
	}
}

func (m *Manager) List(ctx context.Context) ([]store.SessionMeta, error) {
	metas, err := m.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	// Attention is derived, never stored: a live actor answers from its
	// projection (which knows about pending permissions and questions), and a
	// session with no actor answers from its phase — a dead actor cancelled
	// its pending requests on the way down, so the phase is the whole story.
	m.mu.Lock()
	for i := range metas {
		if a, ok := m.actors[metas[i].ID]; ok {
			metas[i].Attention = a.Attention()
		} else {
			metas[i].Attention = projection.AttentionForPhase(metas[i].Phase)
		}
	}
	m.mu.Unlock()
	return metas, nil
}

// Close disposes a session's harness. The log is untouched.
func (m *Manager) Close(ctx context.Context, id, reason string) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.closeLocked(ctx, id, reason)
}

func (m *Manager) closeLocked(ctx context.Context, id, reason string) error {
	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	a, ok := m.actors[id]
	delete(m.actors, id)
	m.mu.Unlock()
	if ok {
		if meta.Phase == "closed" {
			a.Dispose(reason)
		} else {
			a.Close(reason)
		}
	} else {
		if meta.Phase != "closed" {
			if _, err := m.store.Append(ctx, id, proto.Emit(proto.SessionClosed, proto.SessionClosedPayload{Reason: reason})); err != nil {
				return err
			}
			if err := m.store.SetPhase(ctx, id, "closed"); err != nil {
				return err
			}
		}
		// No actor means no checkpointer to drop this session's snapshots.
		purgeCheckpoints(ctx, meta.Cwd, id, m.logf)
	}
	m.notifyList()
	return nil
}

// Delete removes a session and its log entirely. removeWorktree is the user's
// answer to the checkbox in the confirmation dialog: without it nothing on disk
// is touched, whatever mode the session ran in.
func (m *Manager) Delete(ctx context.Context, id string, removeWorktree bool) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if removeWorktree {
		if meta.WorkspaceMode == "local" {
			return errors.New("the main checkout is not omniplex's to remove")
		}
		// Sessions may share a checkout, so the last one out is the only one
		// that may take it with them. The dialog hides the checkbox in this
		// case; a stale client could still ask.
		if holder, shared := m.otherSessionIn(ctx, id, meta.Cwd); shared {
			return fmt.Errorf("%s is still used by %q", filepath.Base(meta.Cwd), holder)
		}
	}
	if meta.Phase == "closed" {
		// A closed session has no harness and no actor to narrate teardown,
		// but its checkout is still on disk and the user may still have asked
		// for it to go. Removing it here rather than falling through keeps the
		// checkbox meaning the same thing whatever phase the row is in.
		if removeWorktree && meta.WorkspaceMode != "local" {
			p, projectErr := m.store.Project(ctx, meta.ProjectID)
			if projectErr != nil {
				return projectErr
			}
			if err := m.removeGitWorktree(ctx, meta, p, nil, true); err != nil {
				return err
			}
		}
		if err := m.store.DeleteSession(ctx, id); err != nil {
			return err
		}
		m.purgeAttachments(id)
		m.notifyList()
		return nil
	}
	if meta.ProjectID == "" {
		if err := m.closeLocked(ctx, id, "deleted"); err != nil {
			return err
		}
		if err := m.store.DeleteSession(ctx, id); err != nil {
			return err
		}
		m.purgeAttachments(id)
		m.notifyList()
		return nil
	}
	if meta.Phase == "cleaning" {
		return errors.New("workspace cleanup is already running")
	}
	p, err := m.store.Project(ctx, meta.ProjectID)
	if err != nil {
		return err
	}
	if live, ok := m.Peek(id); ok {
		live.Dispose("deleting session")
	}
	// Disposing keeps the snapshots, because a disposed session is normally
	// resumed. This one is not coming back.
	purgeCheckpoints(ctx, meta.Cwd, id, m.logf)
	if err := m.store.SetPhase(ctx, id, "cleaning"); err != nil {
		return err
	}
	ad, env := m.cleanupAdapter(meta)
	a, err := RestorePending(ctx, m.store, ad, meta, env, m.logf)
	if err != nil {
		return err
	}
	m.adopt(a)
	go m.cleanup(meta, p, a, true, removeWorktree)
	return nil
}

// otherSessionIn reports a session other than id whose checkout is the same
// directory. It is the "somebody else is still in here" test that guards
// worktree removal now that sharing is allowed. A closed session counts: it
// still names that path, its transcript still refers to it, and it can be
// resumed — "the last session omniplex knows of" is the question, not "the last one
// still running".
func (m *Manager) otherSessionIn(ctx context.Context, id, cwd string) (string, bool) {
	if strings.TrimSpace(cwd) == "" {
		return "", false
	}
	target := canonicalPath(cwd)
	sessions, err := m.store.ListSessions(ctx)
	if err != nil {
		// An unreadable list is not permission to delete somebody's checkout.
		return "another session", true
	}
	for _, s := range sessions {
		if s.ID == id || s.Cwd == "" {
			continue
		}
		if canonicalPath(s.Cwd) != target {
			continue
		}
		if s.Title == "" {
			return "untitled session", true
		}
		return s.Title, true
	}
	return "", false
}

// cleaningSessionIn reports a session that is tearing down the given checkout.
func (m *Manager) cleaningSessionIn(ctx context.Context, cwd string) (string, bool) {
	target := canonicalPath(cwd)
	sessions, err := m.store.ListSessions(ctx)
	if err != nil {
		return "", false
	}
	for _, s := range sessions {
		if s.Phase != "cleaning" || s.Cwd == "" || canonicalPath(s.Cwd) != target {
			continue
		}
		if s.Title == "" {
			return "untitled session", true
		}
		return s.Title, true
	}
	return "", false
}

// ForceDelete skips the project deprovision hook after it has failed. It only
// removes the exact recorded Git worktree, prunes Git metadata, then purges the
// transcript. The project root is never removed.
func (m *Manager) ForceDelete(ctx context.Context, id string) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	meta, err := m.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if meta.Phase != "cleanup_failed" {
		return errors.New("force delete is only available after teardown fails")
	}
	p, err := m.store.Project(ctx, meta.ProjectID)
	if err != nil {
		return err
	}
	// Only a managed worktree is omniplex's to destroy. A borrowed checkout belongs
	// to whoever made it and a local session is the user's own working
	// directory; forcing either session away must not touch their files.
	if meta.WorkspaceMode == "managed" {
		if holder, shared := m.otherSessionIn(ctx, id, meta.Cwd); shared {
			return fmt.Errorf("%s is still used by %q", filepath.Base(meta.Cwd), holder)
		}
		if err := m.removeGitWorktree(ctx, meta, p, nil, true); err != nil {
			return err
		}
	}
	if live, ok := m.Peek(id); ok {
		live.Dispose("force deleted")
	}
	if err := m.store.DeleteSession(ctx, id); err != nil {
		return err
	}
	m.purgeAttachments(id)
	m.notifyList()
	return nil
}

// Shutdown tears down every harness process. Sessions are left resumable: the
// log is the session, and a restart reattaches to it.
func (m *Manager) Shutdown() {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	actors := m.actors
	m.actors = map[string]*Actor{}
	m.mu.Unlock()
	for _, a := range actors {
		a.Dispose("server shutdown")
	}
}

// ---- session-list change notifications ----

func (m *Manager) SubscribeList() (string, chan struct{}) {
	id := uuid.NewString()
	ch := make(chan struct{}, 1)
	m.listMu.Lock()
	m.listSub[id] = ch
	m.listMu.Unlock()
	return id, ch
}

func (m *Manager) UnsubscribeList(id string) {
	m.listMu.Lock()
	delete(m.listSub, id)
	m.listMu.Unlock()
}

// SubscribeHarnesses registers for harness changes: a background model
// listing landing, and nothing else today.
func (m *Manager) SubscribeHarnesses() (string, chan struct{}) {
	id := uuid.NewString()
	ch := make(chan struct{}, 1)
	m.listMu.Lock()
	m.harnessSub[id] = ch
	m.listMu.Unlock()
	return id, ch
}

func (m *Manager) UnsubscribeHarnesses(id string) {
	m.listMu.Lock()
	delete(m.harnessSub, id)
	m.listMu.Unlock()
}

func (m *Manager) notifyHarnesses() {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	for _, ch := range m.harnessSub {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SubscribeLabels registers for label-definition changes: create, save,
// delete. Assignments travel on the session list instead.
func (m *Manager) SubscribeLabels() (string, chan struct{}) {
	id := uuid.NewString()
	ch := make(chan struct{}, 1)
	m.listMu.Lock()
	m.labelSub[id] = ch
	m.listMu.Unlock()
	return id, ch
}

func (m *Manager) UnsubscribeLabels(id string) {
	m.listMu.Lock()
	delete(m.labelSub, id)
	m.listMu.Unlock()
}

func (m *Manager) notifyLabels() {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	for _, ch := range m.labelSub {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// NotifyList wakes every list subscriber; used after a title or phase change.
func (m *Manager) NotifyList() { m.notifyList() }

func (m *Manager) notifyList() {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	for _, ch := range m.listSub {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
