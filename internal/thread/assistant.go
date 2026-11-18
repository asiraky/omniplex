package thread

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/ephemeral"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// AssistantOptions starts an assistant chat: a thread with no project, run on
// the user's assistant prompt rather than Omniplex's.
type AssistantOptions struct {
	Title       string
	Harness     string
	Instance    string
	Model       string
	Mode        string
	Effort      string
	ServiceTier string
	Pinned      bool
	// Private keeps the chat off disk: its rows in memory, its harness with
	// no transcript, its files in a scratch folder. Gone once closed, and on
	// restart.
	Private bool
}

// CreateAssistant persists and returns an attachable assistant chat at once,
// as CreateProject does, and starts its harness in the background. A normal
// chat works in <projects folder>/assistant, so what it makes is somewhere
// the user can find later; a private one in its own scratch folder.
func (m *Manager) CreateAssistant(ctx context.Context, o AssistantOptions) (*Actor, error) {
	// There is no project to inherit an account from, so a client that names
	// none gets the user's default one.
	cfg, _ := userconfig.Load()
	if o.Instance == "" && o.Harness == "" {
		o.Instance, o.Model = cfg.DefaultInstance, cmp.Or(o.Model, cfg.DefaultModel)
	}
	reg, err := m.resolveInstance(o.Instance, o.Harness)
	if err != nil {
		return nil, err
	}
	env, err := m.sessionEnv(reg.inst)
	if err != nil {
		return nil, err
	}
	if avail := reg.ad.Probe(ctx, env); !avail.OK() {
		return nil, fmt.Errorf("%s is not available: %s", reg.inst.DisplayName, avail.Reason)
	}

	id := uuid.NewString()
	var cwd string
	if o.Private {
		id = ephemeral.NewID()
		cwd = ephemeral.ThreadDir(id)
	} else {
		dir, err := cfg.ProjectsDirOrDefault()
		if err != nil {
			return nil, err
		}
		cwd = filepath.Join(dir, "assistant")
	}

	now := proto.NowMillis()
	meta := store.ThreadMeta{ID: id, Cwd: cwd, Harness: reg.inst.Driver, ProviderInstance: reg.inst.ID, CreatedAt: now, UpdatedAt: now, Title: normaliseTitle(o.Title), Phase: "creating", Model: o.Model, Mode: o.Mode, Effort: o.Effort, ServiceTier: o.ServiceTier, WorkspaceMode: "local", Pinned: o.Pinned}
	if err := m.store.CreateThread(ctx, meta); err != nil {
		return nil, err
	}
	a := StartPending(m.store, reg.ad, meta, env, m.logf)
	m.adopt(a)
	m.notifyList()
	go m.startAssistant(meta, a)
	return a, nil
}

// startAssistant makes the chat's folder and starts its harness there. There
// is no workspace to provision, so it goes straight to ready.
func (m *Manager) startAssistant(meta store.ThreadMeta, a *Actor) {
	ctx := context.Background()
	if err := os.MkdirAll(meta.Cwd, 0o700); err != nil {
		m.provisionFailed(meta.ID, a, fmt.Errorf("make the chat's folder: %w", err))
		return
	}
	result := provisionResult{Cwd: meta.Cwd}
	raw, _ := json.Marshal(result)
	if err := m.store.UpdateWorkspace(ctx, meta.ID, meta.Cwd, "", "provisioning", raw); err != nil {
		m.provisionFailed(meta.ID, a, err)
		return
	}
	a.Cwd = meta.Cwd
	ready := proto.WorkspaceReadyPayload{Cwd: meta.Cwd}
	if err := a.Activate(ctx, meta.Cwd, meta.Model, meta.Mode, meta.Effort, meta.ServiceTier, ready); err != nil {
		m.provisionFailed(meta.ID, a, fmt.Errorf("start harness: %w", err))
		return
	}
	m.notifyList()
}
