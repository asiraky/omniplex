package accounthome

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
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
		if _, err := Link(shared, account, entries); err != nil {
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
	if _, err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(account, "CLAUDE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CLAUDE.md linked before it exists: %v", err)
	}
	write(t, filepath.Join(shared, "CLAUDE.md"), "rules")
	if _, err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(account, "CLAUDE.md")); got != "rules" {
		t.Fatalf("CLAUDE.md = %q", got)
	}
}

func TestTheMachineAccountIsLeftAlone(t *testing.T) {
	shared := t.TempDir()
	if _, err := Link(shared, shared, entries); err != nil {
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
	if _, err := Link(shared, account, entries); err != nil {
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

// A machine with no shared home at all: the first account makes the shared
// folders as real directories and links to them, so whatever lands in them
// later (a terminal install, a skill added through any account) is the one
// copy.
func TestAFirstAccountOnACleanMachineCreatesTheSharedFolders(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, ".claude")
	account := InstanceHome(home, "first")
	conflicts, err := Link(shared, account, entries)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("Link = %v, %v", conflicts, err)
	}
	for _, name := range []string{"skills", "agents"} {
		info, err := os.Lstat(filepath.Join(shared, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("shared %s is not a real folder: %v", name, err)
		}
		if target, _ := os.Readlink(filepath.Join(account, name)); target != filepath.Join(shared, name) {
			t.Fatalf("%s -> %q", name, target)
		}
	}
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")
	if got := read(t, filepath.Join(account, "skills", "infra", "SKILL.md")); got != "infra" {
		t.Fatalf("skill through the account = %q", got)
	}
}

// links is every entry of dir that is a link, with its literal target.
func links(t *testing.T, dir string) map[string]string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, de := range des {
		if target, err := os.Readlink(filepath.Join(dir, de.Name())); err == nil {
			out[de.Name()] = target
		}
	}
	return out
}

func TestASecondAccountHasTheSameShapeAsTheFirst(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, ".claude")
	write(t, filepath.Join(shared, "CLAUDE.md"), "rules")
	write(t, filepath.Join(shared, "settings.json"), "{}")
	first, second := InstanceHome(home, "a"), InstanceHome(home, "b")
	for _, account := range []string{first, second} {
		if _, err := Link(shared, account, entries); err != nil {
			t.Fatal(err)
		}
	}
	a, b := links(t, first), links(t, second)
	if len(a) != len(entries) || !reflect.DeepEqual(a, b) {
		t.Fatalf("first %v, second %v", a, b)
	}
}

// The Worksauce case: an account linked straight at the dotfiles, past the
// shared entry that links there itself, resolves to the same files but has a
// different shape, so it is re-pointed at the shared entry.
func TestALinkPastTheSharedEntryIsRepointedAtIt(t *testing.T) {
	shared, account, dotfiles := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(dotfiles, "settings.json"), "dot")
	write(t, filepath.Join(dotfiles, "skills", "a", "SKILL.md"), "a")
	for _, name := range []string{"settings.json", "skills"} {
		if err := os.Symlink(filepath.Join(dotfiles, name), filepath.Join(shared, name)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dotfiles, name), filepath.Join(account, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", "skills"} {
		if target, _ := os.Readlink(filepath.Join(account, name)); target != filepath.Join(shared, name) {
			t.Fatalf("%s -> %q, want the shared entry", name, target)
		}
	}
	if got := read(t, filepath.Join(account, "skills", "a", "SKILL.md")); got != "a" {
		t.Fatalf("skill = %q", got)
	}
	if got := read(t, filepath.Join(dotfiles, "settings.json")); got != "dot" {
		t.Fatalf("dotfiles settings = %q", got)
	}
}

// The same account with no shared entry at all: shared takes the account's
// link rather than a fresh empty folder, so the skills are still there after
// the account is re-pointed. A relative link means the same from shared.
func TestALinkWithNoSharedEntryBecomesTheSharedEntry(t *testing.T) {
	shared, root := t.TempDir(), t.TempDir()
	account, dotfiles := filepath.Join(root, "account"), filepath.Join(root, "dotfiles")
	write(t, filepath.Join(dotfiles, "skills", "a", "SKILL.md"), "a")
	write(t, filepath.Join(dotfiles, "settings.json"), "dot")
	if err := os.MkdirAll(account, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dotfiles, "skills"), filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "dotfiles", "settings.json"), filepath.Join(account, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skills", "settings.json"} {
		if target, _ := os.Readlink(filepath.Join(account, name)); target != filepath.Join(shared, name) {
			t.Fatalf("account %s -> %q, want the shared entry", name, target)
		}
		if target, _ := os.Readlink(filepath.Join(shared, name)); target != filepath.Join(dotfiles, name) {
			t.Fatalf("shared %s -> %q, want the account's old target", name, target)
		}
	}
	if got := read(t, filepath.Join(account, "skills", "a", "SKILL.md")); got != "a" {
		t.Fatalf("skill = %q", got)
	}
	if got := read(t, filepath.Join(account, "settings.json")); got != "dot" {
		t.Fatalf("settings = %q", got)
	}
}

func TestALinkElsewhereOrDanglingIsRepointed(t *testing.T) {
	shared, account, other := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "skills", "a", "SKILL.md"), "a")
	if err := os.Symlink(other, filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "gone"), filepath.Join(account, "agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(shared, account, entries); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(account, "skills", "a", "SKILL.md")); got != "a" {
		t.Fatalf("skills = %q", got)
	}
	if target, _ := os.Readlink(filepath.Join(account, "agents")); target != filepath.Join(shared, "agents") {
		t.Fatalf("agents -> %s", target)
	}
}

func TestAnAccountsOwnCopyBesideASharedOneIsAConflictLeftAsIs(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")
	write(t, filepath.Join(shared, "settings.json"), "shared")
	write(t, filepath.Join(shared, "CLAUDE.md"), "shared rules")
	write(t, filepath.Join(account, "skills", "synced", "x"), "x")
	write(t, filepath.Join(account, "CLAUDE.md"), "my rules")

	conflicts, err := Link(shared, account, entries)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(conflicts)
	if !reflect.DeepEqual(conflicts, []string{"CLAUDE.md", "skills"}) {
		t.Fatalf("conflicts = %v", conflicts)
	}
	if got := read(t, filepath.Join(account, "skills", "synced", "x")); got != "x" {
		t.Fatalf("account's own skills touched: %q", got)
	}
	if got := read(t, filepath.Join(account, "CLAUDE.md")); got != "my rules" {
		t.Fatalf("account's own CLAUDE.md = %q", got)
	}
	if isLink(t, filepath.Join(account, "CLAUDE.md")) || isLink(t, filepath.Join(account, "skills")) {
		t.Fatal("an account's own entry was replaced by a link")
	}
	// The rest of the entries are still shared.
	if got := read(t, filepath.Join(account, "settings.json")); got != "shared" {
		t.Fatalf("settings = %q", got)
	}
}

func TestUseSharedBacksTheAccountsCopyUpAndLinks(t *testing.T) {
	shared, account := t.TempDir(), t.TempDir()
	write(t, filepath.Join(shared, "CLAUDE.md"), "shared rules")
	write(t, filepath.Join(shared, "skills", "infra", "SKILL.md"), "infra")
	write(t, filepath.Join(account, "CLAUDE.md"), "my rules")
	write(t, filepath.Join(account, "skills", "mine", "SKILL.md"), "mine")
	// An earlier backup of the folder is kept, and this one numbered.
	write(t, filepath.Join(account, "skills"+BackupSuffix, "old", "SKILL.md"), "old")

	backup, err := UseShared(shared, account, Entry{Name: "CLAUDE.md"})
	if err != nil || backup != filepath.Join(account, "CLAUDE.md"+BackupSuffix) {
		t.Fatalf("UseShared = %q, %v", backup, err)
	}
	if got := read(t, backup); got != "my rules" {
		t.Fatalf("backup = %q", got)
	}
	if got := read(t, filepath.Join(account, "CLAUDE.md")); got != "shared rules" {
		t.Fatalf("CLAUDE.md = %q", got)
	}

	backup, err = UseShared(shared, account, Entry{Name: "skills", Dir: true})
	if err != nil || backup != filepath.Join(account, "skills"+BackupSuffix+"-2") {
		t.Fatalf("UseShared = %q, %v", backup, err)
	}
	if got := read(t, filepath.Join(backup, "mine", "SKILL.md")); got != "mine" {
		t.Fatalf("backup skill = %q", got)
	}
	if got := read(t, filepath.Join(account, "skills"+BackupSuffix, "old", "SKILL.md")); got != "old" {
		t.Fatalf("earlier backup = %q", got)
	}
	if got := read(t, filepath.Join(account, "skills", "infra", "SKILL.md")); got != "infra" {
		t.Fatalf("shared skill through the account = %q", got)
	}
	if conflicts, err := Link(shared, account, entries); err != nil || len(conflicts) != 0 {
		t.Fatalf("after UseShared, Link = %v, %v", conflicts, err)
	}
}

func TestMoveNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src.jsonl"), "mine")
	write(t, filepath.Join(root, "dst.jsonl"), "theirs")
	write(t, filepath.Join(root, "srcdir", "a"), "a")
	write(t, filepath.Join(root, "dstdir", "b"), "b")
	if err := os.Mkdir(filepath.Join(root, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, dst string }{
		{"src.jsonl", "dst.jsonl"},
		{"srcdir", "dstdir"},
		// A plain rename would replace an empty folder.
		{"srcdir", "emptydir"},
		{"src.jsonl", "dstdir"},
	} {
		if _, err := Move(filepath.Join(root, c.src), filepath.Join(root, c.dst)); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("Move %s -> %s = %v, want ErrExist", c.src, c.dst, err)
		}
	}
	if read(t, filepath.Join(root, "src.jsonl")) != "mine" || read(t, filepath.Join(root, "dst.jsonl")) != "theirs" ||
		read(t, filepath.Join(root, "srcdir", "a")) != "a" || read(t, filepath.Join(root, "dstdir", "b")) != "b" {
		t.Fatal("a refused move changed files")
	}
	if des, _ := os.ReadDir(filepath.Join(root, "emptydir")); len(des) != 0 {
		t.Fatalf("empty destination filled: %v", des)
	}
}

func TestMoveCarriesAFolderWithItsContents(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "conv", "subagents", "s.jsonl"), "sub")
	write(t, filepath.Join(root, "conv", "tool.txt"), "out")
	dst := filepath.Join(root, "other", "deep", "conv")
	aside, err := Move(filepath.Join(root, "conv"), dst)
	if err != nil || aside != "" {
		t.Fatalf("Move = %q, %v", aside, err)
	}
	if read(t, filepath.Join(dst, "subagents", "s.jsonl")) != "sub" || read(t, filepath.Join(dst, "tool.txt")) != "out" {
		t.Fatal("contents did not move")
	}
	if _, err := os.Lstat(filepath.Join(root, "conv")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source left behind: %v", err)
	}
}

func TestMoveOfAMissingSourceFails(t *testing.T) {
	root := t.TempDir()
	if _, err := Move(filepath.Join(root, "nope"), filepath.Join(root, "dst")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

// The cross-filesystem path, driven directly: the copy keeps contents and
// times, and a failed one leaves the source exactly as it was.
func TestCopyTreeIsExclusiveAndKeepsTimes(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "conv")
	write(t, filepath.Join(src, "subagents", "s.jsonl"), "sub")
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(src, "subagents", "s.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "subagents", "s.jsonl"))
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("copy = %v, %v", info, err)
	}
	if err := copyTree(src, dst); !errors.Is(err, fs.ErrExist) || !isRootExists(err, dst) {
		t.Fatalf("second copy = %v", err)
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
