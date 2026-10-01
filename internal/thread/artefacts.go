package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// tool servers, then the user's MCP servers the adapter takes, and the
// folders it may write in outside it. That is the thread's home folder when
// it works somewhere else (a repo, a worktree), so an agent in a repo can put
// what it makes for you outside the repo. A thread scoped to the whole
// project also gets every folder of the project that is not already inside
// another.
func harnessExtras(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, cwd string, logf func(string, ...any)) ([]adapter.MCPServer, []string) {
	home, err := ThreadHome(ctx, st, meta.ProjectID, cwd)
	if err != nil {
		logf("home folder for %s: %v", meta.ID, err)
		home = cwd
	}
	dirs := []string{home}
	if meta.ProjectID != "" && meta.FolderID == "" {
		if p, err := st.Project(ctx, meta.ProjectID); err != nil {
			logf("folders for %s: %v", meta.ID, err)
		} else {
			for _, f := range p.Folders {
				dirs = append(dirs, f.Path)
			}
		}
	}
	extra := extraDirs(cwd, dirs)
	var servers []adapter.MCPServer
	if ToolServers != nil {
		servers = ToolServers(meta.ID, home)
	}
	return append(servers, userMCPServers(ctx, ad)...), extra
}

// extraDirs drops what the agent can already reach: anything inside cwd, or
// inside a folder already on the list.
func extraDirs(cwd string, dirs []string) []string {
	if cwd == "" {
		return nil
	}
	covered := []string{filepath.Clean(cwd)}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if slices.ContainsFunc(covered, func(c string) bool { return artefact.Within(c, d) }) {
			continue
		}
		covered = append(covered, d)
		out = append(out, d)
	}
	return out
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
// first time it is asked for. A project whose one folder is plain works in it
// directly. Any other gets <projects folder>/<name>, because the home is never
// inside a repo: a second repo cloned there would otherwise show up as the
// first one's untracked files.
func ProjectHome(ctx context.Context, st *store.Store, projectID string) (string, error) {
	homeMu.Lock()
	defer homeMu.Unlock()
	p, err := st.Project(ctx, projectID)
	if err != nil {
		return "", err
	}
	home := p.Home
	if home == "" {
		if len(p.Folders) == 1 && !p.Folders[0].Git {
			home = p.Folders[0].Path
		} else if home, err = newHome(ctx, st, p.Name); err != nil {
			return "", err
		}
		if err := st.SetProjectHome(ctx, projectID, home); err != nil {
			return "", err
		}
	}
	return home, os.MkdirAll(home, 0o755)
}

// newHome picks a folder in the projects folder no other project uses and
// nothing already occupies, numbering the name when it is taken.
func newHome(ctx context.Context, st *store.Store, name string) (string, error) {
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
// working directory and its project's folders. Home comes first; it is where
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
			for _, f := range p.Folders {
				roots = append(roots, f.Path)
			}
		}
	}
	return home, roots, nil
}
