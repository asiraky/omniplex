package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/asiraky/omniplex/internal/usage"
)

func recordUsagePricing(ctx context.Context, tx *sql.Tx, session string, seq int64) error {
	var model string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(payload, '$.model'), '') FROM events
 WHERE session_id=? AND seq<=? AND type IN ('session.created','session.config_changed')
 AND COALESCE(json_extract(payload, '$.model'), '') <> '' ORDER BY seq DESC LIMIT 1`, session, seq).Scan(&model)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	raw, err := json.Marshal(usage.RecordPricing(model))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_pricing(session_id,seq,pricing) VALUES(?,?,?)`, session, seq, raw)
	return err
}

// Existing history gets an explicit initial valuation once, on upgrade. New
// accounting events record their rates in the same transaction as the event.
func (s *Store) backfillUsagePricing() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT e.session_id,e.seq FROM events e LEFT JOIN usage_pricing p
 ON p.session_id=e.session_id AND p.seq=e.seq WHERE e.type='usage.updated' AND p.seq IS NULL`)
	if err != nil {
		return err
	}
	type key struct {
		session string
		seq     int64
	}
	var missing []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.session, &k.seq); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range missing {
		if err := recordUsagePricing(ctx, tx, k.session, k.seq); err != nil {
			return err
		}
	}
	return tx.Commit()
}
