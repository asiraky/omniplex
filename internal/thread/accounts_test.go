package thread

import (
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// newManager is NewManager with one configured account per adapter, whose id
// is the adapter's: the shape every install has once each harness has an
// account. Tests about something other than accounts start from it.
func newManager(st *store.Store, logf func(string, ...any), ads ...adapter.Adapter) *Manager {
	m := NewManager(st, logf, ads...)
	m.ConfigureInstances(oneAccountEach(ads...), nil)
	return m
}

// oneAccountEach is one enabled instance per adapter, id and driver its id.
func oneAccountEach(ads ...adapter.Adapter) []provider.Instance {
	out := make([]provider.Instance, 0, len(ads))
	for _, ad := range ads {
		out = append(out, provider.Instance{ID: ad.ID(), Driver: ad.ID(), DisplayName: ad.Meta().Name, Enabled: true})
	}
	return out
}
