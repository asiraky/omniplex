package thread

import (
	"context"
	"os"
	"path/filepath"

	"github.com/asiraky/omniplex/internal/skills"
)

// SkillRoots is where the harnesses look for skills from a thread or a
// project, and the name to show the project's skills under. A thread's roots
// are what its harness actually sees: its provider instance's config dirs (an
// instance can point Claude at a config dir of its own) and its checkout. A
// project alone has only the ambient config dirs and its folder. Neither gives
// the user-level roots on their own.
func (m *Manager) SkillRoots(ctx context.Context, threadID, projectID string) (skills.Roots, string, error) {
	roots, projectID, err := m.harnessRoots(ctx, threadID, projectID)
	if err != nil || roots.ProjectRoot == "" {
		return roots, "", err
	}
	if projectID != "" {
		if p, err := m.store.Project(ctx, projectID); err == nil && p.Name != "" {
			return roots, p.Name, nil
		}
	}
	return roots, filepath.Base(roots.ProjectRoot), nil
}

// harnessRoots works out the roots, and the project they are in when there
// is one.
func (m *Manager) harnessRoots(ctx context.Context, threadID, projectID string) (skills.Roots, string, error) {
	home, _ := os.UserHomeDir()
	switch {
	case threadID != "":
		meta, err := m.store.Thread(ctx, threadID)
		if err != nil {
			return skills.Roots{}, "", err
		}
		var env map[string]string
		if reg, err := m.instanceFor(meta); err == nil {
			env, _ = m.envFor(reg.inst)
		}
		root, warning, err := m.workspaceRoot(ctx, threadID)
		if err != nil || warning != "" {
			root = meta.Cwd
		}
		return skills.DefaultRoots(home, env, root), meta.ProjectID, nil
	case projectID != "":
		p, err := m.store.Project(ctx, projectID)
		if err != nil {
			return skills.Roots{}, "", err
		}
		root := p.Home
		if len(p.Folders) == 1 {
			root = p.Folders[0].Path
		}
		return skills.DefaultRoots(home, nil, root), projectID, nil
	default:
		return skills.DefaultRoots(home, nil, ""), "", nil
	}
}
