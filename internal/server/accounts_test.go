package server

import (
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// newManager is thread.NewManager with one configured account per adapter,
// whose id is the adapter's: the shape every install has once each harness
// has an account.
func newManager(st *store.Store, logf func(string, ...any), ads ...adapter.Adapter) *thread.Manager {
	m := thread.NewManager(st, logf, ads...)
	instances := make([]provider.Instance, 0, len(ads))
	for _, ad := range ads {
		instances = append(instances, provider.Instance{ID: ad.ID(), Driver: ad.ID(), DisplayName: ad.Meta().Name, Enabled: true})
	}
	m.ConfigureInstances(instances, nil)
	return m
}
