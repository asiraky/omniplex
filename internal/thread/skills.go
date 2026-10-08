package thread

import (
	"context"
	"os"
	"path/filepath"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
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
	// Every session loads the bundled plugin, so every listing shows it.
	if BundledPlugin != "" {
		roots.Bundled = adapter.PluginSkills(BundledPlugin)
	}
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
// is one. With a project come its home folder and the repos a skill can be
// installed into: a thread's own checkout of its folder, or every folder.
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
		r := skills.DefaultRoots(home, env, root)
		if meta.ProjectID == "" {
			return r, "", nil
		}
		r.ProjectHome = m.skillHome(ctx, meta.ProjectID)
		p, err := m.store.Project(ctx, meta.ProjectID)
		if err != nil {
			return r, meta.ProjectID, nil
		}
		if meta.FolderID == "" && len(p.Folders) != 1 {
			r.Repos = gitRepos(p)
			return r, meta.ProjectID, nil
		}
		// The thread's checkout of its folder, which is the folder itself
		// unless the thread has a worktree of its own.
		if _, f, err := m.folder(ctx, meta.ProjectID, meta.FolderID); err == nil && f.Git && root != "" {
			r.Repos = []skills.Repo{{Dir: root, Name: filepath.Base(f.Path), Main: samePath(root, f.Path)}}
		}
		return r, meta.ProjectID, nil
	case projectID != "":
		p, err := m.store.Project(ctx, projectID)
		if err != nil {
			return skills.Roots{}, "", err
		}
		projectHome := m.skillHome(ctx, projectID)
		root := p.Home
		if root == "" {
			root = projectHome
		}
		if len(p.Folders) == 1 {
			root = p.Folders[0].Path
		}
		r := skills.DefaultRoots(home, nil, root)
		r.ProjectHome = projectHome
		r.Repos = gitRepos(p)
		return r, projectID, nil
	default:
		return skills.DefaultRoots(home, nil, ""), "", nil
	}
}

// skillHome is the project's home folder, made on first use like an
// artefact's. Without one the project's own skills are just not offered.
func (m *Manager) skillHome(ctx context.Context, projectID string) string {
	home, err := ProjectHome(ctx, m.store, projectID)
	if err != nil {
		return ""
	}
	return home
}

// gitRepos is every folder of the project that is a repository, each in its
// main checkout.
func gitRepos(p project.Project) []skills.Repo {
	var out []skills.Repo
	for _, f := range p.Folders {
		if f.Git {
			out = append(out, skills.Repo{Dir: f.Path, Name: filepath.Base(f.Path), Main: true})
		}
	}
	return out
}

func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
