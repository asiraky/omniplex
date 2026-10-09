package thread

import (
	"context"
	"errors"
	"math"
)

// The sidebar order is metadata, like labels, read state and titles: a store
// write and the list broadcast, with no actor involved, so a thread with no
// live harness can be moved and every paired device shows the same order.

// ErrBadPosition refuses a position that cannot be ordered. JSON cannot carry
// NaN or Inf, but a value that compares false against everything would leave
// the thread with no place in the list, so it is checked here rather than
// trusted to the wire.
var ErrBadPosition = errors.New("a thread position must be a finite number")

// SetThreadPosition moves one thread in the user's order. The client sends
// the midpoint of the neighbours it was dropped between, so one move is one
// row written and one small command on the wire.
func (m *Manager) SetThreadPosition(ctx context.Context, threadID string, position float64) error {
	if math.IsNaN(position) || math.IsInf(position, 0) {
		return ErrBadPosition
	}
	if err := m.store.SetThreadPosition(ctx, threadID, position); err != nil {
		return err
	}
	m.notifyList()
	return nil
}
