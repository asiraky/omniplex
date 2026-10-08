package bundled

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractWritesThePluginAndOnlyThePlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(t.TempDir(), "plugin")

	got, err := Extract(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("Extract = %q, want %q", got, dir)
	}
	// Laid out as a Claude plugin: the manifest names it, and its skill is
	// where codex and pi look when handed <dir>/skills.
	var manifest struct {
		Name string `json:"name"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Name == "" {
		t.Fatalf("manifest %s: %v", raw, err)
	}
	skill := filepath.Join(dir, "skills", "omniplex", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatal(err)
	}
	// Nothing lands in the user's home: no personal skills library, no
	// harness config dir.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("extraction wrote into the home folder: %v", entries)
	}
}

func TestExtractRewritesWhatDiffersAndDropsStrays(t *testing.T) {
	dir := t.TempDir()
	if _, err := Extract(dir); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dir, "skills", "omniplex", "SKILL.md")
	manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
	want, _ := os.ReadFile(skill)

	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(manifest, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "skills", "gone", "SKILL.md")
	os.MkdirAll(filepath.Dir(stray), 0o755)
	os.WriteFile(stray, []byte("---\nname: gone\n---\n"), 0o644)
	// Beside the plugin's own folders: not the plugin's to remove.
	other := filepath.Join(dir, "notes.txt")
	os.WriteFile(other, []byte("x"), 0o644)

	if _, err := Extract(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(skill); string(got) != string(want) {
		t.Fatalf("SKILL.md not rewritten: %q", got)
	}
	if info, err := os.Stat(manifest); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("an unchanged file was rewritten: %v %v", info.ModTime(), err)
	}
	if _, err := os.Stat(filepath.Dir(stray)); !os.IsNotExist(err) {
		t.Fatalf("a skill the binary does not carry survived: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("a file outside the plugin's folders was removed: %v", err)
	}
}
