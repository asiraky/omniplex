package codexapp

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRollout(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRollout(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// The rollout lands under the other account's home at the same dated path,
// where Codex looks for it on resume, and is gone from the first account;
// other conversations stay.
func TestMoveConversationCarriesRolloutToTheOtherHome(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "personal"), filepath.Join(top, "work")
	rel := filepath.Join("sessions", "2026", "10", "15", "rollout-2026-10-15T09-00-00-019a-conv.jsonl")
	archived := filepath.Join("archived_sessions", "rollout-2026-09-01T09-00-00-019a-old.jsonl")
	writeRollout(t, filepath.Join(from, rel), "conv")
	writeRollout(t, filepath.Join(from, archived), "old")
	writeRollout(t, filepath.Join(from, "sessions", "2026", "10", "15", "rollout-2026-10-15T10-00-00-019a-other.jsonl"), "other")

	a := &Adapter{}
	fromEnv, toEnv := map[string]string{"CODEX_HOME": from}, map[string]string{"CODEX_HOME": to}
	if err := a.MoveConversation(fromEnv, toEnv, "/x", "019a-conv"); err != nil {
		t.Fatal(err)
	}
	if readRollout(t, filepath.Join(to, rel)) != "conv" {
		t.Error("rollout did not arrive at its dated path")
	}
	if _, err := os.Lstat(filepath.Join(from, rel)); err == nil {
		t.Error("rollout left under the old account")
	}
	if err := a.MoveConversation(fromEnv, toEnv, "/x", "019a-old"); err != nil {
		t.Fatal(err)
	}
	if readRollout(t, filepath.Join(to, archived)) != "old" {
		t.Error("archived rollout did not move")
	}
	if readRollout(t, filepath.Join(from, "sessions", "2026", "10", "15", "rollout-2026-10-15T10-00-00-019a-other.jsonl")) != "other" {
		t.Error("another conversation was touched")
	}
}

// An existing rollout under the other account is never overwritten; a shared
// home or a thread with no rollout is nothing to do.
func TestMoveConversationRefusesToOverwriteARollout(t *testing.T) {
	top := t.TempDir()
	from, to := filepath.Join(top, "a"), filepath.Join(top, "b")
	rel := filepath.Join("sessions", "2026", "10", "15", "rollout-t-019a-conv.jsonl")
	writeRollout(t, filepath.Join(from, rel), "mine")
	writeRollout(t, filepath.Join(to, rel), "theirs")
	a := &Adapter{}
	if err := a.MoveConversation(map[string]string{"CODEX_HOME": from}, map[string]string{"CODEX_HOME": to}, "/x", "019a-conv"); err == nil {
		t.Fatal("overwrote the other account's rollout")
	}
	if readRollout(t, filepath.Join(to, rel)) != "theirs" || readRollout(t, filepath.Join(from, rel)) != "mine" {
		t.Error("a refused move changed files")
	}
	if err := a.MoveConversation(map[string]string{"CODEX_HOME": from}, map[string]string{"CODEX_HOME": from + "/"}, "/x", "019a-conv"); err != nil {
		t.Errorf("shared home: %v", err)
	}
	if err := a.MoveConversation(map[string]string{"CODEX_HOME": from}, map[string]string{"CODEX_HOME": to}, "/x", "never-ran"); err != nil {
		t.Errorf("no rollout: %v", err)
	}
}
