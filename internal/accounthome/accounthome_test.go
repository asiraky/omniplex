package accounthome

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var entries = []Entry{
	{Name: "settings.json"},
	{Name: "CLAUDE.md"},
	{Name: "skills", Dir: true},
	{Name: "agents", Dir: true},
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func isLink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.Mode()&os.ModeSymlink != 0
}

func TestEveryAccountSeesTheSharedConfiguration(t *testing.T) {
	shared := t.TempDir()
	write(t, filepath.Join(shared, "settings.json"), `{"model":"opus"}`)
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")

	root := t.TempDir()
	for _, id := range []string{"work", "personal", "client"} {
		account := filepath.Join(root, id)
		if err := Link(shared, account, entries); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(account, "skills", "infra", "SKILL.md")); got != "infra" {
			t.Fatalf("%s skill = %q", id, got)
		}
		if got := read(t, filepath.Join(account, "settings.json")); got != `{"model":"opus"}` {
			t.Fatalf("%s settings = %q", id, got)
		}
	}

	// Something added later through any account is in every account.
	write(t, filepath.Join(root, "work", "skills", "deploy", "SKILL.md"), "deploy")
	write(t, filepath.Join(root, "personal", "agents", "reviewer.md"), "reviewer")
	for _, id := range []string{"work", "personal", "client"} {
		if got := read(t, filepath.Join(root, id, "skills", "deploy", "SKILL.md")); got != "deploy" {
			t.Fatalf("%s deploy = %q", id, got)
		}
		if got := read(t, filepath.Join(root, id, "agents", "reviewer.md")); got != "reviewer" {
			t.Fatalf("%s agent = %q", id, got)
		}
	}
	if got := read(t, filepath.Join(shared, "skills", "deploy", "SKILL.md")); got != "deploy" {
		t.Fatalf("shared deploy = %q", got)
	}
}

func TestAMissingSharedFileIsNotLinkedUntilItExists(t *testing.T) {
	shared, account := t.TempDir(), filepath.Join(t.TempDir(), "a")
	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(account, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CLAUDE.md linked before it exists: %v", err)
	}
	write(t, filepath.Join(shared, "CLAUDE.md"), "rules")
	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(account, "CLAUDE.md")); got != "rules" {
		t.Fatalf("CLAUDE.md = %q", got)
	}
}

func TestTheMachineAccountIsLeftAlone(t *testing.T) {
	shared := t.TempDir()
	if err := Link(shared, shared, entries); err != nil {
		t.Fatal(err)
	}
	if des, _ := os.ReadDir(shared); len(des) != 0 {
		t.Fatalf("shared home changed: %v", des)
	}
}

func TestAnAccountsOwnFileBecomesSharedWhenThereIsNoSharedOne(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(account, "CLAUDE.md"), "mine")
	write(t, filepath.Join(account, "skills", "x", "SKILL.md"), "x")
	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CLAUDE.md", "skills"} {
		if !isLink(t, filepath.Join(account, name)) {
			t.Fatalf("%s not linked", name)
		}
	}
	if got := read(t, filepath.Join(shared, "CLAUDE.md")); got != "mine" {
		t.Fatalf("shared CLAUDE.md = %q", got)
	}
	if got := read(t, filepath.Join(shared, "skills", "x", "SKILL.md")); got != "x" {
		t.Fatalf("shared skill = %q", got)
	}
}

func TestAdoptingNeverReplacesASharedEntryThatAppearedMeanwhile(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	// Another account adopted its own copy between this one's check and move.
	write(t, filepath.Join(shared, "CLAUDE.md"), "theirs")
	write(t, filepath.Join(account, "CLAUDE.md"), "mine")
	if err := adopt(filepath.Join(account, "CLAUDE.md"), filepath.Join(shared, "CLAUDE.md"), false); err == nil {
		t.Fatal("adopt replaced the shared file")
	}
	if got := read(t, filepath.Join(shared, "CLAUDE.md")); got != "theirs" {
		t.Fatalf("shared = %q", got)
	}
	if got := read(t, filepath.Join(account, "CLAUDE.md")); got != "mine" {
		t.Fatalf("account = %q", got)
	}
}

func TestAnAccountsOwnCopyBesideASharedOneIsAConflictNotOverwritten(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")
	write(t, filepath.Join(shared, "settings.json"), "shared")
	write(t, filepath.Join(account, "skills", "synced", "x"), "x")

	err := Link(shared, account, entries)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Path != filepath.Join(account, "skills") {
		t.Fatalf("err = %v", err)
	}
	if got := read(t, filepath.Join(account, "skills", "synced", "x")); got != "x" {
		t.Fatalf("account's own skills touched: %q", got)
	}
	// The rest of the entries are still shared.
	if got := read(t, filepath.Join(account, "settings.json")); got != "shared" {
		t.Fatalf("settings = %q", got)
	}
}

func TestALinkElsewhereIsRepointedAndAnEquivalentOneKept(t *testing.T) {
	shared, account, other := t.TempDir(), t.TempDir(), t.TempDir()
	dotfiles := t.TempDir()
	write(t, filepath.Join(dotfiles, "settings.json"), "dot")
	if err := os.Symlink(filepath.Join(dotfiles, "settings.json"), filepath.Join(shared, "settings.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(shared, "skills", "a", "SKILL.md"), "a")
	// Already reaches the shared settings by another path: kept as is.
	if err := os.Symlink(filepath.Join(dotfiles, "settings.json"), filepath.Join(account, "settings.json")); err != nil {
		t.Fatal(err)
	}
	// Points at some other folder, and dangling: both re-pointed.
	if err := os.Symlink(other, filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "gone"), filepath.Join(account, "agents")); err != nil {
		t.Fatal(err)
	}

	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(account, "settings.json")); target != filepath.Join(dotfiles, "settings.json") {
		t.Fatalf("equivalent link replaced: %s", target)
	}
	if got := read(t, filepath.Join(account, "skills", "a", "SKILL.md")); got != "a" {
		t.Fatalf("skills = %q", got)
	}
	if target, _ := os.Readlink(filepath.Join(account, "agents")); target != filepath.Join(shared, "agents") {
		t.Fatalf("agents -> %s", target)
	}
}

func TestResolve(t *testing.T) {
	home := "/home/u"
	for in, want := range map[string]string{
		"":              "",
		"  ":            "",
		"~":             "/home/u",
		"~/.claude":     "/home/u/.claude",
		".claude-work":  "/home/u/.claude-work",
		"/srv/claude/":  "/srv/claude",
		" /srv/claude ": "/srv/claude",
	} {
		if got := Resolve(home, in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}
