package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/asiraky/omniplex/internal/proto"
)

// The due-time index is updated in the same transaction as its source event.
func updateScheduleIndex(ctx context.Context, tx *sql.Tx, threadID string, em proto.Emission, payload []byte) error {
	switch em.Type {
	case proto.PromptScheduled:
		var p proto.ScheduledPrompt
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO scheduled_prompts(thread_id,schedule_id,due_at,status) VALUES(?,?,?,?) ON CONFLICT(thread_id,schedule_id) DO UPDATE SET due_at=excluded.due_at,status=excluded.status`, threadID, p.ID, p.DueAt, p.Status)
		return err
	case proto.TurnStarted:
		var p proto.TurnStartedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE scheduled_prompts SET status='sent' WHERE thread_id=? AND schedule_id=?`, threadID, p.QueueID)
		return err
	case proto.ThreadClosed:
		_, err := tx.ExecContext(ctx, `DELETE FROM scheduled_prompts WHERE thread_id=?`, threadID)
		return err
	}
	return nil
}

func (s *Store) DueScheduleThreads(ctx context.Context, now int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT p.thread_id FROM scheduled_prompts p JOIN threads s ON s.id=p.thread_id WHERE p.status IN ('pending','ready') AND p.due_at<=? AND s.phase IN ('idle','turn')`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) ScheduleCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT thread_id,COUNT(*) FROM scheduled_prompts WHERE status IN ('pending','ready') GROUP BY thread_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
