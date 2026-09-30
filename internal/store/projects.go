package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
)

const projectsDDL = `
CREATE TABLE IF NOT EXISTS projects (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  home          TEXT NOT NULL DEFAULT '',
  defaults      BLOB NOT NULL DEFAULT '{}',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS folders (
  id                  TEXT PRIMARY KEY,
  project_id          TEXT NOT NULL,
  path                TEXT NOT NULL,
  base_branch         TEXT NOT NULL DEFAULT '',
  copies_dir          TEXT NOT NULL DEFAULT '.worktrees',
  provision           TEXT NOT NULL DEFAULT '',
  deprovision         TEXT NOT NULL DEFAULT '',
  provision_timeout   INTEGER NOT NULL DEFAULT 1800,
  deprovision_timeout INTEGER NOT NULL DEFAULT 600,
  position            INTEGER NOT NULL DEFAULT 0,
  created_at          INTEGER NOT NULL,
  UNIQUE (project_id, path)
);

`

// importProjects moves a database from before folders: each project was one
// root with its settings cached from the repo's .omniplex/project.json. It
// becomes a project with one folder, the old root, and every thread in it is
// scoped to that folder. The repo file is read here, once, and never again.
func importProjects(db *sql.DB) error {
	var old int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = 'root'`).Scan(&old); err != nil || old == 0 {
		return err
	}
	type row struct {
		id, root, home   string
		config           []byte
		created, updated int64
	}
	rows, err := db.Query(`SELECT id, root, home, config, created_at, updated_at FROM projects`)
	if err != nil {
		return err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.root, &r.home, &r.config, &r.created, &r.updated); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{`ALTER TABLE projects RENAME TO projects_before_folders`, projectsDDL} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	for _, r := range all {
		name, defaults, f := project.Import(r.root, r.config)
		f.ID = uuid.NewString()
		d, _ := json.Marshal(defaults)
		if _, err := tx.Exec(`INSERT INTO projects (id, name, home, defaults, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			r.id, name, r.home, d, r.created, r.updated); err != nil {
			return err
		}
		if err := insertFolder(tx, r.id, f, 0, r.created); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE threads SET folder_id = ? WHERE project_id = ? AND folder_id = ''`, f.ID, r.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE projects_before_folders`); err != nil {
		return err
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	Exec(query string, args ...any) (sql.Result, error)
}

func insertFolder(x execer, projectID string, f project.Folder, position int, created int64) error {
	f = project.NormalizeFolder(f)
	_, err := x.Exec(`INSERT INTO folders (id, project_id, path, base_branch, copies_dir, provision, deprovision, provision_timeout, deprovision_timeout, position, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, projectID, f.Path, f.BaseBranch, f.CopiesDir, f.Provision, f.Deprovision, f.ProvisionTimeoutSeconds, f.DeprovisionTimeoutSeconds, position, created)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("%w: %s is already in this project", ErrFolderTaken, f.Path)
	}
	return err
}

// ErrFolderTaken is returned when a project already points at a folder.
var ErrFolderTaken = errors.New("folder already added")

// CreateProject inserts a project and its folders in one transaction.
func (s *Store) CreateProject(ctx context.Context, p project.Project) error {
	d, err := json.Marshal(project.NormalizeDefaults(p.Defaults))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, name, home, defaults, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		p.ID, p.Name, p.Home, d, p.CreatedAt, p.UpdatedAt); err != nil {
		return err
	}
	for i, f := range p.Folders {
		if err := insertFolder(tx, p.ID, f, i, p.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateProject saves a project's own settings: name and defaults. Folders
// and home have their own writes.
func (s *Store) UpdateProject(ctx context.Context, p project.Project) error {
	d, err := json.Marshal(project.NormalizeDefaults(p.Defaults))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET name=?, defaults=?, updated_at=? WHERE id=?`, p.Name, d, proto.NowMillis(), p.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProjectHome records the folder omniplex puts new things in for a
// project. Written once, the first time it is needed.
func (s *Store) SetProjectHome(ctx context.Context, id, home string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET home=? WHERE id=?`, home, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddFolder points a project at one more folder, after the ones it has.
func (s *Store) AddFolder(ctx context.Context, projectID string, f project.Folder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n, next int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, projectID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM folders WHERE project_id = ?`, projectID).Scan(&next); err != nil {
		return err
	}
	if err := insertFolder(tx, projectID, f, next, proto.NowMillis()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE id = ?`, proto.NowMillis(), projectID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateFolder saves a folder's settings. The path is not one of them:
// moving a folder is relocate's job.
func (s *Store) UpdateFolder(ctx context.Context, projectID string, f project.Folder) error {
	f = project.NormalizeFolder(f)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE folders SET base_branch=?, copies_dir=?, provision=?, deprovision=?, provision_timeout=?, deprovision_timeout=? WHERE id=? AND project_id=?`,
		f.BaseBranch, f.CopiesDir, f.Provision, f.Deprovision, f.ProvisionTimeoutSeconds, f.DeprovisionTimeoutSeconds, f.ID, projectID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrFolderInUse is returned when threads still work in a folder.
var ErrFolderInUse = errors.New("folder still has threads")

// RemoveFolder drops a project's pointer to a folder. The folder itself is
// left alone. A folder threads still work in is refused, as a project is.
func (s *Store) RemoveFolder(ctx context.Context, projectID, folderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var threads int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM threads WHERE folder_id = ?`, folderID).Scan(&threads); err != nil {
		return err
	}
	if threads > 0 {
		return fmt.Errorf("%w: delete its %s first", ErrFolderInUse, plural(threads, "thread"))
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ? AND project_id = ?`, folderID, projectID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (s *Store) Project(ctx context.Context, id string) (project.Project, error) {
	ps, err := s.projects(ctx, `WHERE id = ?`, id)
	if err != nil {
		return project.Project{}, err
	}
	if len(ps) == 0 {
		return project.Project{}, ErrNotFound
	}
	return ps[0], nil
}

// ListProjects returns every project, most recently changed first, each with
// its folders in the order they were added.
func (s *Store) ListProjects(ctx context.Context) ([]project.Project, error) {
	return s.projects(ctx, ``)
}

func (s *Store) projects(ctx context.Context, where string, args ...any) ([]project.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, home, defaults, created_at, updated_at FROM projects `+where+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := []project.Project{}
	index := map[string]int{}
	for rows.Next() {
		var p project.Project
		var d []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Home, &d, &p.CreatedAt, &p.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(d, &p.Defaults)
		p.Defaults = project.NormalizeDefaults(p.Defaults)
		p.Folders = []project.Folder{}
		index[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	frows, err := s.db.QueryContext(ctx, `SELECT id, project_id, path, base_branch, copies_dir, provision, deprovision, provision_timeout, deprovision_timeout FROM folders ORDER BY position, created_at`)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var f project.Folder
		var pid string
		if err := frows.Scan(&f.ID, &pid, &f.Path, &f.BaseBranch, &f.CopiesDir, &f.Provision, &f.Deprovision, &f.ProvisionTimeoutSeconds, &f.DeprovisionTimeoutSeconds); err != nil {
			return nil, err
		}
		if i, ok := index[pid]; ok {
			f.Git = project.IsGit(f.Path)
			out[i].Folders = append(out[i].Folders, f)
		}
	}
	return out, frows.Err()
}

// ErrProjectInUse is returned when a project still owns threads. Deleting it
// anyway would leave those threads pointing at a project that no longer
// exists, and the threads are the thing with a transcript and a checkout
// behind them — so the threads go first, deliberately, and the project after.
var ErrProjectInUse = errors.New("project still has threads")

// DeleteProject forgets a project and its folders. Nothing on disk is
// touched: the folders are the user's, not omniplex's.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Counted inside the transaction, so a thread created between the check
	// and the delete cannot be orphaned by it.
	var threads int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM threads WHERE project_id = ?`, id).Scan(&threads); err != nil {
		return err
	}
	if threads > 0 {
		return fmt.Errorf("%w: delete its %s first", ErrProjectInUse, plural(threads, "thread"))
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE project_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
