package thread

import "context"

// SetThreadPinned pins a thread to the top of the sidebar, or unpins it. Pure
// metadata over threads, so none of it goes near an actor: pinning needs no
// harness, must work on a thread with no live process, and reaches clients
// through the thread-list broadcast the sidebar already consumes. The set_mode
// path round-trips to the harness and never writes the row the sidebar reads,
// neither of which fits.
func (m *Manager) SetThreadPinned(ctx context.Context, threadID string, pinned bool) error {
	if err := m.store.SetThreadPinned(ctx, threadID, pinned); err != nil {
		return err
	}
	m.notifyList()
	return nil
}
