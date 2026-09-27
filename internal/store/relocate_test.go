package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
)

func TestRelocateProjectRewritesEveryDurablePath(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	oldRoot := filepath.Join(t.TempDir(), "before")
	newRoot := filepath.Join(t.TempDir(), "after")
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	if err := st.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	meta := ThreadMeta{ID: "s1", Cwd: filepath.Join(oldRoot, ".worktrees", "one"), Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateWorkspace(ctx, meta.ID, meta.Cwd, "one", "idle", json.RawMessage(`{"cwd":"`+meta.Cwd+`","resources":{"root":"`+oldRoot+`"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, meta.ID, proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: meta.Cwd, Harness: "claude"})); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSnapshot(ctx, meta.ID, 1, map[string]any{"cwd": meta.Cwd, "workspace": map[string]any{"projectRoot": oldRoot}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ClaimCommand(ctx, "c1", meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteCommand(ctx, "c1", map[string]string{"cwd": meta.Cwd}); err != nil {
		t.Fatal(err)
	}

	outside := ThreadMeta{ID: "outside", Cwd: oldRoot + "-archive", Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateThread(ctx, outside); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, outside.ID, proto.Emit(proto.WorkspaceRequested, proto.WorkspaceRequestedPayload{ProjectID: p.ID, ProjectRoot: oldRoot})); err != nil {
		t.Fatal(err)
	}
	legacy := ThreadMeta{ID: "legacy", Cwd: filepath.Join(oldRoot, "legacy"), Harness: "codex", Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateThread(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, legacy.ID, proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: legacy.Cwd, Harness: "codex"})); err != nil {
		t.Fatal(err)
	}

	stats, err := st.RelocateProject(ctx, oldRoot, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Threads != 2 || stats.Events != 3 || stats.Snapshots != 1 || stats.Commands != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	gotProject, err := st.Project(ctx, p.ID)
	if err != nil || gotProject.Root != newRoot {
		t.Fatalf("project = %+v, %v", gotProject, err)
	}
	got, err := st.Thread(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantCwd := filepath.Join(newRoot, ".worktrees", "one")
	if got.Cwd != wantCwd || !containsJSONPath(got.ProvisionResult, wantCwd) || !containsJSONPath(got.ProvisionResult, newRoot) {
		t.Fatalf("thread was not fully relocated: %+v provision=%s", got, got.ProvisionResult)
	}
	events, err := st.ReadEvents(ctx, meta.ID, 0, 10)
	if err != nil || len(events) != 1 || !containsJSONPath(events[0].Payload, wantCwd) {
		t.Fatalf("events = %+v, %v", events, err)
	}
	_, snapshot, err := st.LatestSnapshot(ctx, meta.ID)
	if err != nil || !containsJSONPath(snapshot, wantCwd) || !containsJSONPath(snapshot, newRoot) {
		t.Fatalf("snapshot = %s, %v", snapshot, err)
	}
	result, done, err := st.ClaimCommand(ctx, "c1", meta.ID)
	if err != nil || !done || !containsJSONPath(result, wantCwd) {
		t.Fatalf("command = %s, done=%v err=%v", result, done, err)
	}
	unchanged, _ := st.Thread(ctx, outside.ID)
	if unchanged.Cwd != outside.Cwd {
		t.Fatalf("prefix lookalike changed to %q", unchanged.Cwd)
	}
	outsideEvents, _ := st.ReadEvents(ctx, outside.ID, 0, 10)
	if len(outsideEvents) != 1 || !containsJSONPath(outsideEvents[0].Payload, newRoot) {
		t.Fatalf("outside-cwd thread retained stale project state: %+v", outsideEvents)
	}
	legacyThread, _ := st.Thread(ctx, legacy.ID)
	if legacyThread.Cwd != filepath.Join(newRoot, "legacy") {
		t.Fatalf("legacy thread cwd = %q", legacyThread.Cwd)
	}
}

func TestRelocateProjectRewritesCanonicalAlias(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldRoot, newRoot := "/alias/project-old", "/alias/project-new"
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	_ = st.PutProject(ctx, p)
	meta := ThreadMeta{ID: "s1", Cwd: "/canonical/project-old/worktree", Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	_ = st.CreateThread(ctx, meta)
	stats, err := st.RelocateProject(ctx, oldRoot, newRoot, RelocationPath{Old: "/canonical/project-old", New: "/canonical/project-new"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := st.Thread(ctx, meta.ID)
	if stats.Threads != 1 || got.Cwd != "/canonical/project-new/worktree" {
		t.Fatalf("stats=%+v cwd=%q", stats, got.Cwd)
	}
}

func TestRelocateProjectRollsBackMalformedJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldRoot, newRoot := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "new")
	p := project.Project{ID: "p1", Root: oldRoot, Config: project.DefaultConfig(oldRoot), CreatedAt: 1, UpdatedAt: 1}
	_ = st.PutProject(ctx, p)
	meta := ThreadMeta{ID: "s1", Cwd: oldRoot, Harness: "claude", ProjectID: p.ID, Phase: "idle", CreatedAt: 1, UpdatedAt: 1}
	_ = st.CreateThread(ctx, meta)
	if _, err := st.db.Exec(`INSERT INTO snapshots(thread_id,seq,state) VALUES(?,?,?)`, meta.ID, 1, []byte(`not-json`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RelocateProject(ctx, oldRoot, newRoot); err == nil {
		t.Fatal("malformed JSON did not abort relocation")
	}
	got, _ := st.Project(ctx, p.ID)
	if got.Root != oldRoot {
		t.Fatalf("project root committed despite rollback: %s", got.Root)
	}
}

func containsJSONPath(blob json.RawMessage, path string) bool {
	var value any
	if json.Unmarshal(blob, &value) != nil {
		return false
	}
	return containsValue(value, path)
}

func containsValue(value any, path string) bool {
	switch value := value.(type) {
	case string:
		return value == path
	case []any:
		for _, item := range value {
			if containsValue(item, path) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if containsValue(item, path) {
				return true
			}
		}
	}
	return false
}

func TestRelocateProjectMovesAHomeInsideTheRootAndNoOther(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for id, home := range map[string]string{"plain": "/old/plain", "git": "/home/me/Omniplex/git", "unset": ""} {
		root := "/old/" + id
		if err := st.PutProject(ctx, project.Project{ID: id, Root: root, Config: project.DefaultConfig(root), CreatedAt: 1, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if home != "" {
			if err := st.SetProjectHome(ctx, id, home); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.RelocateProject(ctx, root, "/new/"+id); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"plain": "/new/plain", "git": "/home/me/Omniplex/git", "unset": ""} {
		if p, _ := st.Project(ctx, id); p.Home != want {
			t.Errorf("%s: home %q, want %q", id, p.Home, want)
		}
	}
}
