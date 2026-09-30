package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// RelocateStats describes the durable records changed by RelocateFolder.
type RelocateStats struct {
	Threads   int
	Events    int
	Snapshots int
	Commands  int
}

// RelocationPath is one equivalent old/new prefix pair. The first pair is the
// path as recorded on the project; callers may add canonical forms for hosts
// whose filesystem aliases paths (for example /var and /private/var on macOS).
type RelocationPath struct {
	Old string
	New string
}

// RelocateFolder atomically rewrites the path-bearing records for a folder
// that moved on disk: every project folder at that path, a project home inside
// it, and the threads of those projects. Callers are expected to run this
// offline: a live manager would retain the old paths in its in-memory actors
// even though the database had changed beneath it.
func (s *Store) RelocateFolder(ctx context.Context, oldRoot, newRoot string, aliases ...RelocationPath) (RelocateStats, error) {
	mappings := append([]RelocationPath{{Old: oldRoot, New: newRoot}}, aliases...)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelocateStats{}, err
	}
	defer tx.Rollback()

	projects := map[string]bool{}
	frows, err := tx.QueryContext(ctx, `SELECT f.project_id, p.home FROM folders f JOIN projects p ON p.id = f.project_id WHERE f.path = ?`, oldRoot)
	if err != nil {
		return RelocateStats{}, err
	}
	homes := map[string]string{}
	for frows.Next() {
		var id, home string
		if err := frows.Scan(&id, &home); err != nil {
			frows.Close()
			return RelocateStats{}, err
		}
		projects[id], homes[id] = true, home
	}
	frows.Close()
	if len(projects) == 0 {
		return RelocateStats{}, fmt.Errorf("folder %s: %w", oldRoot, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE folders SET path = ? WHERE path = ?`, newRoot, oldRoot); err != nil {
		return RelocateStats{}, fmt.Errorf("update folder path: %w", err)
	}
	// A plain-folder project's home is its folder, and a home inside the
	// folder moves with it. One elsewhere stays where it is.
	for id, home := range homes {
		if home == "" {
			continue
		}
		if next, ok := relocatePath(home, mappings); ok {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET home = ? WHERE id = ?`, next, id); err != nil {
				return RelocateStats{}, err
			}
		}
	}

	type threadPath struct {
		id, cwd, projectID string
		changed            bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, cwd, project_id FROM threads`)
	if err != nil {
		return RelocateStats{}, err
	}
	var projectThreads []threadPath
	changedThreads := 0
	for rows.Next() {
		var item threadPath
		if err := rows.Scan(&item.id, &item.cwd, &item.projectID); err != nil {
			rows.Close()
			return RelocateStats{}, err
		}
		next, pathChanged := relocatePath(item.cwd, mappings)
		if pathChanged {
			item.cwd = next
			item.changed = true
			changedThreads++
		}
		if projects[item.projectID] || pathChanged {
			projectThreads = append(projectThreads, item)
		}
	}
	if err := rows.Close(); err != nil {
		return RelocateStats{}, err
	}
	if err := rows.Err(); err != nil {
		return RelocateStats{}, err
	}

	stats := RelocateStats{Threads: changedThreads}
	for _, item := range projectThreads {
		if item.changed {
			// item.cwd was rewritten above. Rows outside the old prefix retain
			// their cwd but still participate in the JSON migration below.
			if _, err := tx.ExecContext(ctx, `UPDATE threads SET cwd = ? WHERE id = ?`, item.cwd, item.id); err != nil {
				return RelocateStats{}, err
			}
		}
	}

	ids := make([]string, 0, len(projectThreads))
	for _, item := range projectThreads {
		ids = append(ids, item.id)
	}
	if len(ids) > 0 {
		if stats.Events, err = relocateJSONRows(ctx, tx, "events", "payload", "thread_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if stats.Snapshots, err = relocateJSONRows(ctx, tx, "snapshots", "state", "thread_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if stats.Commands, err = relocateJSONRows(ctx, tx, "commands", "result", "thread_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if _, err := relocateJSONRows(ctx, tx, "threads", "provision_result", "id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return RelocateStats{}, err
	}
	return stats, nil
}

func relocateJSONRows(ctx context.Context, tx *sql.Tx, table, column, key string, ids []string, mappings []RelocationPath) (int, error) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i := range ids {
		args[i] = ids[i]
	}
	query := fmt.Sprintf("SELECT rowid, %s FROM %s WHERE %s IN (%s) AND %s IS NOT NULL", column, table, key, placeholders, column)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	type change struct {
		rowid int64
		blob  []byte
	}
	var changes []change
	for rows.Next() {
		var rowid int64
		var blob []byte
		if err := rows.Scan(&rowid, &blob); err != nil {
			rows.Close()
			return 0, err
		}
		next, changed, err := relocateJSON(blob, mappings)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("rewrite %s.%s row %d: %w", table, column, rowid, err)
		}
		if changed {
			changes = append(changes, change{rowid: rowid, blob: next})
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, change := range changes {
		query := fmt.Sprintf("UPDATE %s SET %s = ? WHERE rowid = ?", table, column)
		if _, err := tx.ExecContext(ctx, query, change.blob, change.rowid); err != nil {
			return 0, err
		}
	}
	return len(changes), nil
}

func relocateJSON(blob []byte, mappings []RelocationPath) ([]byte, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(blob))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, false, err
	}
	changed := relocateJSONValue(&value, mappings)
	if !changed {
		return blob, false, nil
	}
	next, err := json.Marshal(value)
	return next, true, err
}

func relocateJSONValue(value *any, mappings []RelocationPath) bool {
	switch current := (*value).(type) {
	case string:
		if next, ok := relocatePath(current, mappings); ok {
			*value = next
			return true
		}
	case []any:
		changed := false
		for i := range current {
			changed = relocateJSONValue(&current[i], mappings) || changed
		}
		return changed
	case map[string]any:
		changed := false
		for key, item := range current {
			if relocateJSONValue(&item, mappings) {
				current[key] = item
				changed = true
			}
		}
		return changed
	}
	return false
}

func relocatePath(path string, mappings []RelocationPath) (string, bool) {
	path = filepath.Clean(path)
	for _, mapping := range mappings {
		oldRoot := filepath.Clean(mapping.Old)
		if path == oldRoot {
			return mapping.New, true
		}
		prefix := oldRoot + string(filepath.Separator)
		if strings.HasPrefix(path, prefix) {
			return filepath.Join(mapping.New, strings.TrimPrefix(path, prefix)), true
		}
	}
	return path, false
}
