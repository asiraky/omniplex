package codexapp

import (
	"context"
	"fmt"
)

// Codex records a thread's developer instructions in its history when the
// thread starts, and a resume reads them back from there: the
// developerInstructions sent with thread/resume are ignored. So a restart
// appends the new instructions to the resumed thread's history as a developer
// message, through Codex's own thread/inject_items, before any turn runs.
// The old instructions stay where they were, earlier in the history; the
// message says these replace them.

// restartNote is the developer message a restart appends.
func restartNote(instructions string) string {
	const lead = "This thread was restarted and its developer instructions changed."
	if instructions == "" {
		return lead + " The developer instructions given earlier in this conversation no longer apply."
	}
	return lead + " These replace the developer instructions given earlier in this conversation:\n\n" + instructions
}

// injectInstructions appends a restart's instructions to the thread's history.
func (s *session) injectInstructions(ctx context.Context, instructions string) error {
	item := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []map[string]any{
			{"type": "input_text", "text": restartNote(instructions)},
		},
	}
	err := s.died(s.conn.Call(ctx, "thread/inject_items", map[string]any{
		"threadId": s.threadID,
		"items":    []any{item},
	}, nil))
	if err != nil {
		return fmt.Errorf("codex could not take the new instructions on the restarted thread: %w", err)
	}
	return nil
}
