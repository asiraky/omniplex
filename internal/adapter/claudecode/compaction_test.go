package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// transcript writes entries as a Claude Code transcript, one JSON object a line.
func transcript(t *testing.T, path string, entries ...map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func attach(a map[string]any, rendered ...string) map[string]any {
	e := map[string]any{"type": "attachment", "attachment": a}
	if len(rendered) > 0 {
		var parts []any
		for _, r := range rendered {
			parts = append(parts, map[string]any{"content": r})
		}
		e["rendered"] = parts
	}
	return e
}

func boundary() map[string]any {
	return map[string]any{"type": "system", "subtype": "compact_boundary", "compactMetadata": map[string]any{"trigger": "auto"}}
}

func summaryEntry(text string) map[string]any {
	return map[string]any{"type": "user", "isCompactSummary": true, "message": map[string]any{"role": "user", "content": text}}
}

func answer() map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}}}
}

// A conversation that started, compacted twice, and ran on after each.
func agentTranscript(t *testing.T, path string) {
	transcript(t, path,
		map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "kick off"}},
		attach(map[string]any{"type": "skill_listing", "content": "- merge: merge a PR"}, "skills: merge"),
		attach(map[string]any{"type": "instructions", "files": []any{
			map[string]any{"path": "/home/u/CLAUDE.md", "content": "be terse"},
			map[string]any{"path": "/repo/CLAUDE.md", "content": "use worktrees"},
		}}, "both files, rendered"),
		attach(map[string]any{"type": "date", "date": "2026-11-19"}, "Today's date is 2026-11-19."),
		answer(),
		attach(map[string]any{"type": "file", "filename": "/repo/early.go"}, "read during epoch 0"),

		boundary(),
		summaryEntry("first summary"),
		attach(map[string]any{"type": "file", "filename": "/repo/old.go"}, "old"),
		answer(),

		boundary(),
		attach(map[string]any{"type": "instructions", "files": []any{map[string]any{"path": "/repo/CLAUDE.md", "content": "use worktrees"}}}),
		summaryEntry("second summary"),
		attach(map[string]any{"type": "file", "filename": "/repo/a.go", "content": map[string]any{"type": "text", "file": map[string]any{"content": "package a"}}}, "Called Read", "package a"),
		attach(map[string]any{"type": "file", "filename": "/repo/b.go", "content": map[string]any{"type": "text", "file": map[string]any{"content": "package b"}}}),
		attach(map[string]any{"type": "invoked_skills", "skills": []any{
			map[string]any{"name": "merge", "content": "merge body"},
			map[string]any{"name": "dev", "content": "dev body"},
		}}),
		attach(map[string]any{"type": "task_status", "description": "run the gate"}, "gate running"),
		attach(map[string]any{"type": "compact_file_reference", "filename": "/repo/big.go"}, "too large to include"),
		attach(map[string]any{"type": "credential_org", "organizationUuid": "o"}),
		attach(map[string]any{"type": "session_cron_carry", "tasks": []any{}}),
		map[string]any{"type": "attachment", "isSidechain": true, "attachment": map[string]any{"type": "file", "filename": "/repo/sub.go"}, "rendered": []any{map[string]any{"content": "a subagent's"}}},
		map[string]any{"type": "api-request-blob", "hash": "h"},
		answer(),
		attach(map[string]any{"type": "file", "filename": "/repo/late.go"}, "read after the epoch began"),
	)
}

func names(as []proto.CompactionAttachment) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind+":"+a.Name)
	}
	return out
}

// After a compaction the detail is the last epoch's: its summary, and what
// was attached up to its first answer, split per skill and per instructions
// file, with plumbing and subagent entries left out.
func TestEpochDetailIsTheLastCompaction(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.jsonl")
	agentTranscript(t, file)

	d, ok, err := readEpochDetail(file, false)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if d.Start || d.Summary != "second summary" {
		t.Errorf("start = %v, summary = %q, want the second compaction's", d.Start, d.Summary)
	}
	want := []string{
		"instructions:/repo/CLAUDE.md",
		"file:/repo/a.go", "file:/repo/b.go",
		"skill:merge", "skill:dev",
		"task:run the gate",
		"compact_file_reference:/repo/big.go",
	}
	if got := names(d.Attachments); !slices.Equal(got, want) {
		t.Fatalf("attachments = %v\nwant %v", got, want)
	}
	for _, a := range d.Attachments {
		if a.Chars != len(a.Body) || a.Body == "" {
			t.Errorf("%s: chars %d for a body of %d", a.Name, a.Chars, len(a.Body))
		}
	}
	// Rendered, the body is what the model saw; without it, the file itself.
	if d.Attachments[1].Body != "Called Read\npackage a" || d.Attachments[2].Body != "package b" {
		t.Errorf("file bodies = %q, %q", d.Attachments[1].Body, d.Attachments[2].Body)
	}
}

// The first epoch is what came before the conversation's first answer, with
// no summary.
func TestEpochDetailAtStart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.jsonl")
	agentTranscript(t, file)

	d, ok, err := readEpochDetail(file, true)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if !d.Start || d.Summary != "" {
		t.Errorf("start = %v, summary = %q", d.Start, d.Summary)
	}
	want := []string{"skills:skill listing", "instructions:/home/u/CLAUDE.md", "instructions:/repo/CLAUDE.md"}
	if got := names(d.Attachments); !slices.Equal(got, want) {
		t.Fatalf("attachments = %v, want %v", got, want)
	}
}

func TestEpochDetailBeforeAnyCompaction(t *testing.T) {
	file := filepath.Join(t.TempDir(), "c.jsonl")
	transcript(t, file, attach(map[string]any{"type": "file", "filename": "/a"}, "a"), answer())
	if _, ok, err := readEpochDetail(file, false); ok || err != nil {
		t.Fatalf("ok = %v, err = %v: no compaction, no detail", ok, err)
	}
}

// On an agent's session, the result after a compact boundary reports the new
// epoch, and the first result of a new conversation the first epoch; each
// once. A thread that is not an agent's reads nothing.
func TestResultReportsEpochDetail(t *testing.T) {
	newSession := func(t *testing.T, agent bool) *session {
		dir := t.TempDir()
		s := newTestSession()
		s.configDir, s.cwd, s.harnessSessionID = dir, "/repo", "c1"
		s.epochDetail, s.startDetail = agent, agent
		agentTranscript(t, filepath.Join(dir, "projects", "-repo", "c1.jsonl"))
		return s
	}
	result := func(s *session) {
		s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "stop_reason": "end_turn"}))
	}
	details := func(s *session, want int) []proto.CompactionDetailPayload {
		t.Helper()
		var got []proto.CompactionDetailPayload
		deadline := time.After(2 * time.Second)
		for len(got) < want {
			select {
			case e := <-s.events:
				if d, ok := e.Payload.(proto.CompactionDetailPayload); ok {
					got = append(got, d)
				}
			case <-deadline:
				t.Fatalf("got %d compaction details, want %d", len(got), want)
			}
		}
		// Nothing more is coming.
		time.Sleep(50 * time.Millisecond)
		for {
			select {
			case e := <-s.events:
				if _, ok := e.Payload.(proto.CompactionDetailPayload); ok {
					t.Fatalf("an extra compaction detail: %+v", e.Payload)
				}
				continue
			default:
			}
			return got
		}
	}

	s := newSession(t, true)
	result(s)
	if d := details(s, 1); !d[0].Start {
		t.Fatalf("first result: %+v, want the start", d[0])
	}
	result(s)
	details(s, 0)
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "system", "subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto"}}))
	result(s)
	if d := details(s, 1); d[0].Start || d[0].Summary != "second summary" {
		t.Fatalf("after a compaction: %+v", d[0])
	}

	plain := newSession(t, false)
	result(plain)
	plain.handleSDKMessage(rawSDK(t, map[string]any{"type": "system", "subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "auto"}}))
	result(plain)
	details(plain, 0)
}

// An agent's session options reach the bridge: the compaction hook in its
// settings, whose command prints the instructions file however its path is
// spelt, and the environment for the compact window, memory and raw log.
func TestAgentOptionsReachTheBridge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in bridge is a shell script")
	}
	run := func(t *testing.T, o adapter.CreateOptions) (sidecarConfig, []string) {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
		t.Setenv("HOME", dir)
		claude := filepath.Join(dir, "claude")
		bridge := filepath.Join(dir, "bridge")
		for path, body := range map[string]string{
			claude: "#!/bin/sh\necho 2.0.0\n",
			bridge: "#!/bin/sh\nenv > \"" + dir + "/env\"\nfor a; do :; done\nprintf '%s' \"$a\" > \"" + dir + "/config\"\n",
		} {
			if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		o.ThreadID, o.Cwd = "t", dir
		a := &Adapter{ClaudePath: claude, bundledSidecar: bridge}
		s, err := a.CreateSession(context.Background(), &fakeHost{}, o)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
		for open := true; open; {
			select {
			case _, open = <-s.Events():
			case <-deadline:
				t.Fatal("stand-in bridge never exited")
			}
		}
		_ = s.Close()
		raw, err := os.ReadFile(filepath.Join(dir, "config"))
		if err != nil {
			t.Fatal(err)
		}
		var cfg sidecarConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		env, err := os.ReadFile(filepath.Join(dir, "env"))
		if err != nil {
			t.Fatal(err)
		}
		return cfg, strings.Split(string(env), "\n")
	}

	dir := t.TempDir()
	instructions := filepath.Join(dir, "it's an agent", "compact.md")
	if err := os.MkdirAll(filepath.Dir(instructions), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instructions, []byte("keep the plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	rawDir := filepath.Join(dir, "raw")
	cfg, env := run(t, adapter.CreateOptions{
		CompactInstructionsFile: instructions, CompactAt: 50_000, NoMemory: true, RawRequestLogDir: rawDir,
	})

	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(cfg.Settings, &settings); err != nil {
		t.Fatalf("settings %s: %v", cfg.Settings, err)
	}
	pre := settings.Hooks["PreCompact"]
	if len(pre) != 1 || len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Type != "command" {
		t.Fatalf("PreCompact = %+v", pre)
	}
	out, err := exec.Command("sh", "-c", pre[0].Hooks[0].Command).Output()
	if err != nil || string(out) != "keep the plan" {
		t.Fatalf("hook printed %q (%v)", out, err)
	}
	for _, kv := range []string{
		// Below Claude Code's floor, so the floor.
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
		"OTEL_LOG_RAW_API_BODIES=file:" + rawDir,
	} {
		if !slices.Contains(env, kv) {
			t.Errorf("env lacks %s", kv)
		}
	}
	if info, err := os.Stat(rawDir); err != nil || !info.IsDir() {
		t.Errorf("raw log folder not made: %v", err)
	}

	_, env = run(t, adapter.CreateOptions{CompactAt: 300_000})
	if !slices.Contains(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=300000") {
		t.Error("a window above the floor is passed as it is")
	}

	cfg, env = run(t, adapter.CreateOptions{})
	if cfg.Settings != nil {
		t.Errorf("a plain thread got settings %s", cfg.Settings)
	}
	for _, kv := range env {
		for _, k := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW=", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=", "OTEL_LOG_RAW_API_BODIES="} {
			if strings.HasPrefix(kv, k) {
				t.Errorf("a plain thread got %s", kv)
			}
		}
	}
}

// The real bridge, with the real SDK, hands the settings to Claude Code as
// its --settings flag, which layers over the user's own settings files.
func TestBridgePassesSettingsToClaude(t *testing.T) {
	a := &Adapter{}
	sidecarDir, err := a.sidecarPath()
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil || !sdkInstalled(sidecarDir) {
		t.Skip("node or the SDK is not installed")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "argv.json")
	stub := filepath.Join(dir, "claude.js")
	if err := os.WriteFile(stub, []byte(`import { writeFileSync } from "node:fs";
writeFileSync(`+strconv.Quote(record)+`, JSON.stringify(process.argv));
setInterval(() => {}, 1000);
`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := compactSettings(filepath.Join(dir, "compact.md"))
	r := resolved{runtime: node, runtimeArgs: []string{filepath.Join(sidecarDir, "sidecar.mjs")}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := r.command(ctx, sidecarConfig{Cwd: dir, ClaudePath: stub, Settings: want, SessionID: "00000000-0000-4000-8000-000000000001"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = dir
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var argv []string
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(record); err == nil && json.Unmarshal(data, &argv) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Claude Code was never started")
		}
	}
	i := slices.Index(argv, "--settings")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("no --settings in %v", argv)
	}
	var got, wantV any
	_ = json.Unmarshal([]byte(argv[i+1]), &got)
	_ = json.Unmarshal(want, &wantV)
	if !reflect.DeepEqual(got, wantV) {
		t.Fatalf("--settings %s, want %s", argv[i+1], want)
	}
}
