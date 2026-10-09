package thread

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

func TestSetThreadPositionBroadcastsAndRefusesNonNumbers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.CreateThread(ctx, store.ThreadMeta{ID: "t1", Cwd: "/tmp", Harness: "h", Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	sub, ch := mgr.SubscribeList()
	defer mgr.UnsubscribeList(sub)

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := mgr.SetThreadPosition(ctx, "t1", bad); !errors.Is(err, ErrBadPosition) {
			t.Fatalf("position %v: got %v, want ErrBadPosition", bad, err)
		}
	}
	select {
	case <-ch:
		t.Fatal("a refused move broadcast the list")
	default:
	}

	if err := mgr.SetThreadPosition(ctx, "t1", 2.5); err != nil {
		t.Fatalf("set position: %v", err)
	}
	if got, _ := st.Thread(ctx, "t1"); got.Position != 2.5 {
		t.Fatalf("stored position %v", got.Position)
	}
	select {
	case <-ch:
	default:
		t.Fatal("a move must reach every device through the list broadcast")
	}
}
