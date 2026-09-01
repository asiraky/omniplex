package session

import "context"

// Viewed state is pure metadata over sessions, exactly like labels: no actor,
// no harness, works on a session with no live process, and reaches every
// paired device through the session-list broadcast the sidebar already
// consumes. That broadcast is the point — "I read this on the phone" is what
// clears the unread dot on the laptop.

// MarkSessionViewed records that the user has seen the session up to seq.
// The store keeps it monotonic, so a stale device cannot un-read anything.
func (m *Manager) MarkSessionViewed(ctx context.Context, sessionID string, seq int64) error {
	if err := m.store.MarkSessionViewed(ctx, sessionID, seq); err != nil {
		return err
	}
	m.notifyList()
	return nil
}

// MarkSessionUnread puts the session back in the "needs a look" state — the
// user's explicit flag, and the one path that moves the cursor backwards.
func (m *Manager) MarkSessionUnread(ctx context.Context, sessionID string) error {
	if err := m.store.MarkSessionUnread(ctx, sessionID); err != nil {
		return err
	}
	m.notifyList()
	return nil
}
