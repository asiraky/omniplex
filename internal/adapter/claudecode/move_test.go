package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// The conversation and its subagent transcripts land under the other
// account's config directory, in the project directory Claude filed them
// under, and are gone from the first — so --resume under the new account
// finds them, and nothing is left for the old one to resume by mistake.
func TestMoveConversationCarriesTranscriptAndSubagents(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "personal"), filepath.Join(top, "work")
	cwd := "/home/me/code/app"
	key := "-home-me-code-app"
	writeFile(t, filepath.Join(from, "projects", key, "conv.jsonl"), "main")
	writeFile(t, filepath.Join(from, "projects", key, "conv", "subagents", "agent-1.jsonl"), "sub")
	writeFile(t, filepath.Join(from, "projects", key, "other.jsonl"), "someone else")

	a := &Adapter{}
	err := a.MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, cwd, "conv")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(to, "projects", key, "conv.jsonl")); got != "main" {
		t.Errorf("transcript = %q", got)
	}
	if got := readFile(t, filepath.Join(to, "projects", key, "conv", "subagents", "agent-1.jsonl")); got != "sub" {
		t.Errorf("subagent transcript = %q", got)
	}
	if exists(filepath.Join(from, "projects", key, "conv.jsonl")) || exists(filepath.Join(from, "projects", key, "conv")) {
		t.Error("the conversation was left behind under the old account")
	}
	if !exists(filepath.Join(from, "projects", key, "other.jsonl")) {
		t.Error("another conversation was moved")
	}
}

// Claude shortens long project keys in ways of its own, so the conversation
// is found by id wherever it was filed, and filed under the same key.
func TestMoveConversationFindsTranscriptUnderAnyProjectKey(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "a"), filepath.Join(top, "b")
	writeFile(t, filepath.Join(from, "projects", "shortened-abc123", "conv.jsonl"), "main")

	err := (&Adapter{}).MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, "/some/very/long/path", "conv")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(to, "projects", "shortened-abc123", "conv.jsonl")); got != "main" {
		t.Errorf("transcript = %q", got)
	}
}

// An existing conversation of the same id under the other account is never
// overwritten, and the source is left where it was.
func TestMoveConversationRefusesToOverwrite(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "a"), filepath.Join(top, "b")
	writeFile(t, filepath.Join(from, "projects", "k", "conv.jsonl"), "mine")
	writeFile(t, filepath.Join(to, "projects", "k", "conv.jsonl"), "theirs")

	err := (&Adapter{}).MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, "/x", "conv")
	if err == nil {
		t.Fatal("overwrote the other account's conversation")
	}
	if readFile(t, filepath.Join(to, "projects", "k", "conv.jsonl")) != "theirs" || readFile(t, filepath.Join(from, "projects", "k", "conv.jsonl")) != "mine" {
		t.Error("a refused move changed files")
	}
}

// A clash on the subagent directory rolls the main transcript back, so the
// conversation is never split across two accounts.
func TestMoveConversationRollsBackOnSubagentClash(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "a"), filepath.Join(top, "b")
	writeFile(t, filepath.Join(from, "projects", "k", "conv.jsonl"), "mine")
	writeFile(t, filepath.Join(from, "projects", "k", "conv", "subagents", "s.jsonl"), "sub")
	writeFile(t, filepath.Join(to, "projects", "k", "conv", "subagents", "s.jsonl"), "theirs")

	if err := (&Adapter{}).MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, "/x", "conv"); err == nil {
		t.Fatal("moved into an existing subagent directory")
	}
	if readFile(t, filepath.Join(from, "projects", "k", "conv.jsonl")) != "mine" {
		t.Error("the main transcript was not rolled back")
	}
	if exists(filepath.Join(to, "projects", "k", "conv.jsonl")) {
		t.Error("the main transcript was left under the other account")
	}
}

// Accounts that share a config directory share conversations; a session
// that never ran has none. Neither is an error.
func TestMoveConversationNothingToMove(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, "shared")
	writeFile(t, filepath.Join(dir, "projects", "k", "conv.jsonl"), "main")
	env := map[string]string{"CLAUDE_CONFIG_DIR": dir}
	if err := (&Adapter{}).MoveConversation(env, map[string]string{"CLAUDE_CONFIG_DIR": dir + "/"}, "/x", "conv"); err != nil {
		t.Fatalf("shared directory: %v", err)
	}
	if !exists(filepath.Join(dir, "projects", "k", "conv.jsonl")) {
		t.Error("a shared-directory switch touched the conversation")
	}
	if err := (&Adapter{}).MoveConversation(env, map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(top, "b")}, "/x", "never-ran"); err != nil {
		t.Fatalf("missing conversation: %v", err)
	}
}
