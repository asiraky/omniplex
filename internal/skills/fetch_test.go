package skills

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// staging gives the test a temp folder of its own for staging dirs, so a
// sweep never sees the machine's.
func staging(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", dir)
	return dir
}

func stagingDirs(t *testing.T, tmp string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(tmp, stagePrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// tools stands in for npx and git: each is a function that writes what the
// real one would have fetched. Nothing here reaches the network.
type tools struct {
	missing []string
	npx     func(c Command) ([]byte, error)
	git     func(c Command) error
	calls   []Command
}

func (x *tools) fetcher() Fetcher {
	return Fetcher{
		LookPath: func(name string) (string, error) {
			if slices.Contains(x.missing, name) {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/" + name, nil
		},
		Run: func(ctx context.Context, c Command) ([]byte, error) {
			x.calls = append(x.calls, c)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			switch {
			case c.Name == "npx" && x.npx != nil:
				return x.npx(c)
			case c.Name == "git" && x.git != nil:
				return nil, x.git(c)
			}
			return nil, errors.New(c.Name + " was not expected to run")
		},
	}
}

func (x *tools) ran(name string) []Command {
	var out []Command
	for _, c := range x.calls {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func writeAll(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		write(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
	}
}

// npxAdds is an npx that leaves files in its working dir, as `skills add`
// does for a project: .agents/skills/<name>/ and skills-lock.json.
func npxAdds(t *testing.T, files map[string]string) func(Command) ([]byte, error) {
	return func(c Command) ([]byte, error) {
		writeAll(t, c.Dir, files)
		return []byte("[]"), nil
	}
}

// gitClones is a git whose clone produces the given checkout.
func gitClones(t *testing.T, files map[string]string) func(Command) error {
	return func(c Command) error {
		if c.Args[0] == "clone" {
			writeAll(t, c.Args[len(c.Args)-1], files)
		}
		return nil
	}
}

func stagedByName(t *testing.T, s Staged) map[string]StagedSkill {
	t.Helper()
	out := map[string]StagedSkill{}
	for _, sk := range s.Skills {
		if _, dup := out[sk.Name]; dup {
			t.Fatalf("staged %q twice", sk.Name)
		}
		out[sk.Name] = sk
	}
	return out
}

func stagedNames(s Staged) []string {
	names := []string{}
	for _, sk := range s.Skills {
		names = append(names, sk.Name)
	}
	return names
}

const manualMD = "---\nname: quiet\ndescription: Only when asked\ndisable-model-invocation: true\n---\nbody\n"

var npxRepo = map[string]string{
	".agents/skills/show-me/SKILL.md":       skillMD("show-me", "Shows things"),
	".agents/skills/show-me/scripts/run.sh": "#!/bin/sh\n",
	".agents/skills/quiet/SKILL.md":         manualMD,
	".agents/skills/not-a-skill/README.md":  "nothing here",
	"skills-lock.json": `{"version":1,"skills":{
		"show-me":{"source":"humanlayer/skills","ref":"main","sourceType":"github","skillPath":"skills/show-me/SKILL.md","computedHash":"h"},
		"quiet":{"source":"humanlayer/skills","ref":"main","sourceType":"github","skillPath":"extras/quiet/SKILL.md","computedHash":"h"}}}`,
}

var gitRepo = map[string]string{
	"README.md":                        "a repo",
	"skills/show-me/SKILL.md":          skillMD("show-me", "Shows things"),
	"skills/show-me/examples/SKILL.md": skillMD("inner", "An example inside a skill"),
	"extras/quiet/SKILL.md":            manualMD,
	".git/HEAD":                        "ref: refs/heads/main",
	".git/hooks/SKILL.md":              skillMD("hidden", "Inside .git"),
	"node_modules/dep/SKILL.md":        skillMD("dep", "Somebody's dependency"),
}

func TestStageWithNpx(t *testing.T) {
	staging(t)
	r := machine(t)
	r.ProjectHome = r.ProjectRoot
	write(t, filepath.Join(r.Library, "quiet", "SKILL.md"), manualMD)
	write(t, filepath.Join(r.ProjectRoot, ".agents", "skills", "show-me", "SKILL.md"), skillMD("show-me", "Already here"))
	x := &tools{npx: npxAdds(t, npxRepo)}

	got, err := x.fetcher().Stage(context.Background(), r, "npx skills add humanlayer/skills --skill show-me -g -a claude-code -y")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodNpx || got.Repo != "humanlayer/skills" || got.Note != "" {
		t.Errorf("staged = %+v", got)
	}
	// No ref was asked for, so the one the CLI resolved is reported.
	if got.Ref != "main" {
		t.Errorf("ref = %q, want the lock's", got.Ref)
	}
	skills := stagedByName(t, got)
	if !reflect.DeepEqual(stagedNames(got), []string{"quiet", "show-me"}) {
		t.Fatalf("staged %v", stagedNames(got))
	}
	show, quiet := skills["show-me"], skills["quiet"]
	if show.Path != "skills/show-me" || quiet.Path != "extras/quiet" {
		t.Errorf("paths = %q and %q, want the lock's folders", show.Path, quiet.Path)
	}
	if !show.Picked || quiet.Picked {
		t.Errorf("picked: show-me %v, quiet %v", show.Picked, quiet.Picked)
	}
	// Each destination's library is told apart: installing in one replaces
	// only what is there.
	if !reflect.DeepEqual(show.InstalledIn, []string{r.ProjectRoot}) || !reflect.DeepEqual(quiet.InstalledIn, []string{""}) {
		t.Errorf("clashes: show-me %+v, quiet %+v", show, quiet)
	}
	var files []string
	for _, f := range show.Files {
		files = append(files, f.Path)
	}
	if !reflect.DeepEqual(files, []string{"SKILL.md", "scripts/run.sh"}) {
		t.Errorf("files = %v", files)
	}
	if show.Description != "Shows things" || show.Problem != "" {
		t.Errorf("show-me = %+v", show)
	}

	if len(x.ran("git")) != 0 || len(x.ran("npx")) != 1 {
		t.Fatalf("ran %+v", x.calls)
	}
	npx := x.ran("npx")[0]
	dir, _ := stagePath(got.ID)
	if npx.Dir != dir {
		t.Errorf("npx ran in %q, want the staging dir %q", npx.Dir, dir)
	}
	if !slices.Contains(npx.Args, "skills@"+CLIVersion) || !slices.Contains(npx.Args, "humanlayer/skills") {
		t.Errorf("npx args = %v", npx.Args)
	}
}

func TestNpxIsHandedTheRefAsWritten(t *testing.T) {
	staging(t)
	r := machine(t)
	x := &tools{npx: npxAdds(t, npxRepo)}
	got, err := x.fetcher().Stage(context.Background(), r, "humanlayer/skills#v2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "v2" {
		t.Errorf("ref = %q", got.Ref)
	}
	if args := x.ran("npx")[0].Args; !slices.Contains(args, "humanlayer/skills#v2") {
		t.Errorf("npx args = %v", args)
	}
}

func TestStageFallsBackToGit(t *testing.T) {
	boom := errors.New("npx: npm ERR! network")
	tests := []struct {
		name    string
		missing []string
		npx     func(t *testing.T) func(Command) ([]byte, error)
		ranNpx  int
		note    string // a word the note must carry
	}{
		{"npx is not installed", []string{"npx"}, nil, 0, "not installed"},
		{"npx fails", nil, func(t *testing.T) func(Command) ([]byte, error) {
			return func(c Command) ([]byte, error) {
				// It got as far as writing one skill before it died.
				writeAll(t, c.Dir, map[string]string{".agents/skills/half/SKILL.md": skillMD("half", "Half fetched")})
				return nil, boom
			}
		}, 1, "network"},
		{"npx succeeds and writes nothing", nil, func(t *testing.T) func(Command) ([]byte, error) {
			return npxAdds(t, nil)
		}, 1, "no skills"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staging(t)
			r := machine(t)
			x := &tools{missing: tt.missing, git: gitClones(t, gitRepo)}
			if tt.npx != nil {
				x.npx = tt.npx(t)
			}
			got, err := x.fetcher().Stage(context.Background(), r, "humanlayer/skills#v2")
			if err != nil {
				t.Fatal(err)
			}
			if got.Method != MethodGit || got.Ref != "v2" {
				t.Errorf("staged = %+v", got)
			}
			if !strings.Contains(got.Note, tt.note) || !strings.Contains(got.Note, "git") {
				t.Errorf("note = %q, want it to say git was used because %q", got.Note, tt.note)
			}
			// Only what git fetched is offered: not npx's leavings, not .git,
			// not a dependency, not an example inside a skill.
			if !reflect.DeepEqual(stagedNames(got), []string{"quiet", "show-me"}) {
				t.Errorf("staged %v", stagedNames(got))
			}
			if p := stagedByName(t, got)["show-me"].Path; p != "skills/show-me" {
				t.Errorf("path = %q", p)
			}
			if len(x.ran("npx")) != tt.ranNpx {
				t.Errorf("npx ran %d times", len(x.ran("npx")))
			}
			clone := x.ran("git")[0].Args
			if !slices.Contains(clone, "https://github.com/humanlayer/skills.git") {
				t.Errorf("git args = %v", clone)
			}
			if i := slices.Index(clone, "--branch"); i < 0 || clone[i+1] != "v2" {
				t.Errorf("git args = %v, want the ref as the branch", clone)
			}
		})
	}
}

func TestStageFailsWhenEveryFetcherDoes(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	tests := map[string]*tools{
		"npx and git both fail": {
			npx: func(Command) ([]byte, error) { return nil, errors.New("npx broke") },
			git: func(Command) error { return errors.New("git broke") },
		},
		"neither is installed": {missing: []string{"npx", "git"}},
		"the repo has no skills": {
			missing: []string{"npx"},
			git:     gitClones(t, map[string]string{"README.md": "nothing to install"}),
		},
	}
	for name, x := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := x.fetcher().Stage(context.Background(), r, "owner/repo")
			if err == nil {
				t.Fatalf("staged %+v", got)
			}
			if name == "npx and git both fail" && (!strings.Contains(err.Error(), "npx broke") || !strings.Contains(err.Error(), "git broke")) {
				t.Errorf("error %q does not say what each did", err)
			}
			if left := stagingDirs(t, tmp); len(left) != 0 {
				t.Errorf("a failed fetch left %v behind", left)
			}
		})
	}
}

func TestACancelledFetchDoesNotFallBack(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	ctx, cancel := context.WithCancel(context.Background())
	x := &tools{git: gitClones(t, gitRepo)}
	x.npx = func(Command) ([]byte, error) {
		cancel() // the user gave up while npx was running
		return nil, ctx.Err()
	}
	_, err := x.fetcher().Stage(ctx, r, "owner/repo")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation", err)
	}
	if len(x.ran("git")) != 0 {
		t.Errorf("git ran after the cancel: %+v", x.ran("git"))
	}
	if left := stagingDirs(t, tmp); len(left) != 0 {
		t.Errorf("a cancelled fetch left %v behind", left)
	}
}

func TestStageATreeURL(t *testing.T) {
	const url = "https://github.com/humanlayer/skills/tree/main/extras"
	t.Run("npx: only the skills the lock places under the folder", func(t *testing.T) {
		staging(t)
		x := &tools{npx: npxAdds(t, npxRepo)}
		got, err := x.fetcher().Stage(context.Background(), machine(t), url)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stagedNames(got), []string{"quiet"}) || got.Repo != "humanlayer/skills" || got.Ref != "main" {
			t.Errorf("staged = %+v", got)
		}
	})
	t.Run("git: only the folder is searched", func(t *testing.T) {
		staging(t)
		x := &tools{missing: []string{"npx"}, git: gitClones(t, gitRepo)}
		got, err := x.fetcher().Stage(context.Background(), machine(t), url)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stagedNames(got), []string{"quiet"}) || stagedByName(t, got)["quiet"].Path != "extras/quiet" {
			t.Errorf("staged = %+v", got)
		}
	})
	t.Run("git: a folder the repo does not have", func(t *testing.T) {
		staging(t)
		x := &tools{missing: []string{"npx"}, git: gitClones(t, gitRepo)}
		if got, err := x.fetcher().Stage(context.Background(), machine(t), "https://github.com/humanlayer/skills/tree/main/nope"); err == nil {
			t.Errorf("staged %+v", got)
		}
	})
	t.Run("git: a folder that is a link out of the checkout", func(t *testing.T) {
		staging(t)
		r := machine(t)
		write(t, filepath.Join(r.Home, "private", "secret", "SKILL.md"), skillMD("secret", "Not in the repo"))
		x := &tools{missing: []string{"npx"}}
		x.git = func(c Command) error {
			repo := c.Args[len(c.Args)-1]
			writeAll(t, repo, gitRepo)
			link(t, filepath.Join(r.Home, "private"), filepath.Join(repo, "extras", "out"))
			return nil
		}
		if got, err := x.fetcher().Stage(context.Background(), r, "https://github.com/humanlayer/skills/tree/main/extras/out"); err == nil {
			t.Errorf("staged %+v from outside the checkout", got)
		}
	})
}

func TestGitFetchesACommitByItsHash(t *testing.T) {
	staging(t)
	const sha = "0123456789abcdef0123456789abcdef01234567"
	x := &tools{missing: []string{"npx"}}
	x.git = func(c Command) error {
		if c.Args[0] == "clone" {
			return errors.New("clone --branch does not take a commit")
		}
		if i := slices.Index(c.Args, "fetch"); i >= 0 {
			if !slices.Contains(c.Args, sha) {
				return errors.New("fetched something else")
			}
			writeAll(t, c.Args[slices.Index(c.Args, "-C")+1], gitRepo)
		}
		return nil
	}
	got, err := x.fetcher().Stage(context.Background(), machine(t), "humanlayer/skills#"+sha)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != sha || len(got.Skills) != 2 {
		t.Errorf("staged = %+v", got)
	}
}

func TestANoteSaysWhatNpxCouldNotFetch(t *testing.T) {
	staging(t)
	x := &tools{}
	x.npx = func(c Command) ([]byte, error) {
		writeAll(t, c.Dir, npxRepo)
		return []byte(`npm warn something
[{"name":"show-me","status":"installed"},{"name":"broken","status":"failed","error":"boom"},{"name":"old","status":"skipped"}]`), nil
	}
	got, err := x.fetcher().Stage(context.Background(), machine(t), "humanlayer/skills")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodNpx || !strings.Contains(got.Note, "broken") || strings.Contains(got.Note, "old") {
		t.Errorf("method %q, note %q", got.Method, got.Note)
	}
}

func TestStagedNames(t *testing.T) {
	staging(t)
	r := machine(t)
	src := filepath.Join(r.Home, "src")
	writeAll(t, src, map[string]string{
		// The frontmatter names the skill, whatever folder it came in.
		"Folder One/SKILL.md": skillMD("renamed", "Named by its frontmatter"),
		// No usable name in the frontmatter: the folder is the fallback.
		"plain/SKILL.md":   "---\ndescription: No name\n---\n",
		"shouted/SKILL.md": skillMD("Shouted Name", "A name no harness takes"),
		// Neither is usable.
		"Bad Folder/SKILL.md": "---\ndescription: No name at all\n---\n",
		// Two skills of one name: one is offered.
		"a/twin/SKILL.md": skillMD("twin", "The first"),
		"b/twin/SKILL.md": skillMD("twin", "The second"),
	})
	got, err := Fetcher{}.Stage(context.Background(), r, src)
	if err != nil {
		t.Fatal(err)
	}
	skills := stagedByName(t, got)
	if !reflect.DeepEqual(stagedNames(got), []string{"Bad Folder", "plain", "renamed", "shouted", "twin"}) {
		t.Fatalf("staged %v", stagedNames(got))
	}
	if s := skills["renamed"]; s.Problem != "" || s.Path != "Folder One" {
		t.Errorf("renamed = %+v", s)
	}
	for _, name := range []string{"plain", "shouted", "Bad Folder"} {
		if skills[name].Problem == "" {
			t.Errorf("%s has no problem reported", name)
		}
	}
	if skills["twin"].Description != "The first" {
		t.Errorf("twin = %+v", skills["twin"])
	}
	// A name that cannot be a skill's folder is shown and cannot be installed.
	if _, err := InstallStaged(r, got.ID, []string{"Bad Folder"}, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("installing a badly named skill: %v", err)
	}
	if exists(r.Library) && len(readDirs(r.Library)) != 0 {
		t.Errorf("library holds %v", readDirs(r.Library))
	}
}

func TestStageALocalFolder(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	src := filepath.Join(r.Home, "code", "my skills")
	writeAll(t, src, map[string]string{
		"show-me/SKILL.md":       skillMD("show-me", "Shows things"),
		"show-me/notes.md":       "v1",
		"show-me/.git/HEAD":      "ref",
		"deep/er/quiet/SKILL.md": manualMD,
	})
	write(t, filepath.Join(r.Home, "secret.txt"), "private")
	writeAll(t, src, map[string]string{"leaky/SKILL.md": skillMD("leaky", "Links out")})
	link(t, filepath.Join(r.Home, "secret.txt"), filepath.Join(src, "leaky", "secret.txt"))

	got, err := Fetcher{}.Stage(context.Background(), r, "~/code/my skills")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodLocal || got.Repo != src || got.Note != "" {
		t.Errorf("staged = %+v", got)
	}
	skills := stagedByName(t, got)
	if !reflect.DeepEqual(stagedNames(got), []string{"leaky", "quiet", "show-me"}) {
		t.Fatalf("staged %v", stagedNames(got))
	}
	if skills["quiet"].Path != "deep/er/quiet" {
		t.Errorf("quiet = %+v", skills["quiet"])
	}
	// A skill that links out of itself is shown with the reason and cannot
	// be read or installed.
	if skills["leaky"].Problem == "" {
		t.Errorf("leaky = %+v", skills["leaky"])
	}
	if content, _, err := ReadStagedFile(got.ID, "leaky", "secret.txt"); err == nil {
		t.Errorf("read %q through the link", content)
	}
	if _, err := InstallStaged(r, got.ID, []string{"leaky"}, ""); err == nil {
		t.Error("installed a skill that links out of itself")
	}

	// The preview and the install read the staged copy, not the folder: what
	// the user looked at is what lands.
	write(t, filepath.Join(src, "show-me", "notes.md"), "v2")
	if content, _, err := ReadStagedFile(got.ID, "show-me", "notes.md"); err != nil || content != "v1" {
		t.Errorf("staged notes.md = %q, %v", content, err)
	}
	if len(stagingDirs(t, tmp)) != 1 {
		t.Errorf("staging dirs = %v", stagingDirs(t, tmp))
	}

	for name, source := range map[string]string{
		"a folder that is not there": filepath.Join(r.Home, "nope"),
		"a file":                     filepath.Join(r.Home, "secret.txt"),
		"a folder with no skills":    filepath.Join(r.Home, "code"),
	} {
		if name == "a folder with no skills" {
			if err := os.RemoveAll(src); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := (Fetcher{}).Stage(context.Background(), r, source); err == nil {
			t.Errorf("%s: staged %+v", name, got)
		}
	}
}

func TestWhatAFetcherWritesIsNotTrusted(t *testing.T) {
	staging(t)
	r := machine(t)
	outside := filepath.Join(r.Home, "private")
	write(t, filepath.Join(outside, "SKILL.md"), skillMD("private", "Not part of the source"))
	write(t, filepath.Join(outside, "id_rsa"), "key")
	x := &tools{}
	x.npx = func(c Command) ([]byte, error) {
		writeAll(t, c.Dir, npxRepo)
		// A skill folder that is really somewhere else on the machine.
		link(t, outside, filepath.Join(c.Dir, ".agents", "skills", "private"))
		// A file inside a real skill that is.
		link(t, filepath.Join(outside, "id_rsa"), filepath.Join(c.Dir, ".agents", "skills", "show-me", "key"))
		return nil, nil
	}
	got, err := x.fetcher().Stage(context.Background(), r, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(stagedNames(got), "private") {
		t.Errorf("staged %v, including a folder outside staging", stagedNames(got))
	}
	for _, rel := range []string{"key", "../private/id_rsa", "../../../skills-lock.json", filepath.Join(outside, "id_rsa")} {
		if content, _, err := ReadStagedFile(got.ID, "show-me", rel); err == nil {
			t.Errorf("read %q as %q", rel, content)
		}
	}
	if _, _, err := ReadStagedFile(got.ID, "private", "SKILL.md"); err == nil {
		t.Error("read a skill that was not staged")
	}
	if content, binary, err := ReadStagedFile(got.ID, "show-me", "scripts/run.sh"); err != nil || binary || content != "#!/bin/sh\n" {
		t.Errorf("an ordinary file = %q, %v, %v", content, binary, err)
	}
	// The link is refused on the way into the library too, and nothing of the
	// skill is left there.
	if _, err := InstallStaged(r, got.ID, []string{"show-me"}, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("install = %v, want a refusal", err)
	}
	if entries, _ := os.ReadDir(r.Library); len(entries) != 0 {
		t.Errorf("library holds %v after a refused install", entries)
	}
}

func TestStagingIDs(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	victim := filepath.Join(tmp, "victim")
	write(t, filepath.Join(victim, "SKILL.md"), skillMD("victim", "Not a staging dir"))
	// A dir with the right prefix that no fetch made.
	planted := filepath.Join(tmp, stagePrefix+"planted")
	write(t, filepath.Join(planted, "SKILL.md"), skillMD("planted", "Not a staging dir"))

	for _, id := range []string{"", "victim", "../victim", "planted", "0123", strings.Repeat("g", 32), strings.Repeat("A", 32), strings.Repeat("a", 31) + "/", strings.Repeat("a", 33)} {
		if _, _, err := ReadStagedFile(id, "victim", "SKILL.md"); !errors.Is(err, ErrInvalid) {
			t.Errorf("read with id %q: %v", id, err)
		}
		if _, err := InstallStaged(r, id, []string{"victim"}, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("install with id %q: %v", id, err)
		}
		if _, err := ApplyUpdate(r, id, []string{victim}); !errors.Is(err, ErrInvalid) {
			t.Errorf("apply with id %q: %v", id, err)
		}
		if err := DiscardStaged(id); !errors.Is(err, ErrInvalid) {
			t.Errorf("discard with id %q: %v", id, err)
		}
	}
	if !exists(filepath.Join(victim, "SKILL.md")) || !exists(filepath.Join(planted, "SKILL.md")) {
		t.Error("a bad id removed something")
	}

	// A well-formed id nothing was staged under is simply gone.
	unknown := strings.Repeat("a", 32)
	if _, _, err := ReadStagedFile(unknown, "x", "SKILL.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("read of an unknown id: %v", err)
	}
	if err := DiscardStaged(unknown); err != nil {
		t.Errorf("discarding what is already gone: %v", err)
	}
	// So is a symlink somebody put where a staging dir would be.
	link(t, victim, filepath.Join(tmp, stagePrefix+unknown))
	if _, _, err := ReadStagedFile(unknown, "victim", "SKILL.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("read through a planted symlink: %v", err)
	}
}

func TestDiscardStaged(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	src := filepath.Join(r.Home, "src")
	write(t, filepath.Join(src, "one", "SKILL.md"), skillMD("one", "One"))
	got, err := Fetcher{}.Stage(context.Background(), r, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := DiscardStaged(got.ID); err != nil {
		t.Fatal(err)
	}
	if left := stagingDirs(t, tmp); len(left) != 0 {
		t.Errorf("left %v", left)
	}
	if _, _, err := ReadStagedFile(got.ID, "one", "SKILL.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("read after discard: %v", err)
	}
	if !exists(filepath.Join(src, "one", "SKILL.md")) {
		t.Error("discarding removed the source")
	}
}

func TestStaleStagingIsSweptOnTheNextFetch(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	src := filepath.Join(r.Home, "src")
	write(t, filepath.Join(src, "one", "SKILL.md"), skillMD("one", "One"))
	stage := func() string {
		t.Helper()
		got, err := Fetcher{}.Stage(context.Background(), r, src)
		if err != nil {
			t.Fatal(err)
		}
		dir, _ := stagePath(got.ID)
		return dir
	}
	age := func(path string, d time.Duration) {
		t.Helper()
		when := time.Now().Add(-d)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	old, recent := stage(), stage()
	age(old, 2*time.Hour)
	age(recent, 30*time.Minute)
	// Old, and in the same temp folder, but not ours.
	other := filepath.Join(tmp, "somebody-elses")
	almost := filepath.Join(tmp, stagePrefix+"notes")
	for _, dir := range []string{other, almost} {
		mkdir(t, dir)
		age(dir, 2*time.Hour)
	}

	fresh := stage()
	if exists(old) {
		t.Error("a two hour old staging dir survived")
	}
	for _, dir := range []string{recent, fresh, other, almost} {
		if !exists(dir) {
			t.Errorf("%s was swept", filepath.Base(dir))
		}
	}
}

func TestRunCommandStopsWhenTheContextDoes(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCommand(ctx, Command{Name: "sleep", Args: []string{"30"}, Dir: t.TempDir()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %v to stop", took)
	}
}

func TestRunCommandReportsWhyAProgramFailed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	out, err := runCommand(context.Background(), Command{Name: "sh", Args: []string{"-c", `echo "$GREETING"; echo first >&2; echo the reason >&2; exit 3`}, Dir: t.TempDir(), Env: []string{"GREETING=hello"}})
	if err == nil || !strings.Contains(err.Error(), "the reason") || strings.Contains(err.Error(), "first") {
		t.Errorf("err = %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("stdout = %q", out)
	}
}

func TestSweepSparesAHeldStage(t *testing.T) {
	tmp := staging(t)
	kept, err := newStage()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newStage(); err != nil {
		t.Fatal(err)
	}
	HoldStaged(kept.id)
	t.Cleanup(func() { ReleaseStaged(kept.id) })

	later := time.Now().Add(2 * stageMaxAge)
	sweepStages(later)
	if got := stagingDirs(t, tmp); len(got) != 1 || filepath.Base(got[0]) != stagePrefix+kept.id {
		t.Fatalf("after the sweep: %v, want only %s", got, kept.id)
	}

	ReleaseStaged(kept.id)
	sweepStages(later)
	if got := stagingDirs(t, tmp); len(got) != 0 {
		t.Fatalf("a released stage outlived the sweep: %v", got)
	}
}
