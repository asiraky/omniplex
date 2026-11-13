package thread

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// Rewinding a thread: "Edit from here" on an earlier prompt.
//
// The thread is cut back to just before one of its turns, in the harness's
// own history first and then in the log, and optionally the checkout is put
// back to that turn's starting snapshot. The order is fixed: the harness,
// then the files, then thread.rewound. A harness that will not rewind leaves
// everything as it was. A harness that rewound has already forgotten the
// turns, so from there on the log follows it whatever the files do: a restore
// that fails still gets thread.rewound, saying the files were not restored,
// and the error says so too.

// rewindTimeout bounds the harness's part of a rewind. Claude restarts its
// process to fork the conversation, which is the slow case.
const rewindTimeout = 2 * time.Minute

// Rewind cuts the thread back to just before turnID: that turn and everything
// after it leave the harness's memory and the transcript. With restoreFiles
// the checkout goes back to how it was when that turn started. Refused while
// anything is in flight, for a turn the harness has no anchor for, and for a
// restore the thread is not allowed (restoreRefusal).
func (a *Actor) Rewind(ctx context.Context, turnID string, restoreFiles bool) error {
	_, err := a.call(ctx, command{kind: cmdRewind, reqID: turnID, restoreFiles: restoreFiles})
	return err
}

// rewind is cmdRewind on the loop. The harness is asked off it, in line with
// every other control call, and the result is recorded back on it.
func (a *Actor) rewind(ctx context.Context, turnID string, restoreFiles bool, reply chan cmdResult) {
	rewinder, err := a.rewindRefusal(turnID)
	if err == nil && restoreFiles {
		err = a.restoreRefusal(ctx)
	}
	if err != nil {
		reply <- cmdResult{err: err}
		return
	}
	idx := a.state.TurnIndex(turnID)
	target := adapter.RewindTarget{Turn: a.state.Turns[idx].HarnessRef}
	if idx > 0 {
		target.Previous = a.state.Turns[idx-1].HarnessRef
	}
	cp, sess, threadID := a.checkpoints, a.sess, a.ID
	// Held until the result is back on the loop. inOrder's pending count
	// already keeps prompts and the queue waiting; this stops what starts a
	// turn without asking it (rewindBlocks).
	a.rewinding = true
	a.inOrder(rewindTimeout, func(ctx context.Context) (any, error) {
		// Checked before the harness is asked: a restore that cannot happen
		// must not cost the conversation.
		if restoreFiles && !checkpointExists(ctx, cp.root, checkpointRef(threadID, turnID, "base")) {
			return nil, errNoBaseCheckpoint
		}
		if err := rewinder.Rewind(ctx, target); err != nil {
			return nil, err
		}
		if !restoreFiles {
			return nil, nil
		}
		// Its own clock: the harness may have used most of the call's.
		rctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
		defer cancel()
		return restoreResult{err: cp.restore(rctx, turnID)}, nil
	}, func(v any, err error) {
		a.rewinding = false
		if err != nil {
			reply <- cmdResult{err: err}
			return
		}
		restored, _ := v.(restoreResult)
		reply <- cmdResult{err: a.recordRewind(sess, turnID, restoreFiles, restored.err)}
	})
}

// restoreResult carries a restore's outcome out of a harness call that
// itself succeeded: the conversation was rewound either way.
type restoreResult struct{ err error }

// recordRewind writes thread.rewound for a rewind the harness has done, and
// says what the caller is told.
func (a *Actor) recordRewind(sess adapter.Session, turnID string, restoreFiles bool, restoreErr error) error {
	if sess != a.sess {
		// Nothing replaces the process mid-rewind today (rewindBlocks). Were
		// it to, the new process resumed a conversation the old one may or
		// may not have cut, and the log cannot claim either.
		if restoreFiles && restoreErr == nil {
			return errors.New("the harness restarted during the rewind, so the conversation may not have been rewound; the files were restored to before that turn")
		}
		return errHarnessRestarted
	}
	idx := a.state.TurnIndex(turnID)
	if idx < 0 {
		// Only another rewind could have taken it, and rewindRefusal turns
		// a second one away while this one runs.
		return fmt.Errorf("turn %q left the thread during the rewind", turnID)
	}
	ok := restoreFiles && restoreErr == nil
	if err := a.append(proto.Emit(proto.ThreadRewound, proto.ThreadRewoundPayload{
		TurnID: turnID, At: rewindCut(a.state, idx), RestoredFiles: ok,
	})); err != nil {
		return err
	}
	// The last turn is always among those cut, and a usage-limit
	// continuation only ever waits on the last turn: it would continue
	// work the agent no longer remembers.
	a.disarmLimit()
	if restoreFiles && restoreErr != nil {
		return fmt.Errorf("the conversation was rewound, but the files were not restored: %w", restoreErr)
	}
	return nil
}

// rewindCut is the index of the first item a rewind to before Turns[idx]
// removes: the first item that turn owns. A turn with no items of its own (one
// the harness opened with nothing in it yet) cuts at the first item of a turn
// after it, and with none of those either, at the end: there is nothing in the
// transcript to remove, only turns.
func rewindCut(s *projection.State, idx int) int {
	for _, t := range s.Turns[idx:] {
		if at := s.RewindAt(t.ID); at >= 0 {
			return at
		}
	}
	return len(s.Items)
}

// rewindRefusal says why the thread cannot be rewound to before turnID now,
// or hands back the harness that can do it. Everything in flight belongs to
// the conversation being cut, so a rewind waits for all of it, the way an
// account switch does.
func (a *Actor) rewindRefusal(turnID string) (adapter.Rewinder, error) {
	switch {
	case a.state.Closed:
		return nil, ErrClosed
	case a.sess == nil:
		return nil, ErrNotReady
	case a.rewinding:
		return nil, errors.New("a rewind is already in progress")
	case a.turnActive != "" || a.stopping != nil || a.state.Phase != "idle":
		return nil, errors.New("wait for the turn to finish before rewinding")
	case len(interruptedJobs(a.state)) > 0:
		return nil, errors.New("background work is still running; wait for it or stop it before rewinding")
	case len(a.state.Pending) > 0 || a.state.Questions() > 0:
		return nil, errors.New("answer the waiting question before rewinding")
	case a.controlPending > 0:
		return nil, errors.New("a change is still on its way to the harness; try again once it has landed")
	}
	a.mu.Lock()
	recovering := a.recovery != nil
	a.mu.Unlock()
	if recovering {
		return nil, errors.New("the thread is still resuming work a restart interrupted; wait for it before rewinding")
	}
	rewinder, ok := a.sess.(adapter.Rewinder)
	if !ok {
		return nil, errors.New("this harness cannot rewind a conversation")
	}
	idx := a.state.TurnIndex(turnID)
	switch {
	case idx < 0:
		return nil, fmt.Errorf("there is no turn %q in this thread", turnID)
	case a.state.CanRewindTo(idx):
		return rewinder, nil
	case a.state.Turns[idx].HarnessRef == "":
		return nil, errors.New("this turn predates rewind support, so the harness has nothing to rewind it to")
	}
	return nil, errors.New("the turn before this one predates rewind support, so the harness has nothing to resume from")
}

// rewindBlocks says a command must not run while a rewind is in flight: it
// would start a turn, or replace the harness process, without waiting on the
// control line the way a prompt does. A turn started now would land in the
// log before thread.rewound and be cut with the turns it follows.
func (a *Actor) rewindBlocks(c command) bool {
	if !a.rewinding {
		return false
	}
	switch c.kind {
	case cmdContinue, cmdRunComposer, cmdSwitchAccount:
		return true
	case cmdPrompt:
		// A human prompt queues behind the control line; the server's own
		// continuation does not.
		return c.plan != nil || c.recovery != nil
	}
	return false
}

// restoreRefusal says why this thread may not have its files put back, or
// nil. Read from the store on the loop: rows are small, and the answer must be
// the one that holds when the command is taken.
func (a *Actor) restoreRefusal(ctx context.Context) error {
	if a.checkpoints == nil {
		return errNoCheckpoints
	}
	self, err := a.store.Thread(ctx, a.ID)
	if err != nil {
		return err
	}
	threads, err := a.store.ListThreads(ctx)
	if err != nil {
		return err
	}
	checkoutOf := func(dir string) string { return repoRoot(ctx, dir) }
	return restoreBlocked(self, a.checkpoints.root, threads, checkoutOf)
}

// restoreBlocked is the rule for offering a files restore, which the web
// applies to its own thread list to decide what the rewind sheet offers.
// Files are only put back in a worktree omniplex made for this thread, and
// only while no other open thread works in that folder, inside it, or in a
// folder that holds it: a restore writes and deletes files under whoever else
// is there. root is the checkout the snapshots are of, which holds the
// thread's folder; both are checked. checkoutOf answers the top of the git
// checkout a folder is in, or "".
func restoreBlocked(self store.ThreadMeta, root string, threads []store.ThreadMeta, checkoutOf func(string) string) error {
	if self.WorkspaceMode != "managed" {
		return errors.New("files are only restored in a worktree omniplex made for this thread")
	}
	if other, ok := sharedCheckout(self.ID, root, []string{self.Cwd, root}, threads, checkoutOf); ok {
		title := other.Title
		if title == "" {
			title = "untitled thread"
		}
		return fmt.Errorf("files cannot be restored while %q works in this checkout too", title)
	}
	return nil
}

// sharedCheckout finds an open thread other than self whose folder is one of
// dirs, inside one, or holds one. A closed thread has released its folder. A
// managed thread still being made has the project root as a placeholder, not
// a folder it works in (see ListWorkspaces).
//
// A thread whose folder holds one of dirs only counts when it is not in a git
// checkout of its own. Worktrees live under the project folder, so a thread
// working in the project checkout holds every one of them on disk, but its
// harness works in that checkout, where the worktree is only an ignored
// folder. A plain folder that holds the worktree is another matter: nothing
// keeps an agent there out of these files.
func sharedCheckout(self, root string, dirs []string, threads []store.ThreadMeta, checkoutOf func(string) string) (store.ThreadMeta, bool) {
	ownCheckout := ""
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			ownCheckout = canonicalPath(abs)
		}
	}
	var mine []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			mine = append(mine, canonicalPath(abs))
		}
	}
	for _, t := range threads {
		if t.ID == self || t.Phase == "closed" || t.Cwd == "" {
			continue
		}
		if t.WorkspaceMode == "managed" && !provisioned(t.Phase) {
			continue
		}
		abs, err := filepath.Abs(t.Cwd)
		if err != nil {
			continue
		}
		theirs := canonicalPath(abs)
		for _, d := range mine {
			if theirs == d || within(theirs, d) {
				return t, true
			}
			if within(d, theirs) && !inOtherCheckout(theirs, ownCheckout, checkoutOf) {
				return t, true
			}
		}
	}
	return store.ThreadMeta{}, false
}

// inOtherCheckout reports whether dir is inside a git checkout other than own.
func inOtherCheckout(dir, own string, checkoutOf func(string) string) bool {
	co := checkoutOf(dir)
	if co == "" {
		return false
	}
	if abs, err := filepath.Abs(co); err == nil {
		co = canonicalPath(abs)
	}
	return co != own
}
