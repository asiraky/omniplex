package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// TestAgentArgs guards what an agent hands codex. A prompt file that is not
// there yet would stop the app-server starting, so it is left out and said
// so; a plain thread adds nothing.
func TestAgentArgs(t *testing.T) {
	prompt := filepath.Join(t.TempDir(), "compact.md")
	if err := os.WriteFile(prompt, []byte("keep the plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone.md")
	tests := []struct {
		name    string
		o       adapter.CreateOptions
		want    []string
		skipped bool
	}{
		{"plain thread", adapter.CreateOptions{}, nil, false},
		{"agent", adapter.CreateOptions{CompactInstructionsFile: prompt, CompactAt: 300000, NoMemory: true}, []string{
			"-c", "experimental_compact_prompt_file=" + `"` + prompt + `"`,
			"-c", "model_auto_compact_token_limit=300000",
			"-c", "features.memories=false",
		}, false},
		{"missing prompt file", adapter.CreateOptions{CompactInstructionsFile: missing, CompactAt: 300000}, []string{
			"-c", "model_auto_compact_token_limit=300000",
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, skipped := agentArgs(tt.o)
			if !slices.Equal(got, tt.want) {
				t.Errorf("args = %q, want %q", got, tt.want)
			}
			if (skipped != "") != tt.skipped {
				t.Errorf("skipped = %q, want skipped %v", skipped, tt.skipped)
			}
		})
	}
}

// TestCompactionReportsSizeAndTime: codex says only that it compacted, so the
// size is the last request's context and the time runs from the item starting.
// A compaction whose item never started has no time to report, and one with
// no request since the last has no size.
func TestCompactionReportsSizeAndTime(t *testing.T) {
	s := subagentSession(t)
	openTurn(s, "turn-1")
	s.handleNotification("thread/tokenUsage/updated", json.RawMessage(
		`{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"inputTokens":1000,"outputTokens":10},"last":{"totalTokens":180000},"modelContextWindow":200000}}`))
	s.handleNotification("item/started", itemNotification("turn-1", `{"type":"contextCompaction","id":"cmp_1"}`))
	s.mu.Lock()
	s.compactStarted = s.compactStarted.Add(-3 * time.Second)
	s.mu.Unlock()
	s.handleNotification("item/completed", itemNotification("turn-1", `{"type":"contextCompaction","id":"cmp_1"}`))

	openTurn(s, "turn-2")
	s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"thread-1","turnId":"turn-2"}`))

	var got []proto.ContextCompactedPayload
	for _, e := range drain(s) {
		if e.Type == proto.ContextCompacted {
			got = append(got, e.Payload.(proto.ContextCompactedPayload))
		}
	}
	if len(got) != 2 {
		t.Fatalf("%d context.compacted, want 2: %+v", len(got), got)
	}
	if got[0].PreTokens != 180000 || got[0].DurationMs < 3000 || got[0].DurationMs > 60000 {
		t.Errorf("first = %+v, want PreTokens 180000 and about 3s", got[0])
	}
	if got[1].DurationMs != 0 || got[1].PreTokens != 0 {
		t.Errorf("second = %+v, want no duration without a started item and no size without a request since", got[1])
	}
}
