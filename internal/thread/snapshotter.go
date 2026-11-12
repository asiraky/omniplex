package thread

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/asiraky/omniplex/internal/store"
)

// snapshotter writes an actor's snapshots off the actor goroutine. A long
// thread's snapshot is megabytes, and writing it holds the store's write lock
// for as long as the disk takes, so on the actor it stalled the thread's own
// events and every read that queues behind them in the inbox.
//
// The projection is the actor's alone, so the actor still encodes it; only
// the bytes cross over. One write is in flight at a time and only the newest
// waiting one is kept: a snapshot is a cache, and one superseded before it
// was written has nothing left to offer.
type snapshotter struct {
	store *store.Store
	id    string
	logf  func(string, ...any)

	mu     sync.Mutex
	next   *pendingSnapshot
	wake   chan struct{}
	done   chan struct{}
	exited chan struct{}
}

type pendingSnapshot struct {
	seq  int64
	blob []byte
}

func newSnapshotter(st *store.Store, id string, logf func(string, ...any)) *snapshotter {
	s := &snapshotter{store: st, id: id, logf: logf,
		wake: make(chan struct{}, 1), done: make(chan struct{}), exited: make(chan struct{})}
	go s.loop()
	return s
}

// put encodes state now, on the caller's goroutine, and leaves the write to
// the snapshotter.
func (s *snapshotter) put(seq int64, state any) {
	blob, err := json.Marshal(state)
	if err != nil {
		s.logf("snapshot %s: %v", s.id, err)
		return
	}
	s.mu.Lock()
	s.next = &pendingSnapshot{seq: seq, blob: blob}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// close writes whatever is still waiting and returns once it is stored, so
// a thread deleted after its actor stops cannot have a snapshot land after.
func (s *snapshotter) close() {
	close(s.done)
	<-s.exited
}

func (s *snapshotter) loop() {
	defer close(s.exited)
	for {
		select {
		case <-s.wake:
			s.write()
		case <-s.done:
			s.write()
			return
		}
	}
}

func (s *snapshotter) write() {
	s.mu.Lock()
	p := s.next
	s.next = nil
	s.mu.Unlock()
	if p == nil {
		return
	}
	if err := s.store.PutSnapshotBlob(context.Background(), s.id, p.seq, p.blob); err != nil {
		s.logf("snapshot %s: %v", s.id, err)
	}
}
