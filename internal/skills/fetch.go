package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/procgroup"
)

// fetchTimeout bounds one run of npx or git. A fetch is on the far side of
// somebody's 4G, so it is long, but one that has not finished by now will not.
const fetchTimeout = 120 * time.Second

// Command is one external program a fetch runs.
type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string // on top of the server's own environment
}

// Fetcher stages skills from a source. The zero value runs the real npx and
// git; a test swaps Run for one that writes the files they would have.
type Fetcher struct {
	Run      func(ctx context.Context, c Command) (stdout []byte, err error)
	LookPath func(name string) (string, error)
}

func (f Fetcher) run(ctx context.Context, c Command) ([]byte, error) {
	if f.Run != nil {
		return f.Run(ctx, c)
	}
	return runCommand(ctx, c)
}

func (f Fetcher) has(name string) bool {
	look := f.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(name)
	return err == nil
}

// runCommand runs c to the end or until ctx is done. npx is a tree of
// processes, so the whole tree is killed, not just the one that was started.
func runCommand(ctx context.Context, c Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	tree := procgroup.Attach(cmd, "skills-"+c.Name)
	defer tree.Kill()
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	// A grandchild that outlives the kill must not hold the pipes open.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	switch {
	case err == nil:
		return stdout.Bytes(), nil
	case ctx.Err() != nil:
		return stdout.Bytes(), ctx.Err()
	}
	if reason := lastLine(stderr.String()); reason != "" {
		return stdout.Bytes(), fmt.Errorf("%s: %s", c.Name, reason)
	}
	return stdout.Bytes(), fmt.Errorf("%s: %w", c.Name, err)
}

// lastLine is the end of a program's complaints, which is where the reason
// usually is, kept short enough to show on a phone.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(line); len(r) > 300 {
		line = string(r[:300]) + "…"
	}
	return line
}

// Stage fetches whatever the user pasted into a fresh staging dir and lists
// the skills found there. Nothing is installed.
func (f Fetcher) Stage(ctx context.Context, r Roots, source string) (Staged, error) {
	src, err := ParseSource(source, r.Home)
	if err != nil {
		return Staged{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	_, staged, err := f.stage(ctx, r, src)
	return staged, err
}

func (f Fetcher) stage(ctx context.Context, r Roots, src ParsedSource) (*stage, Staged, error) {
	sweepStages(time.Now())
	st, err := newStage()
	if err != nil {
		return nil, Staged{}, err
	}
	method, note, found, err := f.fetch(ctx, r, src, st.dir)
	var staged Staged
	if err == nil {
		staged, err = st.describe(r, src, method, note, found)
	}
	if err != nil {
		_ = os.RemoveAll(st.dir)
		return nil, Staged{}, err
	}
	return st, staged, nil
}

// fetch picks the fetcher. npx is the usual way to a remote source, and never
// the only one: with no npx, or an npx run that did not produce skills, git
// fetches the same repo.
func (f Fetcher) fetch(ctx context.Context, r Roots, src ParsedSource, dir string) (method, note string, found []fetched, err error) {
	if src.Local {
		found, err = fetchLocal(ctx, src, dir)
		return MethodLocal, "", found, err
	}
	var npxErr error
	if !f.has("npx") {
		note = "npx is not installed; fetched with git"
	} else {
		var warning string
		found, warning, npxErr = f.fetchNpx(ctx, r, src, dir)
		if npxErr == nil && len(found) > 0 {
			return MethodNpx, warning, underPath(found, src.Path), nil
		}
		// The user cancelling, or the command running out of time, is not npx
		// failing: git would only be cancelled too.
		if err := ctx.Err(); err != nil {
			return "", "", nil, err
		}
		note = "npx skills add found no skills; fetched with git"
		if npxErr != nil {
			note = "npx skills add failed (" + npxErr.Error() + "); fetched with git"
		}
		// Whatever npx left half-written is not part of what git fetches.
		_ = os.RemoveAll(filepath.Join(dir, ".agents"))
		_ = os.Remove(filepath.Join(dir, "skills-lock.json"))
	}
	found, err = f.fetchGit(ctx, src, dir)
	if err != nil {
		if npxErr != nil && ctx.Err() == nil {
			return "", "", nil, fmt.Errorf("%v; then %w", npxErr, err)
		}
		return "", "", nil, err
	}
	return MethodGit, note, underPath(found, src.Path), nil
}

// underPath keeps the skills inside the folder a tree URL pointed at. A skill
// whose place in the repo is not known stays.
func underPath(found []fetched, sub string) []fetched {
	if sub == "" {
		return found
	}
	kept := found[:0]
	for _, f := range found {
		if f.path == "" || f.path == sub || strings.HasPrefix(f.path, sub+"/") {
			kept = append(kept, f)
		}
	}
	return kept
}

var cliVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-]*$`)

// fetchNpx has the skills CLI install every skill of the source into the
// staging dir, as though that were a project. The CLI is always named with a
// version: a bare `npx skills` does not resolve. The ref is passed as the user
// gave it; pinning a commit here would record the commit as the ref, and the
// skill would never update again.
func (f Fetcher) fetchNpx(ctx context.Context, r Roots, src ParsedSource, dir string) (found []fetched, warning string, err error) {
	if !cliVersionRe.MatchString(r.CLIVersion) {
		return nil, "", fmt.Errorf("%q is not a skills CLI version", r.CLIVersion)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	stdout, err := f.run(ctx, Command{
		Name: "npx",
		Args: []string{"-y", "skills@" + r.CLIVersion, "add", src.spec(), "-s", "*", "-a", "universal", "--copy", "-y", "--json"},
		Dir:  dir,
		Env:  []string{"DISABLE_TELEMETRY=1"},
	})
	if err != nil {
		return nil, "", err
	}
	// What the CLI printed is its opinion; what it wrote is the fact.
	lock := ReadCLILock(filepath.Join(dir, "skills-lock.json"))
	root := filepath.Join(dir, ".agents", "skills")
	for _, name := range readDirs(root) {
		folder := filepath.Join(root, name)
		if !hasSkillFile(folder) {
			continue
		}
		one := fetched{folder: folder, base: name}
		if e, ok := lock[name]; ok {
			source := e.source()
			one.path, one.ref = source.Path, source.Ref
		}
		found = append(found, one)
	}
	return found, npxFailures(stdout), nil
}

// npxFailures names the skills the CLI said it could not install, so a source
// that came through with some missing says so.
func npxFailures(stdout []byte) string {
	start, end := bytes.IndexByte(stdout, '['), bytes.LastIndexByte(stdout, ']')
	if start < 0 || end < start {
		return ""
	}
	var results []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if json.Unmarshal(stdout[start:end+1], &results) != nil {
		return ""
	}
	var failed []string
	for _, res := range results {
		if res.Status == "failed" && res.Name != "" {
			failed = append(failed, res.Name)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return "npx skills add could not fetch " + strings.Join(failed, ", ")
}

// fetchGit clones the source and looks for skills in the checkout.
func (f Fetcher) fetchGit(ctx context.Context, src ParsedSource, dir string) ([]fetched, error) {
	if !f.has("git") {
		return nil, errors.New("git is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	repo := filepath.Join(dir, "repo")
	// A private repo must fail, not wait for a password nobody can type.
	git := func(args ...string) error {
		_, err := f.run(ctx, Command{Name: "git", Args: args, Dir: dir, Env: []string{"GIT_TERMINAL_PROMPT=0"}})
		return err
	}
	var err error
	if fullShaRe.MatchString(src.Ref) {
		// clone --branch takes a branch or a tag, not a commit.
		for _, args := range [][]string{
			{"init", "-q", repo},
			{"-C", repo, "fetch", "-q", "--depth", "1", "--", src.cloneURL(), src.Ref},
			{"-C", repo, "checkout", "-q", "FETCH_HEAD"},
		} {
			if err = git(args...); err != nil {
				break
			}
		}
	} else {
		args := []string{"clone", "-q", "--depth", "1"}
		if src.Ref != "" {
			args = append(args, "--branch", src.Ref)
		}
		err = git(append(args, "--", src.cloneURL(), repo)...)
	}
	if err != nil {
		return nil, err
	}

	top, ok := realDir(repo)
	if !ok {
		return nil, errors.New("git fetched nothing")
	}
	root := top
	if src.Path != "" {
		// A folder in the repo may be a symlink to anywhere.
		if root, ok = realDir(filepath.Join(top, filepath.FromSlash(src.Path))); !ok || !within(top, root) {
			return nil, fmt.Errorf("%w: no folder %s in %s", ErrInvalid, src.Path, src.Repo)
		}
	}
	folders, err := scanSkills(ctx, root)
	if err != nil {
		return nil, err
	}
	var found []fetched
	for _, folder := range folders {
		one := fetched{folder: folder, base: filepath.Base(folder), ref: src.Ref}
		if rel, err := filepath.Rel(top, folder); err == nil && rel != "." {
			one.path = filepath.ToSlash(rel)
		} else {
			one.base = src.shortName() // the repo is the skill
		}
		found = append(found, one)
	}
	return found, nil
}

// fetchLocal copies the skills under a folder on this machine into staging,
// so an install reads the same bytes the preview showed.
func fetchLocal(ctx context.Context, src ParsedSource, dir string) ([]fetched, error) {
	top, ok := realDir(src.Repo)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrInvalid, src.Repo)
	}
	folders, err := scanSkills(ctx, top)
	if err != nil {
		return nil, err
	}
	var found []fetched
	for i, folder := range folders {
		one := fetched{base: filepath.Base(folder)}
		if rel, err := filepath.Rel(top, folder); err == nil && rel != "." {
			one.path = filepath.ToSlash(rel)
		}
		// Numbered, not named: two folders of one name can both be skills.
		dst := filepath.Join(dir, "skills", strconv.Itoa(i))
		if err := copySkill(folder, dst); err != nil {
			_ = os.RemoveAll(dst)
			one.problem = err.Error()
		} else {
			one.folder = dst
		}
		found = append(found, one)
	}
	return found, nil
}
