package thread

import (
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// testManager is NewManager with one account per adapter, its id the
// driver's id, the way most tests want a harness to be usable. There is no
// implicit account any more; tests about a driver with none use NewManager.
func testManager(st *store.Store, logf func(string, ...any), ads ...adapter.Adapter) *Manager {
	m := NewManager(st, logf, ads...)
	instances := make([]provider.Instance, 0, len(ads))
	for _, ad := range ads {
		instances = append(instances, provider.Instance{ID: ad.ID(), Driver: ad.ID(), DisplayName: ad.Meta().Name, Enabled: true})
	}
	m.ConfigureInstances(instances, nil)
	return m
}
