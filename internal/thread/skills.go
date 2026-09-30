package thread

import (
	"context"
	"os"
	"path/filepath"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// SkillRoots is where the harnesses look for skills from a thread or a
// project. A thread's are what its harness actually sees: its provider
// instance's config dirs (an instance can point Claude at a config dir of its
// own) and its checkout. A project alone has only the ambient config dirs and
// its folder. Neither gives the user-level roots on their own. The libraries
// Omniplex writes into come from the user config on top of either.
func (m *Manager) SkillRoots(ctx context.Context, threadID, projectID string) (skills.Roots, error) {
	roots, err := m.harnessRoots(ctx, threadID, projectID)
	if err != nil {
		return skills.Roots{}, err
	}
	// A config that does not parse is an error here rather than a silent
	// fall back to the default library: the next write would land in the
	// wrong place.
	cfg, err := userconfig.Load()
	if err != nil {
		return skills.Roots{}, err
	}
	return withSkillsConfig(roots, cfg.Skills)
}

// withSkillsConfig points the roots at the configured libraries. An empty
// setting keeps the default DefaultRoots chose.
func withSkillsConfig(r skills.Roots, cfg userconfig.SkillsConfig) (skills.Roots, error) {
	if cfg.Library != "" {
		library, err := userconfig.ExpandHome(cfg.Library)
		if err != nil {
			return r, err
		}
		r.Library = library
	}
	if cfg.ProjectLibrary != "" && r.ProjectRoot != "" {
		r.ProjectLibrary = filepath.Join(r.ProjectRoot, filepath.FromSlash(cfg.ProjectLibrary))
	}
	if cfg.CLIVersion != "" {
		r.CLIVersion = cfg.CLIVersion
	}
	return r, nil
}

func (m *Manager) harnessRoots(ctx context.Context, threadID, projectID string) (skills.Roots, error) {
	home, _ := os.UserHomeDir()
	switch {
	case threadID != "":
		meta, err := m.store.Thread(ctx, threadID)
		if err != nil {
			return skills.Roots{}, err
		}
		var env map[string]string
		if reg, err := m.instanceFor(meta); err == nil {
			env, _ = m.envFor(reg.inst)
		}
		root, warning, err := m.workspaceRoot(ctx, threadID)
		if err != nil || warning != "" {
			root = meta.Cwd
		}
		return skills.DefaultRoots(home, env, root), nil
	case projectID != "":
		p, err := m.store.Project(ctx, projectID)
		if err != nil {
			return skills.Roots{}, err
		}
		root := p.Home
		if len(p.Folders) == 1 {
			root = p.Folders[0].Path
		}
		return skills.DefaultRoots(home, nil, root), nil
	default:
		return skills.DefaultRoots(home, nil, ""), nil
	}
}
