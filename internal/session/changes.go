package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// The truth about what a session changed is in Git, not in the event log. A
// harness edits files through tools we parse, but it also runs formatters,
// codemods and `sed`, and those changes are just as real. Asking the worktree
// catches all of it, and gives honest line counts for free.

const (
	// A diff nobody can read is not worth the bytes it costs to send.
	maxPatchBytes = 256 * 1024
	// Enough files for any session a human is going to review by hand.
	maxChangedFiles = 2000
)

type diffRange struct {
	base string
	head string
}

// ChangedFile is one path a change set touched, aggregated over the whole set
// rather than per tool call: a file edited five times appears once. It is the
// event schema's type, so a turn's file list and a session's are the same shape
// on the wire.
type ChangedFile = proto.ChangedFile

// SessionChanges is the PR-style file list for one session's checkout.
type SessionChanges struct {
	Root    string `json:"root"`
	Branch  string `json:"branch,omitempty"`
	Mode    string `json:"mode"`
	BaseRef string `json:"baseRef,omitempty"`
	// Base is the resolved merge base the diff is taken against; empty means
	// the comparison is against the working tree's own HEAD.
	Base      string        `json:"base,omitempty"`
	Head      string        `json:"head,omitempty"`
	Files     []ChangedFile `json:"files"`
	Additions int           `json:"additions"`
	Deletions int           `json:"deletions"`
	Truncated bool          `json:"truncated,omitempty"`
	// Warning explains an empty list that is not simply "nothing changed" —
	// a checkout that is not a repository, most often.
	Warning string `json:"warning,omitempty"`
}

const (
	DiffUncommitted = "uncommitted"
	DiffBranch      = "branch"
	DiffPullRequest = "pull_request"
)

// FileDiff is one file's unified diff, for the panel that opens beside the list.
type FileDiff struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Patch     string `json:"patch"`
	Binary    bool   `json:"binary,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// SessionChanges lists every file the session's checkout differs by, measured
// against the base branch it was cut from.
func (m *Manager) SessionChanges(ctx context.Context, sessionID, mode string) (SessionChanges, error) {
	scope, err := m.diffScope(ctx, sessionID, mode)
	if err != nil {
		return SessionChanges{}, err
	}
	return changesForScope(ctx, scope)
}

func changesForScope(ctx context.Context, scope diffScope) (SessionChanges, error) {
	out := SessionChanges{Root: scope.root, Branch: scope.branch, Mode: scope.mode, BaseRef: scope.baseRef, Base: scope.base, Head: scope.head, Files: []ChangedFile{}}
	if scope.warning != "" {
		out.Warning = scope.warning
		return out, nil
	}

	files, err := trackedChanges(ctx, scope)
	if err != nil {
		return SessionChanges{}, err
	}
	if scope.mode != DiffPullRequest {
		untracked, err := untrackedChanges(ctx, scope.root)
		if err != nil {
			return SessionChanges{}, err
		}
		files = append(files, untracked...)
	}

	// Totals are counted over everything that changed, then the list is cut:
	// a truncated list still has to report honest sums.
	for _, f := range files {
		out.Additions += f.Additions
		out.Deletions += f.Deletions
	}
	if len(files) > maxChangedFiles {
		files = files[:maxChangedFiles]
		out.Truncated = true
	}
	out.Files = files
	return out, nil
}

// SessionFileDiff renders one file's unified diff. The path must be one the
// change list reported: a session's checkout is not a file server.
func (m *Manager) SessionFileDiff(ctx context.Context, sessionID, path, mode, base, head string) (FileDiff, error) {
	var changes SessionChanges
	var err error
	if mode == DiffPullRequest && base != "" && head != "" {
		// The list response already resolved the attached PR. Reuse that immutable
		// commit range for its file reads instead of paying for another network
		// lookup per expanded row.
		scope, scopeErr := m.diffScope(ctx, sessionID, DiffUncommitted)
		if scopeErr != nil {
			return FileDiff{}, scopeErr
		}
		m.diffMu.RLock()
		resolved, ok := m.diffPR[sessionID]
		m.diffMu.RUnlock()
		if !ok || resolved.base != base || resolved.head != head {
			return FileDiff{}, errors.New("the pull request comparison changed; refresh the diff")
		}
		if _, verifyErr := runGit(ctx, scope.root, "rev-parse", "--verify", base+"^{commit}"); verifyErr != nil {
			return FileDiff{}, errors.New("the pull request's base commit is not available locally")
		}
		if _, verifyErr := runGit(ctx, scope.root, "rev-parse", "--verify", head+"^{commit}"); verifyErr != nil {
			return FileDiff{}, errors.New("the pull request's head commit is not available locally")
		}
		scope.mode, scope.base, scope.head = mode, base, head
		changes, err = changesForScope(ctx, scope)
	} else {
		changes, err = m.SessionChanges(ctx, sessionID, mode)
	}
	if err != nil {
		return FileDiff{}, err
	}
	var target *ChangedFile
	for i := range changes.Files {
		if changes.Files[i].Path == path {
			target = &changes.Files[i]
			break
		}
	}
	if target == nil {
		return FileDiff{}, fmt.Errorf("%q is not one of this session's changed files", path)
	}
	if changes.Base != base || changes.Head != head {
		return FileDiff{}, errors.New("the comparison changed; refresh the diff")
	}

	out := FileDiff{Path: target.Path, OldPath: target.OldPath, Status: target.Status, Binary: target.Binary}
	if target.Binary {
		return out, nil
	}

	var args []string
	if target.Untracked {
		args = []string{"diff", "--no-index", "--no-color", "--", devNull, "./" + target.Path}
	} else {
		args = []string{"diff", "-M", "--no-color", changes.Base}
		if changes.Head != "" {
			args = append(args, changes.Head)
		}
		args = append(args, "--", target.Path)
		if target.OldPath != "" {
			args = append(args, target.OldPath)
		}
	}
	raw, truncated, err := gitBounded(ctx, changes.Root, maxPatchBytes, args...)
	if err != nil {
		return FileDiff{}, err
	}
	out.Patch, out.Truncated = string(raw), truncated
	return out, nil
}

// devNull is git's own name for "the other side does not exist", and is spelled
// this way on every platform git runs on, Windows included.
const devNull = "/dev/null"

type diffScope struct {
	root    string
	branch  string
	mode    string
	baseRef string
	// base is what the diff is actually taken against: the merge base with the
	// base branch, or HEAD when there is no usable base branch.
	base    string
	head    string
	warning string
}

// diffScope works out where to run Git and what to compare against. Failure to
// find a base branch is not an error: comparing against HEAD still shows
// everything uncommitted, which is most of what a live session has.
func (m *Manager) diffScope(ctx context.Context, sessionID, mode string) (diffScope, error) {
	meta, err := m.store.Session(ctx, sessionID)
	if err != nil {
		return diffScope{}, err
	}
	if meta.Cwd == "" {
		return diffScope{warning: "this session has no checkout"}, nil
	}

	top, err := runGit(ctx, meta.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return diffScope{warning: "this session's directory is not a Git repository"}, nil
	}
	if mode == "" {
		mode = DiffUncommitted
	}
	if mode != DiffUncommitted && mode != DiffBranch && mode != DiffPullRequest {
		return diffScope{}, fmt.Errorf("unknown diff mode %q", mode)
	}
	scope := diffScope{root: strings.TrimSpace(string(top)), mode: mode}

	if b, err := runGit(ctx, scope.root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		scope.branch = strings.TrimSpace(string(b))
	}

	// A repository with no commits can still hold work; everything in it is
	// untracked, and there is nothing to be a base.
	if _, err := runGit(ctx, scope.root, "rev-parse", "--verify", "HEAD"); err != nil {
		return scope, nil
	}
	scope.base = "HEAD"
	if mode == DiffUncommitted {
		return scope, nil
	}
	if mode == DiffPullRequest {
		m.diffMu.Lock()
		delete(m.diffPR, sessionID)
		m.diffMu.Unlock()
		pr, reason := m.SessionPR(ctx, sessionID)
		if pr == nil || pr.BaseRefOid == "" || pr.HeadRefOid == "" {
			if reason == "" {
				reason = "the attached pull request has no commit range"
			}
			scope.warning = reason
			return scope, nil
		}
		merged, mergeErr := runGit(ctx, scope.root, "merge-base", pr.BaseRefOid, pr.HeadRefOid)
		if mergeErr != nil {
			scope.warning = "the attached pull request's commits are not available locally"
			return scope, nil
		}
		scope.baseRef = pr.BaseRefName
		scope.base = strings.TrimSpace(string(merged))
		scope.head = pr.HeadRefOid
		m.diffMu.Lock()
		if m.diffPR == nil {
			m.diffPR = make(map[string]diffRange)
		}
		m.diffPR[sessionID] = diffRange{base: scope.base, head: scope.head}
		m.diffMu.Unlock()
		return scope, nil
	}

	for _, candidate := range m.baseCandidates(ctx, meta) {
		merged, err := runGit(ctx, scope.root, "merge-base", candidate, "HEAD")
		if err != nil {
			continue
		}
		scope.baseRef, scope.base = candidate, strings.TrimSpace(string(merged))
		break
	}
	return scope, nil
}

// baseCandidates is what a branch might have been cut from, best guess first:
// the project's configured base branch, then the remote's default, then the
// conventional names.
func (m *Manager) baseCandidates(ctx context.Context, meta store.SessionMeta) []string {
	var out []string
	add := func(ref string) {
		if ref == "" {
			return
		}
		for _, existing := range out {
			if existing == ref {
				return
			}
		}
		out = append(out, ref)
	}

	if meta.ProjectID != "" {
		if p, err := m.store.Project(ctx, meta.ProjectID); err == nil {
			add(p.Config.Defaults.BaseBranch)
		}
	}
	if head, err := runGit(ctx, meta.Cwd, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		add(strings.TrimSpace(string(head)))
	}
	add("main")
	add("master")
	return out
}

func trackedChanges(ctx context.Context, scope diffScope) ([]ChangedFile, error) {
	if scope.base == "" {
		return nil, nil
	}
	comparison := []string{scope.base}
	if scope.head != "" {
		comparison = append(comparison, scope.head)
	}
	nameArgs := append([]string{"diff", "-M", "--name-status", "-z"}, comparison...)
	nameStatus, err := runGit(ctx, scope.root, nameArgs...)
	if err != nil {
		return nil, err
	}
	numArgs := append([]string{"diff", "-M", "--numstat", "-z"}, comparison...)
	numstat, err := runGit(ctx, scope.root, numArgs...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(string(nameStatus))
	counts := parseNumstat(string(numstat))
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = c.additions, c.deletions, c.binary
		}
	}
	return files, nil
}

func untrackedChanges(ctx context.Context, root string) ([]ChangedFile, error) {
	listed, err := runGit(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var out []ChangedFile
	for _, path := range splitNUL(string(listed)) {
		f := ChangedFile{Path: path, Status: "added", Untracked: true}
		// A whole new file is every line added, which is cheaper to count here
		// than to ask a git process per file — and a checkout can hold tens of
		// thousands of untracked files.
		f.Additions, f.Binary = newFileLines(filepath.Join(root, path))
		out = append(out, f)
	}
	return out, nil
}

// newFileLines counts the lines of a file git has never seen, and reports it
// binary on the same evidence git uses: a NUL byte near the start.
func newFileLines(path string) (lines int, binary bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	first := true
	trailing := byte('\n')
	for {
		n, err := f.Read(buf)
		chunk := buf[:n]
		if first && bytes.IndexByte(chunk, 0) >= 0 {
			return 0, true
		}
		first = false
		lines += bytes.Count(chunk, []byte{'\n'})
		if n > 0 {
			trailing = chunk[n-1]
		}
		if err != nil {
			break
		}
	}
	// A file whose last line has no newline is still a line.
	if trailing != '\n' {
		lines++
	}
	return lines, false
}

type lineCount struct {
	additions, deletions int
	binary               bool
}

// parseNameStatus reads `git diff --name-status -z`: a status field followed by
// one path, or by two for a rename or copy.
func parseNameStatus(raw string) []ChangedFile {
	fields := splitNUL(raw)
	var out []ChangedFile
	for i := 0; i < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		renamed := code[0] == 'R' || code[0] == 'C'
		if renamed {
			if i+2 >= len(fields) {
				break
			}
			out = append(out, ChangedFile{Path: fields[i+2], OldPath: fields[i+1], Status: statusName(code[0])})
			i += 2
			continue
		}
		if i+1 >= len(fields) {
			break
		}
		out = append(out, ChangedFile{Path: fields[i+1], Status: statusName(code[0])})
		i++
	}
	return out
}

// parseNumstat reads `git diff --numstat -z`, keyed by the file's new path. A
// rename splits its paths into the two NUL fields that follow the counts.
func parseNumstat(raw string) map[string]lineCount {
	out := map[string]lineCount{}
	fields := splitNUL(raw)
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		c := lineCount{binary: parts[0] == "-" || parts[1] == "-"}
		c.additions, _ = strconv.Atoi(parts[0])
		c.deletions, _ = strconv.Atoi(parts[1])
		path := parts[2]
		if path == "" {
			if i+2 >= len(fields) {
				break
			}
			path = fields[i+2]
			i += 2
		}
		out[path] = c
	}
	return out
}

func statusName(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

func splitNUL(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// gitBounded runs git and stops reading at limit bytes, killing the command
// rather than buffering a diff nobody could read anyway. A minified bundle or a
// vendored tree can produce hundreds of megabytes on one path.
func gitBounded(ctx context.Context, dir string, limit int, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	raw, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	truncated := len(raw) > limit
	if truncated {
		raw = raw[:limit]
		// Stop git rather than drain it: nothing further will be shown.
		cancel()
	}
	// Draining what is left keeps git from blocking on a full pipe while it
	// waits to be reaped.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	if truncated {
		return raw, true, nil
	}
	if readErr != nil {
		return nil, false, readErr
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) && ee.ExitCode() == 1 {
		// --no-index reports "they differ" as exit 1, which is the normal case
		// for a file that exists on only one side.
		return raw, false, nil
	}
	if waitErr != nil {
		return nil, false, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), waitErr, strings.TrimSpace(stderr.String()))
	}
	return raw, false, nil
}
