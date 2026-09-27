package relocate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
)

func TestRunRepairsDatabaseLifecycleGitAndClaudeState(t *testing.T) {
	ctx := context.Background()
	top := t.TempDir()
	home := filepath.Join(top, "home")
	oldRoot, newRoot := filepath.Join(top, "old-project"), filepath.Join(top, "new-project")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, oldRoot, "init", "-b", "main")
	git(t, oldRoot, "config", "user.email", "test@example.com")
	git(t, oldRoot, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(oldRoot, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, oldRoot, "add", "README")
	git(t, oldRoot, "commit", "-m", "base")
	worktree := filepath.Join(oldRoot, ".worktrees", "one")
	git(t, oldRoot, "worktree", "add", "-b", "one", worktree)

	dbPath := filepath.Join(home, ".omniplex", "omniplex.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	if err := st.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	meta := store.ThreadMeta{ID: "s1", Cwd: worktree, Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	stateDir := filepath.Join(home, ".omniplex", "workspaces", meta.ID)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "context.json")
	if err := os.WriteFile(statePath, []byte(`{"projectRoot":"`+oldRoot+`","cwd":"`+worktree+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(home, ".claude", "projects", claudeProjectKey(worktree))
	if err := os.MkdirAll(transcript, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcript, "conversation.jsonl"), []byte("history\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	report, err := Run(ctx, oldRoot, newRoot, Options{DBPath: dbPath, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if !report.GitRepaired || report.Database.Threads != 1 || report.WorkspaceFiles != 1 || report.ClaudeTranscripts != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	newWorktree := filepath.Join(newRoot, ".worktrees", "one")
	git(t, newWorktree, "status", "--short")
	newTranscript := filepath.Join(home, ".claude", "projects", claudeProjectKey(newWorktree), "conversation.jsonl")
	if blob, err := os.ReadFile(newTranscript); err != nil || string(blob) != "history\n" {
		t.Fatalf("transcript = %q, %v", blob, err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Fatalf("old transcript still exists: %v", err)
	}
	state, err := os.ReadFile(statePath)
	if err != nil || strings.Contains(string(state), oldRoot) || !strings.Contains(string(state), newRoot) {
		t.Fatalf("state = %s, %v", state, err)
	}
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Thread(ctx, meta.ID)
	if err != nil || got.Cwd != newWorktree {
		t.Fatalf("thread = %+v, %v", got, err)
	}
}

func TestRunRefusesTranscriptCollisionBeforeChangingState(t *testing.T) {
	ctx := context.Background()
	top, home := t.TempDir(), t.TempDir()
	oldRoot, newRoot := filepath.Join(top, "old"), filepath.Join(top, "new")
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(top, "hy.db")
	st, _ := store.Open(dbPath)
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	_ = st.PutProject(ctx, p)
	meta := store.ThreadMeta{ID: "s1", Cwd: oldRoot, Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	_ = st.CreateThread(ctx, meta)
	_ = st.Close()
	for _, cwd := range []string{oldRoot, newRoot} {
		if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", claudeProjectKey(cwd)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Run(ctx, oldRoot, newRoot, Options{DBPath: dbPath, HomeDir: home}); err == nil || !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("collision error = %v", err)
	}
	st, _ = store.Open(dbPath)
	defer st.Close()
	got, _ := st.Project(ctx, p.ID)
	if got.Root != oldRoot {
		t.Fatalf("database changed despite preflight failure: %s", got.Root)
	}
}

func TestRunRelocatesCanonicalizedThreadPath(t *testing.T) {
	ctx := context.Background()
	top, home := t.TempDir(), t.TempDir()
	realParent := filepath.Join(top, "real")
	aliasParent := filepath.Join(top, "alias")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Fatal(err)
	}
	oldRoot, newRoot := filepath.Join(aliasParent, "old"), filepath.Join(aliasParent, "new")
	realOld, realNew := filepath.Join(realParent, "old"), filepath.Join(realParent, "new")
	if err := os.MkdirAll(filepath.Join(realOld, "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(top, "hy.db")
	st, _ := store.Open(dbPath)
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	_ = st.PutProject(ctx, p)
	canonicalWorktree, err := filepath.EvalSymlinks(filepath.Join(oldRoot, "worktree"))
	if err != nil {
		t.Fatal(err)
	}
	meta := store.ThreadMeta{ID: "s1", Cwd: canonicalWorktree, Harness: "codex", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	_ = st.CreateThread(ctx, meta)
	_ = st.Close()
	if err := os.Rename(realOld, realNew); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, oldRoot, newRoot, Options{DBPath: dbPath, HomeDir: home}); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Open(dbPath)
	defer st.Close()
	got, _ := st.Thread(ctx, meta.ID)
	want, err := filepath.EvalSymlinks(filepath.Join(newRoot, "worktree"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != want {
		t.Fatalf("canonical cwd = %q", got.Cwd)
	}
}

func TestClaudeProjectKeyMatchesClaudeCodeSanitizing(t *testing.T) {
	got := claudeProjectKey("/Users/ada/code/acme/.worktrees/one")
	if got != "-Users-ada-code-acme--worktrees-one" {
		t.Fatalf("key = %q", got)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
