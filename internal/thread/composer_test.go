package thread

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
)

// draftAdapter lists a draft catalogue and records where it was asked about.
type draftAdapter struct {
	fakeAdapter
	cwd   string
	items []adapter.ComposerItem
}

func (a *draftAdapter) DraftComposerItems(_ context.Context, _ map[string]string, cwd string) ([]adapter.ComposerItem, error) {
	a.cwd = cwd
	return a.items, nil
}

func TestDraftComposerItemsAsksAboutWhereTheThreadWouldStart(t *testing.T) {
	first, second, home := t.TempDir(), t.TempDir(), t.TempDir()
	st, p := testProject(t, first)
	ad := &draftAdapter{items: []adapter.ComposerItem{{ID: "skill:ship", Name: "ship"}}}
	mgr := NewManager(st, func(string, ...any) {}, ad)
	defer mgr.Shutdown()
	ctx := context.Background()

	items, err := mgr.DraftComposerItems(ctx, "fake", "", p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "ship" {
		t.Fatalf("items = %+v, want the adapter's", items)
	}
	if ad.cwd != first {
		t.Fatalf("one folder, none chosen: asked about %q, want the only folder %q", ad.cwd, first)
	}

	if err := st.SetProjectHome(ctx, p.ID, home); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFolder(ctx, p.ID, project.NewFolder("f-two", second)); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.DraftComposerItems(ctx, "fake", "", p.ID, "f-two"); err != nil {
		t.Fatal(err)
	}
	if ad.cwd != second {
		t.Fatalf("chosen folder: asked about %q, want %q", ad.cwd, second)
	}
	if _, err := mgr.DraftComposerItems(ctx, "fake", "", p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if ad.cwd != home {
		t.Fatalf("whole project: asked about %q, want the project home %q", ad.cwd, home)
	}

	if _, err := mgr.DraftComposerItems(ctx, "fake", "", p.ID, "f-gone"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown folder gave %v, want ErrNotFound", err)
	}
	if _, err := mgr.DraftComposerItems(ctx, "nope", "", p.ID, ""); err == nil {
		t.Fatal("unknown harness was answered")
	}
}

// Most adapters never learn to answer before a session exists. That is an
// empty menu, not an error the composer has to explain.
func TestDraftComposerItemsIsEmptyForAnAdapterThatCannotSay(t *testing.T) {
	st, p := testProject(t, filepath.Join(t.TempDir()))
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	items, err := mgr.DraftComposerItems(context.Background(), "fake", "", p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("items = %#v, want an empty list that encodes as []", items)
	}
}
