package thread

import "context"

// Viewed state is pure metadata over threads, exactly like labels: no actor,
// no harness, works on a thread with no live process, and reaches every
// paired device through the thread-list broadcast the sidebar already
// consumes. That broadcast is the point — "I read this on the phone" is what
// clears the unread dot on the laptop.

// MarkThreadViewed records that the user has seen the thread up to seq.
// The store keeps it monotonic, so a stale device cannot un-read anything.
func (m *Manager) MarkThreadViewed(ctx context.Context, threadID string, seq int64) error {
	if err := m.store.MarkThreadViewed(ctx, threadID, seq); err != nil {
		return err
	}
	m.notifyList()
	return nil
}

// MarkThreadUnread puts the thread back in the "needs a look" state — the
// user's explicit flag, and the one path that moves the cursor backwards.
func (m *Manager) MarkThreadUnread(ctx context.Context, threadID string) error {
	if err := m.store.MarkThreadUnread(ctx, threadID); err != nil {
		return err
	}
	m.notifyList()
	return nil
}
