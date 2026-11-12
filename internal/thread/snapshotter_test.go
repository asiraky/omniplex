package thread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

// close is the actor's last word on its snapshot: whatever was put last is
// what the next restore finds, however far behind the writer had fallen.
func TestSnapshotterCloseStoresTheLastPut(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.CreateThread(ctx, store.ThreadMeta{ID: "t", Cwd: "/x", Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	s := newSnapshotter(st, "t", t.Logf)
	for seq := int64(1); seq <= 50; seq++ {
		s.put(seq, map[string]int64{"seq": seq})
	}
	s.close()
	seq, blob, err := st.LatestSnapshot(ctx, "t")
	if err != nil || seq != 50 || string(blob) != `{"seq":50}` {
		t.Fatalf("stored %d %s (%v), want 50", seq, blob, err)
	}
}

func TestSnapshotterCloseWithNothingPendingReturns(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	newSnapshotter(st, "t", t.Logf).close()
	if _, blob, err := st.LatestSnapshot(context.Background(), "t"); err != nil || blob != nil {
		t.Fatalf("got snapshot %s (%v) with none put", blob, err)
	}
}
