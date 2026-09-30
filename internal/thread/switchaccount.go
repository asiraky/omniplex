package thread

import (
	"context"
	"errors"
	"fmt"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// SwitchAccount moves a thread to another account of the same harness —
// the way out when one account hits its usage limit. The conversation goes
// with it: the harness's own record is moved to where the new account keeps
// conversations, and the next turn resumes it there. Another harness is a
// different agent, not another login, so that is refused.
func (m *Manager) SwitchAccount(ctx context.Context, threadID, instanceID string) error {
	m.switchMu.Lock()
	defer m.switchMu.Unlock()
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return err
	}
	if meta.Phase != "idle" {
		return fmt.Errorf("a thread can only switch account while idle (it is %s)", meta.Phase)
	}
	from, err := m.instanceFor(meta)
	if err != nil {
		return err
	}
	if from.inst.ID == instanceID {
		return nil
	}
	to, ok := m.lookup(instanceID)
	switch {
	case !ok:
		return fmt.Errorf("unknown account %q", instanceID)
	case to.inst.Driver != meta.Harness:
		return fmt.Errorf("%s is not a %s account; a thread can only switch between accounts of its own harness", to.inst.DisplayName, meta.Harness)
	case !to.inst.Enabled:
		return fmt.Errorf("%s is disabled", to.inst.DisplayName)
	case to.ad == nil:
		return fmt.Errorf("no %q driver in this build", to.inst.Driver)
	}
	mover, ok := to.ad.(adapter.ConversationMover)
	if !ok {
		return errors.New("this harness cannot move a conversation to another account")
	}
	fromEnv, err := m.envFor(from.inst)
	if err != nil {
		return err
	}
	toEnv, err := m.envFor(to.inst)
	if err != nil {
		return err
	}
	a, err := m.View(ctx, threadID)
	if err != nil {
		return err
	}
	err = a.SwitchAccount(ctx, accountSwitch{
		ad:  to.ad,
		env: toEnv,
		move: func(harnessID string) error {
			if err := mover.MoveConversation(fromEnv, toEnv, meta.Cwd, harnessID); err != nil {
				return err
			}
			if err := m.store.SetProviderInstance(context.Background(), threadID, to.inst.ID); err != nil {
				// Put the conversation back where the account the thread
				// still has will look for it.
				if back := mover.MoveConversation(toEnv, fromEnv, meta.Cwd, harnessID); back != nil {
					m.logf("switch account %s: moving the conversation back failed: %v", threadID, back)
				}
				return err
			}
			return nil
		},
		changed: proto.ThreadAccountChangedPayload{
			From: from.inst.ID, To: to.inst.ID,
			FromName: from.inst.DisplayName, ToName: to.inst.DisplayName,
		},
	})
	if err != nil {
		return err
	}
	// Limit pushes from the next process belong to the new account.
	m.bindQuota(a)
	m.notifyList()
	return nil
}
