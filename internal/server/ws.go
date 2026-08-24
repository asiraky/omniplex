package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// conn is one presenter connection. It holds no session state of its own —
// everything a UI needs is reconstructible from the log.
type conn struct {
	srv *Server
	ws  *websocket.Conn
	id  string
	ctx context.Context

	// deviceID is the device that authorised the upgrade, so the connection
	// can be cut when that device is revoked.
	deviceID string

	wmu sync.Mutex

	amu      sync.Mutex
	attached map[string]context.CancelFunc
}

func (s *Server) handleWS(ws *websocket.Conn, ctx context.Context, deviceID string) {
	c := &conn{
		srv:      s,
		ws:       ws,
		id:       uuid.NewString(),
		ctx:      ctx,
		deviceID: deviceID,
		attached: map[string]context.CancelFunc{},
	}
	defer c.detachAll()

	// Authorisation is checked once, at upgrade. A socket therefore outlives
	// the credential that opened it unless something closes it, which is what
	// this registration is for: revoking a stolen device has to cut the
	// connection it already holds, not merely refuse the next one.
	s.register(c)
	defer s.unregister(c)

	// Session-list changes push a fresh welcome-shaped frame.
	listID, listCh := s.mgr.SubscribeList()
	defer s.mgr.UnsubscribeList(listID)

	// Harness changes push the harness list on its own. A model catalogue is
	// read from the harness in the background, so it can land seconds after
	// the welcome frame — a client that only ever learned the list at connect
	// would show the fallback until it reconnected.
	harnessID, harnessCh := s.mgr.SubscribeHarnesses()
	defer s.mgr.UnsubscribeHarnesses(harnessID)

	// Label definitions are user-level and shared across paired devices, so a
	// change on one device pushes the whole list to every connection.
	labelsID, labelsCh := s.mgr.SubscribeLabels()
	defer s.mgr.UnsubscribeLabels(labelsID)

	// The project registry is machine-level and shared across paired devices,
	// so adding, editing or removing one pushes the whole list to every
	// connection rather than waiting for each to reconnect.
	projectsID, projectsCh := s.mgr.SubscribeProjects()
	defer s.mgr.UnsubscribeProjects(projectsID)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-listCh:
				c.sendSessions()
			case <-harnessCh:
				c.send(serverFrame{Type: "harnesses", Harnesses: s.mgr.Harnesses(ctx)})
			case <-labelsCh:
				c.sendLabels()
			case <-projectsCh:
				c.sendProjects()
			}
		}
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var f clientFrame
		if err := json.Unmarshal(data, &f); err != nil {
			c.send(serverFrame{Type: "error", Error: "malformed frame"})
			continue
		}
		c.dispatch(f)
	}
}

func (c *conn) dispatch(f clientFrame) {
	switch f.Type {
	case "hello":
		sessions, _ := c.srv.mgr.List(c.ctx)
		projects, _ := c.srv.mgr.Projects(c.ctx)
		labels, _ := c.srv.mgr.Labels(c.ctx)
		c.send(serverFrame{
			Type:      "welcome",
			ServerID:  c.srv.id,
			Build:     c.srv.web.BuildID(),
			Sessions:  sessions,
			Harnesses: c.srv.mgr.Harnesses(c.ctx),
			Projects:  projects,
			Labels:    labels,
			Cwd:       c.srv.defaultCwd,
			Access:    c.srv.access(c.ctx),
		})

	case "attach":
		c.attach(f)

	case "detach":
		c.detach(f.SessionID)

	case "command":
		// Off the read loop: creating a session spawns a harness process and
		// a prompt can wait on the actor, neither of which may stall the
		// connection's other frames. Acks carry their commandId, so order
		// does not matter.
		go c.command(f)

	case "ping":
		c.send(serverFrame{Type: "pong"})
	}
}

func (c *conn) sendSessions() {
	sessions, err := c.srv.mgr.List(c.ctx)
	if err != nil {
		return
	}
	c.send(serverFrame{Type: "sessions", Sessions: sessions})
}

func (c *conn) sendProjects() {
	projects, err := c.srv.mgr.Projects(c.ctx)
	if err != nil {
		return
	}
	c.send(serverFrame{Type: "projects", Projects: projects})
}

func (c *conn) sendLabels() {
	labels, err := c.srv.mgr.Labels(c.ctx)
	if err != nil {
		return
	}
	c.send(serverFrame{Type: "labels", Labels: labels})
}

// attach implements the ordering the spec calls load-bearing: subscribe first,
// then read history, then mark synchronized, then drain what buffered.
func (c *conn) attach(f clientFrame) {
	started := time.Now()
	if f.SessionID == "" {
		c.send(serverFrame{Type: "error", Error: "attach requires sessionId"})
		return
	}
	c.detach(f.SessionID) // re-attach is idempotent

	actor, err := c.srv.mgr.View(c.ctx, f.SessionID)
	if err != nil {
		c.send(serverFrame{Type: "error", SessionID: f.SessionID, Error: err.Error()})
		return
	}
	restoreDuration := time.Since(started)

	// 1. Subscribe first, so nothing can land in the gap between the read
	//    below and the start of live delivery.
	sub := actor.Subscribe()

	after := int64(0)
	hasCursor := f.AfterSeq != nil
	if hasCursor {
		after = *f.AfterSeq
	}

	res, err := actor.Attach(c.ctx, after, hasCursor)
	if err != nil {
		actor.Unsubscribe(sub)
		c.send(serverFrame{Type: "error", SessionID: f.SessionID, Error: err.Error()})
		return
	}

	payloadBytes := 0
	switch res.Kind {
	case session.AttachSnapshot:
		state := res.Snapshot
		payloadBytes += c.send(serverFrame{Type: "snapshot", SessionID: f.SessionID, Seq: res.Seq, State: state})
	default:
		for i := range res.Events {
			ev := res.Events[i]
			payloadBytes += c.send(serverFrame{Type: "event", SessionID: f.SessionID, Seq: ev.Seq, Event: &ev})
		}
	}
	payloadBytes += c.send(serverFrame{Type: "synchronized", SessionID: f.SessionID, Seq: res.Seq})
	c.srv.logf("session_attach session=%s duration_ms=%d restore_ms=%d payload_bytes=%d kind=%s seq=%d",
		f.SessionID, time.Since(started).Milliseconds(), restoreDuration.Milliseconds(), payloadBytes, res.Kind, res.Seq)

	ctx, cancel := context.WithCancel(c.ctx)
	c.amu.Lock()
	c.attached[f.SessionID] = func() {
		cancel()
		actor.Unsubscribe(sub)
	}
	c.amu.Unlock()

	// 2. Drain the live queue. Events at or below the catch-up point are
	//    dropped here; the client also discards seq <= lastApplied.
	go func() {
		defer actor.Unsubscribe(sub)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.Resync:
				c.send(serverFrame{Type: "resync", SessionID: f.SessionID})
				return
			case <-sub.ComposerChanged:
				c.send(serverFrame{Type: "composer_items_changed", SessionID: f.SessionID})
			case ev, ok := <-sub.Ch:
				if !ok {
					return
				}
				if ev.Seq <= res.Seq {
					continue
				}
				c.send(serverFrame{Type: "event", SessionID: f.SessionID, Seq: ev.Seq, Event: &ev})
			}
		}
	}()
}

func (c *conn) detach(sessionID string) {
	c.amu.Lock()
	cancel, ok := c.attached[sessionID]
	delete(c.attached, sessionID)
	c.amu.Unlock()
	if ok {
		cancel()
	}
}

func (c *conn) detachAll() {
	c.amu.Lock()
	all := c.attached
	c.attached = map[string]context.CancelFunc{}
	c.amu.Unlock()
	for _, cancel := range all {
		cancel()
	}
}

// commandTimeout bounds one command. Sixty seconds is the rule: every command
// here either talks to a local process or reads the log, and one that has not
// answered in a minute is wedged.
//
// Summarising is the exception, and honestly so. It starts a cold harness
// against a model and waits for prose, which on a slow machine or a long
// transcript is minutes rather than seconds — and the session-level timeout it
// runs under is shorter than this, so the harness still gets the last word on
// giving up.
func commandTimeout(command string) time.Duration {
	if command == "summarize_session" {
		return 5 * time.Minute
	}
	return 60 * time.Second
}

// sessionOfArgs reads the session a command is about out of its arguments.
//
// Clients address a session inside args rather than on the frame, so the
// stored command row would otherwise have no session and survive that
// session's deletion. That matters most for a summary, whose stored result is
// model-written prose about the transcript.
func sessionOfArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ""
	}
	return a.SessionID
}

// command executes an idempotent command. A repeated commandId replays the
// stored result rather than re-executing, so a retry after a dropped
// connection cannot double-send a prompt.
func (c *conn) command(f clientFrame) {
	if f.CommandID == "" {
		f.CommandID = uuid.NewString()
	}
	if f.SessionID == "" {
		f.SessionID = sessionOfArgs(f.Args)
	}

	// The command ledger exists so that retrying a dropped mutation cannot
	// run it twice — it is a record of user operations. A poll is not one:
	// nothing is mutated, replaying it would return a stale answer rather
	// than protect anything, and a row per poll would grow the table for as
	// long as the tab stayed open. So a poll executes without a ledger entry.
	if pollingCommand(f.Command) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		result, err := c.execute(ctx, f)
		c.ack(f.CommandID, result, err)
		return
	}

	// A command belongs to the user operation, not to the socket that happened
	// to carry it. Let it finish and persist its result after a disconnect so a
	// reconnect can recover the acknowledgement with the same command id.
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout(f.Command))
	defer cancel()

	for {
		stored, done, err := c.srv.store.ClaimCommand(ctx, f.CommandID, f.SessionID)
		switch {
		case errors.Is(err, store.ErrCommandInProgress):
			select {
			case <-time.After(25 * time.Millisecond):
				continue
			case <-ctx.Done():
				c.ack(f.CommandID, nil, ctx.Err())
				return
			}
		case err != nil:
			c.ack(f.CommandID, nil, err)
			return
		case done:
			c.send(serverFrame{Type: "ack", CommandID: f.CommandID, Result: stored})
			return
		default:
			goto claimed
		}
	}

claimed:
	result, err := c.execute(ctx, f)
	if err != nil {
		// A failed command is not done; release the claim so retrying this exact
		// user operation can try again.
		_ = c.srv.store.ReleaseCommand(context.Background(), f.CommandID)
		c.ack(f.CommandID, nil, err)
		return
	}
	if err := c.srv.store.CompleteCommand(context.Background(), f.CommandID, result); err != nil {
		c.ack(f.CommandID, nil, fmt.Errorf("command ran but its result could not be persisted: %w", err))
		return
	}
	c.ack(f.CommandID, result, nil)
}

// pollingCommand reports whether a command is a pure read the client issues on
// a timer rather than a user issuing it once. Kept to exactly the commands that
// are polled: everything else, including every read a user's own click causes,
// keeps the replay guarantee it has always had.
func pollingCommand(name string) bool { return name == "session_pr" }

func (c *conn) execute(ctx context.Context, f clientFrame) (any, error) {
	switch f.Command {
	case "create_session":
		var a createArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		if a.ProjectID != "" {
			actor, err := c.srv.mgr.CreateProject(ctx, session.CreateProjectOptions{ProjectID: a.ProjectID, Harness: a.Harness, Instance: a.Instance, Model: a.Model, Mode: a.Mode, Branch: a.Branch, Workspace: a.Workspace, WorkspacePath: a.WorkspacePath, BaseRef: a.BaseRef})
			if err != nil {
				return nil, err
			}
			return map[string]any{"sessionId": actor.ID}, nil
		}
		if a.Cwd == "" {
			a.Cwd = c.srv.defaultCwd
		}
		actor, err := c.srv.mgr.Create(ctx, a.Harness, a.Instance, a.Cwd, a.Model, a.Mode)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessionId": actor.ID}, nil

	case "prompt":
		var a promptArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		var images []proto.PromptImage
		if len(a.ImageIDs) > 0 {
			if c.srv.attachments == nil {
				return nil, errors.New("this server does not store attachments")
			}
			if len(a.ImageIDs) > attachment.MaxPerPrompt {
				return nil, fmt.Errorf("a message may carry at most %d images", attachment.MaxPerPrompt)
			}
			metas, paths, resolveErr := c.srv.attachments.Resolve(a.SessionID, a.ImageIDs)
			if resolveErr != nil {
				return nil, resolveErr
			}
			for i, m := range metas {
				images = append(images, proto.PromptImage{ID: m.ID, MediaType: m.MediaType, Path: paths[i]})
			}
		}
		turnID, err := actor.Prompt(ctx, a.Text, images)
		if err != nil {
			return nil, err
		}
		c.srv.mgr.NotifyList()
		return map[string]any{"turnId": turnID}, nil

	case "continue_session":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		turnID, err := actor.Continue(ctx)
		if err != nil {
			return nil, err
		}
		c.srv.mgr.NotifyList()
		return map[string]any{"turnId": turnID}, nil

	case "cancel":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, ok := c.srv.mgr.Peek(a.SessionID)
		if !ok {
			return map[string]any{"status": "idle"}, nil
		}
		if err := actor.Cancel(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"status": "cancelling"}, nil

	case "set_mode":
		var a setModeArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		if err := actor.SetMode(ctx, a.Mode); err != nil {
			return nil, err
		}
		return map[string]any{"mode": a.Mode}, nil

	case "set_model":
		var a setModelArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		if err := actor.SetModel(ctx, a.Model); err != nil {
			return nil, err
		}
		return map[string]any{"model": a.Model}, nil

	case "set_effort":
		var a setEffortArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		if err := actor.SetEffort(ctx, a.Effort); err != nil {
			return nil, err
		}
		return map[string]any{"effort": a.Effort}, nil

	case "create_label":
		var a createLabelArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		label, err := c.srv.mgr.CreateLabel(ctx, a.Name, a.Color)
		if err != nil {
			return nil, err
		}
		return map[string]any{"label": label}, nil

	case "save_label":
		var a saveLabelArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		label, err := c.srv.mgr.SaveLabel(ctx, store.Label{
			ID: a.LabelID, Name: a.Name, Color: a.Color, Position: a.Position,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"label": label}, nil

	case "delete_label":
		var a deleteLabelArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		if err := c.srv.mgr.DeleteLabel(ctx, a.LabelID); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleted"}, nil

	case "set_session_label":
		var a setSessionLabelArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		if err := c.srv.mgr.SetSessionLabel(ctx, a.SessionID, a.LabelID); err != nil {
			return nil, err
		}
		return map[string]any{"labelId": a.LabelID}, nil

	case "list_composer_items":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		items, err := actor.ComposerItems(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil

	case "run_composer_action":
		var a runComposerActionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, err := c.srv.mgr.Get(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		return actor.RunComposerAction(ctx, a.Action, a.Args, a.Invocation)

	case "resolve_permission":
		var a resolveArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, ok := c.srv.mgr.Peek(a.SessionID)
		if !ok {
			return nil, errors.New("session is not live")
		}
		err := actor.ResolvePermission(ctx, a.RequestID, adapter.PermissionOutcome{
			Outcome: normaliseOutcome(a.Outcome), OptionID: a.OptionID,
		})
		if errors.Is(err, session.ErrAlreadyResolved) {
			return map[string]any{"status": "already_resolved"}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "resolved"}, nil

	case "resolve_elicitation":
		var a resolveElicitationArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		actor, ok := c.srv.mgr.Peek(a.SessionID)
		if !ok {
			return nil, errors.New("session is not live")
		}
		action := a.Action
		if action != "accept" && action != "decline" && action != "cancel" {
			action = "cancel"
		}
		err := actor.ResolveElicitation(ctx, a.RequestID, adapter.ElicitationResult{Action: action, Value: a.Value})
		if errors.Is(err, session.ErrAlreadyResolved) {
			return map[string]any{"status": "already_resolved"}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "resolved"}, nil

	case "enable_https":
		return c.srv.setHTTPS(ctx, true)

	case "disable_https":
		return c.srv.setHTTPS(ctx, false)

	case "recheck_harnesses":
		c.srv.mgr.RecheckHarnesses()
		return map[string]any{"harnesses": c.srv.mgr.Harnesses(ctx)}, nil

	case "list_workspaces":
		var a listWorkspacesArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		spaces, err := c.srv.mgr.ListWorkspaces(ctx, a.ProjectID)
		if err != nil {
			return nil, err
		}
		// Issues are fetched separately. They are advisory — they seed
		// branch-name suggestions — but `gh` can take twelve seconds to answer,
		// and the workspace list carries the warning that another session is
		// already in a checkout. Making that warning wait on a network call is
		// making it arrive after the decision it exists to inform.
		return map[string]any{"workspaces": spaces}, nil

	case "list_issues":
		var a listWorkspacesArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		issues, issuesErr := c.srv.mgr.ListIssues(ctx, a.ProjectID)
		return map[string]any{"issues": issues, "issuesError": issuesErr}, nil

	case "session_pr":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		// Shaped like list_issues: the pull request is advisory, `gh` can take
		// seconds to answer, and not knowing is an ordinary answer rather than
		// a failure — so the reason travels beside the result, not as an error.
		pr, prErr := c.srv.mgr.SessionPR(ctx, a.SessionID)
		return map[string]any{"pr": pr, "prError": prErr}, nil

	case "session_changes":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		changes, err := c.srv.mgr.SessionChanges(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"changes": changes}, nil

	case "session_file_diff":
		var a fileDiffArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		diff, err := c.srv.mgr.SessionFileDiff(ctx, a.SessionID, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"diff": diff}, nil

	case "session_file_tree":
		var a fileTreeArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		tree, err := c.srv.mgr.SessionFileTree(ctx, a.SessionID, a.IncludeIgnored)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tree": tree}, nil

	case "session_read_file":
		var a readFileArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		file, err := c.srv.mgr.SessionReadFile(ctx, a.SessionID, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file": file}, nil

	case "summarize_session":
		var a summarizeArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		summary, err := c.srv.mgr.SummarizeSession(ctx, a.SessionID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"summary": summary}, nil

	case "get_user_config":
		cfg, err := userconfig.Load()
		if err != nil {
			return nil, err
		}
		// Provider instances are server-side configuration: their entries never
		// travel to a client, so no env value — secret or not — can leak
		// through the settings surface.
		cfg.Providers = nil
		return map[string]any{"userConfig": cfg}, nil

	case "save_user_config":
		var a saveUserConfigArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		// Clients never see the providers, so a client echoing config back must
		// not be able to erase (or author) them; the on-disk entries win.
		current, err := userconfig.Load()
		if err != nil {
			return nil, err
		}
		a.Config.Providers = current.Providers
		cfg, err := userconfig.Save(a.Config)
		if err != nil {
			return nil, err
		}
		cfg.Providers = nil
		return map[string]any{"userConfig": cfg}, nil

	case "add_project":
		var a addProjectArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		p, err := c.srv.mgr.AddProject(ctx, a.Root)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": p}, nil

	case "save_project":
		var a saveProjectArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		p, err := c.srv.mgr.SaveProject(ctx, a.ProjectID, a.Config)
		if err != nil {
			return nil, err
		}
		return map[string]any{"project": p}, nil

	case "delete_project":
		var a deleteProjectArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		if err := c.srv.mgr.DeleteProject(ctx, a.ProjectID); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleted"}, nil

	case "retry_provision":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"status": "provisioning"}, c.srv.mgr.RetryProvision(ctx, a.SessionID)

	case "cleanup_session":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"status": "cleaning"}, c.srv.mgr.Cleanup(ctx, a.SessionID)

	case "close_session":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"status": "cleaning"}, c.srv.mgr.Cleanup(ctx, a.SessionID)

	case "delete_session":
		var a deleteSessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleting"}, c.srv.mgr.Delete(ctx, a.SessionID, a.RemoveWorktree)

	case "force_delete_session":
		var a sessionArgs
		if err := json.Unmarshal(f.Args, &a); err != nil {
			return nil, err
		}
		return map[string]any{"status": "deleted"}, c.srv.mgr.ForceDelete(ctx, a.SessionID)

	default:
		return nil, fmt.Errorf("unknown command %q", f.Command)
	}
}

func normaliseOutcome(o string) string {
	switch o {
	case proto.OutcomeAllowOnce, proto.OutcomeAllowAlways,
		proto.OutcomeRejectOnce, proto.OutcomeRejectAlways, proto.OutcomeCancelled:
		return o
	case "allow":
		return proto.OutcomeAllowOnce
	case "always":
		return proto.OutcomeAllowAlways
	default:
		return proto.OutcomeRejectOnce
	}
}

func (c *conn) ack(commandID string, result any, err error) {
	f := serverFrame{Type: "ack", CommandID: commandID}
	if err != nil {
		f.Error = err.Error()
	} else if result != nil {
		raw, _ := json.Marshal(result)
		f.Result = raw
	}
	c.send(f)
}

func (c *conn) send(f serverFrame) int {
	b, err := json.Marshal(f)
	if err != nil {
		return 0
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, b)
	return len(b)
}
