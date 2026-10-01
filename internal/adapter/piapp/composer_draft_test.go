package piapp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeSkill(t *testing.T, dir, frontmatter string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\n"+frontmatter+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// pi tells the model about a skill unless the skill says not to, and only a
// skill the model knows can be acted on from the middle of a prompt.
func TestPiSkillIsInlineOnlyWhenTheModelMayInvokeIt(t *testing.T) {
	root := t.TempDir()
	open := writeSkill(t, filepath.Join(root, "open"), "name: open\ndescription: Anyone\n")
	closed := writeSkill(t, filepath.Join(root, "closed"), "name: closed\ndescription: People\ndisable-model-invocation: true\n")

	items := piComposerItems([]piCommand{
		{Name: "skill:open", Source: "skill", SourceInfo: &piSourceInfo{Path: open, Scope: "user"}},
		{Name: "skill:closed", Source: "skill", SourceInfo: &piSourceInfo{Path: closed, Scope: "user"}},
		{Name: "skill:unplaced", Source: "skill", SourceInfo: &piSourceInfo{Scope: "user"}},
		{Name: "fix-tests", Source: "prompt", SourceInfo: &piSourceInfo{Path: open, Scope: "user"}},
	})
	got := map[string]bool{}
	for _, item := range items {
		got[item.InsertText] = item.Inline
	}
	want := map[string]bool{
		"/skill:open":     true,
		"/skill:closed":   false,
		"/skill:unplaced": false,
		// A template is expanded by pi, never read by the model.
		"/fix-tests": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inline = %v\nwant %v", got, want)
	}
}

func TestDraftComposerItemsListsPiSkillsFromDisk(t *testing.T) {
	root := t.TempDir()
	home, agentDir, cwd := filepath.Join(root, "home"), filepath.Join(root, "pi-agent"), filepath.Join(root, "repo")
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(cwd, ".pi", "skills", "ship"), "name: ship\ndescription: Repo copy\n")
	writeSkill(t, filepath.Join(agentDir, "skills", "ship"), "name: ship\ndescription: Personal copy\n")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "quiet"), "name: quiet\ndescription: People only\ndisable-model-invocation: true\n")
	// Claude's root, which pi never reads.
	writeSkill(t, filepath.Join(cwd, ".claude", "skills", "claude-only"), "name: claude-only\ndescription: Not pi's\n")

	items, err := New("").DraftComposerItems(context.Background(), map[string]string{"PI_CODING_AGENT_DIR": agentDir}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		insert, description, origin string
		inline                      bool
	}
	var got []row
	for _, item := range items {
		got = append(got, row{item.InsertText, item.Description, item.Origin, item.Inline})
	}
	want := []row{
		{"/skill:quiet", "People only", "personal", false},
		{"/skill:ship", "Repo copy", "project", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v\nwant %+v", got, want)
	}
}
