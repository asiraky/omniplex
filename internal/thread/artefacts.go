package thread

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"mime"
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
	"github.com/asiraky/omniplex/internal/ephemeral"
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

// BundledPlugin is omniplex's own plugin folder (see package bundled),
// extracted at startup; "" when there is none. Every session loads it for
// itself alone. Set once at startup, like ToolServers.
var BundledPlugin string

// extras is what a harness gets beside its working directory.
type extras struct {
	mcp       []adapter.MCPServer
	extraDirs []string
	skillDirs []string
	plugins   []string
	// suggestPrompts asks the harness for a next-prompt suggestion after
	// each turn.
	suggestPrompts bool
	// instructions are the session's system prompt: Omniplex's own, or for
	// an assistant chat the user's assistant prompt in its place.
	instructions string
	// ephemeral is a private thread's: the harness keeps nothing on disk.
	ephemeral bool
	// agent is set for an agent's thread: what its harness is told about
	// compaction and memory, and the revisions it starts on.
	agent *agentExtras
	// err is setup the thread cannot start without. Its process is not
	// started: one would run on less than the thread was promised.
	err error
}

// agentExtras is the part of a session's setup an agent decides.
type agentExtras struct {
	id          string
	promptRev   int
	compactRev  int
	compactFile string
	compactAt   int64
	rawDir      string
}

// options fills in everything a session gets from omniplex rather than from
// the thread's own settings, so every path that starts a harness hands over
// the same set.
func (x extras) options(o adapter.CreateOptions) adapter.CreateOptions {
	o.MCPServers, o.ExtraDirs, o.SkillDirs, o.Plugins = x.mcp, x.extraDirs, x.skillDirs, x.plugins
	o.Instructions = x.instructions
	if o.Instructions == "" {
		o.Instructions = omniplexInstructions
	}
	o.SuggestPrompts = x.suggestPrompts
	o.Ephemeral = x.ephemeral
	if ag := x.agent; ag != nil {
		o.CompactInstructionsFile, o.CompactAt, o.RawRequestLogDir = ag.compactFile, ag.compactAt, ag.rawDir
		// An agent's durable memory is its prompt, which the user owns; a
		// harness keeping notes of its own beside it would be a second one.
		o.NoMemory = true
	}
	return o
}

// harnessExtras is what every harness gets beside its working directory: the
// tool servers, then the user's MCP servers the adapter takes, and the
// folders it may write in outside it. That is the thread's home folder when
// it works somewhere else (a repo, a worktree), so an agent in a repo can put
// what it makes for you outside the repo. A thread scoped to the whole
// project also gets every folder of the project that is not already inside
// another. A project's thread working outside the home also gets the home's
// private skills; a harness in the home finds them by itself. Every thread
// gets omniplex's bundled plugin. Prompt suggestions follow the user's
// setting, on unless it is turned off or the config cannot be read. A thread
// with no project is an assistant chat, told the user's assistant prompt
// instead of Omniplex's; a private one asks the harness to keep nothing.
func harnessExtras(ctx context.Context, st *store.Store, ad adapter.Adapter, meta store.ThreadMeta, cwd string, logf func(string, ...any)) extras {
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
	x := extras{extraDirs: extraDirs(cwd, dirs)}
	if meta.ProjectID != "" && cwd != "" && filepath.Clean(cwd) != filepath.Clean(home) {
		x.skillDirs = []string{projectSkills(home, logf)}
	}
	if BundledPlugin != "" {
		x.plugins = []string{BundledPlugin}
	}
	if ToolServers != nil {
		x.mcp = ToolServers(meta.ID, home)
		// An agent's thread is offered propose_prompt_edit; the server still
		// refuses it to any other thread.
		if meta.AgentID != "" {
			for i := range x.mcp {
				x.mcp[i].Env = maps.Clone(x.mcp[i].Env)
				if x.mcp[i].Env == nil {
					x.mcp[i].Env = map[string]string{}
				}
				x.mcp[i].Env["OMNIPLEX_AGENT_ID"] = meta.AgentID
			}
		}
	}
	x.mcp = append(x.mcp, userMCPServers(ctx, ad, meta.ProjectID)...)
	cfg, err := userconfig.Load()
	x.suggestPrompts = err != nil || cfg.SuggestPrompts == nil || *cfg.SuggestPrompts
	x.instructions = omniplexInstructions
	if meta.ProjectID == "" {
		x.instructions = cfg.AssistantPromptOrDefault()
	}
	x.ephemeral = ephemeral.Is(meta.ID)
	if meta.AgentID != "" {
		agentSetup(ctx, st, meta, &x, logf)
	}
	return x
}

// agentSetup composes an agent thread's system prompt, Omniplex's own then
// the agent preamble then the agent's latest prompt, and readies the
// compaction instructions file its harness reads. An agent that cannot be
// read leaves the thread starting as a plain project thread, which is
// better than not starting it at all. A file that cannot be written fails
// the start: the harness would compact on whatever the file held before, or
// on its default, and the log would record instructions it never read.
func agentSetup(ctx context.Context, st *store.Store, meta store.ThreadMeta, x *extras, logf func(string, ...any)) {
	// An agent that is gone leaves its thread a plain one. One that cannot be
	// read fails the start: a thread that came up without its standing rules
	// would look like the agent and not be it.
	ag, err := st.Agent(ctx, meta.AgentID)
	if errors.Is(err, store.ErrNotFound) {
		logf("agent %s of %s: %v", meta.AgentID, meta.ID, err)
		return
	}
	if err != nil {
		x.err = fmt.Errorf("reading agent %s: %w", meta.AgentID, err)
		return
	}
	prompt, err := st.LatestAgentRevision(ctx, ag.ID, store.RevisionPrompt)
	if err != nil {
		x.err = fmt.Errorf("reading agent %s's prompt: %w", ag.ID, err)
		return
	}
	compact, err := st.LatestAgentRevision(ctx, ag.ID, store.RevisionCompact)
	if err != nil {
		x.err = fmt.Errorf("reading agent %s's compaction instructions: %w", ag.ID, err)
		return
	}
	x.instructions = composeAgentInstructions(prompt.Body)
	setup := &agentExtras{id: ag.ID, promptRev: prompt.N, compactRev: compact.N, compactAt: ag.CompactAt}
	if setup.compactFile, err = writeCompactFile(st, ag.ID, compact.Body); err != nil {
		x.err = fmt.Errorf("writing agent %s's compaction instructions for its harness: %w", ag.ID, err)
		return
	}
	if ag.RawLog {
		setup.rawDir = filepath.Join(agentDir(st, ag.ID), "raw")
		if err := os.MkdirAll(setup.rawDir, 0o700); err != nil {
			logf("raw request log of agent %s: %v", ag.ID, err)
			setup.rawDir = ""
		}
	}
	x.agent = setup
}

// projectSkills readies a project home's private skills folder for a session
// working elsewhere and returns it. Claude reaches it through the home's
// .claude/skills and only watches skills folders that existed when it
// started, so both are made now, empty or not: a skill installed mid-session
// then shows up without a restart.
func projectSkills(home string, logf func(string, ...any)) string {
	skills := filepath.Join(home, ".agents", "skills")
	for _, d := range []string{skills, filepath.Join(home, ".claude", "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			logf("project skills folder: %v", err)
		}
	}
	return skills
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

// newHome picks a folder in the projects folder no other project uses, nor
// assistant chats, and nothing already occupies, numbering the name when it is taken.
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
	taken := map[string]bool{filepath.Join(dir, assistantFolder): true}
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
	// Inline asks for the page to render in the conversation. Only HTML
	// can, so anything else ignores it and shows as a card.
	Inline bool
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
		Inline: s.Inline && isHTML(s.Info.MediaType),
	}
	if t := a.lastTurn(); t != nil && !t.Done {
		payload.TurnID = t.ID
	}
	if err := a.append(proto.Emit(proto.ArtefactShown, payload)); err != nil {
		return proto.ArtefactShownPayload{}, err
	}
	return payload, nil
}

// isHTML reports whether a media type is an HTML page. For a folder the
// type is its entry's, so a folder of images that opens on no page is not
// one.
func isHTML(mediaType string) bool {
	mt, _, err := mime.ParseMediaType(mediaType)
	return err == nil && mt == "text/html"
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
