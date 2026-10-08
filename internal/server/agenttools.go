package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/thread"
)

// The tools behind `omniplex mcp`: an agent reads what omniplex gives its
// sessions, and proposes changes as cards the user answers. The agent's
// token names its thread, and the thread's project is the only project the
// tools reach.

// cardWait is how long a write waits on its card before telling the agent
// to look again later. Tests shorten it.
var cardWait = 25 * time.Minute

// maxToolBody bounds a tool's arguments; a SKILL.md is the largest.
const maxToolBody = 2 << 20

// toolError is a refusal the agent gets as its error text.
type toolError struct {
	code int
	msg  string
}

func (e *toolError) Error() string { return e.msg }

func badArgs(format string, a ...any) error {
	return &toolError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

func newID() string { return uuid.NewString() }

// toolThread is the thread a token names, and its project.
type toolThread struct {
	id, harness            string
	projectID, projectName string
}

func (s *Server) handleAgentTool(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		writeError(w, http.StatusNotImplemented, "this server does not take agent tools")
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := s.signer.Check(tok, artefact.KindAgent, time.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "bad agent token")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxToolBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body) > maxToolBody {
		writeError(w, http.StatusRequestEntityTooLarge, "the arguments are too large")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	ctx := r.Context()
	meta, err := s.store.Thread(ctx, claims.Thread)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such thread")
		return
	}
	t := toolThread{id: meta.ID, harness: meta.Harness, projectID: meta.ProjectID}
	if t.projectID != "" {
		if p, err := s.store.Project(ctx, t.projectID); err == nil {
			t.projectName = p.Name
		}
	}

	var (
		text string
		card *heldCard
	)
	switch name := r.PathValue("name"); name {
	case "list_mcp_servers":
		text, err = s.listMCPServers(ctx, t)
	case "list_skills":
		text, err = s.listSkills(ctx, t)
	case "list_sign_ins":
		text, err = s.listSignIns(ctx)
	case cardAddServer, cardRemoveServer, cardInstallSkill, cardCreateSkill, cardRemoveSkill, cardAddSignIn, cardAddAccount:
		card, text, err = s.propose(ctx, t, name, body)
	default:
		writeError(w, http.StatusNotFound, "no tool named "+name)
		return
	}
	if err == nil && card != nil {
		var gone bool
		text, gone, err = s.raiseAndWait(ctx, card, text)
		if gone {
			return // the caller left; the card stays answerable
		}
	}
	if err != nil {
		var te *toolError
		switch {
		case errors.As(err, &te):
			writeError(w, te.code, te.msg)
		case errors.Is(err, thread.ErrClosed):
			writeError(w, http.StatusConflict, "this thread is closed")
		case errors.Is(err, errNoConnections):
			writeError(w, http.StatusNotImplemented, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

// raiseAndWait puts the card on the thread and waits for the user. gone is
// set when the caller went away first.
func (s *Server) raiseAndWait(ctx context.Context, c *heldCard, prompt string) (text string, gone bool, err error) {
	actor, err := s.mgr.View(ctx, c.thread)
	if err != nil {
		c.discard()
		return "", false, err
	}
	// Held before it is raised, so it can be answered the moment it shows.
	s.cards.add(c)
	if err := actor.RaiseCard(ctx, c.id, prompt, c.card); err != nil {
		s.cards.drop(c.id)
		c.discard()
		return "", false, err
	}
	timer := time.NewTimer(cardWait)
	defer timer.Stop()
	select {
	case <-c.done:
		return c.text, false, nil
	case <-ctx.Done():
		return "", true, nil
	case <-timer.C:
		return "The card is still waiting for the user. It stays answerable; when they answer, the result shows in the matching list_ tool. Do not propose it again.", false, nil
	}
}

// --- reads ---

func (s *Server) listMCPServers(ctx context.Context, t toolThread) (string, error) {
	if s.conns == nil {
		return "", errNoConnections
	}
	listing, err := s.conns.List(ctx, t.projectID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, v := range listing.Servers {
		// Without a project, List has every project's; this thread gets
		// none of them.
		if v.Project != "" && v.Project != t.projectID {
			continue
		}
		n++
		scope := "everywhere"
		if v.Project != "" {
			scope = "this project"
		}
		fmt.Fprintf(&b, "- %s (%s): ", v.Name, scope)
		if v.URL != "" {
			b.WriteString(v.URL)
		} else {
			b.WriteString(strings.Join(append([]string{v.Command}, v.Args...), " "))
		}
		status := v.Status
		if v.URL == "" && status == mcp.StatusUnchecked {
			status = "runs locally, not checked"
		}
		fmt.Fprintf(&b, ". Status: %s", status)
		if v.Error != "" {
			fmt.Fprintf(&b, " (%s)", v.Error)
		}
		if names := harnessNames(listing.Harnesses, v.URL, v.Off); len(names) > 0 {
			fmt.Fprintf(&b, ". Harnesses: %s", strings.Join(names, ", "))
		} else {
			b.WriteString(". No harness gets it")
		}
		if len(v.EnvNames) > 0 {
			fmt.Fprintf(&b, ". Env: %s", strings.Join(v.EnvNames, ", "))
		}
		if len(v.HeaderNames) > 0 {
			fmt.Fprintf(&b, ". Headers: %s", strings.Join(v.HeaderNames, ", "))
		}
		if t.projectID != "" && slices.Contains(v.OffIn, t.projectID) {
			b.WriteString(". Off in this project")
		}
		b.WriteString(".\n")
	}
	if n == 0 {
		return "Omniplex gives this thread no MCP servers of its own.", nil
	}
	return "MCP servers Omniplex gives this thread (values are never shown):\n" + b.String(), nil
}

// harnessNames is who gets a server: harnesses that run its kind and are
// not switched off for it.
func harnessNames(hs []mcp.HarnessView, url string, off []string) []string {
	kind := "http"
	if url == "" {
		kind = "stdio"
	}
	var out []string
	for _, h := range hs {
		if slices.Contains(h.Transports, kind) && !slices.Contains(off, h.ID) {
			out = append(out, h.Name)
		}
	}
	return out
}

func (s *Server) listSkills(ctx context.Context, t toolThread) (string, error) {
	roots, _, err := s.mgr.SkillRoots(ctx, t.id, "")
	if err != nil {
		return "", err
	}
	found, err := skills.Discover(roots)
	if err != nil {
		return "", err
	}
	names := map[string]string{}
	for _, d := range s.mgr.Drivers() {
		names[d.ID] = d.Name
	}
	var b strings.Builder
	for _, sk := range found {
		fmt.Fprintf(&b, "- %s: %s", sk.Name, skillWhere(sk))
		var hs []string
		for _, h := range sk.Harnesses {
			if n := names[string(h)]; n != "" {
				hs = append(hs, n)
			} else {
				hs = append(hs, string(h))
			}
		}
		if len(hs) > 0 {
			fmt.Fprintf(&b, ". Harnesses: %s", strings.Join(hs, ", "))
		}
		switch {
		case sk.Mode == skills.ModeOff:
			b.WriteString(". Off")
		case sk.Mode == skills.ModeManual || sk.UserOnly:
			b.WriteString(". Manual only: the user names it to run it")
		}
		if !sk.Editable {
			b.WriteString(". Read-only")
		}
		if sk.Problem != "" {
			fmt.Fprintf(&b, ". Problem: %s", sk.Problem)
		}
		b.WriteString(".\n")
	}
	if b.Len() == 0 {
		return "This thread has no skills.", nil
	}
	return "Skills for this thread:\n" + b.String(), nil
}

// skillWhere is where a skill lives, in the words the tools use.
func skillWhere(sk skills.Skill) string {
	switch sk.Scope {
	case skills.ScopeProject:
		if sk.Private {
			return "project (this project's own)"
		}
		if sk.Folder != "" {
			return "repo (" + filepath.Base(sk.Folder) + ")"
		}
		return "repo"
	case skills.ScopeUser:
		return "personal"
	case skills.ScopePlugin:
		return "plugin " + sk.Plugin
	case skills.ScopeOmniplex:
		return "bundled with Omniplex"
	case skills.ScopeSystem:
		return "built into the harness"
	}
	return sk.Scope
}

func (s *Server) listSignIns(ctx context.Context) (string, error) {
	if s.conns == nil {
		return "", errNoConnections
	}
	listing, err := s.conns.List(ctx, "")
	if err != nil {
		return "", err
	}
	if len(listing.CLIs) == 0 {
		return "Omniplex holds no sign-ins.", nil
	}
	// Fresh statuses: whether an account is signed in is the point.
	clis := make([]mcp.CLIView, len(listing.CLIs))
	var wg sync.WaitGroup
	for i, cli := range listing.CLIs {
		clis[i] = cli
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := s.conns.CheckCLI(ctx, cli.ID); err == nil {
				clis[i] = v
			}
		}()
	}
	wg.Wait()
	var b strings.Builder
	b.WriteString("Sign-ins Omniplex holds. Run a tool's commands with an account's env set to act as that account:\n")
	for _, cli := range clis {
		fmt.Fprintf(&b, "- %s (id %s)", cli.Name, cli.ID)
		if len(cli.Accounts) == 0 {
			b.WriteString(": no accounts yet.\n")
			continue
		}
		b.WriteString(":\n")
		for _, a := range cli.Accounts {
			fmt.Fprintf(&b, "  - %s: %s", a.Name, strings.ReplaceAll(a.Status, "_", " "))
			env := mcp.ExpandAccountEnv(a.Env)
			if len(env) > 0 {
				var pairs []string
				for _, k := range sortedKeys(env) {
					pairs = append(pairs, k+"="+shellQuote(env[k]))
				}
				fmt.Fprintf(&b, ". Env: %s", strings.Join(pairs, " "))
			}
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,~-]*$`)

func shellQuote(v string) string {
	if v != "" && shellSafe.MatchString(v) {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// findServer finds the server of a name in one scope: a project, or ""
// for everywhere.
func (s *Server) findServer(ctx context.Context, name, project string) (mcp.ServerView, bool, error) {
	listing, err := s.conns.List(ctx, project)
	if err != nil {
		return mcp.ServerView{}, false, err
	}
	for _, v := range listing.Servers {
		if v.Name == name && v.Project == project {
			return v, true, nil
		}
	}
	return mcp.ServerView{}, false, nil
}

// --- proposals ---

// propose checks a write's arguments and builds its card, with the
// one-line prompt that goes on the thread.
func (s *Server) propose(ctx context.Context, t toolThread, kind string, body []byte) (*heldCard, string, error) {
	c := newHeldCard(t, kind)
	card := cardJSON{Kind: kind, ProjectID: t.projectID, ProjectName: t.projectName}
	var (
		prompt string
		err    error
	)
	switch kind {
	case cardAddServer, cardRemoveServer, cardAddSignIn, cardAddAccount:
		if s.conns == nil {
			return nil, "", errNoConnections
		}
	}
	switch kind {
	case cardAddServer:
		prompt, err = s.proposeAddServer(ctx, t, c, &card, body)
	case cardRemoveServer:
		prompt, err = s.proposeRemoveServer(ctx, t, c, &card, body)
	case cardInstallSkill, cardCreateSkill:
		prompt, err = s.proposeSkill(ctx, t, c, &card, body)
	case cardRemoveSkill:
		prompt, err = s.proposeRemoveSkill(ctx, t, c, &card, body)
	case cardAddSignIn:
		prompt, err = s.proposeAddSignIn(ctx, c, &card, body)
	case cardAddAccount:
		prompt, err = s.proposeAddAccount(ctx, c, &card, body)
	}
	if err != nil {
		c.discard()
		return nil, "", err
	}
	if c.card, err = json.Marshal(card); err != nil {
		c.discard()
		return nil, "", err
	}
	return c, prompt, nil
}

func decodeArgs(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return badArgs("arguments: %v", err)
	}
	return nil
}

// mcpScope reads a scope argument: the project's ID ("" for everywhere)
// and the scope's name.
func (t toolThread) mcpScope(arg string) (project, scope string, err error) {
	switch arg {
	case "":
		if t.projectID != "" {
			return t.projectID, "project", nil
		}
		return "", "everywhere", nil
	case "project":
		if t.projectID == "" {
			return "", "", badArgs("this thread is not in a project, so the scope can only be everywhere")
		}
		return t.projectID, "project", nil
	case "everywhere":
		return "", "everywhere", nil
	}
	return "", "", badArgs("scope is project or everywhere, not %q", arg)
}

func (s *Server) proposeAddServer(ctx context.Context, t toolThread, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		Config string `json:"config"`
		Scope  string `json:"scope"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	project, scope, err := t.mcpScope(a.Scope)
	if err != nil {
		return "", err
	}
	d, err := mcp.Parse(a.Config)
	if err != nil {
		return "", badArgs("could not read that config: %v", err)
	}
	if err := mcp.CheckName(d.Name); err != nil {
		return "", badArgs("%v", err)
	}
	if d.Name == mcp.ReservedName {
		return "", badArgs("%q is omniplex's own server", d.Name)
	}
	d.Project = project
	c.draft, c.scope = d, scope

	listing, err := s.conns.List(ctx, project)
	if err != nil {
		return "", err
	}
	for _, v := range listing.Servers {
		if v.Name == d.Name && v.Project == project {
			card.Replaces = true
		}
	}
	kind := "http"
	if d.URL == "" {
		kind = "stdio"
	}
	hs := []cardHarness{}
	for _, h := range listing.Harnesses {
		if slices.Contains(h.Transports, kind) {
			hs = append(hs, cardHarness{ID: h.ID, Name: h.Name})
		}
	}
	card.Harnesses = &hs
	held := func(values map[string]string) []cardSecret {
		var out []cardSecret
		for _, name := range sortedKeys(values) {
			out = append(out, cardSecret{Name: name, Held: values[name] != ""})
		}
		return out
	}
	card.Scope = scope
	card.Server = &cardServer{Name: d.Name, URL: d.URL, Command: d.Command, Args: d.Args, Env: held(d.Env), Headers: held(d.Headers)}
	if scope == "project" {
		return "Add the MCP server " + d.Name + " to this project", nil
	}
	return "Add the MCP server " + d.Name + " everywhere", nil
}

func (s *Server) proposeRemoveServer(ctx context.Context, t toolThread, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	project, scope, err := t.mcpScope(a.Scope)
	if err != nil {
		return "", err
	}
	listing, err := s.conns.List(ctx, project)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(listing.Servers, func(v mcp.ServerView) bool { return v.Name == a.Name && v.Project == project })
	ok := i >= 0
	if !ok {
		if scope == "project" {
			return "", badArgs("this project has no MCP server named %q of its own; list_mcp_servers says which scope each is in", a.Name)
		}
		return "", badArgs("there is no MCP server named %q that goes everywhere", a.Name)
	}
	v := listing.Servers[i]
	c.removeName, c.removeProject, c.scope = v.Name, v.Project, scope
	hs := []cardHarness{}
	for _, h := range listing.Harnesses {
		if slices.Contains(harnessNames([]mcp.HarnessView{h}, v.URL, v.Off), h.Name) {
			hs = append(hs, cardHarness{ID: h.ID, Name: h.Name})
		}
	}
	card.Harnesses = &hs
	detail := v.URL
	if detail == "" {
		detail = strings.Join(append([]string{v.Command}, v.Args...), " ")
	}
	card.Scope = scope
	card.Remove = &cardRemove{Name: v.Name, Detail: detail, Scope: scope}
	if scope == "project" {
		return "Remove the MCP server " + v.Name + " from this project", nil
	}
	return "Remove the MCP server " + v.Name + ", which goes everywhere", nil
}

// destination reads a destination argument into one of the thread's.
func (t toolThread) destination(roots skills.Roots, arg string) (skills.Destination, error) {
	dests := roots.Destinations()
	if arg == "" {
		def := roots.DefaultDestination()
		for _, d := range dests {
			if d.Folder == def {
				return d, nil
			}
		}
		return dests[len(dests)-1], nil
	}
	switch arg {
	case skills.DestProject, skills.DestRepo:
		if t.projectID == "" {
			return skills.Destination{}, badArgs("this thread is not in a project, so a skill can only be personal")
		}
	case skills.DestPersonal:
	default:
		return skills.Destination{}, badArgs("destination is project, repo or personal, not %q", arg)
	}
	for _, d := range dests {
		if d.Kind == arg {
			return d, nil
		}
	}
	return skills.Destination{}, badArgs("this project has no %s for skills to go in", arg)
}

func (s *Server) proposeSkill(ctx context.Context, t toolThread, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		Source      string   `json:"source"`
		Skills      []string `json:"skills"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Content     string   `json:"content"`
		Destination string   `json:"destination"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	roots, _, err := s.mgr.SkillRoots(ctx, t.id, "")
	if err != nil {
		return "", err
	}
	dest, err := t.destination(roots, a.Destination)
	if err != nil {
		return "", err
	}
	if c.kind == cardCreateSkill {
		a.Name, a.Description = strings.TrimSpace(a.Name), strings.TrimSpace(a.Description)
		if err := skills.ValidateName(a.Name); err != nil {
			return "", badArgs("%v", err)
		}
		if a.Description == "" {
			return "", badArgs("a skill needs a description")
		}
		if err := skills.CheckContent(a.Name, a.Content); err != nil {
			return "", badArgs("%v", err)
		}
		c.skillName, c.description, c.content = a.Name, a.Description, a.Content
		card.Skill = &cardSkill{Name: a.Name, Description: a.Description, Content: a.Content}
	} else {
		source := strings.TrimSpace(a.Source)
		if source == "" {
			return "", badArgs("give the source to install from")
		}
		staged, err := s.skillFetch.Stage(ctx, roots, source)
		if err != nil {
			return "", badArgs("could not fetch %s: %v", source, err)
		}
		c.stagedID = staged.ID
		skills.HoldStaged(staged.ID)
		if len(a.Skills) > 0 {
			for _, name := range a.Skills {
				if !slices.ContainsFunc(staged.Skills, func(sk skills.StagedSkill) bool { return sk.Name == name }) {
					return "", badArgs("%s has no skill named %q", source, name)
				}
			}
			for i := range staged.Skills {
				staged.Skills[i].Picked = slices.Contains(a.Skills, staged.Skills[i].Name)
			}
		}
		if len(staged.Skills) == 0 {
			return "", badArgs("%s has no skills", source)
		}
		if len(staged.Skills) == 1 && staged.Skills[0].Problem == "" {
			staged.Skills[0].Picked = true
		}
		cs := &cardStaged{ID: staged.ID, Source: source}
		for _, sk := range staged.Skills {
			if sk.Picked {
				c.picked = append(c.picked, sk.Name)
			}
			files := sk.Files
			if files == nil {
				files = []skills.File{}
			}
			cs.Skills = append(cs.Skills, cardStagedSkill{Name: sk.Name, Description: sk.Description, Files: files, Picked: sk.Picked, Problem: sk.Problem})
		}
		card.Staged = cs
	}
	c.destination = dest.Folder
	c.claudeHad = map[string]bool{}
	for _, d := range roots.Destinations() {
		c.claudeHad[d.Folder] = dirExists(claudeSkillsDir(roots, d.Folder))
	}
	hs := s.harnessList(nil)
	card.Harnesses = &hs
	folder := dest.Folder
	card.Destination = &folder
	card.Destinations = roots.Destinations()
	if c.kind == cardCreateSkill {
		return "Create the skill " + a.Name + " in " + dest.Phrase(), nil
	}
	return "Install skills from " + card.Staged.Source + " into " + dest.Phrase(), nil
}

func (s *Server) proposeRemoveSkill(ctx context.Context, t toolThread, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	roots, _, err := s.mgr.SkillRoots(ctx, t.id, "")
	if err != nil {
		return "", err
	}
	found, err := skills.Discover(roots)
	if err != nil {
		return "", err
	}
	readOnly := false
	for _, sk := range found {
		if sk.Name != a.Name {
			continue
		}
		if !sk.Editable {
			readOnly = true
			continue
		}
		c.removeName, c.removeDir = sk.Name, sk.Dir
		detail := sk.Dir
		if roots.Home != "" {
			if rel, err := filepath.Rel(roots.Home, sk.Dir); err == nil && filepath.IsLocal(rel) {
				detail = "~/" + filepath.ToSlash(rel)
			}
		}
		card.Remove = &cardRemove{Name: sk.Name, Detail: detail, Scope: skillWhere(sk)}
		hs := s.harnessList(sk.Harnesses)
		card.Harnesses = &hs
		return "Remove the skill " + sk.Name, nil
	}
	if readOnly {
		return "", badArgs("%s is read-only: omniplex cannot remove it", a.Name)
	}
	return "", badArgs("this thread has no skill named %q", a.Name)
}

var notIDChar = regexp.MustCompile(`[^a-z0-9_-]+`)

func (s *Server) proposeAddSignIn(ctx context.Context, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		Definition cliDefinition `json:"definition"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	def := a.Definition
	def.Name = strings.TrimSpace(def.Name)
	if def.ID == "" {
		def.ID = strings.Trim(notIDChar.ReplaceAllString(strings.ToLower(def.Name), "-"), "-_")
	}
	if err := mcp.CheckName(def.ID); err != nil {
		return "", badArgs("id: %v", err)
	}
	switch {
	case def.Name == "":
		return "", badArgs("a sign-in needs the tool's name")
	case strings.TrimSpace(def.StatusCommand) == "":
		return "", badArgs("give the command that checks whether an account is signed in")
	case strings.TrimSpace(def.SignInCommand) == "":
		return "", badArgs("give the command that signs an account in")
	}
	if def.SignedInPattern != "" {
		if _, err := regexp.Compile(def.SignedInPattern); err != nil {
			return "", badArgs("signedInPattern: %v", err)
		}
	}
	for i, acct := range def.Accounts {
		def.Accounts[i] = strings.TrimSpace(acct)
		if err := mcp.CheckName(def.Accounts[i]); err != nil {
			return "", badArgs("account %q: %v", acct, err)
		}
	}
	listing, err := s.conns.List(ctx, "")
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(listing.CLIs, func(v mcp.CLIView) bool { return v.ID == def.ID }) {
		return "", badArgs("there is already a sign-in with id %q; add an account to it with add_account", def.ID)
	}
	c.cli = def
	card.CLI = &def
	return "Add a sign-in for " + def.Name, nil
}

func (s *Server) proposeAddAccount(ctx context.Context, c *heldCard, card *cardJSON, body []byte) (string, error) {
	var a struct {
		CLI  string `json:"cli"`
		Name string `json:"name"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	a.Name = strings.TrimSpace(a.Name)
	if err := mcp.CheckName(a.Name); err != nil {
		return "", badArgs("account: %v", err)
	}
	listing, err := s.conns.List(ctx, "")
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(listing.CLIs, func(v mcp.CLIView) bool { return v.ID == a.CLI })
	if i < 0 {
		return "", badArgs("there is no sign-in with id %q; list_sign_ins has them", a.CLI)
	}
	cli := listing.CLIs[i]
	if slices.ContainsFunc(cli.Accounts, func(v mcp.AccountView) bool { return v.Name == a.Name }) {
		return "", badArgs("%s already has an account named %q", cli.Name, a.Name)
	}
	c.accountCLI, c.accountCLIName, c.accountName = cli.ID, cli.Name, a.Name
	card.Account = &cardAccount{CLI: cli.ID, CLIName: cli.Name, Name: a.Name}
	return "Add the account " + a.Name + " to " + cli.Name, nil
}

// harnessList is the harnesses here, or those of them named in only.
func (s *Server) harnessList(only []skills.Harness) []cardHarness {
	out := []cardHarness{}
	for _, d := range s.mgr.Drivers() {
		if only == nil || slices.Contains(only, skills.Harness(d.ID)) {
			out = append(out, cardHarness{ID: d.ID, Name: d.Name})
		}
	}
	return out
}

// --- keeping values out of the log ---

// isAddServerTool says whether a stored tool call is add_mcp_server: Claude
// names it mcp__omniplex__add_mcp_server and Codex omniplex/add_mcp_server.
// Any name ending so counts, so a harness that spells it some other way is
// masked too; masking another server's tool of that name costs nothing.
func isAddServerTool(name string) bool {
	return strings.HasSuffix(name, cardAddServer)
}

// redacted stands in for a value in a stored tool input.
const redacted = "••••"

// RedactAgentToolInput keeps the values an agent passed to add_mcp_server
// out of a stored tool call: every env and header value its config holds is
// masked wherever it appears in the input. A config that cannot be read is
// masked whole, since what in it is a value cannot be told. Every other
// tool's input is returned as it is.
func RedactAgentToolInput(toolName string, input json.RawMessage) json.RawMessage {
	if !isAddServerTool(toolName) || len(input) == 0 {
		return input
	}
	var v any
	if err := json.Unmarshal(input, &v); err != nil {
		return input
	}
	var configs []string
	collectConfigs(v, &configs)
	if len(configs) == 0 {
		return input
	}
	var secrets []string
	unreadable := false
	for _, cfg := range configs {
		d, err := mcp.Parse(cfg)
		if err != nil {
			unreadable = true
			continue
		}
		values := configValues(cfg)
		for _, m := range []map[string]string{d.Env, d.Headers} {
			for _, val := range m {
				values = append(values, val)
			}
		}
		for _, val := range values {
			secrets = append(secrets, secretForms(val)...)
		}
	}
	// Longest first, so a value is masked whole before a part of it is.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	v = maskValues(v, secrets, unreadable, "")
	out, err := json.Marshal(v)
	if err != nil {
		return input
	}
	return out
}

func collectConfigs(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && k == "config" {
				*out = append(*out, s)
				continue
			}
			collectConfigs(val, out)
		}
	case []any:
		for _, val := range x {
			collectConfigs(val, out)
		}
	}
}

// configValues is every env and header value in a JSON or TOML config, of
// every server in it: Parse reads only the first, and the others' values are
// in the stored input all the same. Nil for a command line.
func configValues(cfg string) []string {
	cfg = strings.TrimSpace(cfg)
	var doc any
	switch {
	case strings.HasPrefix(cfg, "{") || strings.HasPrefix(cfg, `"`):
		if json.Unmarshal([]byte(cfg), &doc) != nil {
			// A fragment copied out of a bigger file, as Parse takes it.
			_ = json.Unmarshal([]byte("{"+strings.TrimSuffix(cfg, ",")+"}"), &doc)
		}
	case strings.HasPrefix(cfg, "[") || strings.HasPrefix(cfg, "mcp_servers."):
		var m map[string]any
		if _, err := toml.Decode(cfg, &m); err == nil {
			doc = m
		}
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if values, ok := val.(map[string]any); ok && valueKeys[strings.ToLower(k)] {
					for _, value := range values {
						if s, ok := value.(string); ok {
							out = append(out, s)
						}
					}
					continue
				}
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	walk(doc)
	return out
}

// valueKeys are the keys a config keeps env and header values under.
var valueKeys = map[string]bool{"env": true, "headers": true, "http_headers": true}

// secretForms is a value and, for one with a scheme in front ("Bearer
// abc"), the credential alone, which is how a config can give it. Each also
// in the escaped form a JSON or TOML string writes it in, which is how it
// stands in a config pasted as either.
func secretForms(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	plain := []string{val}
	if _, rest, ok := strings.Cut(val, " "); ok && strings.TrimSpace(rest) != "" {
		plain = append(plain, strings.TrimSpace(rest))
	}
	out := plain
	for _, p := range plain {
		quoted := strconv.Quote(p)
		if esc := quoted[1 : len(quoted)-1]; esc != p {
			out = append(out, esc)
		}
		if b, err := json.Marshal(p); err == nil {
			if esc := string(b[1 : len(b)-1]); esc != p {
				out = append(out, esc)
			}
		}
	}
	return out
}

func maskValues(v any, secrets []string, unreadable bool, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = maskValues(val, secrets, unreadable, k)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = maskValues(val, secrets, unreadable, "")
		}
		return x
	case string:
		if key == "config" && unreadable {
			return redacted
		}
		return maskIn(x, secrets)
	}
	return v
}

// maskIn masks each secret in s where it stands alone: not run into a
// letter or digit on either side, so a short value does not take bites out
// of words.
func maskIn(s string, secrets []string) string {
	for _, secret := range secrets {
		var b strings.Builder
		rest := s
		for {
			i := strings.Index(rest, secret)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			end := i + len(secret)
			if wordByte(rest, i-1) || wordByte(rest, end) {
				b.WriteString(rest[:end])
			} else {
				b.WriteString(rest[:i])
				b.WriteString(redacted)
			}
			rest = rest[end:]
		}
		s = b.String()
	}
	return s
}

func wordByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
