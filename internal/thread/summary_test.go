package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

// summaryFixture is a two-turn session covering everything renderTranscript
// has to make a decision about: a prompt, agent prose, a private thought, a
// tool call with output, a subagent item, an empty message, and a recorded
// diff.
func summaryFixture() *projection.State {
	return &projection.State{
		SessionID: "s1",
		Seq:       42,
		Title:     "Fix the login redirect",
		Cwd:       "/repo",
		Harness:   "claude",
		Model:     "opus",
		Phase:     "idle",
		Turns: []projection.Turn{
			{
				ID:         "t1",
				Prompt:     "the login page bounces me back, please fix it",
				Done:       true,
				StopReason: proto.StopEndTurn,
				Diff: &proto.TurnDiffPayload{
					TurnID:    "t1",
					Files:     []proto.ChangedFile{{Path: "auth/session.go", Status: "modified", Additions: 12, Deletions: 3}},
					Additions: 12,
					Deletions: 3,
				},
			},
			{ID: "t2", Prompt: "now add a test", Done: false},
		},
		Items: []projection.Item{
			// TurnStarted logs the prompt as an item as well as on the turn
			// record; a fixture without it would not be a real transcript.
			{ID: "prompt:t1", Kind: projection.ItemMessage, TurnID: "t1", Role: "user", ContentKind: "text", Text: "the login page bounces me back, please fix it"},
			{ID: "i1", Kind: projection.ItemMessage, TurnID: "t1", Role: "agent", ContentKind: "thought", Text: "PRIVATE REASONING"},
			{ID: "i2", Kind: projection.ItemMessage, TurnID: "t1", Role: "agent", ContentKind: "text", Text: "Found it — the cookie was scoped wrong."},
			{ID: "i3", Kind: projection.ItemTool, TurnID: "t1", ToolKind: proto.KindEdit, Status: proto.StatusCompleted, Title: "Edit auth/session.go",
				Content: []proto.ToolContent{{Type: "diff", Path: "auth/session.go", Text: "@@ -1 +1 @@"}}},
			{ID: "i4", Kind: projection.ItemMessage, TurnID: "t1", ParentID: "i3", Role: "agent", ContentKind: "text", Text: "SUBAGENT CHATTER"},
			{ID: "i5", Kind: projection.ItemMessage, TurnID: "t1", Role: "agent", ContentKind: "text", Text: "   "},
			{ID: "prompt:t2", Kind: projection.ItemMessage, TurnID: "t2", Role: "user", ContentKind: "text", Text: "now add a test"},
			{ID: "i6", Kind: projection.ItemMessage, TurnID: "t2", Role: "agent", ContentKind: "text", Text: "Adding a test now."},
		},
	}
}

func TestRenderTranscriptKeepsWhatASummaryNeeds(t *testing.T) {
	got := renderTranscript(summaryFixture())

	for _, want := range []string{
		"Fix the login redirect",
		"Harness: claude",
		"the login page bounces me back, please fix it",
		"now add a test",
		"Found it — the cookie was scoped wrong.",
		"Edit auth/session.go",
		"auth/session.go (modified +12/-3)",
		"Turn 2: still running",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript is missing %q\n\n%s", want, got)
		}
	}
}

// The dropped material is the point of the renderer: a fast model has a small
// window, and reasoning and subagent chatter are the bulkiest things in a
// transcript that say least about what actually happened.
func TestRenderTranscriptDropsNoise(t *testing.T) {
	got := renderTranscript(summaryFixture())

	for _, unwanted := range []string{"PRIVATE REASONING", "SUBAGENT CHATTER"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("transcript should not carry %q\n\n%s", unwanted, got)
		}
	}
}

// A prompt must be printed once, before the turn's first item, not repeated
// ahead of every item that carries the same turn id.
func TestRenderTranscriptPrintsEachPromptOnce(t *testing.T) {
	got := renderTranscript(summaryFixture())
	if n := strings.Count(got, "the login page bounces me back"); n != 1 {
		t.Errorf("prompt appears %d times, want 1\n\n%s", n, got)
	}
}

func TestRenderTranscriptReportsATurnThatChangedNothing(t *testing.T) {
	state := &projection.State{
		SessionID: "s1",
		Harness:   "codex",
		Turns:     []projection.Turn{{ID: "t1", Prompt: "look around", Done: true, Diff: &proto.TurnDiffPayload{TurnID: "t1"}}},
		Items:     []projection.Item{{ID: "i1", Kind: projection.ItemMessage, TurnID: "t1", Role: "agent", ContentKind: "text", Text: "Nothing to change."}},
	}
	if got := renderTranscript(state); !strings.Contains(got, "changed no files") {
		t.Errorf("a measured turn with no files must say so\n\n%s", got)
	}
}

func TestRenderTranscriptClipsALongToolResult(t *testing.T) {
	huge := strings.Repeat("x", toolContentBudget*3)
	state := &projection.State{
		SessionID: "s1",
		Harness:   "claude",
		Turns:     []projection.Turn{{ID: "t1", Prompt: "read it", Done: true}},
		Items: []projection.Item{{
			ID: "i1", Kind: projection.ItemTool, TurnID: "t1", ToolKind: proto.KindRead,
			Status: proto.StatusCompleted, Title: "Read big.txt",
			Content: []proto.ToolContent{{Type: "text", Text: huge}},
			Input:   json.RawMessage(`{"path":"big.txt"}`),
		}},
	}
	got := renderTranscript(state)
	if !strings.Contains(got, "(truncated)") {
		t.Errorf("an oversized tool result must be clipped\n\n%s", got)
	}
	if strings.Count(got, "x") > toolContentBudget+100 {
		t.Errorf("clipped result is still %d bytes of payload", strings.Count(got, "x"))
	}
}

// clipMiddle keeps the request and the outcome and drops the repetitive middle,
// which is the opposite of what a plain head-truncation would do.
func TestClipMiddleKeepsBothEndsOnRuneBoundaries(t *testing.T) {
	body := "HEAD—" + strings.Repeat("é", 5000) + "—TAIL"
	got := clipMiddle(body, 400)

	if !strings.HasPrefix(got, "HEAD—") {
		t.Errorf("head was lost: %q", got[:20])
	}
	if !strings.HasSuffix(got, "—TAIL") {
		t.Errorf("tail was lost: %q", got[len(got)-20:])
	}
	if !strings.Contains(got, "omitted") {
		t.Error("clipping must say that it clipped")
	}
	if strings.ContainsRune(got, '�') {
		t.Error("clipping split a UTF-8 sequence")
	}
	if len(got) > 400+120 {
		t.Errorf("clipped body is %d bytes, well over the 400 budget", len(got))
	}
}

func TestClipMiddleLeavesAShortTranscriptAlone(t *testing.T) {
	body := "short enough"
	if got := clipMiddle(body, 400); got != body {
		t.Errorf("got %q, want it untouched", got)
	}
}

func TestRenderTranscriptOfNothingIsEmpty(t *testing.T) {
	if got := renderTranscript(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// A turn whose changes could not be measured must not be summarised as a turn
// that changed nothing: the checkpointer emits exactly that shape — an empty
// file list carrying an error — when a snapshot fails.
func TestRenderTranscriptDoesNotCallAFailedMeasurementACleanTurn(t *testing.T) {
	state := &projection.State{
		SessionID: "s1",
		Harness:   "claude",
		Turns: []projection.Turn{{
			ID: "t1", Prompt: "refactor it", Done: true,
			Diff: &proto.TurnDiffPayload{TurnID: "t1", Files: []proto.ChangedFile{}, Error: "git snapshot failed"},
		}},
		Items: []projection.Item{{ID: "i1", Kind: projection.ItemMessage, TurnID: "t1", Role: "agent", ContentKind: "text", Text: "Done."}},
	}
	got := renderTranscript(state)
	if strings.Contains(got, "changed no files") {
		t.Errorf("an unmeasurable turn was reported as clean\n\n%s", got)
	}
	if !strings.Contains(got, "could not be measured") {
		t.Errorf("a failed measurement must say so\n\n%s", got)
	}
}

// A user message that is not the turn prompt — a mid-turn steer, say — is
// still worth carrying; only the duplicate of the prompt is dropped.
func TestRenderTranscriptKeepsUserMessagesThatAreNotThePrompt(t *testing.T) {
	state := &projection.State{
		SessionID: "s1",
		Harness:   "claude",
		Turns:     []projection.Turn{{ID: "t1", Prompt: "fix the bug", Done: true}},
		Items: []projection.Item{
			{ID: "prompt:t1", Kind: projection.ItemMessage, TurnID: "t1", Role: "user", ContentKind: "text", Text: "fix the bug"},
			{ID: "i2", Kind: projection.ItemMessage, TurnID: "t1", Role: "user", ContentKind: "text", Text: "actually, do it in the other file"},
		},
	}
	got := renderTranscript(state)
	if n := strings.Count(got, "fix the bug"); n != 1 {
		t.Errorf("prompt appears %d times, want 1\n\n%s", n, got)
	}
	if !strings.Contains(got, "actually, do it in the other file") {
		t.Errorf("a mid-turn steer was dropped\n\n%s", got)
	}
}
