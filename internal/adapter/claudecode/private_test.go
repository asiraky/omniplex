package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
)

func privateSetup(t *testing.T) {
	t.Helper()
	dir, err := ephemeral.Init(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
}

// A private thread's conversation moves to another account without a byte of
// it reaching that account's folder: the folder gets a link to the one
// transcript in memory, which both accounts then see.
func TestMovingAPrivateConversationOnlyLinks(t *testing.T) {
	privateSetup(t)
	id := ephemeral.NewID()
	cwd := ephemeral.ThreadDir(id)
	top := t.TempDir()
	from, to := filepath.Join(top, "personal"), filepath.Join(top, "work")
	if linked, err := privateTranscripts(from, cwd); err != nil || !linked {
		t.Fatalf("link = %v, %v", linked, err)
	}
	conv := ephemeral.Bare(id)
	writeFile(t, filepath.Join(from, "projects", projectKey(cwd), conv+".jsonl"), "main")

	a := &Adapter{}
	if err := a.MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, cwd, conv); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(to, "projects", projectKey(cwd))
	if info, err := os.Lstat(dst); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the other account's project folder: %v, want a link", err)
	}
	if got := readFile(t, filepath.Join(dst, conv+".jsonl")); got != "main" {
		t.Fatalf("transcript under the other account = %q", got)
	}

	if err := ephemeral.Forget(id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dst, filepath.Join(from, "projects", projectKey(cwd))} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after close: %v, want gone", p, err)
		}
	}
}

// A folder that is no private thread's gets no link.
func TestOrdinaryFolderGetsNoPrivateLink(t *testing.T) {
	privateSetup(t)
	cfg := t.TempDir()
	if linked, err := privateTranscripts(cfg, "/home/me/code/app"); err != nil || linked {
		t.Fatalf("link = %v, %v; want none", linked, err)
	}
	if exists(filepath.Join(cfg, "projects")) {
		t.Fatal("made a projects folder for an ordinary thread")
	}
}

// Links left by an earlier run, whose scratch folder is gone, are swept; a
// live link, a real folder, and anyone else's link are kept.
func TestSweepRemovesOnlyDeadPrivateLinks(t *testing.T) {
	privateSetup(t)
	cfg := t.TempDir()
	live := ephemeral.NewID()
	if _, err := privateTranscripts(cfg, ephemeral.ThreadDir(live)); err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(cfg, "projects")
	gone := filepath.Join(ephemeral.Dir(), "state", ephemeral.NewID(), "gone")
	dead := filepath.Join(projects, projectKey(ephemeral.ThreadDir(ephemeral.NewID())))
	deadEnv := filepath.Join(cfg, "session-env", "0f0f")
	if err := os.MkdirAll(filepath.Dir(deadEnv), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dead, deadEnv} {
		if err := os.Symlink(gone, p); err != nil {
			t.Fatal(err)
		}
	}
	// Someone else's link to nowhere, under a name a session could have.
	otherEnv := filepath.Join(cfg, "session-env", "1e1e")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), otherEnv); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(projects, projectKey(ephemeral.ThreadDir(ephemeral.NewID())))
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(projects, "-dev-shm-omniplex-private-000000000000-threads-private-x")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), foreign); err != nil {
		t.Fatal(err)
	}

	sweepPrivateLinks(cfg)
	for _, p := range []string{dead, deadEnv} {
		if exists(p) {
			t.Errorf("dead link %s survived the sweep", p)
		}
	}
	liveEnv := filepath.Join(cfg, "session-env", ephemeral.Bare(live))
	for _, p := range []string{filepath.Join(projects, projectKey(ephemeral.ThreadDir(live))), liveEnv, real, foreign, otherEnv} {
		if !exists(p) {
			t.Errorf("%s was swept", p)
		}
	}
}
