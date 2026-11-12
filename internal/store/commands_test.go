package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// A command claimed by a process that died before finishing it can be
// claimed again after a restart; a finished one still replays its result.
func TestOpenFreesCommandsALastProcessLeftUnfinished(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, id := range []string{"cmd-cut-off", "cmd-done"} {
		if _, _, err := s.ClaimCommand(ctx, id, "t1"); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	if err := s.CompleteCommand(ctx, "cmd-done", map[string]string{"turnId": "x"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, _, err := s.ClaimCommand(ctx, "cmd-cut-off", "t1"); !errors.Is(err, ErrCommandInProgress) {
		t.Fatalf("before restart: got %v, want ErrCommandInProgress", err)
	}
	_ = s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if res, done, err := s.ClaimCommand(ctx, "cmd-cut-off", "t1"); err != nil || done || res != nil {
		t.Fatalf("after restart: got (%s, %v, %v), want a fresh claim", res, done, err)
	}
	res, done, err := s.ClaimCommand(ctx, "cmd-done", "t1")
	if err != nil || !done || string(res) != `{"turnId":"x"}` {
		t.Fatalf("finished command: got (%s, %v, %v), want its result replayed", res, done, err)
	}
}
