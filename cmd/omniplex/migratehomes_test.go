package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter/claudecode"
	"github.com/asiraky/omniplex/internal/datalock"
)

// ownedClaudeHome lays out a temp HOME with one old Omniplex conversation in
// ~/.claude and returns its path.
func ownedClaudeHome(t *testing.T) (home, transcript string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", "")
	t.Setenv("OMNIPLEX_DB", "")
	t.Setenv("OMNIPLEX_MIGRATE_HOMES", "")
	transcript = filepath.Join(home, ".claude", "projects", "-p", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user","entrypoint":"sdk-ts"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".omniplex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".omniplex", "config.json"), []byte(`{"version":1,"providers":[{"id":"claude","driver":"claude"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, transcript
}

func TestStartupMigrationOnlyRunsOnTheMachinesOwnData(t *testing.T) {
	home, transcript := ownedClaudeHome(t)
	var log []string
	logf := func(format string, args ...any) { log = append(log, format) }

	// A worktree's dev server: its own database, the real home.
	migrateHomesAtStartup(context.Background(), nil, filepath.Join(home, "worktree", ".omniplex", "dev.db"), logf, claudecode.New(""))
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("a dev server moved the machine's conversation: %v", err)
	}

	t.Setenv("OMNIPLEX_MIGRATE_HOMES", "off")
	migrateHomesAtStartup(context.Background(), nil, filepath.Join(home, ".omniplex", "omniplex.db"), logf, claudecode.New(""))
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("the migration ran while turned off: %v", err)
	}

	t.Setenv("OMNIPLEX_MIGRATE_HOMES", "")
	migrateHomesAtStartup(context.Background(), nil, filepath.Join(home, ".omniplex", "omniplex.db"), logf, claudecode.New(""))
	if _, err := os.Stat(filepath.Join(home, ".omniplex", "instances", "claude", "projects", "-p", "s1.jsonl")); err != nil {
		t.Fatalf("the server on the machine's data did not migrate: %v (log %v)", err, log)
	}
}

func TestMigrateHomesDryRunChangesNothing(t *testing.T) {
	home, _ := ownedClaudeHome(t)
	before := snapshot(t, home)
	var out bytes.Buffer
	if err := runMigrateHomesCommand(context.Background(), []string{"--dry-run"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatalf("a dry run printed nothing")
	}
	if after := snapshot(t, home); after != before {
		t.Fatalf("a dry run changed the home:\nbefore %s\nafter  %s", before, after)
	}
}

func TestMigrateHomesRefusesWhileTheServerRuns(t *testing.T) {
	home, transcript := ownedClaudeHome(t)
	lock, err := datalock.Acquire(filepath.Join(home, ".omniplex", "omniplex.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := runMigrateHomesCommand(context.Background(), nil, &bytes.Buffer{}); err == nil {
		t.Fatalf("a real run went ahead with the server holding the database")
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("the conversation moved: %v", err)
	}
}

// snapshot is every path under root with its size, mode and content.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		b.WriteString(path + " " + info.Mode().String())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			b.Write(data)
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
