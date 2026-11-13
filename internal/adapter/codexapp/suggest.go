package codexapp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// suggestPrompt is what thread/sample asks of the finished turn's context.
// "Reply with nothing" invites a model to say so in words, which would then
// be offered as the next prompt; a word to look for is easier to drop.
var suggestPrompt = fmt.Sprintf("Predict the user's most likely next message to you in this conversation. "+
	"Reply with only that message, as the user would type it: one line, under %d characters, "+
	"no preamble, no quotes. If there is no clear next step, reply with only %s.", proto.MaxSuggestion, noSuggestion)

// noSuggestion is the answer that means there is no next step to offer.
const noSuggestion = "NONE"

// declined reports whether text is the model saying it has no suggestion.
func declined(text string) bool {
	return strings.EqualFold(strings.Trim(text, " \t\r\n.\"'`"), noSuggestion)
}

// suggestTimeout bounds a sample. It runs after the turn has finished, so it
// holds nothing up, but a hung one should not linger.
const suggestTimeout = time.Minute

// suggestNext asks codex for the human's likely next prompt after turn, ours,
// which codex knows as serverTurn, and emits it as turn.suggested. thread/sample
// is a side completion on the thread's context that adds no turn. Codex
// refuses it when serverTurn is no longer the latest finished turn, or a turn
// is running; that, any other failure, and an empty answer all mean no
// suggestion.
func (s *session) suggestNext(turn, serverTurn string) {
	ctx, cancel := context.WithTimeout(context.Background(), suggestTimeout)
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	var res struct {
		Text *string `json:"text"`
	}
	err := s.conn.Call(ctx, "thread/sample", map[string]any{
		"threadId":       s.threadID,
		"expectedTurnId": serverTurn,
		"prompt":         suggestPrompt,
		"effort":         "low",
	}, &res)
	// A turn that started while the sample ran owns the composer now. That is
	// also the usual reason codex refuses one, so a refusal then is expected
	// and not worth a log line.
	s.mu.Lock()
	stale := s.turnID != "" || s.lastTurn != turn
	s.mu.Unlock()
	if stale {
		return
	}
	if err != nil {
		s.host.Logf("codex suggestion dropped: %v", err)
		return
	}
	if res.Text == nil || declined(*res.Text) {
		return
	}
	if p, ok := proto.Suggested(turn, *res.Text); ok {
		s.emit(proto.Emit(proto.TurnSuggested, p))
	}
}
