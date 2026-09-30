package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

func TestDraftComposerItemsListsCodexSkillsFromDisk(t *testing.T) {
	root := t.TempDir()
	home, codexHome, cwd := filepath.Join(root, "home"), filepath.Join(root, "codex"), filepath.Join(root, "repo")
	t.Setenv("HOME", home)
	for path, body := range map[string]string{
		filepath.Join(cwd, ".agents", "skills", "ship", "SKILL.md"):                                             "---\nname: ship\ndescription: Repo copy\n---\n",
		filepath.Join(home, ".agents", "skills", "ship", "SKILL.md"):                                            "---\nname: ship\ndescription: Personal copy\n---\n",
		filepath.Join(codexHome, "skills", "mine", "SKILL.md"):                                                  "---\nname: mine\ndescription: Personal\n---\n",
		filepath.Join(codexHome, "skills", ".system", "sys", "SKILL.md"):                                        "---\nname: sys\ndescription: Bundled\n---\n",
		filepath.Join(codexHome, "plugins", "cache", "market", "pages", "1.0.0", "skills", "write", "SKILL.md"): "---\nname: write\ndescription: From a plugin\n---\n",
		// Claude's root, which Codex never reads.
		filepath.Join(cwd, ".claude", "skills", "claude-only", "SKILL.md"): "---\nname: claude-only\ndescription: Not Codex's\n---\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	items, err := New("").DraftComposerItems(context.Background(), map[string]string{"CODEX_HOME": codexHome}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ insert, description, origin string }
	var got []row
	for _, item := range items {
		// No slash command: each of those acts on a thread, and a draft has none.
		if item.Trigger != "$" || item.Behavior != adapter.ComposerPrompt || !item.Inline {
			t.Errorf("%s = trigger %q behavior %q inline %v, want an inline $ prompt", item.Name, item.Trigger, item.Behavior, item.Inline)
		}
		got = append(got, row{item.InsertText, item.Description, item.Origin})
	}
	want := []row{
		{"$ship", "Repo copy", "repo"},
		{"$mine", "Personal", "personal"},
		{"$pages:write", "From a plugin", "plugin"},
		{"$sys", "Bundled", "system"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v\nwant %+v", got, want)
	}
}
