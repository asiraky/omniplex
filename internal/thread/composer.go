package thread

import (
	"context"
	"fmt"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

// DraftComposerItems is the completion catalogue for a thread that has not
// been created: what the chosen provider would offer in the chosen folder,
// answered without starting a harness. A provider that cannot say offers
// nothing, which is what the composer showed before it asked.
func (m *Manager) DraftComposerItems(ctx context.Context, harness, instance, projectID, folderID string) ([]adapter.ComposerItem, error) {
	reg, err := m.resolveInstance(instance, harness)
	if err != nil {
		return nil, err
	}
	cataloguer, ok := reg.ad.(adapter.DraftCataloguer)
	if !ok {
		return []adapter.ComposerItem{}, nil
	}
	p, err := m.store.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	// Where CreateProject would start the thread: the chosen folder, the only
	// folder, or the project's own directory when it spans several.
	cwd := p.Home
	switch {
	case folderID != "":
		f, ok := p.Folder(folderID)
		if !ok {
			return nil, fmt.Errorf("folder %s is not part of %s: %w", folderID, p.Name, store.ErrNotFound)
		}
		cwd = f.Path
	case len(p.Folders) == 1:
		cwd = p.Folders[0].Path
	}
	// An instance whose secrets cannot be read still has skills on disk.
	env, _ := m.envFor(reg.inst)
	items, err := cataloguer.DraftComposerItems(ctx, env, cwd)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []adapter.ComposerItem{}
	}
	return items, nil
}
