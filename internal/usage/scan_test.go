package usage

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// now is on the hour so records a few minutes apart share a bucket, and
// near the wall clock so freshly written files fall inside every window.
var now = time.Now().Truncate(time.Hour)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func stamp(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }

func jsonl(t *testing.T, lines ...any) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func claudeLine(ago time.Duration, msgID, reqID, model string, in, out, read, cacheWrite, hour int64) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": stamp(ago),
		"requestId": reqID,
		"message": map[string]any{
			"id":    msgID,
			"model": model,
			"usage": map[string]any{
				"input_tokens":                in,
				"output_tokens":               out,
				"cache_read_input_tokens":     read,
				"cache_creation_input_tokens": cacheWrite,
				"cache_creation":              map[string]any{"ephemeral_1h_input_tokens": hour},
			},
		},
	}
}

func report(t *testing.T, s *Scanner, sources []Source, rng string) Report {
	t.Helper()
	spec, err := RangeFor(rng)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := s.Records(context.Background(), sources, now.Add(-spec.Window))
	if err != nil {
		t.Fatal(err)
	}
	return Build(recs, spec, now)
}

func TestClaudeCountsSubagentsAndMergesRepeatedResponses(t *testing.T) {
	root := t.TempDir()
	// The main conversation: one response written as two content-block
	// lines, the first mid-stream with a partial output count.
	write(t, filepath.Join(root, "proj", "sess.jsonl"), jsonl(t,
		map[string]any{"type": "user", "timestamp": stamp(time.Hour), "message": map[string]any{"content": "hi"}},
		claudeLine(time.Hour, "m1", "r1", "claude-opus-5", 10, 3, 1000, 0, 0),
		claudeLine(time.Hour, "m1", "r1", "claude-opus-5", 10, 500, 1000, 0, 0),
	))
	// A subagent's own transcript, beside the session.
	write(t, filepath.Join(root, "proj", "sess", "subagents", "agent-a.jsonl"), jsonl(t,
		claudeLine(30*time.Minute, "m2", "r2", "claude-opus-5", 20, 200, 5000, 0, 0),
	))
	// A resumed session copies the earlier response into its own file.
	write(t, filepath.Join(root, "proj", "resumed.jsonl"), jsonl(t,
		claudeLine(time.Hour, "m1", "r1", "claude-opus-5", 10, 500, 1000, 0, 0),
		claudeLine(10*time.Minute, "m3", "r3", "<synthetic>", 0, 0, 0, 0, 0),
	))

	rep := report(t, NewScanner(), []Source{{Provider: "claude", Root: root}}, "24h")
	want := Totals{Input: 30, Output: 700, CacheRead: 6000}
	got := rep.Totals
	got.Cost, got.Unpriced = 0, 0
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
}

func TestClaudeOneHourCacheWritesPricedAtTheirTier(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "s.jsonl"), jsonl(t,
		claudeLine(time.Hour, "m", "r", "claude-opus-5", 0, 0, 0, 3_000_000, 1_000_000),
	))
	rep := report(t, NewScanner(), []Source{{Provider: "claude", Root: root}}, "24h")
	five := Price("claude-opus-5", Counts{CacheWrite: 2_000_000})
	hour := Price("claude-opus-5", Counts{CacheWrite: 1_000_000, CacheWrite1h: 1_000_000})
	if rep.Totals.CacheWrite != 3_000_000 {
		t.Fatalf("cache write = %d", rep.Totals.CacheWrite)
	}
	if !near(rep.Totals.Cost, five.CostUSD+hour.CostUSD) {
		t.Fatalf("cost = %v, want %v", rep.Totals.Cost, five.CostUSD+hour.CostUSD)
	}
}

func codexCount(ago time.Duration, totalIn, totalOut, in, cached, out int64) map[string]any {
	return map[string]any{
		"timestamp": stamp(ago),
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{"input_tokens": totalIn, "output_tokens": totalOut},
				"last_token_usage":  map[string]any{"input_tokens": in, "cached_input_tokens": cached, "output_tokens": out},
			},
		},
	}
}

func TestCodexCountsEachResponseOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "2026", "10", "10", "rollout-a.jsonl"), jsonl(t,
		map[string]any{"timestamp": stamp(2 * time.Hour), "type": "session_meta", "payload": map[string]any{"id": "a"}},
		// Usage before any turn_context has no model to price it by.
		codexCount(2*time.Hour, 50, 5, 50, 0, 5),
		map[string]any{"timestamp": stamp(time.Hour), "type": "turn_context", "payload": map[string]any{"model": "gpt-5.4"}},
		codexCount(time.Hour, 1050, 105, 1000, 800, 100),
		// A re-emitted count: the running total did not move.
		codexCount(time.Hour, 1050, 105, 1000, 800, 100),
		codexCount(time.Hour, 3050, 305, 2000, 1500, 200),
	))
	rep := report(t, NewScanner(), []Source{{Provider: "codex", Root: root}}, "24h")
	// Input is net of the cached share.
	want := Totals{Input: 50 + 200 + 500, Output: 305, CacheRead: 2300}
	got := rep.Totals
	got.Cost, got.Unpriced = 0, 0
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
	if rep.Totals.Unpriced != 55 {
		t.Fatalf("unpriced = %d, want the model-less response", rep.Totals.Unpriced)
	}
}

func TestCodexForkSkipsReplayedParentHistory(t *testing.T) {
	root := t.TempDir()
	meta := now.Add(-time.Hour)
	at := func(d time.Duration) string { return meta.Add(d).Format(time.RFC3339Nano) }
	count := func(d time.Duration, total, in int64) map[string]any {
		c := codexCount(0, total, 0, in, 0, 0)
		c["timestamp"] = at(d)
		return c
	}
	write(t, filepath.Join(root, "rollout-fork.jsonl"), jsonl(t,
		map[string]any{"timestamp": at(0), "type": "session_meta", "payload": map[string]any{
			"id": "child", "source": map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "p"}}},
		}},
		map[string]any{"timestamp": at(0), "type": "turn_context", "payload": map[string]any{"model": "gpt-5.4"}},
		// The parent's responses, replayed within a second of each other.
		count(100*time.Millisecond, 100, 100),
		count(900*time.Millisecond, 300, 200),
		count(1500*time.Millisecond, 600, 300),
		// The child's own work, seconds later.
		count(8*time.Second, 1000, 400),
	))
	rep := report(t, NewScanner(), []Source{{Provider: "codex", Root: root}}, "24h")
	if rep.Totals.Input != 400 {
		t.Fatalf("input = %d, want only the child's own response", rep.Totals.Input)
	}
}

func piLine(ago time.Duration, responseID string, in, out int64, cost float64) map[string]any {
	return map[string]any{
		"type":      "message",
		"timestamp": stamp(ago),
		"message": map[string]any{
			"role":       "assistant",
			"model":      "claude-opus-5",
			"responseId": responseID,
			"usage":      map[string]any{"input": in, "output": out, "cost": map[string]any{"total": cost}},
		},
	}
}

func TestPiUsesItsOwnCostAndFallsBackWhenItHasNone(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "--proj--", "s.jsonl"), jsonl(t,
		piLine(time.Hour, "a", 100, 100, 0.25),
		piLine(time.Hour, "b", 1_000_000, 0, 0),
	))
	rep := report(t, NewScanner(), []Source{{Provider: "pi", Root: root}}, "24h")
	want := 0.25 + Price("claude-opus-5", Counts{Input: 1_000_000}).CostUSD
	if !near(rep.Totals.Cost, want) {
		t.Fatalf("cost = %v, want %v", rep.Totals.Cost, want)
	}
}

func TestPiCountsSummariesToolCallsAndForkCopiesOnce(t *testing.T) {
	root := t.TempDir()
	noResponseID := piLine(time.Hour, "", 300, 0, 0)
	noResponseID["id"] = "e1"
	compaction := map[string]any{
		"type": "compaction", "id": "e2", "timestamp": stamp(50 * time.Minute),
		"usage": map[string]any{"input": 1000, "output": 50},
	}
	toolResult := map[string]any{
		"type": "message", "id": "e3", "timestamp": stamp(40 * time.Minute),
		"message": map[string]any{"role": "toolResult", "usage": map[string]any{"input": 5}},
	}
	parent := jsonl(t,
		map[string]any{"type": "model_change", "id": "e0", "timestamp": stamp(2 * time.Hour), "modelId": "claude-opus-5"},
		noResponseID,
		compaction,
		toolResult,
	)
	write(t, filepath.Join(root, "--proj--", "parent.jsonl"), parent)
	// A fork starts as a verbatim copy of the parent's entries.
	write(t, filepath.Join(root, "--proj--", "fork.jsonl"), parent)

	rep := report(t, NewScanner(), []Source{{Provider: "pi", Root: root}}, "24h")
	if rep.Totals.Input != 1305 || rep.Totals.Output != 50 {
		t.Fatalf("totals = %+v, want the response, the compaction and the tool's call once each", rep.Totals)
	}
	want := Price("claude-opus-5", Counts{Input: 1305, Output: 50}).CostUSD
	if !near(rep.Totals.Cost, want) {
		t.Fatalf("cost = %v, want the compaction priced as the session's model (%v)", rep.Totals.Cost, want)
	}
}

func TestScannerFollowsASymlinkedRoot(t *testing.T) {
	target := t.TempDir()
	write(t, filepath.Join(target, "p", "s.jsonl"), jsonl(t, claudeLine(time.Hour, "m", "r", "claude-opus-5", 9, 0, 0, 0, 0)))
	link := filepath.Join(t.TempDir(), "projects")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// Reached both through the link and directly, it is still one directory.
	rep := report(t, NewScanner(), []Source{{Provider: "claude", Root: link}, {Provider: "claude", Root: target}}, "24h")
	if rep.Totals.Input != 9 {
		t.Fatalf("input = %d, want 9", rep.Totals.Input)
	}
}

func TestScannerReadsOnlyWhatWasAppended(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "s.jsonl")
	write(t, path, jsonl(t, claudeLine(time.Hour, "m1", "r1", "claude-opus-5", 1, 1, 0, 0, 0)))
	s := NewScanner()
	src := []Source{{Provider: "claude", Root: root}}
	if got := report(t, s, src, "24h").Totals.Input; got != 1 {
		t.Fatalf("input = %d", got)
	}

	// A line still being written is not consumed until it is whole.
	whole := jsonl(t, claudeLine(time.Minute, "m2", "r2", "claude-opus-5", 10, 1, 0, 0, 0))
	appendTo(t, path, whole[:len(whole)/2])
	if got := report(t, s, src, "24h").Totals.Input; got != 1 {
		t.Fatalf("input with a partial line = %d, want 1", got)
	}
	appendTo(t, path, whole[len(whole)/2:])
	if got := report(t, s, src, "24h").Totals.Input; got != 11 {
		t.Fatalf("input after the line completed = %d, want 11", got)
	}

	// A rewritten, shorter file is parsed from scratch.
	write(t, path, jsonl(t, claudeLine(time.Minute, "m9", "r9", "claude-opus-5", 5, 1, 0, 0, 0)))
	if got := report(t, s, src, "24h").Totals.Input; got != 5 {
		t.Fatalf("input after rewrite = %d, want 5", got)
	}

	// A deleted file takes its records with it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := report(t, s, src, "24h").Totals.Input; got != 0 {
		t.Fatalf("input after delete = %d, want 0", got)
	}
}

func TestScannerSkipsFilesLastWrittenBeforeTheWindow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "old.jsonl")
	// The record claims to be recent, but the file has not been touched in
	// two days: a 24h report never opens it.
	write(t, path, jsonl(t, claudeLine(time.Hour, "m", "r", "claude-opus-5", 7, 0, 0, 0, 0)))
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	s := NewScanner()
	src := []Source{{Provider: "claude", Root: root}}
	if got := report(t, s, src, "24h").Totals.Input; got != 0 {
		t.Fatalf("24h input = %d, want 0", got)
	}
	if got := report(t, s, src, "7d").Totals.Input; got != 7 {
		t.Fatalf("7d input = %d, want 7", got)
	}
}

func TestScannerToleratesMissingRoots(t *testing.T) {
	rep := report(t, NewScanner(), []Source{{Provider: "codex", Root: filepath.Join(t.TempDir(), "nope")}}, "24h")
	if len(rep.Rows) != 0 {
		t.Fatalf("rows = %+v", rep.Rows)
	}
}

func TestBuildBucketsByTimeProviderAndModel(t *testing.T) {
	spec, _ := RangeFor("24h")
	recs := []Record{
		{Timestamp: now.Add(-90 * time.Minute).UnixMilli(), Provider: "claude", Model: "claude-opus-5", Counts: Counts{Input: 1}},
		{Timestamp: now.Add(-80 * time.Minute).UnixMilli(), Provider: "claude", Model: "claude-opus-5", Counts: Counts{Input: 2}},
		{Timestamp: now.Add(-80 * time.Minute).UnixMilli(), Provider: "codex", Model: "gpt-5.4", Counts: Counts{Input: 4}},
		// Outside the window.
		{Timestamp: now.Add(-25 * time.Hour).UnixMilli(), Provider: "claude", Model: "claude-opus-5", Counts: Counts{Input: 8}},
	}
	rep := Build(recs, spec, now)
	if len(rep.Rows) != 2 {
		t.Fatalf("rows = %+v", rep.Rows)
	}
	if rep.Rows[0].Provider != "claude" || rep.Rows[0].Totals.Input != 3 {
		t.Fatalf("claude row = %+v", rep.Rows[0])
	}
	if rep.Rows[0].Start != bucketStart(recs[0].Timestamp, spec.Bucket.Milliseconds()) {
		t.Fatalf("bucket start = %d", rep.Rows[0].Start)
	}
	if rep.Totals.Input != 7 {
		t.Fatalf("total input = %d", rep.Totals.Input)
	}
}
