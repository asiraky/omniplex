package thread

import (
	"context"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
)

// DraftComposerItems is the completion catalogue for a thread that has not
// been created: what the chosen provider would offer where the thread would
// start, answered without starting a harness. A provider that cannot say
// offers nothing, which is what the composer showed before it asked.
//
// workspacePath is the existing copy the thread would attach to, whose branch
// may carry different skills from the folder's own checkout. A copy that does
// not exist yet has nothing to read, so the folder stands in for it.
func (m *Manager) DraftComposerItems(ctx context.Context, harness, instance, projectID, folderID, workspacePath string) ([]adapter.ComposerItem, error) {
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
	var f project.Folder
	scoped := true
	switch {
	case folderID != "":
		var ok bool
		if f, ok = p.Folder(folderID); !ok {
			return nil, fmt.Errorf("folder %s is not part of %s: %w", folderID, p.Name, store.ErrNotFound)
		}
	case len(p.Folders) == 1:
		f = p.Folders[0]
	default:
		scoped = false
	}
	if scoped {
		cwd = f.Path
		// Only a worktree of the folder is read, as only one can be attached to.
		if strings.TrimSpace(workspacePath) != "" {
			w, err := m.ResolveWorkspace(ctx, p.ID, f.ID, workspacePath)
			if err != nil {
				return nil, err
			}
			cwd = w.Path
		}
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
