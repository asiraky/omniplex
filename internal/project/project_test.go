package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportPrefersTheRepoFileAndMovesLegacyAgentDefaults(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".omniplex"), 0o755)
	os.WriteFile(filepath.Join(root, legacyFile), []byte(`{"version":1,"name":"On disk",
		"defaults":{"harness":"claude","model":"opus","mode":"bypassPermissions","baseBranch":"develop"},
		"workspace":{"suggestedRoot":"trees","provision":"scripts/up.sh"}}`), 0o644)

	name, d, f := Import(root, []byte(`{"name":"Cached"}`))
	if name != "On disk" {
		t.Fatalf("name = %q, want the repo file's", name)
	}
	if got := d.Harnesses["claude"]; got.Model != "opus" || got.Mode != "bypassPermissions" {
		t.Fatalf("legacy defaults not moved into the harness profile: %+v", d.Harnesses)
	}
	if f.Path != root || f.BaseBranch != "develop" || f.CopiesDir != "trees" || f.Provision != "scripts/up.sh" || f.ProvisionTimeoutSeconds != 1800 {
		t.Fatalf("folder = %+v", f)
	}
}

func TestImportFallsBackToTheCachedCopyThenTheFolderName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "recipes")
	name, _, f := Import(root, []byte(`{"name":"Recipe site","defaults":{"harnesses":{"codex":{"model":"x"}}}}`))
	if name != "Recipe site" || f.CopiesDir != ".worktrees" {
		t.Fatalf("cached import: %q %+v", name, f)
	}
	if name, _, _ = Import(root, nil); name != "recipes" {
		t.Fatalf("no settings at all: name %q", name)
	}
}

func TestValidateFolderRefusesHooksOutsideIt(t *testing.T) {
	for _, hook := range []string{"../up.sh", "/bin/sh"} {
		if err := ValidateFolder(Folder{Path: "/p", Provision: hook}); err == nil {
			t.Errorf("accepted provision hook %q", hook)
		}
	}
	if err := ValidateFolder(Folder{Path: "/p", Deprovision: "scripts/down.sh", CopiesDir: "../trees"}); err != nil {
		t.Fatalf("refused a hook inside the folder: %v", err)
	}
}
