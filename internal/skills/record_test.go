package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRecord(t *testing.T) {
	entry := RecordEntry{Method: MethodNpx, Repo: "owner/repo", Ref: "main", Path: "skills/a", Hash: "abc", InstalledAt: "2026-01-02T03:04:05Z", UpdatedAt: "2026-02-02T03:04:05Z"}

	t.Run("a library without a record has an empty one", func(t *testing.T) {
		lib := t.TempDir()
		rec, err := LoadRecord(lib)
		if err != nil || len(rec.Skills) != 0 {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if _, ok := rec.Get("a"); ok {
			t.Error("found an entry in an empty record")
		}
		if exists(filepath.Join(lib, RecordFile)) {
			t.Error("loading created the file")
		}
	})

	t.Run("set, save, load, delete", func(t *testing.T) {
		lib := filepath.Join(t.TempDir(), "not", "made", "yet")
		if err := UpdateRecord(lib, func(rec *Record) error {
			rec.Set("a", entry)
			rec.Set("b", RecordEntry{Method: MethodLocal, Repo: "/src/b"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		rec, err := LoadRecord(lib)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := rec.Get("a"); !ok || got != entry {
			t.Errorf("a = %+v, %v; want %+v", got, ok, entry)
		}
		if !rec.Delete("a") || rec.Delete("a") {
			t.Error("Delete should report an entry once, then none")
		}
		if err := SaveRecord(lib, rec); err != nil {
			t.Fatal(err)
		}
		rec, _ = LoadRecord(lib)
		if _, ok := rec.Get("a"); ok || len(rec.Skills) != 1 {
			t.Errorf("after delete: %+v", rec.Skills)
		}
		// No temp file is left beside the record.
		if entries, _ := os.ReadDir(lib); len(entries) != 1 {
			t.Errorf("library holds %d entries, want only the record", len(entries))
		}
	})

	t.Run("a zero record can be set", func(t *testing.T) {
		var rec Record
		rec.Set("a", entry)
		if got, ok := rec.Get("a"); !ok || got != entry {
			t.Errorf("got %+v, %v", got, ok)
		}
	})

	t.Run("fn failing abandons the write", func(t *testing.T) {
		lib := t.TempDir()
		boom := errors.New("boom")
		if err := UpdateRecord(lib, func(rec *Record) error { rec.Set("a", entry); return boom }); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if exists(filepath.Join(lib, RecordFile)) {
			t.Error("an abandoned update wrote the record")
		}
	})

	t.Run("a symlinked library and record are written through", func(t *testing.T) {
		base := t.TempDir()
		write(t, filepath.Join(base, "repo", "record.json"), `{"version":1,"skills":{}}`)
		link(t, "repo", filepath.Join(base, "lib"))
		link(t, "record.json", filepath.Join(base, "repo", RecordFile))
		if err := UpdateRecord(filepath.Join(base, "lib"), func(rec *Record) error { rec.Set("a", entry); return nil }); err != nil {
			t.Fatal(err)
		}
		if !isSymlink(filepath.Join(base, "lib")) || !isSymlink(filepath.Join(base, "repo", RecordFile)) {
			t.Error("a symlink was replaced by a file")
		}
		rec, err := LoadRecord(filepath.Join(base, "repo"))
		if _, ok := rec.Get("a"); err != nil || !ok {
			t.Errorf("entry not in the real file: %+v, %v", rec, err)
		}
	})

	for name, content := range map[string]string{
		"not json":         "{nope",
		"a future version": `{"version":2,"skills":{"a":{"method":"npx","repo":"x/y"}}}`,
	} {
		t.Run(name+" is an error and is not overwritten", func(t *testing.T) {
			lib := t.TempDir()
			file := filepath.Join(lib, RecordFile)
			write(t, file, content)
			if _, err := LoadRecord(lib); err == nil {
				t.Fatal("loaded")
			}
			if err := UpdateRecord(lib, func(rec *Record) error { rec.Set("a", entry); return nil }); err == nil {
				t.Error("updated a record that could not be read")
			}
			if read(t, file) != content {
				t.Error("the unreadable record was replaced")
			}
		})
	}
}

func TestHashDir(t *testing.T) {
	skill := func(t *testing.T, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		for rel, content := range files {
			write(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
		}
		return dir
	}
	hash := func(t *testing.T, dir string) string {
		t.Helper()
		h, err := HashDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := map[string]string{"SKILL.md": "hello", "scripts/run.sh": "world"}
	want := hash(t, skill(t, base))

	t.Run("is sha256 over path, NUL, bytes in path order", func(t *testing.T) {
		sum := sha256.Sum256([]byte("SKILL.md\x00hello" + "scripts/run.sh\x00world"))
		if want != hex.EncodeToString(sum[:]) {
			t.Errorf("hash = %s", want)
		}
	})

	same := map[string]map[string]string{
		"a .git folder":        {"SKILL.md": "hello", "scripts/run.sh": "world", ".git/HEAD": "ref"},
		"a nested .git":        {"SKILL.md": "hello", "scripts/run.sh": "world", "scripts/.git/HEAD": "ref"},
		"node_modules":         {"SKILL.md": "hello", "scripts/run.sh": "world", "node_modules/x/index.js": "x"},
		"a .git worktree file": {"SKILL.md": "hello", "scripts/run.sh": "world", ".git": "gitdir: elsewhere"},
	}
	for name, files := range same {
		t.Run(name+" does not change the hash", func(t *testing.T) {
			if got := hash(t, skill(t, files)); got != want {
				t.Errorf("hash changed: %s", got)
			}
		})
	}

	different := map[string]map[string]string{
		"a changed byte":                {"SKILL.md": "hellO", "scripts/run.sh": "world"},
		"a renamed file":                {"SKILL.md": "hello", "scripts/go.sh": "world"},
		"a moved file":                  {"SKILL.md": "hello", "run.sh": "world"},
		"an added file":                 {"SKILL.md": "hello", "scripts/run.sh": "world", "extra": ""},
		"a removed file":                {"SKILL.md": "hello"},
		"a dotfile that is not .git":    {"SKILL.md": "hello", "scripts/run.sh": "world", ".env.example": "K=v"},
		"the same bytes in other files": {"SKILL.md": "hel", "scripts/run.sh": "loworld"},
	}
	for name, files := range different {
		t.Run(name+" changes the hash", func(t *testing.T) {
			if got := hash(t, skill(t, files)); got == want {
				t.Error("hash did not change")
			}
		})
	}

	t.Run("a symlinked skill hashes as its target", func(t *testing.T) {
		dir := skill(t, base)
		via := filepath.Join(t.TempDir(), "via")
		link(t, dir, via)
		if got := hash(t, via); got != want {
			t.Errorf("hash through the symlink = %s", got)
		}
	})

	t.Run("a symlinked file hashes as its content", func(t *testing.T) {
		dir := skill(t, map[string]string{"SKILL.md": "hello"})
		elsewhere := filepath.Join(t.TempDir(), "run.sh")
		write(t, elsewhere, "world")
		link(t, elsewhere, filepath.Join(dir, "scripts", "run.sh"))
		if got := hash(t, dir); got != want {
			t.Errorf("hash with a symlinked file = %s", got)
		}
	})

	t.Run("a missing dir is an error", func(t *testing.T) {
		if h, err := HashDir(filepath.Join(t.TempDir(), "gone")); err == nil {
			t.Errorf("hashed nothing as %s", h)
		}
	})
}

func TestSkillSource(t *testing.T) {
	globalLock := `{"version":3,"skills":{
		"shared":{"source":"cli/global","sourceType":"github","sourceUrl":"https://github.com/cli/global.git","ref":"main","skillPath":"skills/shared/SKILL.md","skillFolderHash":"h","installedAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z"},
		"dev":{"source":"cli/global-dev","sourceType":"github"},
		"cloud-one":{"source":"cli/synced","sourceType":"github"},
		"plug-skill":{"source":"cli/plugin","sourceType":"github"},
		"codex-only":{"source":"","sourceType":"local","sourceUrl":"/src/codex-only","skillPath":"SKILL.md"}}}`
	projectLock := `{"version":1,"skills":{
		"dev":{"source":"cli/project","ref":"v2","sourceType":"github","skillPath":"dev/SKILL.md","computedHash":"h"},
		"pi-only":{"source":"cli/project-pi","sourceType":"github"}}}`
	ours := func(repo string) Record {
		return Record{Skills: map[string]RecordEntry{
			"shared": {Method: MethodGit, Repo: repo, Ref: "trunk", Path: "pkg/shared", Hash: "h", InstalledAt: "2026-03-01T00:00:00Z", UpdatedAt: "2026-03-02T00:00:00Z"},
			"dev":    {Method: MethodGit, Repo: repo},
		}}
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, r Roots)
		skill string
		want  *Source
	}{
		{"nothing recorded", func(t *testing.T, r Roots) {}, "shared", nil},
		{"global lock speaks for a personal skill", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
		}, "shared", &Source{Method: MethodNpx, Repo: "cli/global", Ref: "main", Path: "skills/shared", InstalledAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"}},
		{"our record beats the global lock", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
			if err := SaveRecord(r.Library, ours("our/personal")); err != nil {
				t.Fatal(err)
			}
		}, "shared", &Source{Method: MethodGit, Repo: "our/personal", Ref: "trunk", Path: "pkg/shared", Managed: true, InstalledAt: "2026-03-01T00:00:00Z", UpdatedAt: "2026-03-02T00:00:00Z"}},
		{"the global lock does not speak for a project skill", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
		}, "dev", nil},
		{"the project lock does", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
			write(t, ProjectCLILock(r.ProjectRoot), projectLock)
		}, "dev", &Source{Method: MethodNpx, Repo: "cli/project", Ref: "v2", Path: "dev"}},
		{"the project lock does not speak for a personal skill", func(t *testing.T, r Roots) {
			write(t, ProjectCLILock(r.ProjectRoot), projectLock)
		}, "pi-only", nil},
		{"a record in a project library is not its provenance", func(t *testing.T, r Roots) {
			write(t, ProjectCLILock(r.ProjectRoot), projectLock)
			if err := SaveRecord(filepath.Join(r.ProjectRoot, ".agents", "skills"), ours("our/project")); err != nil {
				t.Fatal(err)
			}
		}, "dev", &Source{Method: MethodNpx, Repo: "cli/project", Ref: "v2", Path: "dev"}},
		{"a library's record only covers its own skills", func(t *testing.T, r Roots) {
			if err := SaveRecord(r.Library, ours("our/personal")); err != nil {
				t.Fatal(err)
			}
		}, "dev", nil},
		{"a record that does not parse falls back to the lock", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
			write(t, filepath.Join(r.Library, RecordFile), "{nope")
		}, "shared", &Source{Method: MethodNpx, Repo: "cli/global", Ref: "main", Path: "skills/shared", InstalledAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"}},
		{"a local lock entry with the skill at the source's top", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
		}, "codex-only", &Source{Method: MethodLocal, Repo: "/src/codex-only"}},
		{"a synced skill of a locked name has no source", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
		}, "cloud-one", nil},
		{"a plugin skill of a locked name has no source", func(t *testing.T, r Roots) {
			write(t, r.CLILock, globalLock)
		}, "plug-skill", nil},
		{"a lock that does not parse is no provenance", func(t *testing.T, r Roots) {
			write(t, r.CLILock, "{nope")
		}, "shared", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := fixture(t)
			tt.setup(t, r)
			got := byName(t, mustDiscover(t, r))[tt.skill].Source
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("source = %+v, want %+v", got, tt.want)
			}
		})
	}
}
