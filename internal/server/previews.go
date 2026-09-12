package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/asiraky/omniplex/internal/preview"
	"github.com/asiraky/omniplex/internal/store"
)

// previewTargets is the set of sessions worth polling for running services.
//
// Only sessions someone is looking at, plus any that already have a preview.
// The first half keeps the cost proportional to attention rather than to how
// many sessions exist; the second means a preview open in a tab keeps being
// re-verified, so a service that stops is noticed rather than proxied to
// whatever later takes its port.
func (s *Server) previewTargets(ctx context.Context) []preview.Target {
	watched := s.attachedSessions()
	for _, set := range s.previews.All() {
		watched[set.SessionID] = struct{}{}
	}
	if len(watched) == 0 {
		return nil
	}

	metas, err := s.mgr.List(ctx)
	if err != nil {
		return nil
	}

	targets := make([]preview.Target, 0, len(watched))
	live := map[string]struct{}{}
	for _, meta := range metas {
		if _, ok := watched[meta.ID]; !ok {
			continue
		}
		live[meta.ID] = struct{}{}
		targets = append(targets, preview.Target{
			SessionID: meta.ID,
			Root:      meta.Cwd,
			Branch:    meta.Branch,
			Declared:  declaredServices(meta),
		})
	}

	// A session that has gone away entirely must lose its previews, or its
	// ids stay routable after the checkout they named is gone.
	for id := range watched {
		if _, ok := live[id]; !ok {
			s.previews.Forget(id)
		}
	}
	return targets
}

// declaredServices reads the services a project's provision hook named.
//
// The shape is the one the workspace lifecycle spec has documented since
// before any of this existed, so a project that already writes appUrl into
// its result gets a preview without being touched.
func declaredServices(meta store.SessionMeta) []preview.Found {
	if len(meta.ProvisionResult) == 0 {
		return nil
	}
	var result struct {
		Resources map[string]any `json:"resources"`
	}
	if err := json.Unmarshal(meta.ProvisionResult, &result); err != nil {
		return nil
	}
	return preview.FromResources(result.Resources)
}

// attachedSessions is every session some open connection is currently
// attached to.
func (s *Server) attachedSessions() map[string]struct{} {
	s.liveMu.Lock()
	conns := make([]*conn, 0, len(s.live))
	for c := range s.live {
		conns = append(conns, c)
	}
	s.liveMu.Unlock()

	out := map[string]struct{}{}
	for _, c := range conns {
		c.amu.Lock()
		for id := range c.attached {
			out[id] = struct{}{}
		}
		c.amu.Unlock()
	}
	return out
}

// handleOpenPreview sends a browser to a preview.
//
// It is a redirect rather than a link the client builds because the correct
// destination depends on how this request arrived — through the public domain,
// over the LAN, or on loopback — and on a ticket that must be minted fresh.
// A client cannot know either.
func (s *Server) handleOpenPreview(w http.ResponseWriter, r *http.Request) {
	if s.previewRouter == nil {
		http.Error(w, "previews are not configured", http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	p, ok := s.previews.Lookup(id)
	if !ok {
		http.Error(w, "no such preview", http.StatusNotFound)
		return
	}

	target := s.previewRouter.URLFor(r.Host, p)
	if s.previewRouter.Published(target) {
		device, _ := s.guard.Authorize(r)
		target = s.previewRouter.EnterURL(p.ID, s.previewAuth.Ticket(p.ID, device.ID))
	}

	// Never cached: the ticket in it is single-use, so a cached redirect is a
	// broken one.
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// previewsFor renders one connection's view of the current previews, with
// each URL resolved for the origin that connection is talking to.
func (s *Server) previewsFor(requestHost string) []previewSet {
	if s.previewRouter == nil {
		return nil
	}
	sets := s.previews.All()
	out := make([]previewSet, 0, len(sets))
	for _, set := range sets {
		items := make([]previewItem, 0, len(set.Previews))
		for _, p := range set.Previews {
			items = append(items, previewItem{
				Preview: p,
				URL:     s.previewRouter.URLFor(requestHost, p),
			})
		}
		out = append(out, previewSet{SessionID: set.SessionID, Previews: items})
	}
	return out
}
