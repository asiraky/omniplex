package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// Rewinding a Claude conversation is a fork: the SDK starts a new conversation
// that copies an existing one up to and including one of its entries
// (resume + resumeSessionAt + forkSession, under a sessionId of our choosing),
// and the conversation it was cut from is left as it was. The fork is only
// written to disk when its first turn is, though, so until then the new id
// alone cannot be resumed. The id the session reports while that is so says
// how to rebuild it instead:
//
//	<new>;from=<conversation>;at=<entry>   fork <conversation> at <entry>
//	<new>;new                              start <new> empty
//
// The host keeps it like any other id and hands it back on a restart, which
// starts the same fork again. Once a turn has gone into <new> the session
// reports the bare id, and a restart that still has the long form resumes
// <new> as it is.
type conversation struct {
	id    string
	from  string // what it is cut from; "" unless pending
	at    string // the last entry of from it keeps
	fresh bool   // pending, and starts empty
	// resume is a plain id that already exists. Not part of the encoding:
	// the caller knows whether it is starting or resuming.
	resume bool
}

func parseConversation(s string) conversation {
	id, rest, ok := strings.Cut(s, ";")
	if !ok {
		return conversation{id: s}
	}
	c := conversation{id: id}
	for _, part := range strings.Split(rest, ";") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "new":
			c.fresh = true
		case "from":
			c.from = v
		case "at":
			c.at = v
		}
	}
	if c.from == "" || c.at == "" {
		c.from, c.at = "", ""
		c.fresh = true
	}
	return c
}

func (c conversation) pending() bool { return c.fresh || c.from != "" }

func (c conversation) String() string {
	switch {
	case c.fresh:
		return c.id + ";new"
	case c.from != "":
		return c.id + ";from=" + c.from + ";at=" + c.at
	}
	return c.id
}

// apply sets the conversation fields of a bridge config.
func (c conversation) apply(cfg *sidecarConfig) {
	cfg.SessionID, cfg.Resume, cfg.ResumeAt, cfg.Fork = "", "", "", false
	switch {
	case c.from != "":
		cfg.Resume, cfg.ResumeAt, cfg.Fork, cfg.SessionID = c.from, c.at, true, c.id
	case c.resume:
		cfg.Resume = c.id
	default:
		cfg.SessionID = c.id
	}
}

// moveID is the conversation an account switch has to carry: the pending
// fork's source while the fork is still only a recipe, the conversation
// itself otherwise, and nothing for a pending empty one.
func (c conversation) moveID(configDir, cwd string) string {
	if !c.pending() || conversationOnDisk(configDir, cwd, c.id) {
		return c.id
	}
	return c.from
}

func conversationOnDisk(configDir, cwd, id string) bool {
	file, err := findConversation(configDir, cwd, id)
	return err == nil && file != ""
}

// settleRewind reports a rewound conversation by its own id once it is on
// disk, which is the first point it can be resumed by it.
func (s *session) settleRewind() {
	s.mu.Lock()
	conv := parseConversation(s.harnessSessionID)
	s.mu.Unlock()
	if !conv.pending() || !conversationOnDisk(s.configDir, s.cwd, conv.id) {
		return
	}
	s.mu.Lock()
	// Unless something moved it on meanwhile (/clear, another rewind).
	if s.harnessSessionID != conv.String() {
		s.mu.Unlock()
		return
	}
	s.harnessSessionID = conv.id
	s.mu.Unlock()
	s.emit(proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{HarnessSessionID: conv.id}))
}

// noteEntry takes the HarnessRef of a turn the harness opened by itself from
// its first top-level transcript entry: an assistant message, or a user
// message that is not the echo of a prompt (those set it in handleReplay).
func (s *session) noteEntry(msg map[string]json.RawMessage) {
	switch str(msg["type"]) {
	case "assistant", "user":
	default:
		return
	}
	if str(msg["parent_tool_use_id"]) != "" {
		return
	}
	id := str(msg["uuid"])
	if id == "" {
		return
	}
	if string(msg["isReplay"]) == "true" {
		return
	}
	s.mu.Lock()
	if s.refAny && s.ref == "" && s.turnID != "" {
		s.ref, s.refAny = id, false
	}
	s.mu.Unlock()
}

// rewindBoot bounds how long a rewind waits for the new bridge to come up
// when the caller set no deadline: a CLI boot is seconds, not minutes.
const rewindBoot = 90 * time.Second

// Rewind forgets target.Turn and everything after it. Claude Code cannot cut
// a conversation in place, so this starts a second bridge on a fork of the
// conversation ending just before the turn, and swaps it in once it answers;
// the session, its event channel and the turn bookkeeping carry on across the
// swap, and the old bridge is stopped without a word. The fork point is the
// transcript parent of the turn's first entry, so whatever the turn before it
// wrote after it was finished (an interrupted turn's tail) is kept with it.
//
// Every failure leaves the session as it was, running the old bridge: a turn
// the transcript does not have, a turn running, a fork the CLI will not boot.
// Nothing here touches files.
func (s *session) Rewind(ctx context.Context, target adapter.RewindTarget) error {
	if target.Turn == "" {
		return errors.New("claude: that turn has no transcript entry to rewind to")
	}
	s.rewindMu.Lock()
	defer s.rewindMu.Unlock()

	s.mu.Lock()
	busy := s.turnID != ""
	conv := parseConversation(s.harnessSessionID)
	cfg := s.launch.cfg
	s.mu.Unlock()
	if busy {
		return errors.New("claude: cannot rewind while a turn is running")
	}

	// A conversation rewound and not yet written is still the one it was
	// cut from, as far as its transcript goes.
	source := conv.id
	if conv.pending() && !conversationOnDisk(s.configDir, s.cwd, conv.id) {
		if conv.from == "" {
			return errors.New("claude: nothing to rewind: the conversation has no turns yet")
		}
		source = conv.from
	}
	parent, err := entryParent(s.configDir, s.cwd, source, target.Turn)
	if err != nil {
		return err
	}
	next := conversation{id: uuid.NewString(), fresh: parent == ""}
	if parent != "" {
		next.from, next.at = source, parent
	}
	next.apply(&cfg)

	s.procMu.Lock()
	if s.closing {
		s.procMu.Unlock()
		return errors.New("claude: the session is closed")
	}
	s.lastGen++
	gen := s.lastGen
	s.procMu.Unlock()

	b, err := s.startBridge(cfg, next.id, gen, false)
	if err != nil {
		return fmt.Errorf("claude: rewind: %w", err)
	}
	if err := confirmBridge(ctx, b); err != nil {
		b.stop()
		return fmt.Errorf("claude: rewind: %w", err)
	}

	s.procMu.Lock()
	if s.closing {
		s.procMu.Unlock()
		b.stop()
		return errors.New("claude: the session is closed")
	}
	s.mu.Lock()
	if s.turnID != "" {
		// The harness started work by itself while the fork booted.
		s.mu.Unlock()
		s.procMu.Unlock()
		b.stop()
		return errors.New("claude: cannot rewind while a turn is running")
	}
	old := s.current()
	s.install(b)
	// What was in flight belonged to the old process.
	s.streams = map[string]*stream{}
	s.steers = map[string]bool{}
	s.suppress, s.sawResult, s.compacting = false, false, false
	s.fatal = ""
	s.todos = nil
	s.limited, s.rejected = false, nil
	s.ref, s.refAny = "", false
	s.lastPromptTokens = 0
	s.harnessSessionID = next.String()
	s.mu.Unlock()
	s.procMu.Unlock()
	go s.watchExit(b.conn, gen)

	s.emit(proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{
		HarnessSessionID: next.String(),
	}))
	old.stop()
	// Background work died with the old process; finish its jobs.
	s.handleBackgroundTasks(nil)
	return nil
}

// confirmBridge waits for a new bridge to answer, which is the CLI booted on
// the conversation it was given.
func confirmBridge(ctx context.Context, b *bridge) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rewindBoot)
		defer cancel()
	}
	var res json.RawMessage
	err := b.conn.Call(ctx, "supportedCommands", map[string]any{}, &res)
	if err == nil {
		return nil
	}
	b.mu.Lock()
	fatal := b.fatal
	b.mu.Unlock()
	if msg, ok := loginRequired(fatal); ok {
		return &adapter.FailureError{Kind: proto.FailureAuth, Err: errors.New(msg)}
	}
	if fatal != "" {
		return errors.New("claude exited: " + briefly(fatal))
	}
	return err
}

// entryParent finds entry in the transcript of conversation id and returns
// its parent's uuid, "" when it is the first. An entry it cannot find is an
// error: resuming at a uuid the CLI does not know leaves a session that fails
// every turn.
func entryParent(configDir, cwd, id, entry string) (string, error) {
	file, err := findConversation(configDir, cwd, id)
	if err != nil {
		return "", err
	}
	if file == "" {
		return "", fmt.Errorf("claude: no transcript for conversation %s", id)
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	needle := []byte(`"` + entry + `"`)
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, needle) {
			var e struct {
				UUID   string  `json:"uuid"`
				Parent *string `json:"parentUuid"`
			}
			if json.Unmarshal(line, &e) == nil && e.UUID == entry {
				if e.Parent == nil {
					return "", nil
				}
				return *e.Parent, nil
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("claude: turn %s is not in conversation %s", entry, id)
}
