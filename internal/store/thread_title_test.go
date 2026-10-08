package store

import (
	"context"
	"errors"
	"testing"
)

// A prompt-derived title only fills a blank; a rename has to win over one
// that is already there, and leave the activity stamp where it was.
func TestRenameThreadOverwritesWithoutBumpingActivity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "s1")
	if err := s.SetTitle(ctx, "s1", "fix the build"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Thread(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RenameThread(ctx, "s1", "Build fix"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	after, _ := s.Thread(ctx, "s1")
	if after.Title != "Build fix" {
		t.Fatalf("title = %q, want %q", after.Title, "Build fix")
	}
	if after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at moved from %d to %d", before.UpdatedAt, after.UpdatedAt)
	}

	// The next prompt's title must not undo the rename.
	if err := s.SetTitle(ctx, "s1", "second prompt"); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Thread(ctx, "s1")
	if after.Title != "Build fix" {
		t.Fatalf("a later prompt overwrote the rename: %q", after.Title)
	}

	if err := s.RenameThread(ctx, "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renaming an unknown thread: got %v, want ErrNotFound", err)
	}
}
