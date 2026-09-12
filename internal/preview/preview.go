// Package preview publishes the web services a session's checkout is running.
//
// The problem it solves is a phone one. A dev server on port 5050 is trivially
// reachable from the machine it runs on and completely unreachable from the
// train, which is where half the work here happens. Rendering
// "http://localhost:5050" into the UI would be honest and useless. So a
// preview is not a string: it is a named service with an identity stable
// enough to route to, which the server can then publish under its own origin.
//
// Previews are live state, not history. They are pushed like the label set —
// re-sent whole when they change — rather than appended to the event log,
// because "a port was listening at 14:03" is a fact about the world now and
// not part of the conversation. Writing every start and stop of a dev server
// into a session's permanent transcript would be noise that outlives its
// usefulness by months.
package preview

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Preview is one addressable service belonging to a session.
type Preview struct {
	// ID is the routing identity: a single DNS label, because previews are
	// published as <id>.<preview domain> and a wildcard certificate covers
	// exactly one level. It is stable for the lifetime of a
	// (session, port) pair so a bookmark and an open WebSocket survive the
	// dev server being restarted.
	ID string `json:"id"`
	// Port is on loopback, always. A preview is something this machine can
	// reach at 127.0.0.1; nothing else is proxied.
	Port  int    `json:"port"`
	Label string `json:"label"`
	// Scheme is what the proxy must use to reach the service on loopback. It
	// never reaches the browser, which only ever sees the public https URL.
	Scheme string `json:"scheme"`
	// Source records how we found out, so the UI can distinguish a service
	// the project named from a port we noticed.
	Source Source `json:"source"`
}

// Set is one session's previews, ordered for display.
type Set struct {
	SessionID string    `json:"sessionId"`
	Previews  []Preview `json:"previews"`
}

// Registry holds the current previews for every live session and hands out
// their identities.
type Registry struct {
	mu sync.RWMutex
	// bySession is the current published set per session.
	bySession map[string][]Preview
	// ids remembers the identity assigned to a (session, port), so a service
	// that stops and starts comes back as itself.
	ids map[string]string
	// owner maps a preview id back to its session, which is what the proxy
	// needs to answer "may this request be served, and where does it go".
	owner map[string]Preview

	// excluded are ports this process is itself serving. Omniplex runs inside
	// a checkout when it is being developed on, so without this it detects
	// its own listener and offers the UI you are looking at as a preview of
	// itself — noise at best, and a proxy that loops back into this server at
	// worst.
	excluded map[int]bool

	subs   map[int]chan struct{}
	nextID int
}

func NewRegistry() *Registry {
	return &Registry{
		bySession: map[string][]Preview{},
		ids:       map[string]string{},
		owner:     map[string]Preview{},
		excluded:  map[int]bool{},
		subs:      map[int]chan struct{}{},
	}
}

// Exclude marks ports this process serves, so they are never offered as
// previews of the sessions whose checkouts they happen to run in.
func (r *Registry) Exclude(ports ...int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range ports {
		if p > 0 {
			r.excluded[p] = true
		}
	}
}

// Refresh re-detects the services of one session and republishes its set.
// It reports whether anything changed, and notifies subscribers if so.
//
// declared are services the project named — from the provision hook's
// resources or project config — and are trusted without probing.
func (r *Registry) Refresh(ctx context.Context, sessionID, root, branch string, declared []Found) bool {
	found := append([]Found{}, declared...)
	found = append(found, detectDocker(ctx, root)...)
	found = append(found, detectProcesses(ctx, root)...)
	found = dedupe(found)
	found = r.dropExcluded(found)
	found = keepServed(ctx, found)

	next := r.identify(sessionID, branch, found)

	r.mu.Lock()
	changed := !sameSet(r.bySession[sessionID], next)
	if changed {
		for _, p := range r.bySession[sessionID] {
			delete(r.owner, p.ID)
		}
		r.bySession[sessionID] = next
		for _, p := range next {
			r.owner[p.ID] = p
		}
	}
	r.mu.Unlock()

	if changed {
		r.notify()
	}
	return changed
}

// Lookup resolves a preview id to the service it names. The proxy calls this
// on every request, and an id it does not know is the difference between a
// reverse proxy and an open one.
func (r *Registry) Lookup(id string) (Preview, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.owner[id]
	return p, ok
}

// ForSession returns one session's current previews.
func (r *Registry) ForSession(sessionID string) []Preview {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Preview{}, r.bySession[sessionID]...)
}

// All returns every live preview, which is what a client receives on connect.
func (r *Registry) All() []Set {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sets := make([]Set, 0, len(r.bySession))
	for id, ps := range r.bySession {
		if len(ps) == 0 {
			continue
		}
		sets = append(sets, Set{SessionID: id, Previews: append([]Preview{}, ps...)})
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].SessionID < sets[j].SessionID })
	return sets
}

// Forget drops a session's previews, which retires their ids: a request to a
// retired preview is refused rather than proxied to whatever has since taken
// the port. Called when a session ends or its workspace is released.
func (r *Registry) Forget(sessionID string) {
	r.mu.Lock()
	had := len(r.bySession[sessionID]) > 0
	for _, p := range r.bySession[sessionID] {
		delete(r.owner, p.ID)
	}
	delete(r.bySession, sessionID)
	for key := range r.ids {
		if strings.HasPrefix(key, sessionID+"/") {
			delete(r.ids, key)
		}
	}
	r.mu.Unlock()

	if had {
		r.notify()
	}
}

// ---- identity ----

// identify assigns each found service its stable id, minting one on first
// sight. Ids are readable rather than opaque — `web-add-previews` beats
// `p-7f3a9c21` on a phone, where the hostname is the only clue about which
// worktree a tab belongs to — and readability costs nothing here, because the
// ticket handshake, not obscurity, is what keeps strangers out.
func (r *Registry) identify(sessionID, branch string, found []Found) []Preview {
	r.mu.Lock()
	defer r.mu.Unlock()

	taken := map[string]bool{}
	for id := range r.owner {
		taken[id] = true
	}
	// This session's own existing ids are not collisions: they are the ones
	// we are trying to reuse.
	for _, p := range r.bySession[sessionID] {
		delete(taken, p.ID)
	}

	out := make([]Preview, 0, len(found))
	for _, f := range found {
		key := sessionID + "/" + strconv.Itoa(f.Port)
		id, ok := r.ids[key]
		if !ok {
			id = uniqueLabel(hostLabel(f.Label, branch), taken)
			r.ids[key] = id
		}
		taken[id] = true
		out = append(out, Preview{ID: id, Port: f.Port, Label: f.Label, Scheme: f.Scheme, Source: f.Source})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// maxLabel is the DNS limit for one label. Nothing here should come close,
// but a branch name can be arbitrarily long and a hostname that silently
// fails to resolve is a bad way to find out.
const maxLabel = 63

// hostLabel builds a readable single DNS label from a service name and a
// branch. Both are slugged and joined with a dash rather than a dot: a
// wildcard certificate matches one label only, so `web.my-branch.example.com`
// would need a certificate nobody can issue.
func hostLabel(service, branch string) string {
	parts := make([]string, 0, 2)
	if s := slug(service); s != "" {
		parts = append(parts, s)
	}
	if b := slug(branch); b != "" {
		parts = append(parts, b)
	}
	if len(parts) == 0 {
		return "preview"
	}
	label := strings.Join(parts, "-")
	if len(label) > maxLabel {
		label = strings.Trim(label[:maxLabel], "-")
	}
	return label
}

// slug reduces a string to the characters a hostname may contain. Case is
// folded because DNS is case-insensitive and a mixed-case id would compare
// unequal to the host header it arrives in.
func slug(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// uniqueLabel appends a counter until the label is free. Collisions are
// expected: two worktrees of the same project running the same service
// produce the same name, and the branch is usually but not always enough to
// separate them.
func uniqueLabel(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if len(candidate) > maxLabel {
			base = strings.Trim(base[:maxLabel-len(strconv.Itoa(n))-1], "-")
			continue
		}
		if !taken[candidate] {
			return candidate
		}
	}
}

// ---- declared services ----

// FromResources reads services out of a provision hook's result.
//
// The shape is the one the workspace lifecycle spec already documents —
// {"appUrl": "http://…:8204", "database": "…"} — so a project that wrote a
// hook against that spec gets previews without touching it. Values that do
// not parse as an http URL are silently ignored: `database` and `redisDb` are
// resources too, and are not things to open in a browser.
func FromResources(resources map[string]any) []Found {
	var found []Found
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		raw, ok := resources[key].(string)
		if !ok {
			continue
		}
		port, scheme, ok := portOfURL(raw)
		if !ok {
			continue
		}
		found = append(found, Found{Port: port, Label: resourceLabel(key), Scheme: scheme, Source: SourceDeclared})
	}
	return found
}

// portOfURL pulls the port from a declared URL, defaulting by scheme when it
// is absent.
func portOfURL(raw string) (int, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return 0, "", false
	}
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		return port, u.Scheme, err == nil
	}
	if u.Scheme == "https" {
		return 443, "https", true
	}
	return 80, "http", true
}

// resourceLabel turns a resource key into a service name: `appUrl` is the app,
// `apiUrl` is the api. The suffix carries no information once the value has
// been recognised as a URL.
func resourceLabel(key string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(key, "URL"), "Url")
	if trimmed == "" {
		return key
	}
	return trimmed
}

// ---- helpers ----

// dedupe keeps the first mention of each port. Order matters at the call
// site: declared before Docker before process, so the most deliberate name
// for a port wins.
func dedupe(found []Found) []Found {
	seen := map[int]bool{}
	out := make([]Found, 0, len(found))
	for _, f := range found {
		if seen[f.Port] {
			continue
		}
		seen[f.Port] = true
		out = append(out, f)
	}
	return out
}

// dropExcluded removes this process's own ports.
func (r *Registry) dropExcluded(found []Found) []Found {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.excluded) == 0 {
		return found
	}
	out := found[:0]
	for _, f := range found {
		if !r.excluded[f.Port] {
			out = append(out, f)
		}
	}
	return out
}

func sameSet(a, b []Preview) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- subscriptions ----

// Subscribe returns a channel that ticks when any session's previews change.
// It mirrors the label set's arrangement: a bare signal, with the reader
// fetching current state, so a slow client cannot make the registry block.
func (r *Registry) Subscribe() (int, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := r.nextID
	ch := make(chan struct{}, 1)
	r.subs[id] = ch
	return id, ch
}

func (r *Registry) Unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, id)
}

func (r *Registry) notify() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
