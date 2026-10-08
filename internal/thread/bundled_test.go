package thread

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/asiraky/omniplex/internal/bundled"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
)

// Every thread, in a project or not, loads omniplex's bundled plugin, and
// every skills listing shows its skill read-only.
func TestBundledPluginReachesEveryThreadAndListing(t *testing.T) {
	mgr, _ := projectsIn(t)
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	plugin, err := bundled.Extract(filepath.Join(t.TempDir(), "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	BundledPlugin = plugin
	t.Cleanup(func() { BundledPlugin = "" })

	ctx := context.Background()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Path: t.TempDir(), Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	now := proto.NowMillis()
	loose := t.TempDir()
	for _, meta := range []store.ThreadMeta{
		{ID: "in", Cwd: p.Home, Harness: "fake", ProjectID: p.ID, Phase: "idle", CreatedAt: now, UpdatedAt: now},
		{ID: "out", Cwd: loose, Harness: "fake", Phase: "idle", CreatedAt: now, UpdatedAt: now},
	} {
		if err := mgr.store.CreateThread(ctx, meta); err != nil {
			t.Fatal(err)
		}
		if got := harnessExtras(ctx, mgr.store, nil, meta, meta.Cwd, t.Logf).plugins; !slices.Equal(got, []string{plugin}) {
			t.Fatalf("thread %s plugins = %v", meta.ID, got)
		}
	}

	for _, at := range []struct{ thread, project string }{{"in", ""}, {"out", ""}, {"", p.ID}, {"", ""}} {
		roots, _, err := mgr.SkillRoots(ctx, at.thread, at.project)
		if err != nil {
			t.Fatal(err)
		}
		list, err := skills.Discover(roots)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(list, func(s skills.Skill) bool { return s.Scope == skills.ScopeOmniplex })
		if i < 0 || list[i].Name != "omniplex" || list[i].Editable {
			t.Fatalf("listing at %+v: %+v", at, list)
		}
	}
}
