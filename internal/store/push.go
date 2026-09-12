package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---- Web Push ----
//
// A subscription is a browser's handle on its own push service, plus the two
// keys that service's payloads must be encrypted to. It belongs to a device
// rather than standing alone: revoking a paired phone has to take its
// notifications with it, or a revoked device keeps being told what the agent
// is doing.
//
// The endpoint is the identity. A browser re-subscribing after its push
// service rotated the endpoint hands us a fresh one and the old simply stops
// working, which is why sending prunes on 404 and 410 rather than retrying.

const pushSchema = `
CREATE TABLE IF NOT EXISTS push_subscriptions (
  endpoint   TEXT PRIMARY KEY,
  device_id  TEXT NOT NULL,
  p256dh     TEXT NOT NULL,
  auth       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS push_subscriptions_device ON push_subscriptions(device_id);

-- One row, holding the VAPID keypair this server identifies itself to push
-- services with. It lives beside the event log for the reason the auth tables
-- do: one file to back up, one file to delete to revoke everything.
CREATE TABLE IF NOT EXISTS push_keys (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  public_key  TEXT NOT NULL,
  private_key TEXT NOT NULL
);
`

// PushSubscription is one browser's push endpoint.
type PushSubscription struct {
	Endpoint  string `json:"endpoint"`
	DeviceID  string `json:"-"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) initPush() error {
	_, err := s.db.Exec(pushSchema)
	return err
}

// SaveSubscription records a browser's push endpoint against a device.
//
// Upsert rather than insert: a browser that already holds a working
// subscription re-sends it on every load, and its keys may have been rotated
// under the same endpoint. Re-registering has to be idempotent, because it is
// the only repair path a user has when something drifts.
func (s *Store) SaveSubscription(ctx context.Context, sub PushSubscription) error {
	if sub.Endpoint == "" || sub.DeviceID == "" {
		return errors.New("subscription needs an endpoint and a device")
	}
	if sub.CreatedAt == 0 {
		sub.CreatedAt = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO push_subscriptions (endpoint, device_id, p256dh, auth, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(endpoint) DO UPDATE SET
  device_id = excluded.device_id,
  p256dh    = excluded.p256dh,
  auth      = excluded.auth`,
		sub.Endpoint, sub.DeviceID, sub.P256dh, sub.Auth, sub.CreatedAt)
	return err
}

// DeleteSubscription forgets one endpoint. Used both when a user turns
// notifications off and when a push service reports the endpoint is gone.
func (s *Store) DeleteSubscription(ctx context.Context, endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// DeleteDeviceSubscriptions drops everything a device is subscribed with.
// Called on revocation: a revoked phone must stop being told what the agent is
// doing, and its subscription is a credential of its own — the push service
// will deliver to it whether or not the device token still exists.
func (s *Store) DeleteDeviceSubscriptions(ctx context.Context, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE device_id = ?`, deviceID)
	return err
}

// ListSubscriptions returns every registered endpoint.
func (s *Store) ListSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	return s.querySubscriptions(ctx, `
SELECT endpoint, device_id, p256dh, auth, created_at
FROM push_subscriptions ORDER BY created_at`)
}

// DeviceSubscriptions returns the endpoints one device holds. The UI asks this
// to say whether *this* browser is currently subscribed, which it cannot
// answer from the browser permission alone: permission is granted per origin
// and survives the subscription being dropped on the server.
func (s *Store) DeviceSubscriptions(ctx context.Context, deviceID string) ([]PushSubscription, error) {
	return s.querySubscriptions(ctx, `
SELECT endpoint, device_id, p256dh, auth, created_at
FROM push_subscriptions WHERE device_id = ? ORDER BY created_at`, deviceID)
}

func (s *Store) querySubscriptions(ctx context.Context, query string, args ...any) ([]PushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PushSubscription
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.Endpoint, &sub.DeviceID, &sub.P256dh, &sub.Auth, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// VAPIDKeys returns the stored keypair, or empty strings when none has been
// generated yet.
func (s *Store) VAPIDKeys(ctx context.Context) (public, private string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRowContext(ctx,
		`SELECT public_key, private_key FROM push_keys WHERE id = 1`).Scan(&public, &private)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return public, private, nil
}

// SaveVAPIDKeys stores the keypair, once.
//
// The insert does nothing when a row already exists, so two starts racing
// cannot leave half of one keypair beside half of another: whoever loses
// writes nothing and the caller re-reads. Rotating the pair invalidates every
// subscription in existence, so it is deliberately not offered here.
func (s *Store) SaveVAPIDKeys(ctx context.Context, public, private string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_keys (id, public_key, private_key) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO NOTHING`, public, private)
	return err
}
