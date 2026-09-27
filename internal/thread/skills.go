package session

import (
	"context"
	"os"

	"github.com/asiraky/omniplex/internal/skills"
)

// SkillRoots is where the harnesses look for skills from a session or a
// project. A session's are what its harness actually sees: its provider
// instance's config dirs (an instance can point Claude at a config dir of its
// own) and its checkout. A project alone has only the ambient config dirs and
// its root. Neither gives the user-level roots on their own.
func (m *Manager) SkillRoots(ctx context.Context, sessionID, projectID string) (skills.Roots, error) {
	home, _ := os.UserHomeDir()
	switch {
	case sessionID != "":
		meta, err := m.store.Session(ctx, sessionID)
		if err != nil {
			return skills.Roots{}, err
		}
		var env map[string]string
		if reg, err := m.instanceFor(meta); err == nil {
			env, _ = m.envFor(reg.inst)
		}
		root, warning, err := m.workspaceRoot(ctx, sessionID)
		if err != nil || warning != "" {
			root = meta.Cwd
		}
		return skills.DefaultRoots(home, env, root), nil
	case projectID != "":
		p, err := m.store.Project(ctx, projectID)
		if err != nil {
			return skills.Roots{}, err
		}
		return skills.DefaultRoots(home, nil, p.Root), nil
	default:
		return skills.DefaultRoots(home, nil, ""), nil
	}
}
