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

// RelocateStats describes the durable records changed by RelocateProject.
type RelocateStats struct {
	Sessions  int
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

// RelocateProject atomically rewrites the path-bearing records for one project.
// Callers are expected to run this offline: a live manager would retain the old
// paths in its in-memory actors even though the database had changed beneath it.
func (s *Store) RelocateProject(ctx context.Context, oldRoot, newRoot string, aliases ...RelocationPath) (RelocateStats, error) {
	mappings := append([]RelocationPath{{Old: oldRoot, New: newRoot}}, aliases...)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RelocateStats{}, err
	}
	defer tx.Rollback()

	var projectID, home string
	if err := tx.QueryRowContext(ctx, `SELECT id, home FROM projects WHERE root = ?`, oldRoot).Scan(&projectID, &home); err != nil {
		if err == sql.ErrNoRows {
			return RelocateStats{}, fmt.Errorf("project rooted at %s: %w", oldRoot, ErrNotFound)
		}
		return RelocateStats{}, err
	}
	// A plain-folder project's home is its root, and a home inside the root
	// moves with it. One elsewhere (a git project's) stays where it is.
	if home != "" {
		home, _ = relocatePath(home, mappings)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET root = ?, home = ? WHERE id = ?`, newRoot, home, projectID); err != nil {
		return RelocateStats{}, fmt.Errorf("update project root: %w", err)
	}

	type sessionPath struct {
		id, cwd, projectID string
		changed            bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, cwd, project_id FROM sessions`)
	if err != nil {
		return RelocateStats{}, err
	}
	var projectSessions []sessionPath
	changedSessions := 0
	for rows.Next() {
		var item sessionPath
		if err := rows.Scan(&item.id, &item.cwd, &item.projectID); err != nil {
			rows.Close()
			return RelocateStats{}, err
		}
		next, pathChanged := relocatePath(item.cwd, mappings)
		if pathChanged {
			item.cwd = next
			item.changed = true
			changedSessions++
		}
		if item.projectID == projectID || pathChanged {
			projectSessions = append(projectSessions, item)
		}
	}
	if err := rows.Close(); err != nil {
		return RelocateStats{}, err
	}
	if err := rows.Err(); err != nil {
		return RelocateStats{}, err
	}

	stats := RelocateStats{Sessions: changedSessions}
	for _, item := range projectSessions {
		if item.changed {
			// item.cwd was rewritten above. Rows outside the old prefix retain
			// their cwd but still participate in the JSON migration below.
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET cwd = ? WHERE id = ?`, item.cwd, item.id); err != nil {
				return RelocateStats{}, err
			}
		}
	}

	ids := make([]string, 0, len(projectSessions))
	for _, item := range projectSessions {
		ids = append(ids, item.id)
	}
	if len(ids) > 0 {
		if stats.Events, err = relocateJSONRows(ctx, tx, "events", "payload", "session_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if stats.Snapshots, err = relocateJSONRows(ctx, tx, "snapshots", "state", "session_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if stats.Commands, err = relocateJSONRows(ctx, tx, "commands", "result", "session_id", ids, mappings); err != nil {
			return RelocateStats{}, err
		}
		if _, err := relocateJSONRows(ctx, tx, "sessions", "provision_result", "id", ids, mappings); err != nil {
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
