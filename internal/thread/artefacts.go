package thread

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// ToolServers returns the MCP servers omniplex runs beside a thread's
// harness, given the thread's home folder. Set once at startup, before any
// thread starts; nil means none. A package variable rather than a manager
// field because every path that starts a harness (create, resume, activate)
// needs it and none of them has the manager.
var ToolServers func(threadID, home string) []adapter.MCPServer

// harnessExtras is what every harness gets beside its working directory: the
// tool servers, and the thread's home folder as a folder it may write in
// when it works somewhere else (a repo, a worktree). That is what lets an
// agent in a repo put what it makes for you outside the repo.
func harnessExtras(ctx context.Context, st *store.Store, meta store.ThreadMeta, cwd string, logf func(string, ...any)) ([]adapter.MCPServer, []string) {
	home, err := ThreadHome(ctx, st, meta.ProjectID, cwd)
	if err != nil {
		logf("home folder for %s: %v", meta.ID, err)
		home = cwd
	}
	var extra []string
	if home != "" && cwd != "" && !artefact.Within(filepath.Clean(cwd), filepath.Clean(home)) {
		extra = []string{home}
	}
	if ToolServers == nil {
		return nil, extra
	}
	return ToolServers(meta.ID, home), extra
}

// ThreadHome is where a thread puts what it makes: its project's home
// folder, or its working directory when it has no project.
func ThreadHome(ctx context.Context, st *store.Store, projectID, cwd string) (string, error) {
	if projectID == "" {
		return cwd, nil
	}
	return ProjectHome(ctx, st, projectID)
}

var homeMu sync.Mutex

// ProjectHome returns the project's home folder, choosing and creating it the
// first time it is asked for. A project whose root is a plain folder works in
// it directly. A git project gets <projects folder>/<name>, because the home
// is never inside a repo: a second repo cloned there would otherwise show up
// as the first one's untracked files.
func ProjectHome(ctx context.Context, st *store.Store, projectID string) (string, error) {
	homeMu.Lock()
	defer homeMu.Unlock()
	p, err := st.Project(ctx, projectID)
	if err != nil {
		return "", err
	}
	home := p.Home
	if home == "" {
		if !insideGit(ctx, p.Root) {
			home = p.Root
		} else {
			if home, err = newHome(ctx, st, p.Config.Name, p.Root); err != nil {
				return "", err
			}
		}
		if err := st.SetProjectHome(ctx, projectID, home); err != nil {
			return "", err
		}
	}
	return home, os.MkdirAll(home, 0o755)
}

// newHome picks a folder in the projects folder no other project uses and
// nothing already occupies, numbering the name when it is taken.
func newHome(ctx context.Context, st *store.Store, name, root string) (string, error) {
	cfg, _ := userconfig.Load()
	dir, err := cfg.ProjectsDirOrDefault()
	if err != nil {
		return "", err
	}
	projects, err := st.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, p := range projects {
		if p.Home != "" {
			taken[p.Home] = true
		}
	}
	base := slug(name)
	if base == "" {
		base = slug(filepath.Base(root))
	}
	if base == "" {
		base = "project"
	}
	for i := 1; ; i++ {
		candidate := base
		if i > 1 {
			candidate = base + "-" + strconv.Itoa(i)
		}
		p := filepath.Join(dir, candidate)
		if taken[p] {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		return p, nil
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func insideGit(ctx context.Context, dir string) bool {
	return exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-dir").Run() == nil
}

// Show is a request to put a file or folder in front of the user.
type Show struct {
	// Path is absolute and already checked to be somewhere the thread may
	// show from.
	Path   string
	Name   string
	Note   string
	Source string
	Info   artefact.Info
}

// ShowArtefact records a shown file. The same path is the same artefact, so
// the agent revising a report and showing it again updates it in place; the
// decision is made inside the actor so two shows of one new path cannot both
// mint an id.
func (a *Actor) ShowArtefact(ctx context.Context, s Show) (proto.ArtefactShownPayload, error) {
	v, err := a.call(ctx, command{kind: cmdShowArtefact, show: &s})
	if err != nil {
		return proto.ArtefactShownPayload{}, err
	}
	return v.(proto.ArtefactShownPayload), nil
}

// ErrNoArtefact is returned for an artefact id the thread has not shown.
var ErrNoArtefact = errors.New("no such artefact")

// Artefact is one artefact the thread has shown, without copying the rest
// of the state: the file routes ask for it on every request.
func (a *Actor) Artefact(ctx context.Context, id string) (projection.Artefact, error) {
	v, err := a.call(ctx, command{kind: cmdArtefact, reqID: id})
	if err != nil {
		return projection.Artefact{}, err
	}
	return v.(projection.Artefact), nil
}

func (a *Actor) handleShow(s *Show) (proto.ArtefactShownPayload, error) {
	id := uuid.NewString()
	if existing, ok := a.state.ArtefactByPath(s.Path); ok {
		id = existing.ID
	}
	name := s.Name
	if name == "" {
		name = filepath.Base(s.Path)
	}
	payload := proto.ArtefactShownPayload{
		ArtefactID: id, Path: s.Path, Name: name, Dir: s.Info.Dir, MediaType: s.Info.MediaType, Size: s.Info.Size,
		Entry: s.Info.Entry, Files: s.Info.Files, ModifiedAt: s.Info.ModifiedAt, Source: s.Source, Note: s.Note,
	}
	if t := a.lastTurn(); t != nil && !t.Done {
		payload.TurnID = t.ID
	}
	if err := a.append(proto.Emit(proto.ArtefactShown, payload)); err != nil {
		return proto.ArtefactShownPayload{}, err
	}
	return payload, nil
}

// ArtefactRoots is where a thread may show files from: its home folder, its
// working directory and its project's root. Home comes first; it is where
// uploads go.
func (m *Manager) ArtefactRoots(ctx context.Context, threadID string) (home string, roots []string, err error) {
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return "", nil, err
	}
	home, err = ThreadHome(ctx, m.store, meta.ProjectID, meta.Cwd)
	if err != nil {
		return "", nil, err
	}
	roots = []string{home}
	if meta.Cwd != "" {
		roots = append(roots, meta.Cwd)
	}
	if meta.ProjectID != "" {
		if p, err := m.store.Project(ctx, meta.ProjectID); err == nil {
			roots = append(roots, p.Root)
		}
	}
	return home, roots, nil
}
