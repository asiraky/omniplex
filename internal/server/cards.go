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
	"github.com/asiraky/omniplex/internal/store"
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
	// cardPromptEdit is the card propose_prompt_edit raises: the one card
	// not named after its tool.
	cardPromptEdit = "prompt_edit"
)

// When the agent can use what was saved.
const (
	liveNow         = "now"
	liveNextTurn    = "next_turn"
	liveNextSession = "next_session"
	// liveAfterSignIn is a server the live session took but cannot reach
	// until the user signs in, and reconnects to once they have.
	liveAfterSignIn = "after_sign_in"
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
	// propose_prompt_edit: the agent, which of its texts, the revision the
	// edit was made against, the edit itself and what it made of that
	// revision. saved is the text that was saved, and rebased the newer
	// revision the edit was applied to instead, if any. restart is what the
	// save did to the live thread (thread.Restart*), and reads when the
	// agent's harness reads compaction instructions.
	agentID, editKind     string
	fromRev, rebased      int
	find, replace, reason string
	after, saved          string
	saveNote              string
	restart               string
	reads                 adapter.CompactReads
}

func newHeldCard(t toolThread, kind string) *heldCard {
	return &heldCard{id: "card_" + newID(), thread: t.id, kind: kind, projectID: t.projectID, done: make(chan struct{})}
}

// discard lets go of what a card holds outside memory.
func (c *heldCard) discard() {
	if c.stagedID != "" {
		skills.ReleaseStaged(c.stagedID)
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
	PromptEdit   *promptEditCard      `json:"promptEdit,omitempty"`
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

// promptEditCard is an agent's proposed change to its prompt or compaction
// instructions: the whole text before and after, so the card can show the
// diff and the user can edit the result.
type promptEditCard struct {
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	Kind      string `json:"kind"`
	FromRev   int    `json:"fromRev"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Reason    string `json:"reason"`
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
	// Shadowed is a server saved everywhere that this project's own server
	// of the same name stands in for here.
	Shadowed bool `json:"shadowed,omitempty"`
	// Revision is the n a saved prompt edit got: r<n> or c<n>.
	Revision int `json:"revision,omitempty"`
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
	// Body is a prompt edit's whole text, typed over the proposal.
	Body *string `json:"body"`
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
	case cardPromptEdit:
		out.Summary = "Declined the change to the " + agentTextNoun(c.editKind)
		return out, "Nothing was saved. You may follow the rule for the rest of this epoch, but it will not survive compaction."
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
	case cardPromptEdit:
		return s.applyPromptEdit(ctx, c, e)
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
	// A server saved everywhere under the name of one this project has
	// already is not what this project's sessions get: theirs stands.
	if d.Project == "" && c.projectID != "" {
		if _, ok, err := s.findServer(ctx, d.Name, c.projectID); err != nil {
			return cardOutcome{}, nil, err
		} else if ok {
			out.Shadowed = true
			return out, changed, nil
		}
	}
	out.Live = s.mcpLive(ctx, c.thread, harness, d, out.NeedsSignIn)
	return out, changed, nil
}

// mcpLive pushes a saved server into the thread's live session when the
// harness can take it there, and says when the agent gets it. "" when the
// thread's harness does not run it at all. A server that wants a sign-in is
// refused until it has one (the proxy has no token to put on its requests),
// but the session keeps it, and the card reconnects it after the sign-in.
func (s *Server) mcpLive(ctx context.Context, threadID, harness string, d mcp.Draft, needsSignIn bool) string {
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
	err := s.mgr.ReconnectMCP(ctx, threadID, d.Name)
	switch {
	case err == nil:
		return liveNow
	case errors.Is(err, adapter.ErrMCPUnsupported):
		return liveNextSession
	case needsSignIn:
		return liveAfterSignIn
	}
	s.logf("mcp %s into thread %s: %v", d.Name, threadID, err)
	return liveNextSession
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
	label, phrase := "", ""
	for _, d := range roots.Destinations() {
		if d.Folder == folder {
			label, phrase = d.Label, d.Phrase()
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
		skills.ReleaseStaged(c.stagedID)
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
		Summary:     verb + strings.Join(names, ", ") + " in " + phrase,
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

// applyPromptEdit saves the agent's edit, or the text the user typed over it,
// as a new revision the agent wrote, with the user's quoted words as its
// reason. A prompt revision restarts the live thread onto it, which waits for
// this turn to end; so does a compaction one on a harness that reads its
// instructions only at start. When the text has moved on since the card was raised,
// the edit is made again on the newer text if it still applies there; the
// user's own typed text was written against the old one, so it is refused.
func (s *Server) applyPromptEdit(ctx context.Context, c *heldCard, e cardEdits) (cardOutcome, []string, error) {
	cur, err := s.store.LatestAgentRevision(ctx, c.agentID, c.editKind)
	if err != nil {
		return cardOutcome{}, nil, err
	}
	letter := revisionLetter(c.editKind)
	body, changed := c.after, []string(nil)
	edited := e.Body != nil && *e.Body != c.after
	if edited {
		body, changed = *e.Body, []string{"the text"}
	}
	c.rebased = 0
	if cur.N != c.fromRev {
		if edited {
			return cardOutcome{}, nil, fmt.Errorf("the %s changed to %s%d after this card was raised, and your text was written against %s%d; decline it and ask the agent to propose it again", agentTextNoun(c.editKind), letter, cur.N, letter, c.fromRev)
		}
		if body, err = editAgentText(cur.Body, c.find, c.replace, c.editKind); err != nil {
			return cardOutcome{}, nil, fmt.Errorf("the %s changed to %s%d after this card was raised, and the edit no longer applies to it (%v); decline it and ask the agent to propose it again", agentTextNoun(c.editKind), letter, cur.N, err)
		}
		c.rebased = cur.N
	}
	// Saved on the revision just read, so one saved since is refused rather
	// than overwritten.
	rev, restart, err := s.mgr.SaveAgentRevision(ctx, c.agentID, c.editKind, body, store.AuthorAgent, c.reason, cur.N)
	c.saveNote, c.restart = "", restart
	if reads, readsErr := s.mgr.AgentCompactReads(ctx, c.agentID); readsErr != nil {
		s.logf("compaction reads of agent %s: %v", c.agentID, readsErr)
	} else {
		c.reads = reads
	}
	if err != nil {
		if unchanged := (*store.RevisionUnchangedError)(nil); errors.As(err, &unchanged) {
			return cardOutcome{}, nil, fmt.Errorf("your text is the same as %s%d, so there is nothing to save; decline the card to keep the %s as it is", letter, unchanged.Head, agentTextNoun(c.editKind))
		}
		if rev.N == 0 {
			return cardOutcome{}, nil, err
		}
		// Saved, but the restart or the file behind it failed. Failing the
		// card would have the user approve it again and save it twice.
		s.logf("prompt edit of agent %s: %v", c.agentID, err)
		c.saveNote = err.Error()
	}
	c.saved = body
	return cardOutcome{
		Summary:  fmt.Sprintf("Saved the %s as %s%d", agentTextNoun(c.editKind), letter, rev.N),
		Revision: rev.N,
	}, changed, nil
}

// revisionLetter is how a revision of a kind is named: r3 or c3.
func revisionLetter(kind string) string {
	if kind == store.RevisionCompact {
		return "c"
	}
	return "r"
}

// promptEditText is what the agent is told of its saved prompt edit: when
// it takes effect, which for compaction instructions is the harness's.
func promptEditText(c *heldCard, out cardOutcome, edited bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Saved as revision %s%d. ", revisionLetter(c.editKind), out.Revision)
	prompt := map[string]string{
		thread.RestartPending: "Your system prompt changes when this turn ends.",
		thread.RestartNow:     "Your thread has restarted onto it.",
		thread.RestartNone:    "Your system prompt changes when your thread next starts.",
	}
	atStart := map[string]string{
		thread.RestartPending: "they apply once your thread restarts at the end of this turn.",
		thread.RestartNow:     "your thread has restarted onto them.",
		thread.RestartNone:    "they apply when your thread next starts.",
	}
	switch {
	case c.editKind != store.RevisionCompact:
		b.WriteString(prompt[c.restart])
	case c.reads == adapter.CompactReadsAtCompaction:
		b.WriteString("Your compaction instructions apply at the next compaction.")
	case c.reads == adapter.CompactReadsAtStart:
		b.WriteString("Your harness reads compaction instructions only when it starts, so " + atStart[c.restart])
	default:
		b.WriteString("Your harness does not take compaction instructions, so they are kept but not used.")
	}
	if c.rebased != 0 {
		fmt.Fprintf(&b, " The text had changed to %s%d since you proposed this, so your edit was made on that.", revisionLetter(c.editKind), c.rebased)
	}
	if c.saveNote != "" {
		b.WriteString(" " + c.saveNote + ".")
	}
	if edited {
		b.WriteString(" The user edited it first, so the saved text differs from your proposal. It is now:\n\n" + c.saved)
	}
	return b.String()
}

// --- what the agent is told ---

func savedText(c *heldCard, out cardOutcome, changed []string) string {
	if c.kind == cardPromptEdit {
		return promptEditText(c, out, len(changed) > 0)
	}
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
		switch {
		case out.Shadowed:
			b.WriteString(" This project has its own server of that name, which its sessions keep using; this one reaches every other project.")
		case out.Live == liveNow:
			b.WriteString(" It is connected to this session now; its tools are yours to use.")
		case out.Live == liveNextSession:
			b.WriteString(" This session cannot take it while it runs: its tools arrive in the next session.")
		case out.Live == liveAfterSignIn:
			b.WriteString(" It needs a sign-in, which the user does from the card. Until then this session reports it as failed with an HTTP 401 about its Authorization header: that is the missing sign-in, not a header to change. Once the user signs in it reconnects to this session.")
		default:
			b.WriteString(" This thread's agent does not run this kind of MCP server.")
		}
		if out.NeedsSignIn && out.Live != liveAfterSignIn {
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
