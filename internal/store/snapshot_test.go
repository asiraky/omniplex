package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPutSnapshotBlobKeepsOnlyTheNewest(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.CreateThread(ctx, ThreadMeta{ID: "t", Cwd: "/x", Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	// In order, as the actor writes them: a map would range in random order,
	// and writing 9 before 5 leaves both rows.
	for _, s := range []struct {
		seq  int64
		blob string
	}{{5, `"five"`}, {9, `"nine"`}} {
		if err := st.PutSnapshotBlob(ctx, "t", s.seq, []byte(s.blob)); err != nil {
			t.Fatal(err)
		}
	}
	seq, blob, err := st.LatestSnapshot(ctx, "t")
	if err != nil || seq != 9 || string(blob) != `"nine"` {
		t.Fatalf("latest = %d %s %v, want 9 \"nine\"", seq, blob, err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM snapshots WHERE thread_id = 't'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d snapshot rows (%v), want 1", n, err)
	}
}

// The actor writes snapshots off its own goroutine, so one can arrive after
// the thread is gone. It must not leave a row behind for nothing to delete.
func TestPutSnapshotBlobForADeletedThreadWritesNothing(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.CreateThread(ctx, ThreadMeta{ID: "t", Cwd: "/x", Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteThread(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSnapshotBlob(ctx, "t", 3, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if seq, blob, err := st.LatestSnapshot(ctx, "t"); err != nil || blob != nil {
		t.Fatalf("deleted thread has snapshot %d %s (%v)", seq, blob, err)
	}
}
