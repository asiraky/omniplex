package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

const newPrompt = "You are <b>BANANA</b> & nothing else"

// snapshotLine is a prompt_snapshot entry as Claude Code writes one, with
// systemPrompt as given.
func snapshotLine(uuid, parent, systemPrompt string) string {
	return `{"parentUuid":"` + parent + `","isSidechain":false,"type":"attachment","attachment":{"type":"prompt_snapshot","systemPrompt":` + systemPrompt +
		`,"tools":[{"name":"Bash","description":"run <cmd> & wait"}],"cliPrefix":"x-anthropic-billing","reminderFold":true,"echoWireToolInputs":false,"contextRendering":{"mode":"<full>"},"systemTurns":2},"uuid":"` + uuid + `","timestamp":"2026-11-19T00:00:00.000Z"}`
}

func decode(t *testing.T, line string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return m
}

// sameExcept fails unless got and want hold the same keys with byte-equal
// values, apart from skip.
func sameExcept(t *testing.T, what string, got, want map[string]json.RawMessage, skip string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d fields, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		if k == skip {
			continue
		}
		if g, ok := got[k]; !ok || !bytes.Equal(g, w) {
			t.Errorf("%s: %s = %s, want %s", what, k, g, w)
		}
	}
}

// checkSnapshot fails unless got is want with its system prompt replaced by
// prompt, and every other value as it was.
func checkSnapshot(t *testing.T, got, want string, prompt any) {
	t.Helper()
	g, w := decode(t, got), decode(t, want)
	sameExcept(t, "entry", g, w, "attachment")
	var ga, wa map[string]json.RawMessage
	_ = json.Unmarshal(g["attachment"], &ga)
	_ = json.Unmarshal(w["attachment"], &wa)
	sameExcept(t, "attachment", ga, wa, "systemPrompt")
	var sp any
	if err := json.Unmarshal(ga["systemPrompt"], &sp); err != nil {
		t.Fatal(err)
	}
	gb, _ := json.Marshal(sp)
	wb, _ := json.Marshal(prompt)
	if !bytes.Equal(gb, wb) {
		t.Errorf("systemPrompt = %s, want %s", gb, wb)
	}
}

// Every snapshot (the first request's, and the fresh one a compaction
// writes) gets the new prompt as its first block, with Claude Code's own
// blocks after it kept. Nothing else changes: not the snapshot's other
// fields, not the parentUuid chain, not any other line, including ones that
// only mention a snapshot, ones that are not JSON, and a last line with no
// newline. The file keeps its permissions.
func TestRestartRewritesEveryPromptSnapshot(t *testing.T) {
	first := snapshotLine("s1", "u1", `["APPLE","<claude code block>","env & stuff"]`)
	second := snapshotLine("s2", "c1", `["APPLE","after compaction"]`)
	lines := []string{
		`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"hi <there>"}}`,
		first,
		`{"type":"assistant","uuid":"a1","parentUuid":"s1","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"attachment","uuid":"h1","parentUuid":"a1","attachment":{"type":"hook_success","content":"saw a prompt_snapshot & <moved on>"}}`,
		`{"type":"user","uuid":"u2","parentUuid":"h1","message":{"role":"user","content":"what is a prompt_snapshot?"}}`,
		`{"type":"attachment", "broken prompt_snapshot`,
		`{"type":"system","subtype":"compact_boundary","uuid":"c1","parentUuid":null,"logicalParentUuid":"u2"}`,
		second,
		`{"type":"assistant","uuid":"a2","parentUuid":"s2"}`,
	}
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	writeFile(t, path, strings.Join(lines, "\n"))
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := rewritePromptSnapshots(path, newPrompt); err != nil {
		t.Fatal(err)
	}

	body := readFile(t, path)
	if strings.HasSuffix(body, "\n") {
		t.Error("a newline was added after the last line")
	}
	got := strings.Split(body, "\n")
	if len(got) != len(lines) {
		t.Fatalf("%d lines, want %d:\n%s", len(got), len(lines), body)
	}
	for i, want := range lines {
		switch i {
		case 1:
			checkSnapshot(t, got[i], want, []string{newPrompt, "<claude code block>", "env & stuff"})
		case 7:
			checkSnapshot(t, got[i], want, []string{newPrompt, "after compaction"})
		default:
			if got[i] != want {
				t.Errorf("line %d = %s, want %s", i, got[i], want)
			}
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v (%v), want 0640", info.Mode().Perm(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Errorf("left behind %v", left)
	}
}

// A snapshot whose system prompt is one string has it replaced whole.
func TestRestartReplacesAStringSystemPrompt(t *testing.T) {
	line := snapshotLine("s1", "u1", `"APPLE"`)
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	writeFile(t, path, line+"\n")
	if err := rewritePromptSnapshots(path, newPrompt); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, strings.TrimSuffix(readFile(t, path), "\n"), line, newPrompt)
}

// Transcript lines run to megabytes; one of them does not stop the rewrite
// or come out changed.
func TestRestartRewritesPastAHugeLine(t *testing.T) {
	huge := `{"type":"user","uuid":"u1","message":{"content":"` + strings.Repeat("x", 3<<20) + `"}}`
	snap := snapshotLine("s1", "u1", `["APPLE"]`)
	path := filepath.Join(t.TempDir(), "conv.jsonl")
	writeFile(t, path, huge+"\n"+snap+"\n")
	if err := rewritePromptSnapshots(path, newPrompt); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(got) != 2 || got[0] != huge {
		t.Fatalf("the huge line did not come through as it was (%d lines)", len(got))
	}
	checkSnapshot(t, got[1], snap, []string{newPrompt})
}

// A transcript the rewrite cannot apply the prompt to is an error, and is
// left exactly as it was: no snapshot at all, or one whose system prompt is
// not a shape this knows.
func TestRestartRefusesWhatItCannotRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"no snapshot", `{"type":"user","uuid":"u1"}` + "\n" + `{"type":"attachment","attachment":{"type":"hook_success"}}` + "\n", "no prompt snapshot"},
		{"no system prompt", `{"type":"attachment","attachment":{"type":"prompt_snapshot","tools":[]}}` + "\n", "doesn't recognise"},
		{"empty list", snapshotLine("s1", "u1", `[]`) + "\n", "doesn't recognise"},
		{"first block not text", snapshotLine("s1", "u1", `[{"type":"text","text":"APPLE"}]`) + "\n", "doesn't recognise"},
		{"a number", snapshotLine("s1", "u1", `7`) + "\n", "doesn't recognise"},
		{"one good, one not", snapshotLine("s1", "u1", `["APPLE"]`) + "\n" + snapshotLine("s2", "s1", `null`) + "\n", "doesn't recognise"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conv.jsonl")
			writeFile(t, path, tc.body)
			err := rewritePromptSnapshots(path, newPrompt)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one saying %q", err, tc.want)
			}
			if got := readFile(t, path); got != tc.body {
				t.Errorf("transcript changed:\n%s", got)
			}
			if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
				t.Errorf("left behind %v", left)
			}
		})
	}
}

// A transcript in a folder that cannot be written to is reported, not
// skipped, and stays as it was.
func TestRestartReportsAnUnwritableTranscript(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only folders")
	}
	dir := filepath.Join(t.TempDir(), "proj")
	path := filepath.Join(dir, "conv.jsonl")
	body := snapshotLine("s1", "u1", `["APPLE"]`) + "\n"
	writeFile(t, path, body)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := rewritePromptSnapshots(path, newPrompt)
	if err == nil || !strings.Contains(err.Error(), "can't write") {
		t.Fatalf("err = %v, want one saying it can't write", err)
	}
	if got := readFile(t, path); got != body {
		t.Errorf("transcript changed:\n%s", got)
	}
}

// The conversation is found by id like any other, and only its own
// transcript is rewritten: its subagents' transcripts carry snapshots of
// their own prompts, which are not this one.
func TestRestartPromptLeavesSubagentsAlone(t *testing.T) {
	configDir := t.TempDir()
	cwd := "/home/me/code/app"
	dir := filepath.Join(configDir, "projects", "-home-me-code-app")
	main := snapshotLine("s1", "u1", `["APPLE"]`)
	sub := snapshotLine("x1", "y1", `["you are a subagent"]`) + "\n"
	writeFile(t, filepath.Join(dir, "conv.jsonl"), main+"\n")
	writeFile(t, filepath.Join(dir, "conv", "subagents", "agent-1.jsonl"), sub)

	if err := restartPrompt(configDir, cwd, "conv", newPrompt); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, strings.TrimSuffix(readFile(t, filepath.Join(dir, "conv.jsonl")), "\n"), main, []string{newPrompt})
	if got := readFile(t, filepath.Join(dir, "conv", "subagents", "agent-1.jsonl")); got != sub {
		t.Errorf("subagent transcript changed:\n%s", got)
	}
}

// A conversation with no transcript cannot have its prompt applied, which is
// an error; a rewind not yet written resumes from what it was cut from, so
// that is what is rewritten; and one that starts empty needs nothing.
func TestRestartPromptFindsTheTranscriptToResume(t *testing.T) {
	cwd := "/home/me/code/app"
	key := "-home-me-code-app"
	snap := snapshotLine("s1", "u1", `["APPLE"]`)

	t.Run("missing", func(t *testing.T) {
		err := restartPrompt(t.TempDir(), cwd, "conv", newPrompt)
		if err == nil || !strings.Contains(err.Error(), "no transcript") {
			t.Fatalf("err = %v, want one saying there is no transcript", err)
		}
	})
	t.Run("pending fork", func(t *testing.T) {
		configDir := t.TempDir()
		src := filepath.Join(configDir, "projects", key, "O.jsonl")
		writeFile(t, src, snap+"\n")
		if err := restartPrompt(configDir, cwd, "N;from=O;at=s1", newPrompt); err != nil {
			t.Fatal(err)
		}
		checkSnapshot(t, strings.TrimSuffix(readFile(t, src), "\n"), snap, []string{newPrompt})
	})
	t.Run("pending empty", func(t *testing.T) {
		if err := restartPrompt(t.TempDir(), cwd, "N;new", newPrompt); err != nil {
			t.Fatal(err)
		}
	})
}

// A restart puts the new prompt in the transcript before the new process
// reads it; a plain resume leaves the transcript alone.
func TestCreateSessionRestartRewritesBeforeTheBridge(t *testing.T) {
	snapshot := map[string]any{
		"type": "attachment", "uuid": "s1", "parentUuid": "u1",
		"attachment": map[string]any{"type": "prompt_snapshot", "systemPrompt": []string{"APPLE", "claude's own"}},
	}
	for _, tc := range []struct {
		name    string
		restart bool
		want    []string
	}{
		{"restart", true, []string{newPrompt, "claude's own"}},
		{"plain resume", false, []string{"APPLE", "claude's own"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := adapter.CreateOptions{Resume: true, Restart: tc.restart, HarnessSessionID: "O", Instructions: newPrompt}
			f := startFakeBridge(t, o, func(f *fakeBridge) {
				f.entries(t, "O", map[string]any{"type": "user", "uuid": "u1"}, snapshot)
			})
			waitFor(t, func() bool { return exists(filepath.Join(f.dir, "booted-on")) })
			var got []string
			for _, line := range strings.Split(readFile(t, filepath.Join(f.dir, "booted-on")), "\n") {
				var e struct {
					Attachment struct {
						Type         string   `json:"type"`
						SystemPrompt []string `json:"systemPrompt"`
					} `json:"attachment"`
				}
				if json.Unmarshal([]byte(line), &e) == nil && e.Attachment.Type == "prompt_snapshot" {
					got = e.Attachment.SystemPrompt
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("bridge booted on systemPrompt %q, want %q", got, tc.want)
			}
		})
	}
}

// A restart that cannot apply the prompt fails the start rather than
// resuming on the old one: no bridge is started at all.
func TestCreateSessionRestartWithoutASnapshotFails(t *testing.T) {
	o := adapter.CreateOptions{Resume: true, Restart: true, HarnessSessionID: "O", Instructions: newPrompt}
	f, sess, err := createOnFakeBridge(t, o, func(f *fakeBridge) {
		f.transcript(t, "O", "u1", "a1")
	})
	if err == nil {
		_ = sess.Close()
		t.Fatal("CreateSession started a session")
	}
	if !strings.Contains(err.Error(), "no prompt snapshot") {
		t.Errorf("err = %v, want one saying there is no prompt snapshot", err)
	}
	if len(f.configs(t)) != 0 {
		t.Error("a bridge was started")
	}
}
