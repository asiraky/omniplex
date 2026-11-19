package thread

import (
	"context"
	"errors"
)

// RestartThread stands in for #420's restart until that branch's server side
// lands; the rebase onto it deletes this file. It has #420's signature:
// pending is true when the restart waits for a running turn to end.
func (m *Manager) RestartThread(ctx context.Context, threadID string) (pending bool, err error) {
	return false, errors.New("thread restart is not available yet")
}
