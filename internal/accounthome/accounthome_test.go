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

func TestALinkAnywhereButTheSharedEntryIsRepointed(t *testing.T) {
	shared, account, other := t.TempDir(), t.TempDir(), t.TempDir()
	dotfiles := t.TempDir()
	write(t, filepath.Join(dotfiles, "settings.json"), "dot")
	if err := os.Symlink(filepath.Join(dotfiles, "settings.json"), filepath.Join(shared, "settings.json")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(shared, "skills", "a", "SKILL.md"), "a")
	// Reaches the same file, but skips the shared home: re-pointed, so moving
	// the dotfiles behind the shared home moves every account with it.
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
	for _, name := range []string{"settings.json", "skills", "agents"} {
		if target, _ := os.Readlink(filepath.Join(account, name)); target != filepath.Join(shared, name) {
			t.Fatalf("%s -> %s", name, target)
		}
	}
	if got := read(t, filepath.Join(account, "settings.json")); got != "dot" {
		t.Fatalf("settings = %q", got)
	}
	if got := read(t, filepath.Join(account, "skills", "a", "SKILL.md")); got != "a" {
		t.Fatalf("skills = %q", got)
	}
	if got := read(t, filepath.Join(dotfiles, "settings.json")); got != "dot" {
		t.Fatalf("dotfiles touched: %q", got)
	}
}

func TestOnACleanMachineTheSharedFoldersAreMadeThenLinked(t *testing.T) {
	root := t.TempDir()
	shared, account := filepath.Join(root, ".claude"), filepath.Join(root, "instances", "work")
	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.Dir {
			continue
		}
		info, err := os.Lstat(filepath.Join(shared, e.Name))
		if err != nil || !info.IsDir() {
			t.Fatalf("shared %s: %v %v", e.Name, info, err)
		}
		if target, _ := os.Readlink(filepath.Join(account, e.Name)); target != filepath.Join(shared, e.Name) {
			t.Fatalf("%s -> %q", e.Name, target)
		}
	}
	// Running again changes nothing.
	if err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
}

func TestUseSharedBacksUpTheAccountsCopyAndLinks(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "settings.json"), "shared")
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")
	write(t, filepath.Join(account, "settings.json"), "mine")
	write(t, filepath.Join(account, "skills", "synced", "SKILL.md"), "synced")

	err := Link(shared, account, entries)
	conflicts := Conflicts(err)
	if len(conflicts) != 2 {
		t.Fatalf("conflicts = %v", err)
	}
	for _, c := range conflicts {
		entry := Entry{Name: c.Entry, Dir: c.Entry == "skills"}
		if err := UseShared(shared, account, entry); err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(filepath.Join(account, c.Entry)); target != filepath.Join(shared, c.Entry) {
			t.Fatalf("%s -> %q", c.Entry, target)
		}
	}
	if got := read(t, filepath.Join(account, "settings.json.account-backup")); got != "mine" {
		t.Fatalf("settings backup = %q", got)
	}
	if got := read(t, filepath.Join(account, "skills.account-backup", "synced", "SKILL.md")); got != "synced" {
		t.Fatalf("skills backup = %q", got)
	}
	if got := read(t, filepath.Join(shared, "settings.json")); got != "shared" {
		t.Fatalf("shared settings = %q", got)
	}
	if err := Link(shared, account, entries); err != nil {
		t.Fatalf("still conflicting: %v", err)
	}
}

func TestUseSharedNeverOverwritesAnEarlierBackup(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "settings.json"), "shared")
	write(t, filepath.Join(account, "settings.json.account-backup"), "first")
	write(t, filepath.Join(account, "settings.json.account-backup-2"), "second")
	write(t, filepath.Join(account, "settings.json"), "third")
	if err := UseShared(shared, account, Entry{Name: "settings.json"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"settings.json.account-backup":   "first",
		"settings.json.account-backup-2": "second",
		"settings.json.account-backup-3": "third",
		"settings.json":                  "shared",
	} {
		if got := read(t, filepath.Join(account, name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestUseSharedWithNoSharedEntryAdoptsTheAccountsCopy(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(account, "CLAUDE.md"), "mine")
	if err := UseShared(shared, account, Entry{Name: "CLAUDE.md"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(shared, "CLAUDE.md")); got != "mine" {
		t.Fatalf("shared = %q", got)
	}
	if !isLink(t, filepath.Join(account, "CLAUDE.md")) {
		t.Fatal("not linked")
	}
	if _, err := os.Lstat(filepath.Join(account, "CLAUDE.md.account-backup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a backup was made of the only copy: %v", err)
	}
}

func TestUseSharedRefusesTheSharedHomeItself(t *testing.T) {
	shared := t.TempDir()
	write(t, filepath.Join(shared, "settings.json"), "shared")
	if err := UseShared(shared, shared, Entry{Name: "settings.json"}); err == nil {
		t.Fatal("UseShared on the shared home must refuse")
	}
	if got := read(t, filepath.Join(shared, "settings.json")); got != "shared" {
		t.Fatalf("shared = %q", got)
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
