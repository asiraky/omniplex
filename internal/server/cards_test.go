package server

import (
	"context"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

// A prompt edit the user types back to the text the agent already has saves
// nothing: the approval is refused, naming the revision it matches, and the
// card stays for the user to decline.
func TestAPromptEditTypedBackToTheCurrentTextSavesNothing(t *testing.T) {
	r := newToolRig(t)
	threadID, agentID := r.agentThread("Triage bugs.", "Keep state.")
	before := r.restarts(threadID)
	reply := r.start(context.Background(), threadID, "propose_prompt_edit", map[string]any{"kind": "prompt", "replace": "Be terse.", "reason": `"be terse"`})
	pending, _ := r.waitCard(threadID, 1)

	out, errMsg := r.resolve(threadID, pending.RequestID, "accept", map[string]any{"body": "Triage bugs."})
	if errMsg == "" || !strings.Contains(errMsg, "same as r1") {
		t.Fatalf("approved the current text: %+v %q", out, errMsg)
	}
	if revs := r.revisions(agentID, store.RevisionPrompt); len(revs) != 1 {
		t.Fatalf("%d revisions, want r1 alone", len(revs))
	}
	if len(r.cards(threadID)) != 1 {
		t.Fatal("the refused approval dropped the card")
	}
	if n := r.restarts(threadID) - before; n != 0 {
		t.Fatalf("%d restarts, want none", n)
	}

	r.resolve(threadID, pending.RequestID, "decline", nil)
	if got := wait(t, reply); strings.Contains(got.text, "Saved as revision") {
		t.Fatalf("tool replied %q after nothing was saved", got.text)
	}
}
