package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/skills"
)

// fakeSkillsCLI is an npx that leaves one skill in its working dir, as
// `skills add` would. notes is what the repo holds at the moment.
func fakeSkillsCLI(t *testing.T, notes *string) skills.Fetcher {
	t.Helper()
	// Staging dirs go under the temp folder: a test gets its own.
	t.Setenv("TMPDIR", t.TempDir())
	return skills.Fetcher{
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(_ context.Context, c skills.Command) ([]byte, error) {
			if c.Name != "npx" {
				return nil, errors.New(c.Name + " was not expected to run")
			}
			dir := filepath.Join(c.Dir, ".agents", "skills", "show-me")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			files := map[string]string{
				"SKILL.md": "---\nname: show-me\ndescription: Shows things\n---\nbody\n",
				"notes.md": *notes,
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					return nil, err
				}
			}
			return nil, nil
		},
	}
}

func skillsGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// call runs a command that must succeed and decodes its result the way a
// client would.
func call[T any](t *testing.T, c *conn, command string, args map[string]any) T {
	t.Helper()
	result, err := run(t, c, command, args)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return out
}

type fileResult struct {
	Content string `json:"content"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Binary  bool   `json:"binary"`
}

type skillsResult struct {
	Skills []skills.Skill `json:"skills"`
}

func TestInstallAndUpdateASkillOverTheWire(t *testing.T) {
	c, home := commandConn(t)
	notes := "v1"
	c.srv.skillFetch = fakeSkillsCLI(t, &notes)
	installed := filepath.Join(home, ".agents", "skills", "show-me")

	staged := call[skills.Staged](t, c, "stage_skills", map[string]any{"source": "npx skills add owner/repo --skill show-me"})
	if len(staged.Skills) != 1 || !staged.Skills[0].Picked || staged.Repo != "owner/repo" {
		t.Fatalf("staged = %+v", staged)
	}
	file := call[fileResult](t, c, "read_staged_file", map[string]any{"id": staged.ID, "skill": "show-me", "path": "notes.md"})
	if file.Content != "v1" || file.Binary {
		t.Errorf("staged file = %+v", file)
	}
	if _, err := os.Stat(installed); err == nil {
		t.Fatal("looking at a fetch installed it")
	}

	placed := call[skillsResult](t, c, "install_staged", map[string]any{
		"id": staged.ID, "skills": []string{"show-me"},
	})
	if len(placed.Skills) != 1 || placed.Skills[0].Dir != installed {
		t.Fatalf("placed = %+v", placed)
	}
	// Every agent sees it: Codex and pi read the library, Claude through a link.
	if real, err := filepath.EvalSymlinks(filepath.Join(home, ".claude", "skills", "show-me")); err != nil || real != installed {
		t.Errorf("Claude's skills dir has %q (%v)", real, err)
	}
	if _, err := run(t, c, "read_staged_file", map[string]any{"id": staged.ID, "skill": "show-me", "path": "notes.md"}); err == nil {
		t.Error("the fetch outlived the install")
	}

	// Fetched again for nothing: discarded, and the installed copy stays.
	again := call[skills.Staged](t, c, "stage_skills", map[string]any{"source": "owner/repo"})
	if !again.Skills[0].Installed {
		t.Errorf("a second fetch does not say the skill is installed: %+v", again.Skills[0])
	}
	if _, err := run(t, c, "discard_staged", map[string]any{"id": again.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, c, "discard_staged", map[string]any{"id": "../" + filepath.Base(home)}); err == nil {
		t.Error("discarded a folder that is not a fetch")
	}

	notes = "v2"
	update := call[skills.UpdateStage](t, c, "stage_update", map[string]any{"dir": installed})
	if len(update.Skills) != 1 || !update.Skills[0].Changed {
		t.Fatalf("update = %+v", update)
	}
	diff := call[fileResult](t, c, "read_update_file", map[string]any{"id": update.ID, "dir": installed, "path": "notes.md"})
	if diff.Old != "v1" || diff.New != "v2" {
		t.Errorf("diff = %+v", diff)
	}
	updated := call[skillsResult](t, c, "apply_update", map[string]any{"id": update.ID, "dirs": []string{installed}})
	if len(updated.Skills) != 1 || updated.Skills[0].Source == nil || !updated.Skills[0].Source.Managed {
		t.Errorf("updated = %+v", updated)
	}
	if data, _ := os.ReadFile(filepath.Join(installed, "notes.md")); string(data) != "v2" {
		t.Errorf("notes.md = %q after the update", data)
	}
}

func TestCommitTheSkillsLibraryOverTheWire(t *testing.T) {
	c, home := commandConn(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(home))
	type gitResult struct {
		Commit string            `json:"commit"`
		Git    *skills.GitStatus `json:"git"`
	}

	library := filepath.Join(home, ".agents", "skills")
	call[skills.Skill](t, c, "create_skill", map[string]any{"name": "mine", "description": "Written here"})
	// A library that is no repository has nothing to show, which is not an
	// error: the screen just leaves the commit bar out.
	if got := call[gitResult](t, c, "skills_git_status", map[string]any{}); got.Git != nil {
		t.Fatalf("status outside a repository = %+v", got.Git)
	}
	if _, err := run(t, c, "commit_skills", map[string]any{"names": []string{"mine"}, "message": "Add mine"}); err == nil {
		t.Error("committed outside a repository")
	}

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := skillsGit(library, args...); err != nil {
			t.Skipf("git %v: %v", args, err)
		}
	}
	call[skills.Skill](t, c, "create_skill", map[string]any{"name": "other", "description": "Also here"})

	status := call[gitResult](t, c, "skills_git_status", map[string]any{})
	if status.Git == nil || len(status.Git.Changes) != 2 {
		t.Fatalf("status = %+v", status.Git)
	}
	done := call[gitResult](t, c, "commit_skills", map[string]any{"names": []string{"mine"}, "message": "Add mine"})
	if done.Commit == "" || len(done.Git.Changes) != 1 || done.Git.Changes[0].Name != "other" {
		t.Errorf("after the commit = %q, %+v", done.Commit, done.Git)
	}
	files, err := skillsGit(library, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil || files != "mine/SKILL.md" {
		t.Errorf("HEAD holds %q (%v)", files, err)
	}
}
