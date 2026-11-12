package codexapp

import (
	"errors"
	"io/fs"
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

func gone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

const thread = "01a0f001-3582-7c00-b2d2-ee2d223c5a7b"

var (
	live      = filepath.Join("sessions", "2026", "09", "30", "rollout-2026-09-30T11-50-06-"+thread+".jsonl")
	archived  = filepath.Join("archived_sessions", "2026", "08", "01", "rollout-2026-08-01T09-00-00-"+thread+".jsonl")
	unrelated = filepath.Join("sessions", "2026", "09", "30", "rollout-2026-09-30T12-00-00-01a0f0e6-f013-72b3-8f24-98c830b93229.jsonl")
)

func homes(t *testing.T) (from, to string, fromEnv, toEnv map[string]string) {
	t.Helper()
	top := t.TempDir()
	t.Setenv("HOME", top)
	from, to = filepath.Join(top, "personal"), filepath.Join(top, "work")
	return from, to, map[string]string{"CODEX_HOME": from}, map[string]string{"CODEX_HOME": to}
}

// Every rollout of the thread lands under the other account's home at the
// same dated path, and nothing of it is left for the old account.
func TestMoveConversationCarriesEveryRolloutKeepingItsDate(t *testing.T) {
	from, to, fromEnv, toEnv := homes(t)
	writeRollout(t, filepath.Join(from, live), "live")
	writeRollout(t, filepath.Join(from, archived), "archived")
	writeRollout(t, filepath.Join(from, unrelated), "someone else")

	if err := New("").MoveConversation(fromEnv, toEnv, "/x", thread); err != nil {
		t.Fatal(err)
	}
	if got := readRollout(t, filepath.Join(to, live)); got != "live" {
		t.Errorf("live rollout = %q", got)
	}
	if got := readRollout(t, filepath.Join(to, archived)); got != "archived" {
		t.Errorf("archived rollout = %q", got)
	}
	if !gone(filepath.Join(from, live)) || !gone(filepath.Join(from, archived)) {
		t.Error("a rollout was left under the old account")
	}
	if readRollout(t, filepath.Join(from, unrelated)) != "someone else" || !gone(filepath.Join(to, unrelated)) {
		t.Error("another thread's rollout was moved")
	}
}

// No CODEX_HOME in an overlay is Codex's own ~/.codex, never the server
// process's CODEX_HOME.
func TestMoveConversationWithoutCodexHomeUsesTheDefaultHome(t *testing.T) {
	_, to, _, toEnv := homes(t)
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "ambient"))
	def := filepath.Join(os.Getenv("HOME"), ".codex")
	writeRollout(t, filepath.Join(def, live), "live")

	if err := New("").MoveConversation(nil, toEnv, "/x", thread); err != nil {
		t.Fatal(err)
	}
	if got := readRollout(t, filepath.Join(to, live)); got != "live" {
		t.Errorf("rollout = %q", got)
	}
}

// A rollout the other account already has is never overwritten, and nothing
// moves: the thread stays whole under one home.
func TestMoveConversationRefusesToOverwrite(t *testing.T) {
	from, to, fromEnv, toEnv := homes(t)
	writeRollout(t, filepath.Join(from, live), "mine")
	writeRollout(t, filepath.Join(from, archived), "mine archived")
	writeRollout(t, filepath.Join(to, archived), "theirs")

	if err := New("").MoveConversation(fromEnv, toEnv, "/x", thread); err == nil {
		t.Fatal("overwrote the other account's rollout")
	}
	if readRollout(t, filepath.Join(from, live)) != "mine" || readRollout(t, filepath.Join(from, archived)) != "mine archived" {
		t.Error("a refused move changed the source")
	}
	if readRollout(t, filepath.Join(to, archived)) != "theirs" || !gone(filepath.Join(to, live)) {
		t.Error("a refused move changed the destination")
	}
}

// A rollout that fails to move after another has moved puts the first one
// back.
func TestMoveConversationRollsBackWhenALaterRolloutFails(t *testing.T) {
	from, to, fromEnv, toEnv := homes(t)
	writeRollout(t, filepath.Join(from, live), "live")
	writeRollout(t, filepath.Join(from, archived), "archived")
	// The archived rollout's date folder cannot be made under the other home.
	writeRollout(t, filepath.Join(to, "archived_sessions"), "a file, not a folder")

	if err := New("").MoveConversation(fromEnv, toEnv, "/x", thread); err == nil {
		t.Fatal("expected the second rollout to fail")
	}
	if readRollout(t, filepath.Join(from, live)) != "live" || readRollout(t, filepath.Join(from, archived)) != "archived" {
		t.Error("the moved rollout was not put back")
	}
	if !gone(filepath.Join(to, live)) {
		t.Error("a rollout was left under the other account")
	}
}

// Accounts that share a home share the thread; a thread that never ran has
// no rollout. Neither is an error.
func TestMoveConversationNothingToMove(t *testing.T) {
	from, to, fromEnv, toEnv := homes(t)
	writeRollout(t, filepath.Join(from, live), "live")
	if err := New("").MoveConversation(fromEnv, map[string]string{"CODEX_HOME": from + "/"}, "/x", thread); err != nil {
		t.Fatalf("shared home: %v", err)
	}
	if readRollout(t, filepath.Join(from, live)) != "live" {
		t.Error("a shared-home switch touched the rollout")
	}
	if err := New("").MoveConversation(fromEnv, toEnv, "/x", "never-ran"); err != nil {
		t.Fatalf("missing thread: %v", err)
	}
	if !gone(to) {
		t.Error("a thread with no rollout made the other home")
	}
}
