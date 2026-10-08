package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/thread"
)

// A card is a change the agent proposed through omniplex's own tools, waiting
// on the user. The thread holds what the user sees; the server holds the
// rest here, in memory: the values the agent passed (which never reach the
// card or the log), the staging dir of a fetch, and the agent's call waiting
// on the answer. A restart loses all of it, and the thread cancels the cards
// it lost when it next resumes.

const (
	cardAddServer    = "add_mcp_server"
	cardRemoveServer = "remove_mcp_server"
	cardInstallSkill = "install_skill"
	cardCreateSkill  = "create_skill"
	cardRemoveSkill  = "remove_skill"
	cardAddSignIn    = "add_sign_in"
	cardAddAccount   = "add_account"
)

// When the agent can use what was saved.
const (
	liveNow         = "now"
	liveNextTurn    = "next_turn"
	liveNextSession = "next_session"
)

// cardBook is the cards the server holds, by request id.
type cardBook struct {
	mu    sync.Mutex
	cards map[string]*heldCard
	// resolved is what became of each card this server answered, so a
	// client resending its answer after a reconnect gets the same reply.
	resolved map[string]resolvedCard
}

type resolvedCard struct {
	thread, action string
	outcome        cardOutcome
}

func newCardBook() *cardBook {
	return &cardBook{cards: map[string]*heldCard{}, resolved: map[string]resolvedCard{}}
}

// answer moves a card from held to resolved.
func (b *cardBook) answer(c *heldCard, action string, out cardOutcome) {
	b.mu.Lock()
	delete(b.cards, c.id)
	b.resolved[c.id] = resolvedCard{thread: c.thread, action: action, outcome: out}
	b.mu.Unlock()
}

// again is the reply to an answer this server already took: the stored
// outcome when it is the same answer, "already answered" otherwise.
func (b *cardBook) again(a resolveCardArgs) (any, bool, error) {
	b.mu.Lock()
	r, ok := b.resolved[a.RequestID]
	b.mu.Unlock()
	if !ok || r.thread != a.ThreadID {
		return nil, false, nil
	}
	if r.action != a.Action {
		return nil, true, thread.ErrNoCard
	}
	return map[string]any{"outcome": r.outcome}, true, nil
}

// live reports whether the server still holds a card; the thread manager
// asks it when a thread resumes.
func (b *cardBook) live(requestID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.cards[requestID]
	return ok
}

func (b *cardBook) add(c *heldCard) {
	b.mu.Lock()
	b.cards[c.id] = c
	b.mu.Unlock()
}

func (b *cardBook) get(requestID string) *heldCard {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cards[requestID]
}

func (b *cardBook) drop(requestID string) {
	b.mu.Lock()
	delete(b.cards, requestID)
	b.mu.Unlock()
}

// heldCard is one card and the whole proposal behind it.
type heldCard struct {
	id, thread, kind string
	projectID        string
	card             json.RawMessage

	// answering serialises answers: a second waits for the first, then
	// finds the card answered, or still pending when the first failed.
	answering sync.Mutex
	answered  bool
	done      chan struct{}
	text      string // the agent's tool result, set before done closes

	// add_mcp_server: the draft as the agent passed it, values included.
	draft mcp.Draft
	// add_mcp_server, remove_mcp_server: "project" or "everywhere".
	scope string
	// remove_mcp_server: the server; remove_skill: the skill's name and dir.
	removeName, removeProject, removeDir string
	// install_skill
	stagedID string
	picked   []string
	// install_skill, create_skill: the destination folder, "" for personal,
	// and for each destination whether Claude's skills folder there existed
	// when the card was raised.
	destination string
	claudeHad   map[string]bool
	// create_skill
	skillName, description, content string
	// add_sign_in
	cli cliDefinition
	// add_account
	accountCLI, accountCLIName, accountName string
}

func newHeldCard(t toolThread, kind string) *heldCard {
	return &heldCard{id: "card_" + newID(), thread: t.id, kind: kind, projectID: t.projectID, done: make(chan struct{})}
}

// discard lets go of what a card holds outside memory.
func (c *heldCard) discard() {
	if c.stagedID != "" {
		_ = skills.DiscardStaged(c.stagedID)
	}
}

// --- what the card says (never a value) ---

type cardJSON struct {
	Kind         string               `json:"kind"`
	ProjectID    string               `json:"projectId"`
	ProjectName  string               `json:"projectName,omitempty"`
	Scope        string               `json:"scope,omitempty"`
	Server       *cardServer          `json:"server,omitempty"`
	Replaces     bool                 `json:"replaces,omitempty"`
	Harnesses    *[]cardHarness       `json:"harnesses,omitempty"` // [] when no harness here can use it
	Remove       *cardRemove          `json:"remove,omitempty"`
	Staged       *cardStaged          `json:"staged,omitempty"`
	Skill        *cardSkill           `json:"skill,omitempty"`
	Destination  *string              `json:"destination,omitempty"`
	Destinations []skills.Destination `json:"destinations,omitempty"`
	CLI          *cliDefinition       `json:"cli,omitempty"`
	Account      *cardAccount         `json:"account,omitempty"`
}

type cardServer struct {
	Name    string       `json:"name"`
	URL     string       `json:"url,omitempty"`
	Command string       `json:"command,omitempty"`
	Args    []string     `json:"args,omitempty"`
	Env     []cardSecret `json:"env,omitempty"`
	Headers []cardSecret `json:"headers,omitempty"`
}

// cardSecret names a value the server needs. Held: the agent passed one,
// which the server keeps with the card.
type cardSecret struct {
	Name string `json:"name"`
	Held bool   `json:"held"`
}

type cardHarness struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cardRemove struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

type cardStaged struct {
	ID     string            `json:"id"`
	Source string            `json:"source"`
	Skills []cardStagedSkill `json:"skills"`
}

type cardStagedSkill struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Files       []skills.File `json:"files"`
	Picked      bool          `json:"picked"`
	Problem     string        `json:"problem,omitempty"`
}

type cardSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// cliDefinition is a sign-in as the agent proposes it, and as the card and
// the user's edits carry it.
type cliDefinition struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	StatusCommand   string            `json:"statusCommand"`
	SignedInPattern string            `json:"signedInPattern,omitempty"`
	SignInCommand   string            `json:"signInCommand"`
	PrepareCommand  string            `json:"prepareCommand,omitempty"`
	AccountEnv      map[string]string `json:"accountEnv,omitempty"`
	Accounts        []string          `json:"accounts,omitempty"`
}

type cardAccount struct {
	CLI     string `json:"cli"`
	CLIName string `json:"cliName,omitempty"`
	Name    string `json:"name"`
}

// cardOutcome is what became of a card: resolve_card's reply and the
// stored resolution. Never a value.
type cardOutcome struct {
	Result      string         `json:"result"`
	Edited      bool           `json:"edited,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	Server      *outcomeServer `json:"server,omitempty"`
	NeedsSignIn bool           `json:"needsSignIn,omitempty"`
	CLI         *outcomeCLI    `json:"cli,omitempty"`
	Skills      []string       `json:"skills,omitempty"`
	Destination string         `json:"destination,omitempty"`
	Live        string         `json:"live,omitempty"`
}

type outcomeServer struct {
	Name    string `json:"name"`
	Project string `json:"project,omitempty"`
}

type outcomeCLI struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Accounts []string `json:"accounts"`
}

// --- answering ---

type resolveCardArgs struct {
	ThreadID  string          `json:"threadId"`
	RequestID string          `json:"requestId"`
	Action    string          `json:"action"`
	Edits     json.RawMessage `json:"edits"`
}

// cardEdits is what the user changed on a card. Only what changed is sent.
type cardEdits struct {
	Scope       string            `json:"scope"`
	Env         map[string]string `json:"env"`
	Headers     map[string]string `json:"headers"`
	Skills      []string          `json:"skills"`
	Destination *string           `json:"destination"`
	CLI         json.RawMessage   `json:"cli"`
	Name        string            `json:"name"`
}

// resolveCard applies or declines a card. A failed apply leaves it pending,
// for the user to fix or decline.
func (s *Server) resolveCard(ctx context.Context, a resolveCardArgs) (any, error) {
	if a.Action != "accept" && a.Action != "decline" {
		return nil, errors.New(`action must be "accept" or "decline"`)
	}
	c := s.cards.get(a.RequestID)
	if c == nil || c.thread != a.ThreadID {
		if reply, ok, err := s.cards.again(a); ok {
			return reply, err
		}
		return nil, s.resolveLostCard(ctx, a.ThreadID, a.RequestID)
	}
	c.answering.Lock()
	defer c.answering.Unlock()
	if c.answered {
		reply, _, err := s.cards.again(a)
		return reply, err
	}
	var edits cardEdits
	if len(a.Edits) > 0 && string(a.Edits) != "null" {
		if err := json.Unmarshal(a.Edits, &edits); err != nil {
			return nil, fmt.Errorf("edits: %w", err)
		}
	}

	var (
		out  cardOutcome
		text string
	)
	if a.Action == "decline" {
		c.discard()
		out, text = declined(c)
	} else {
		applied, changed, err := s.applyCard(ctx, c, edits)
		if err != nil {
			return nil, err
		}
		out = applied
		out.Result = projection.CardSaved
		out.Edited = len(changed) > 0
		text = savedText(c, out, changed)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	// The change is made; whatever the thread says now, the card is done.
	c.answered = true
	c.text = text
	c.draft.Env, c.draft.Headers = nil, nil // the values are in the secret store now, or nowhere
	s.cards.answer(c, a.Action, out)
	close(c.done)

	actor, err := s.mgr.View(ctx, c.thread)
	if err == nil {
		err = actor.ResolveCard(ctx, c.id, a.Action, raw)
	}
	if err != nil && !errors.Is(err, thread.ErrNoCard) {
		return nil, fmt.Errorf("%s, but the thread did not record it: %w", out.Summary, err)
	}
	return map[string]any{"outcome": out}, nil
}

// resolveLostCard answers for a card the server does not hold: one a
// restart lost is cancelled, so it stops asking; anything else is answered.
func (s *Server) resolveLostCard(ctx context.Context, threadID, requestID string) error {
	actor, err := s.mgr.View(ctx, threadID)
	if err != nil {
		return thread.ErrNoCard
	}
	state, err := actor.State(ctx)
	if err != nil {
		return err
	}
	for _, p := range state.Elicitations {
		if p.RequestID != requestID || !p.IsCard() {
			continue
		}
		raw, _ := json.Marshal(cardOutcome{Result: projection.CardCancelled, Summary: "Lost when omniplex restarted"})
		if err := actor.ResolveCard(ctx, requestID, "cancel", raw); err != nil && !errors.Is(err, thread.ErrNoCard) {
			return err
		}
		return errors.New("omniplex restarted since this card was raised, so it can no longer be applied; ask the agent to propose it again")
	}
	return thread.ErrNoCard
}

func declined(c *heldCard) (cardOutcome, string) {
	out := cardOutcome{Result: projection.CardDeclined}
	var what string
	switch c.kind {
	case cardAddServer:
		what = "adding the MCP server " + c.draft.Name
	case cardRemoveServer:
		what = "removing the MCP server " + c.removeName
	case cardInstallSkill:
		what = "installing those skills"
	case cardCreateSkill:
		what = "creating the skill " + c.skillName
	case cardRemoveSkill:
		what = "removing the skill " + c.removeName
	case cardAddSignIn:
		what = "adding the sign-in " + c.cli.Name
	case cardAddAccount:
		what = "adding the account " + c.accountName
	}
	out.Summary = "Declined " + what
	return out, "The user declined " + what + ". Nothing was changed."
}

// applyCard makes the change a card proposes, with the user's edits, and
// names what the user changed (never a value).
func (s *Server) applyCard(ctx context.Context, c *heldCard, e cardEdits) (cardOutcome, []string, error) {
	meta, err := s.store.Thread(ctx, c.thread)
	if err != nil {
		return cardOutcome{}, nil, err
	}
	switch c.kind {
	case cardAddServer:
		return s.applyAddServer(ctx, c, e, meta.Harness)
	case cardRemoveServer:
		if s.conns == nil {
			return cardOutcome{}, nil, errNoConnections
		}
		if err := s.conns.Remove(c.removeName, c.removeProject); err != nil {
			return cardOutcome{}, nil, err
		}
		return cardOutcome{
			Summary: "Removed " + c.removeName + " " + scopePhrase(c.scope),
			Server:  &outcomeServer{Name: c.removeName, Project: c.removeProject},
			Live:    liveNextSession,
		}, nil, nil
	case cardInstallSkill, cardCreateSkill, cardRemoveSkill:
		return s.applySkillCard(ctx, c, e, meta.Harness)
	case cardAddSignIn:
		return s.applyAddSignIn(c, e)
	case cardAddAccount:
		if s.conns == nil {
			return cardOutcome{}, nil, errNoConnections
		}
		name, changed := c.accountName, []string(nil)
		if n := strings.TrimSpace(e.Name); n != "" && n != name {
			name, changed = n, []string{"the account name"}
		}
		if _, err := s.conns.AddAccount(c.accountCLI, name); err != nil {
			return cardOutcome{}, nil, err
		}
		return cardOutcome{
			Summary: "Added the account " + name + " to " + c.accountCLIName,
			CLI:     &outcomeCLI{ID: c.accountCLI, Name: c.accountCLIName, Accounts: []string{name}},
			Live:    liveNow,
		}, changed, nil
	}
	return cardOutcome{}, nil, fmt.Errorf("unknown card %q", c.kind)
}

func (s *Server) applyAddServer(ctx context.Context, c *heldCard, e cardEdits, harness string) (cardOutcome, []string, error) {
	if s.conns == nil {
		return cardOutcome{}, nil, errNoConnections
	}
	d := c.draft
	d.Env, d.Headers = maps.Clone(c.draft.Env), maps.Clone(c.draft.Headers)
	var changed []string
	scope := c.scope
	if e.Scope != "" && e.Scope != scope {
		switch e.Scope {
		case "project":
			if c.projectID == "" {
				return cardOutcome{}, nil, errors.New("this thread is not in a project, so the server can only go everywhere")
			}
			d.Project = c.projectID
		case "everywhere":
			d.Project = ""
		default:
			return cardOutcome{}, nil, fmt.Errorf("unknown scope %q", e.Scope)
		}
		scope = e.Scope
		changed = append(changed, "the scope")
	}
	typed := func(kind string, held, edits map[string]string) error {
		for _, name := range sortedKeys(edits) {
			if _, ok := held[name]; !ok {
				return fmt.Errorf("the server has no %s %q", kind, name)
			}
			if v := strings.TrimSpace(edits[name]); v != "" {
				held[name] = v
				changed = append(changed, kind+" "+name)
			}
		}
		return nil
	}
	if err := typed("env", d.Env, e.Env); err != nil {
		return cardOutcome{}, nil, err
	}
	if err := typed("header", d.Headers, e.Headers); err != nil {
		return cardOutcome{}, nil, err
	}
	// One already there in that scope is replaced; a value left empty
	// keeps what it had.
	previous := ""
	if _, ok, err := s.findServer(ctx, d.Name, d.Project); err != nil {
		return cardOutcome{}, nil, err
	} else if ok {
		previous = d.Name
	}
	view, err := s.conns.Save(ctx, d, previous)
	if err != nil {
		return cardOutcome{}, nil, err
	}
	out := cardOutcome{
		Summary:     "Saved " + d.Name + " " + scopePhrase(scope),
		Server:      &outcomeServer{Name: view.Name, Project: view.Project},
		NeedsSignIn: view.Status == mcp.StatusSignIn,
	}
	c.scope = scope
	out.Live = s.mcpLive(ctx, c.thread, harness, d)
	return out, changed, nil
}

// mcpLive pushes a saved server into the thread's live session when the
// harness can take it there, and says when the agent gets it. "" when the
// thread's harness does not run it at all.
func (s *Server) mcpLive(ctx context.Context, threadID, harness string, d mcp.Draft) string {
	kind := "http"
	if d.URL == "" {
		kind = "stdio"
	}
	runs := false
	for _, h := range s.mgr.MCPHosts() {
		if h.ID == harness && slices.Contains(h.Host.MCPTransports(), kind) {
			runs = true
		}
	}
	if !runs {
		return ""
	}
	if err := s.mgr.ReconnectMCP(ctx, threadID, d.Name); err != nil {
		if !errors.Is(err, adapter.ErrMCPUnsupported) {
			s.logf("mcp %s into thread %s: %v", d.Name, threadID, err)
		}
		return liveNextSession
	}
	return liveNow
}

func (s *Server) applySkillCard(ctx context.Context, c *heldCard, e cardEdits, harness string) (cardOutcome, []string, error) {
	roots, _, err := s.mgr.SkillRoots(ctx, c.thread, "")
	if err != nil {
		return cardOutcome{}, nil, err
	}
	if c.kind == cardRemoveSkill {
		if err := skills.Remove(roots, c.removeDir); err != nil {
			return cardOutcome{}, nil, err
		}
		return cardOutcome{
			Summary: "Removed the skill " + c.removeName,
			Skills:  []string{c.removeName},
			Live:    skillLive(harness, true),
		}, nil, nil
	}
	var changed []string
	folder := c.destination
	if e.Destination != nil && *e.Destination != folder {
		folder = *e.Destination
		changed = append(changed, "the destination")
	}
	label := ""
	for _, d := range roots.Destinations() {
		if d.Folder == folder {
			label = d.Label
		}
	}
	if label == "" {
		return cardOutcome{}, nil, fmt.Errorf("%s is not somewhere a skill can go from this thread", folder)
	}
	var names []string
	if c.kind == cardInstallSkill {
		names = c.picked
		if e.Skills != nil && !sameSet(e.Skills, c.picked) {
			names = e.Skills
			changed = append(changed, "which skills")
		}
		placed, err := skills.InstallStaged(roots, c.stagedID, names, folder)
		if err != nil {
			return cardOutcome{}, nil, err
		}
		skills.MarkUncommitted(ctx, roots, placed)
		names = names[:0:0]
		for _, p := range placed {
			names = append(names, p.Name)
		}
		c.stagedID = "" // InstallStaged dropped it
	} else {
		created, err := skills.Create(roots, c.skillName, c.description, folder)
		if err != nil {
			return cardOutcome{}, nil, err
		}
		if err := skills.Save(roots, created.Dir, c.content); err != nil {
			_ = skills.Remove(roots, created.Dir)
			return cardOutcome{}, nil, err
		}
		names = []string{c.skillName}
	}
	verb := "Installed "
	if c.kind == cardCreateSkill {
		verb = "Created "
	}
	return cardOutcome{
		Summary:     verb + strings.Join(names, ", ") + " in " + label,
		Skills:      names,
		Destination: label,
		Live:        skillLive(harness, c.claudeHad[folder]),
	}, changed, nil
}

// skillLive is when a harness picks up a skill that just landed: Claude
// watches the skills folders that were there when the session started,
// Codex looks again every turn, and pi reads them once.
func skillLive(harness string, claudeWatches bool) string {
	switch harness {
	case "claude":
		if claudeWatches {
			return liveNow
		}
		return liveNextSession
	case "codex":
		return liveNextTurn
	}
	return liveNextSession
}

// claudeSkillsDir is the folder Claude reads a destination's skills from.
func claudeSkillsDir(roots skills.Roots, folder string) string {
	if folder == "" {
		return filepath.Join(roots.ClaudeConfigDir, "skills")
	}
	return filepath.Join(folder, ".claude", "skills")
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func (s *Server) applyAddSignIn(c *heldCard, e cardEdits) (cardOutcome, []string, error) {
	if s.conns == nil {
		return cardOutcome{}, nil, errNoConnections
	}
	def := c.cli
	def.AccountEnv = maps.Clone(c.cli.AccountEnv)
	def.Accounts = slices.Clone(c.cli.Accounts)
	var changed []string
	if len(e.CLI) > 0 && string(e.CLI) != "null" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(e.CLI, &fields); err != nil {
			return cardOutcome{}, nil, fmt.Errorf("edits: %w", err)
		}
		if err := json.Unmarshal(e.CLI, &def); err != nil {
			return cardOutcome{}, nil, fmt.Errorf("edits: %w", err)
		}
		changed = sortedKeys(fields)
	}
	if def.ID == "" {
		def.ID = c.cli.ID
	}
	// Saved without accounts, then each added: adding is what fills an
	// account's env in from the definition's.
	cli := mcp.CLI{
		ID: def.ID, Name: def.Name, StatusCommand: def.StatusCommand, SignedInPattern: def.SignedInPattern,
		SignInCommand: def.SignInCommand, PrepareCommand: def.PrepareCommand, AccountEnv: def.AccountEnv,
	}
	saved, err := s.conns.SaveCLI(cli, "")
	if err != nil {
		return cardOutcome{}, nil, err
	}
	for _, a := range def.Accounts {
		if _, err := s.conns.AddAccount(saved.ID, a); err != nil {
			_ = s.conns.RemoveCLI(saved.ID)
			return cardOutcome{}, nil, err
		}
	}
	accounts := def.Accounts
	if accounts == nil {
		accounts = []string{}
	}
	return cardOutcome{
		Summary: "Saved the sign-in " + saved.Name,
		CLI:     &outcomeCLI{ID: saved.ID, Name: saved.Name, Accounts: accounts},
		Live:    liveNow,
	}, changed, nil
}

// --- what the agent is told ---

func savedText(c *heldCard, out cardOutcome, changed []string) string {
	var b strings.Builder
	if len(changed) > 0 {
		fmt.Fprintf(&b, "The user edited the card (changed %s) and saved it: ", strings.Join(changed, ", "))
	} else {
		b.WriteString("The user saved it: ")
	}
	b.WriteString(lowerFirst(out.Summary))
	b.WriteString(".")
	switch c.kind {
	case cardAddServer:
		switch out.Live {
		case liveNow:
			b.WriteString(" It is connected to this session now; its tools are yours to use.")
		case liveNextSession:
			b.WriteString(" This session cannot take it while it runs: its tools arrive in the next session.")
		case "":
			b.WriteString(" This thread's agent does not run this kind of MCP server.")
		}
		if out.NeedsSignIn {
			b.WriteString(" It needs a sign-in, which the user does from the card; until then its tools fail.")
		}
	case cardRemoveServer:
		b.WriteString(" Its tools go at the next session.")
	case cardInstallSkill, cardCreateSkill, cardRemoveSkill:
		switch out.Live {
		case liveNow:
			b.WriteString(" This session sees the change now.")
		case liveNextTurn:
			b.WriteString(" This session sees the change from its next turn.")
		default:
			b.WriteString(" This session does not see the change; the next session does.")
		}
	case cardAddSignIn, cardAddAccount:
		b.WriteString(" The user signs each account in from the card; list_sign_ins says when one is signed in and the env to run its commands with.")
	}
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// scopePhrase is a server's scope in words.
func scopePhrase(scope string) string {
	if scope == "project" {
		return "for this project"
	}
	return "everywhere"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}
