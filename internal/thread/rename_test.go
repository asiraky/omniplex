package thread

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/store"
)

func TestNormaliseTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  Build fix  ", "Build fix"},
		{"line one\nline two\t\tand tabs", "line one line two and tabs"},
		{" \n\t ", ""},
	} {
		if got := normaliseTitle(tc.in); got != tc.want {
			t.Errorf("normaliseTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The cap counts runes, so a multi-byte title is not cut mid-character.
	long := strings.Repeat("é", maxTitleRunes+10)
	got := normaliseTitle(long)
	if utf8.RuneCountInString(got) != maxTitleRunes || !utf8.ValidString(got) {
		t.Fatalf("capped title has %d runes (valid=%v)", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}

func TestRenameThreadBroadcastsAndRefusesBlank(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.CreateThread(ctx, store.ThreadMeta{ID: "t1", Cwd: "/tmp", Harness: "h", Phase: "idle", Title: "old"}); err != nil {
		t.Fatal(err)
	}
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	sub, ch := mgr.SubscribeList()
	defer mgr.UnsubscribeList(sub)

	if _, err := mgr.RenameThread(ctx, "t1", "  \n "); !errors.Is(err, ErrEmptyTitle) {
		t.Fatalf("blank rename: got %v, want ErrEmptyTitle", err)
	}
	if got, _ := st.Thread(ctx, "t1"); got.Title != "old" {
		t.Fatalf("blank rename changed the title to %q", got.Title)
	}

	title, err := mgr.RenameThread(ctx, "t1", " New\nname ")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if title != "New name" {
		t.Fatalf("returned title %q, want %q", title, "New name")
	}
	if got, _ := st.Thread(ctx, "t1"); got.Title != "New name" {
		t.Fatalf("stored title %q", got.Title)
	}
	select {
	case <-ch:
	default:
		t.Fatal("a rename must reach the sidebar through the list broadcast")
	}
}

// A compare thread is named by the client before it runs, and the title the
// first prompt would give it must not replace that name.
func TestCreateProjectTitleSurvivesTheFirstPrompt(t *testing.T) {
	for _, tc := range []struct{ name, title, want string }{
		{"supplied", "Fix the login · Sonnet 5", "Fix the login · Sonnet 5"},
		{"normalised", "  Fix the\nlogin \t· Sonnet 5 ", "Fix the login · Sonnet 5"},
		{"blank falls back to the prompt", " \n ", "fix the login page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := gitRepo(t)
			st, p := testProject(t, root)
			fa := &fakeAdapter{}
			mgr := newManager(st, func(string, ...any) {}, fa)
			defer mgr.Shutdown()
			ctx := context.Background()

			a, err := mgr.CreateProject(ctx, CreateProjectOptions{ProjectID: p.ID, Workspace: "local", Title: tc.title})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Dispose("test done")
			ready(t, st, a.ID)

			if _, err := a.Prompt(ctx, "fix the login page", nil); err != nil {
				t.Fatal(err)
			}
			<-fa.thread().prompts
			if got, _ := st.Thread(ctx, a.ID); got.Title != tc.want {
				t.Fatalf("title = %q, want %q", got.Title, tc.want)
			}
		})
	}
}
