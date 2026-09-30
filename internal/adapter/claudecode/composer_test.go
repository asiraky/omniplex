package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

func writeSkills(t *testing.T, files map[string]string) {
	t.Helper()
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudeDiskSkills(t *testing.T) {
	root := t.TempDir()
	config, cwd := filepath.Join(root, "config"), filepath.Join(root, "repo")
	userPlugin, otherPlugin := filepath.Join(root, "plugins", "user"), filepath.Join(root, "plugins", "elsewhere")
	writeSkills(t, map[string]string{
		filepath.Join(config, "skills", "mine", "SKILL.md"):        "---\nname: mine\ndescription: Personal\n---\n",
		filepath.Join(config, "skills", "shadowed", "SKILL.md"):    "---\nname: shadowed\ndescription: Personal copy\n---\n",
		filepath.Join(config, "skills", "private", "SKILL.md"):     "---\nname: private\ndescription: Hidden\ndisable-model-invocation: true\n---\n",
		filepath.Join(cwd, ".claude", "skills", "dir", "SKILL.md"): "---\nname: shadowed\ndescription: Project copy\n---\n",
		// Codex and pi read this root; Claude does not.
		filepath.Join(cwd, ".agents", "skills", "codex-only", "SKILL.md"): "---\nname: codex-only\ndescription: Not Claude's\n---\n",
		filepath.Join(userPlugin, "skills", "review", "SKILL.md"):         "---\nname: review\ndescription: From a plugin\n---\n",
		filepath.Join(otherPlugin, "skills", "away", "SKILL.md"):          "---\nname: away\ndescription: Another project's\n---\n",
	})
	manifest, err := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{
		"tools@market": []map[string]string{{"scope": "user", "installPath": userPlugin}},
		"far@market":   []map[string]string{{"scope": "project", "projectPath": filepath.Join(root, "other"), "installPath": otherPlugin}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeSkills(t, map[string]string{filepath.Join(config, "plugins", "installed_plugins.json"): string(manifest)})

	got := claudeDiskSkills(config, cwd)
	want := map[string]claudeSkill{
		"mine":         {name: "mine", description: "Personal", origin: "personal"},
		"private":      {name: "private", description: "Hidden", origin: "personal", userOnly: true},
		"shadowed":     {name: "shadowed", description: "Project copy", origin: "project"},
		"tools:review": {name: "tools:review", description: "From a plugin", origin: "plugin"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disk skills = %+v\nwant %+v", got, want)
	}
}

func TestClaudeComposerItemsTakesKindOriginAndInlineFromDisk(t *testing.T) {
	disk := map[string]claudeSkill{
		"open":   {name: "open", origin: "project"},
		"closed": {name: "closed", origin: "personal", userOnly: true},
	}
	items := claudeComposerItems([]claudeCommand{
		{Name: "open", Description: "Model may call it"},
		{Name: " closed ", Description: "Only a person may"},
		{Name: "compact", Description: "Built in", ArgumentHint: " [instructions] "},
		{Name: "bundled", Description: "Shipped with Claude (user)"},
		{Name: "OPEN", Description: "A second listing of the same command"},
		{Name: ""},
	}, disk)

	type row struct {
		kind, origin string
		inline       bool
	}
	got := map[string]row{}
	for _, item := range items {
		if _, dup := got[item.InsertText]; dup {
			t.Fatalf("%s listed twice", item.InsertText)
		}
		got[item.InsertText] = row{item.Kind, item.Origin, item.Inline}
	}
	want := map[string]row{
		"/open":    {"skill", "project", true},
		"/closed":  {"skill", "personal", false},
		"/compact": {"command", "built-in", false},
		// Known to be a skill only from its label: whether the model may call
		// it is not known, so it is not offered mid-prompt.
		"/bundled": {"skill", "personal", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v\nwant %+v", got, want)
	}
}

func TestDraftComposerItemsAddsWhatALiveSessionReported(t *testing.T) {
	root := t.TempDir()
	config, cwd := filepath.Join(root, "config"), filepath.Join(root, "repo")
	writeSkills(t, map[string]string{
		filepath.Join(config, "skills", "mine", "SKILL.md"):          "---\nname: mine\ndescription: Personal\n---\n",
		filepath.Join(cwd, ".claude", "skills", "local", "SKILL.md"): "---\nname: local\ndescription: Here\n---\n",
		filepath.Join(config, "skills", "desktop", "SKILL.md"):       "---\nname: desktop\ndescription: For another Claude\n---\n",
	})
	env := map[string]string{"CLAUDE_CONFIG_DIR": config}
	a := New("")
	names := func(cwd string, env map[string]string) []string {
		t.Helper()
		items, err := a.DraftComposerItems(context.Background(), env, cwd)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item.Behavior != adapter.ComposerPrompt {
				t.Errorf("%s behaves as %q; a draft has no thread to act on", item.Name, item.Behavior)
			}
			out = append(out, item.InsertText)
		}
		return out
	}

	if got, want := names(cwd, env), []string{"/desktop", "/local", "/mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before any session = %v, want the disk's %v", got, want)
	}

	// A session elsewhere under the same account lists its catalogue, which
	// leaves out one of the skills on disk.
	elsewhere := filepath.Join(root, "elsewhere")
	writeSkills(t, map[string]string{
		filepath.Join(elsewhere, ".claude", "skills", "theirs", "SKILL.md"): "---\nname: theirs\ndescription: There\n---\n",
	})
	disk := claudeDiskSkills(config, elsewhere)
	a.commands.remember(config, claudeComposerItems([]claudeCommand{
		{Name: "compact", Description: "Built in"},
		{Name: "mine", Description: "Personal"},
		{Name: "theirs", Description: "There"},
		{Name: "their-command", Description: "A command file in that repo (project)"},
	}, disk), disk)

	if got, want := names(cwd, env), []string{"/compact", "/local", "/mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after a session = %v, want %v: built-ins carry over, that folder's entries do not, and what Claude left out stays out", got, want)
	}
	other := map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(root, "other-account")}
	if got := names(cwd, other); !reflect.DeepEqual(got, []string{"/local"}) {
		t.Fatalf("another account = %v, want only /local", got)
	}
}

func TestClaudeConfigDirUsesTheProcessEnvironment(t *testing.T) {
	ambient := filepath.Join(t.TempDir(), "ambient-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)
	if got := claudeConfigDir(t.TempDir(), nil); got != ambient {
		t.Fatalf("config dir = %q, want inherited %q", got, ambient)
	}

	override := filepath.Join(t.TempDir(), "instance-claude")
	if got := claudeConfigDir(t.TempDir(), map[string]string{"CLAUDE_CONFIG_DIR": override}); got != override {
		t.Fatalf("config dir = %q, want instance override %q", got, override)
	}
}

func TestClaudeDescriptionOrigin(t *testing.T) {
	for _, test := range []struct{ description, want string }{
		{"Review a diff. (user)", "personal"},
		{"Run the repo workflow. (project)", "project"},
		{"Built in command", ""},
	} {
		got, _ := claudeDescriptionOrigin(test.description)
		if got != test.want {
			t.Errorf("origin(%q) = %q, want %q", test.description, got, test.want)
		}
	}
}
