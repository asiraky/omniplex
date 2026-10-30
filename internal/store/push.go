package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/proto"
)

// ---- Web push subscriptions ----

const pushSchema = `
CREATE TABLE IF NOT EXISTS push_subscriptions (
  id          TEXT PRIMARY KEY,
  endpoint    TEXT NOT NULL UNIQUE,
  p256dh      TEXT NOT NULL,
  auth        TEXT NOT NULL,
  label       TEXT NOT NULL DEFAULT '',
  device_id   TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
`

// PushSubscription is one browser's Web Push subscription. The keys encrypt
// what is sent to it and never leave the server.
type PushSubscription struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"-"`
	Auth     string `json:"-"`
	Label    string `json:"label"`
	// DeviceID is the paired device that subscribed, "" on loopback.
	DeviceID  string `json:"deviceId"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) initPush() error {
	_, err := s.db.Exec(pushSchema)
	return err
}

// SavePushSubscription upserts by endpoint, so a browser subscribing again
// keeps one row with fresh keys and label, and returns the row's id.
func (s *Store) SavePushSubscription(ctx context.Context, p PushSubscription) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, label, device_id, created_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(endpoint) DO UPDATE SET
		   p256dh = excluded.p256dh, auth = excluded.auth,
		   label = excluded.label, device_id = excluded.device_id
		 RETURNING id`,
		uuid.NewString(), p.Endpoint, p.P256dh, p.Auth, p.Label, p.DeviceID, proto.NowMillis()).
		Scan(&id)
	return id, err
}

// ListPushSubscriptions returns every subscription, newest first.
func (s *Store) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, endpoint, p256dh, auth, label, device_id, created_at
		 FROM push_subscriptions ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PushSubscription{}
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.Endpoint, &p.P256dh, &p.Auth, &p.Label, &p.DeviceID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePushSubscription removes one subscription. ErrNotFound if absent.
func (s *Store) DeletePushSubscription(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
