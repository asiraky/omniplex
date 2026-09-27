package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
)

// A database from before folders had one root per project, its settings
// cached from the repo's project.json. Opening it gives each project one
// folder at that root, with the repo file's settings, and scopes its threads
// to that folder.
func TestOpenImportsRootedProjectsAsOneFolderEach(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".omniplex"), 0o755)
	os.WriteFile(filepath.Join(root, ".omniplex", "project.json"), []byte(`{"name":"Omniplex",
		"defaults":{"harness":"claude","baseBranch":"main","harnesses":{"claude":{"model":"opus"}}},
		"workspace":{"provision":"scripts/omniplex-provision.mjs"}}`), 0o644)

	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(schema, projectsDDL, `CREATE TABLE projects (id TEXT PRIMARY KEY, root TEXT NOT NULL UNIQUE, config BLOB NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);`, 1)
	for _, stmt := range []string{
		old,
		`ALTER TABLE threads ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO projects VALUES ('p1', '` + root + `', '{"name":"Cached"}', 5, 6)`,
		`INSERT INTO projects VALUES ('p2', '/gone/recipes', '{"name":"Recipes"}', 7, 8)`,
		`INSERT INTO threads (id, cwd, harness, title, created_at, updated_at, head_seq, phase, project_id) VALUES ('t1', '` + root + `', 'claude', '', 1, 1, 0, 'idle', 'p1'), ('t2', '/tmp', 'claude', '', 1, 1, 0, 'idle', '')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()

	for range 2 { // the second open finds nothing left to import
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		ctx := context.Background()
		p, err := s.Project(ctx, "p1")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "Omniplex" || p.Defaults.Harnesses["claude"].Model != "opus" || p.CreatedAt != 5 {
			t.Fatalf("project = %+v", p)
		}
		if len(p.Folders) != 1 || p.Folders[0].Path != root || p.Folders[0].BaseBranch != "main" || p.Folders[0].Provision != "scripts/omniplex-provision.mjs" {
			t.Fatalf("folders = %+v", p.Folders)
		}
		if gone, _ := s.Project(ctx, "p2"); gone.Name != "Recipes" || gone.Folders[0].Path != "/gone/recipes" {
			t.Fatalf("a missing root lost its cached settings: %+v", gone)
		}
		if th, _ := s.Thread(ctx, "t1"); th.FolderID != p.Folders[0].ID {
			t.Fatalf("thread scoped to %q, want the project's folder", th.FolderID)
		}
		if th, _ := s.Thread(ctx, "t2"); th.FolderID != "" {
			t.Fatalf("a thread outside any project got folder %q", th.FolderID)
		}
		s.Close()
	}
}

func TestFoldersAreRefusedTwiceAndKeptWhileThreadsUseThem(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p := project.Project{ID: "p", Name: "p", Folders: []project.Folder{project.NewFolder("a", "/a")}}
	if err := s.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFolder(ctx, "p", project.NewFolder("a2", "/a")); !errors.Is(err, ErrFolderTaken) {
		t.Fatalf("adding /a twice: %v", err)
	}
	if err := s.AddFolder(ctx, "p", project.NewFolder("b", "/b")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(ctx, ThreadMeta{ID: "t", Cwd: "/a", Harness: "h", Phase: "idle", ProjectID: "p", FolderID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFolder(ctx, "p", "a"); !errors.Is(err, ErrFolderInUse) {
		t.Fatalf("removing a folder a thread uses: %v", err)
	}
	if err := s.RemoveFolder(ctx, "p", "b"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Project(ctx, "p")
	if len(got.Folders) != 1 || got.Folders[0].ID != "a" {
		t.Fatalf("folders = %+v", got.Folders)
	}
}
