package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/datalock"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// migrateHomesMachine is a home with one Omniplex-started Claude conversation
// on ~/.claude and a config entry using it, and returns the transcript path.
func migrateHomesMachine(t *testing.T) (home, transcript string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", "")
	t.Setenv("OMNIPLEX_DB", "")
	transcript = filepath.Join(home, ".claude", "projects", "-p", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user","entrypoint":"sdk-ts","sessionId":"s1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}
	cfg := userconfig.Default()
	cfg.Providers = []json.RawMessage{json.RawMessage(`{"id":"claude","driver":"claude","displayName":"Me","enabled":true}`)}
	if _, err := userconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return home, transcript
}

func openMigrateStore(t *testing.T, path string) *store.Store {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrateHomesRefusesWhileAServerHoldsTheDatabase(t *testing.T) {
	_, transcript := migrateHomesMachine(t)
	db := defaultDB()
	openMigrateStore(t, db)
	lock, err := datalock.Acquire(db)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	for _, args := range [][]string{nil, {"--reverse", "/nowhere.json"}} {
		err := runMigrateHomesCommand(context.Background(), args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "stop the server") {
			t.Errorf("%v: err = %v, want a refusal", args, err)
		}
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Errorf("the refused run moved the conversation: %v", err)
	}

	// A dry run works beside it and changes nothing.
	var out strings.Builder
	if err := runMigrateHomesCommand(context.Background(), []string{"--dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "move 1 conversations") {
		t.Errorf("dry run output:\n%s", out.String())
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Errorf("the dry run moved the conversation: %v", err)
	}
}

func TestMigrateHomesAtStartupOnlyOnTheDefaultDatabase(t *testing.T) {
	home, transcript := migrateHomesMachine(t)
	cfgPath, _ := userconfig.Path()
	before, _ := os.ReadFile(cfgPath)

	// A worktree's dev server shares the machine's home and config.
	dev := filepath.Join(home, "worktree", "omniplex.db")
	migrateHomesAtStartup(context.Background(), openMigrateStore(t, dev), dev, t.Logf)
	if _, err := os.Stat(transcript); err != nil {
		t.Errorf("a dev server's start moved the conversation: %v", err)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("a dev server's start changed the config")
	}

	db := defaultDB()
	migrateHomesAtStartup(context.Background(), openMigrateStore(t, db), db, t.Logf)
	moved := filepath.Join(home, ".omniplex", "instances", "claude", "projects", "-p", "s1.jsonl")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the server's start did not move the conversation: %v", err)
	}
	if _, err := os.Stat(transcript); err == nil {
		t.Error("the conversation is still on ~/.claude")
	}
}
