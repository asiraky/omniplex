package skills

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// dotfiles is a machine whose library is a folder of a git repository that
// holds other things too, with two skills and the record committed.
func dotfiles(t *testing.T) (Roots, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := machine(t)
	isolateGit(t, r)
	repo := filepath.Join(r.Home, "dot")
	r.Library = filepath.Join(repo, "agents", "skills")
	writeAll(t, r.Library, map[string]string{
		"alpha/SKILL.md":       skillMD("alpha", "A"),
		"alpha/scripts/run.sh": "#!/bin/sh\n",
		"beta/SKILL.md":        skillMD("beta", "B"),
		"beta/notes.md":        "notes",
		RecordFile:             `{"version":1,"skills":{}}`,
	})
	write(t, filepath.Join(repo, "zshrc"), "export A=1\n")
	git(t, repo, "init", "-q", "-b", "main")
	// CommitSkills runs git as the user would, so who they are is the repo's.
	git(t, repo, "config", "user.name", "t")
	git(t, repo, "config", "user.email", "t@example.invalid")
	git(t, repo, "config", "commit.gpgsign", "false")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "start")
	return r, repo
}

func gitSays(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func lines(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// edit leaves the library with one skill changed, one removed, one new, the
// record changed, and the rest of the repository changed too.
func edit(t *testing.T, r Roots, repo string) {
	t.Helper()
	write(t, filepath.Join(r.Library, "alpha", "SKILL.md"), skillMD("alpha", "A, edited"))
	write(t, filepath.Join(r.Library, "alpha", "new.md"), "new")
	if err := os.RemoveAll(filepath.Join(r.Library, "beta")); err != nil {
		t.Fatal(err)
	}
	writeAll(t, filepath.Join(r.Library, "gamma"), map[string]string{"SKILL.md": skillMD("gamma", "G"), "a/b/c.md": "deep", "d.md": "d"})
	write(t, filepath.Join(r.Library, RecordFile), `{"version":1,"skills":{"gamma":{}}}`)
	write(t, filepath.Join(repo, "zshrc"), "export A=2\n")
	write(t, filepath.Join(repo, "agents", "notes.md"), "beside the library")
}

func TestLibraryStatus(t *testing.T) {
	r, repo := dotfiles(t)
	ctx := context.Background()

	clean, err := LibraryStatus(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if clean == nil || clean.Root != repo || clean.Branch != "main" || clean.Changes == nil || len(clean.Changes) != 0 {
		t.Fatalf("clean status = %+v", clean)
	}

	edit(t, r, repo)
	// Staged or not is git's business: a change counts once either way.
	git(t, repo, "add", "agents/skills/alpha/new.md")
	write(t, filepath.Join(r.Library, "alpha", "new.md"), "new, and edited after staging")

	got, err := LibraryStatus(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	want := []GitChange{
		{Name: RecordFile, Status: ChangeModified, Files: 1},
		{Name: "alpha", Status: ChangeModified, Files: 2},
		{Name: "beta", Status: ChangeRemoved, Files: 2},
		{Name: "gamma", Status: ChangeAdded, Files: 3},
	}
	if !reflect.DeepEqual(got.Changes, want) {
		t.Errorf("changes = %+v\nwant %+v", got.Changes, want)
	}
}

func TestLibraryStatusWhereThereIsNothingToReport(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ctx := context.Background()
	t.Run("no library yet", func(t *testing.T) {
		r := machine(t)
		isolateGit(t, r)
		if got, err := LibraryStatus(ctx, r); got != nil || err != nil {
			t.Errorf("status = %+v, %v", got, err)
		}
	})
	t.Run("a library outside any repository", func(t *testing.T) {
		r := machine(t)
		isolateGit(t, r)
		write(t, filepath.Join(r.Library, "one", "SKILL.md"), skillMD("one", "One"))
		if got, err := LibraryStatus(ctx, r); got != nil || err != nil {
			t.Errorf("status = %+v, %v", got, err)
		}
		if _, _, err := CommitSkills(ctx, r, []string{"one"}, "add one"); !errors.Is(err, ErrInvalid) {
			t.Errorf("commit = %v", err)
		}
	})
	t.Run("a repository with no commit yet", func(t *testing.T) {
		r := machine(t)
		isolateGit(t, r)
		write(t, filepath.Join(r.Library, "one", "SKILL.md"), skillMD("one", "One"))
		git(t, r.Library, "init", "-q", "-b", "trunk")
		got, err := LibraryStatus(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if got.Root != r.Library || got.Branch != "trunk" || !reflect.DeepEqual(got.Changes, []GitChange{{Name: "one", Status: ChangeAdded, Files: 1}}) {
			t.Errorf("status = %+v", got)
		}
	})
	t.Run("a library reached through a link", func(t *testing.T) {
		r, repo := dotfiles(t)
		link(t, r.Library, filepath.Join(r.Home, ".agents", "skills"))
		r.Library = filepath.Join(r.Home, ".agents", "skills")
		write(t, filepath.Join(r.Library, "alpha", "new.md"), "new")
		got, err := LibraryStatus(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if got.Root != repo || !reflect.DeepEqual(got.Changes, []GitChange{{Name: "alpha", Status: ChangeModified, Files: 1}}) {
			t.Errorf("status = %+v", got)
		}
	})
}

func TestCommitSkills(t *testing.T) {
	r, repo := dotfiles(t)
	ctx := context.Background()
	remote := filepath.Join(r.Home, "remote.git")
	git(t, r.Home, "init", "-q", "--bare", "-b", "main", remote)
	git(t, repo, "remote", "add", "origin", remote)
	git(t, repo, "push", "-q", "-u", "origin", "main")
	start := gitSays(t, repo, "rev-parse", "HEAD")

	edit(t, r, repo)
	// Something the user had staged themselves, outside the library.
	git(t, repo, "add", "zshrc")

	commit, after, err := CommitSkills(ctx, r, []string{"gamma", RecordFile, "gamma"}, "  Add gamma\n")
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" || commit != gitSays(t, repo, "rev-parse", "--short", "HEAD") {
		t.Errorf("commit = %q, HEAD is %s", commit, gitSays(t, repo, "rev-parse", "--short", "HEAD"))
	}
	if gitSays(t, repo, "rev-parse", "HEAD~1") != start {
		t.Error("more than one commit was made")
	}
	if got := gitSays(t, repo, "log", "-1", "--format=%B"); got != "Add gamma" {
		t.Errorf("message = %q", got)
	}
	committed := lines(gitSays(t, repo, "show", "--name-only", "--format=", "HEAD"))
	wantFiles := []string{
		"agents/skills/" + RecordFile,
		"agents/skills/gamma/SKILL.md",
		"agents/skills/gamma/a/b/c.md",
		"agents/skills/gamma/d.md",
	}
	if !reflect.DeepEqual(committed, wantFiles) {
		t.Errorf("committed %v\nwant %v", committed, wantFiles)
	}
	// Everything else is as the user left it: staged stays staged, the rest
	// stays in the work tree.
	if staged := lines(gitSays(t, repo, "diff", "--cached", "--name-only")); !reflect.DeepEqual(staged, []string{"zshrc"}) {
		t.Errorf("staged after the commit: %v", staged)
	}
	wantLeft := []GitChange{
		{Name: "alpha", Status: ChangeModified, Files: 2},
		{Name: "beta", Status: ChangeRemoved, Files: 2},
	}
	if after == nil || !reflect.DeepEqual(after.Changes, wantLeft) {
		t.Errorf("status after = %+v", after)
	}
	if !exists(filepath.Join(repo, "agents", "notes.md")) || gitSays(t, repo, "ls-files", "agents/notes.md") != "" {
		t.Error("a file beside the library was committed")
	}

	// A removal is a change like any other.
	if _, after, err = CommitSkills(ctx, r, []string{"beta"}, "Drop beta"); err != nil {
		t.Fatal(err)
	}
	if gitSays(t, repo, "ls-tree", "-r", "--name-only", "HEAD", "agents/skills/beta") != "" {
		t.Error("beta is still in the commit")
	}
	if !reflect.DeepEqual(after.Changes, wantLeft[:1]) {
		t.Errorf("status after = %+v", after.Changes)
	}
	if got := gitSays(t, remote, "rev-parse", "main"); got != start {
		t.Errorf("the remote moved to %s", got)
	}
}

func TestCommitSkillsRefusals(t *testing.T) {
	r, repo := dotfiles(t)
	ctx := context.Background()
	edit(t, r, repo)
	git(t, repo, "add", "zshrc")
	start := gitSays(t, repo, "rev-parse", "HEAD")
	status := gitSays(t, repo, "status", "--porcelain")

	tests := []struct {
		name    string
		names   []string
		message string
	}{
		{"no message", []string{"alpha"}, " \n"},
		{"nothing named", nil, "msg"},
		{"a skill with nothing to commit", []string{"alpha", "delta"}, "msg"},
		{"a file inside a skill", []string{"alpha/SKILL.md"}, "msg"},
		{"the library itself", []string{"."}, "msg"},
		{"out of the library", []string{"../notes.md"}, "msg"},
		{"the rest of the repository", []string{"../../zshrc"}, "msg"},
		{"an absolute path", []string{filepath.Join(repo, "zshrc")}, "msg"},
		{"a flag", []string{"-A"}, "msg"},
		{"pathspec magic", []string{":/"}, "msg"},
		{"nothing for a name", []string{""}, "msg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if commit, _, err := CommitSkills(ctx, r, tt.names, tt.message); !errors.Is(err, ErrInvalid) {
				t.Errorf("commit %q, err %v", commit, err)
			}
			if gitSays(t, repo, "rev-parse", "HEAD") != start || gitSays(t, repo, "status", "--porcelain") != status {
				t.Error("a refused commit changed the repository")
			}
		})
	}
}

func TestCommitSkillsTakesNamesLiterally(t *testing.T) {
	r, repo := dotfiles(t)
	ctx := context.Background()
	edit(t, r, repo)
	// Legal folder names that git would read as "everything" or as a flag if
	// they were handed over as pathspecs or arguments.
	for _, name := range []string{":(top)", "-A", "*"} {
		write(t, filepath.Join(r.Library, name, "SKILL.md"), skillMD("odd", "An odd folder"))
	}
	for _, name := range []string{":(top)", "-A", "*"} {
		if _, _, err := CommitSkills(ctx, r, []string{name}, "Add an odd one"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := lines(gitSays(t, repo, "show", "--name-only", "--format=", "HEAD"))
		if !reflect.DeepEqual(got, []string{"agents/skills/" + name + "/SKILL.md"}) {
			t.Errorf("committing %q took %v", name, got)
		}
	}
}

func TestCommitSkillsSaysWhyAHookRefused(t *testing.T) {
	r, repo := dotfiles(t)
	ctx := context.Background()
	edit(t, r, repo)
	start := gitSays(t, repo, "rev-parse", "HEAD")
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\necho 'lint: gamma is not ready' >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := CommitSkills(ctx, r, []string{"gamma"}, "Add gamma")
	if err == nil || !strings.Contains(err.Error(), "gamma is not ready") {
		t.Errorf("err = %v, want the hook's reason", err)
	}
	if gitSays(t, repo, "rev-parse", "HEAD") != start {
		t.Error("a commit the hook refused was made")
	}
	// The user's hooks are theirs: nothing here goes round them.
	status, err := LibraryStatus(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range status.Changes {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{RecordFile, "alpha", "beta", "gamma"}) {
		t.Errorf("changes after = %v", names)
	}
}

func TestLibraryStatusStopsWithItsContext(t *testing.T) {
	r, _ := dotfiles(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := LibraryStatus(ctx, r); !errors.Is(err, context.Canceled) {
		t.Errorf("status = %+v, err = %v", got, err)
	}
}
