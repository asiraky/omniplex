package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUnderTreatsOnlyRealDescendantsAsInside(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b/c/d", "/a/b", true},
		// The prefix trap: a sibling whose name starts with the directory's.
		{"/a/bc", "/a/b", false},
		{"/a/b-1/c", "/a/b", false},
		{"/a", "/a/b", false},
		{"/x/y", "/a/b", false},
	}
	for _, c := range cases {
		if got := under(c.path, c.dir); got != c.want {
			t.Errorf("under(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}

func TestDescribeProcsSummarisesBeyondThree(t *testing.T) {
	procs := []procRef{{PID: 1, Name: "a"}, {PID: 2, Name: "b"}, {PID: 3, Name: "c"}, {PID: 4, Name: "d"}, {PID: 5, Name: "e"}}
	got := describeProcs(procs)
	want := "a (pid 1), b (pid 2), c (pid 3), and 2 more"
	if got != want {
		t.Errorf("describeProcs = %q, want %q", got, want)
	}
	if got := describeProcs(procs[:2]); got != "a (pid 1), b (pid 2)" {
		t.Errorf("short list = %q", got)
	}
	if got := describeProcs(nil); got != "" {
		t.Errorf("empty list = %q", got)
	}
}

func TestDescribeProcsFallsBackToThePidWhenTheNameIsUnreadable(t *testing.T) {
	if got := describeProcs([]procRef{{PID: 7}}); got != "7" {
		t.Errorf("describeProcs = %q, want %q", got, "7")
	}
}

// startIn runs a process whose working directory is dir and returns once it is
// actually running, so the /proc scan has something to find.
func startIn(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

func hasPID(procs []procRef, pid int) bool {
	for _, p := range procs {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func TestProcessesInFindsAProcessSittingInTheDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "worktree")
	inner := filepath.Join(target, "apps", "app")
	sibling := filepath.Join(base, "worktree-other")
	for _, d := range []string{inner, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	deep := startIn(t, inner)
	outside := startIn(t, sibling)

	found := processesIn(resolve(target))
	if !hasPID(found, deep) {
		t.Errorf("process in a subdirectory was not reported: %v", found)
	}
	// A sibling directory sharing the target's name prefix must not count, or
	// every delete next to a busy worktree would be refused.
	if hasPID(found, outside) {
		t.Errorf("process outside the target was reported: %v", found)
	}
}

// The process that wedges a delete is precisely the one whose directory has
// already been removed underneath it, which the kernel reports as a "(deleted)"
// symlink. Missing that case would make the retry look safe when it is not.
func TestProcessesInStillFindsAProcessWhoseDirectoryIsAlreadyGone(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "worktree")
	inner := filepath.Join(target, "apps")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := startIn(t, inner)
	resolved := resolve(target)
	if err := os.RemoveAll(inner); err != nil {
		t.Fatal(err)
	}

	if found := processesIn(resolved); !hasPID(found, pid) {
		t.Errorf("process with a deleted cwd was not reported: %v", found)
	}
}

func TestProcessesInReportsNothingForAnIdleDirectory(t *testing.T) {
	if found := processesIn(resolve(t.TempDir())); len(found) != 0 {
		t.Errorf("unexpected processes: %v", found)
	}
}

func TestOrphanedWorktreeOfAcceptsAWorktreeGitHasForgotten(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	rootCommon := resolve(filepath.Join(root, ".git"))
	forgetWorktree(t, root, worktree)

	if !orphanedWorktreeOf(resolve(worktree), rootCommon) {
		t.Fatal("a worktree whose administrative entry Git deleted was not recognised")
	}
}

func TestOrphanedWorktreeOfRejectsAnythingItCannotProveIsOurs(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	rootCommon := resolve(filepath.Join(root, ".git"))

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, ".git"), []byte("gitdir: "+filepath.Join(t.TempDir(), ".git", "worktrees", "x")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	garbage := t.TempDir()
	if err := os.WriteFile(filepath.Join(garbage, ".git"), []byte("not a pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pointsAtAdminRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pointsAtAdminRoot, ".git"), []byte("gitdir: "+filepath.Join(rootCommon, "worktrees")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, ".git"), []byte("gitdir:   \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"another repository's worktree": other,
		"a directory with no .git":      plain,
		"a .git that is not a pointer":  garbage,
		"the worktrees directory":       pointsAtAdminRoot,
		"an empty gitdir":               empty,
		// The project root's .git is a directory, not a pointer file.
		"the project root": root,
	}
	for name, dir := range cases {
		if orphanedWorktreeOf(resolve(dir), rootCommon) {
			t.Errorf("%s was accepted as an orphaned worktree", name)
		}
	}
	// Sanity: the fixture the negative cases are compared against is genuine.
	forgetWorktree(t, root, worktree)
	if !orphanedWorktreeOf(resolve(worktree), rootCommon) {
		t.Fatal("fixture worktree was not recognised")
	}
}
