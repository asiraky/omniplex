package thread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// The truth about what a thread changed is in Git, not in the event log. A
// harness edits files through tools we parse, but it also runs formatters,
// codemods and `sed`, and those changes are just as real. Asking the worktree
// catches all of it, and gives honest line counts for free.

const (
	// A diff nobody can read is not worth the bytes it costs to send.
	maxPatchBytes = 256 * 1024
	// Enough files for any thread a human is going to review by hand.
	maxChangedFiles = 2000
)

type diffRange struct {
	base string
	head string
}

// ChangedFile is one path a change set touched, aggregated over the whole set
// rather than per tool call: a file edited five times appears once. It is the
// event schema's type, so a turn's file list and a thread's are the same shape
// on the wire.
type ChangedFile = proto.ChangedFile

// ThreadChanges is the PR-style file list for one thread's checkout.
type ThreadChanges struct {
	Root    string `json:"root"`
	Branch  string `json:"branch,omitempty"`
	Mode    string `json:"mode"`
	BaseRef string `json:"baseRef,omitempty"`
	// Base is the resolved merge base the diff is taken against; empty means
	// the comparison is against the working tree's own HEAD.
	Base   string `json:"base,omitempty"`
	Head   string `json:"head,omitempty"`
	TurnID string `json:"turnId,omitempty"`
	// From and To are the oldest and newest commits a commit comparison
	// covers, echoed so the client can tell which selection a list answers.
	From      string        `json:"from,omitempty"`
	To        string        `json:"to,omitempty"`
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
	// DiffTurn is one turn's own changes: the diff between the snapshots
	// taken as it started and as it ended.
	DiffTurn = "turn"
	// DiffCommit is a run of the branch's own commits, from the parent of the
	// oldest to the newest, as a reviewer stepping through history reads them.
	DiffCommit = "commit"
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

// DiffRequest names one comparison. Mode picks the kind; TurnID is the turn a
// turn comparison covers, and From and To the oldest and newest commits a
// commit comparison covers. Base and Head are the range the client's list
// resolved to: reads against a list carry them so a list that has since moved
// is refused rather than silently answered from a different range. Listing
// ignores them.
type DiffRequest struct {
	Mode   string
	TurnID string
	From   string
	To     string
	Base   string
	Head   string
}

// PatchOptions shape how one file's patch is rendered. They change what the
// reader sees of a file, never which files are in the list.
type PatchOptions struct {
	IgnoreWhitespace bool
	FullContext      bool
}

// ThreadChanges lists every file the thread's checkout differs by, measured
// against the base branch it was cut from.
func (m *Manager) ThreadChanges(ctx context.Context, threadID string, req DiffRequest) (ThreadChanges, error) {
	scope, err := m.diffScope(ctx, threadID, req)
	if err != nil {
		return ThreadChanges{}, err
	}
	return changesForScope(ctx, scope)
}

func changesForScope(ctx context.Context, scope diffScope) (ThreadChanges, error) {
	out := ThreadChanges{Root: scope.root, Branch: scope.branch, Mode: scope.mode, BaseRef: scope.baseRef, Base: scope.base, Head: scope.head, TurnID: scope.turnID, From: scope.from, To: scope.to, Files: []ChangedFile{}}
	if scope.warning != "" {
		out.Warning = scope.warning
		return out, nil
	}

	files, err := trackedChanges(ctx, scope)
	if err != nil {
		return ThreadChanges{}, err
	}
	// A fixed head is a range between two commits, and what sits untracked in
	// the worktree now is no part of it: a pull request's commits, or a turn's
	// snapshots, which already hold whatever was untracked when they were taken.
	if scope.head == "" {
		untracked, err := untrackedChanges(ctx, scope.root)
		if err != nil {
			return ThreadChanges{}, err
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

// requestedChanges is the change list a read against the client's list is
// answered from, refused if it is no longer the list the client is showing.
// Anything that reads file content goes through here, so a path is only ever
// read when that comparison really reports it.
func (m *Manager) requestedChanges(ctx context.Context, threadID string, req DiffRequest) (ThreadChanges, error) {
	var changes ThreadChanges
	var err error
	if req.Mode == DiffPullRequest && req.Base != "" && req.Head != "" {
		// The list response already resolved the attached PR. Reuse that immutable
		// commit range for its file reads instead of paying for another network
		// lookup per expanded row.
		scope, scopeErr := m.diffScope(ctx, threadID, DiffRequest{Mode: DiffUncommitted})
		if scopeErr != nil {
			return ThreadChanges{}, scopeErr
		}
		if scope.warning != "" {
			return ThreadChanges{}, errors.New(scope.warning)
		}
		m.diffMu.RLock()
		resolved, ok := m.diffPR[threadID]
		m.diffMu.RUnlock()
		if !ok || resolved.base != req.Base || resolved.head != req.Head {
			return ThreadChanges{}, errors.New("the pull request comparison changed; refresh the diff")
		}
		if _, verifyErr := runGit(ctx, scope.root, "rev-parse", "--verify", req.Base+"^{commit}"); verifyErr != nil {
			return ThreadChanges{}, errors.New("the pull request's base commit is not available locally")
		}
		if _, verifyErr := runGit(ctx, scope.root, "rev-parse", "--verify", req.Head+"^{commit}"); verifyErr != nil {
			return ThreadChanges{}, errors.New("the pull request's head commit is not available locally")
		}
		scope.mode, scope.base, scope.head = req.Mode, req.Base, req.Head
		changes, err = changesForScope(ctx, scope)
	} else {
		changes, err = m.ThreadChanges(ctx, threadID, req)
	}
	if err != nil {
		return ThreadChanges{}, err
	}
	// A turn's snapshots never move, so a read from the list its card seeded,
	// which has no commit range yet, names the same commits the server does.
	seededTurn := req.Mode == DiffTurn && req.Base == "" && req.Head == ""
	if !seededTurn && (changes.Base != req.Base || changes.Head != req.Head) {
		return ThreadChanges{}, errors.New("the comparison changed; refresh the diff")
	}
	return changes, nil
}

// fullContextLines is the context asked for when the whole file is wanted. Git
// has no "all of it" spelling, so this is a count no reviewable file reaches;
// the patch byte cap still bounds what a huge file can cost.
const fullContextLines = 100000

// ThreadFileDiff renders one file's unified diff. The path must be one the
// change list reported: a thread's checkout is not a file server.
func (m *Manager) ThreadFileDiff(ctx context.Context, threadID, path string, req DiffRequest, opts PatchOptions) (FileDiff, error) {
	changes, err := m.requestedChanges(ctx, threadID, req)
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
		return FileDiff{}, fmt.Errorf("%q is not one of this thread's changed files", path)
	}

	out := FileDiff{Path: target.Path, OldPath: target.OldPath, Status: target.Status, Binary: target.Binary}
	if target.Binary {
		return out, nil
	}

	var shape []string
	if opts.IgnoreWhitespace {
		shape = append(shape, "-w")
	}
	if opts.FullContext {
		shape = append(shape, "-U"+strconv.Itoa(fullContextLines))
	}
	var args []string
	if target.Untracked {
		args = append([]string{"diff", "--no-index", "--no-color"}, shape...)
		args = append(args, "--", devNull, "./"+target.Path)
	} else {
		args = append([]string{"diff", "-M", "--no-color"}, shape...)
		args = append(args, changes.Base)
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

const (
	// A search box, not a document: anything longer is a paste gone wrong.
	maxFindQueryRunes = 200
	// How much of an untracked file a search reads. Past this a file is a
	// generated blob nobody is searching by hand, and reading all of it would
	// let one file stall the request.
	maxFindFileBytes = 1 << 20
)

// ThreadDiffFind names the files in a comparison whose added or removed lines
// contain query, ignoring case and taking it literally. Unchanged context does
// not count: the search is over what the comparison changed. Paths come back
// in the list's order, and only paths the list reports.
func (m *Manager) ThreadDiffFind(ctx context.Context, threadID, query string, req DiffRequest) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("a search needs something to search for")
	}
	if utf8.RuneCountInString(query) > maxFindQueryRunes {
		return nil, fmt.Errorf("a search is at most %d characters", maxFindQueryRunes)
	}
	// Git matches -G line by line, so a line break could never match anything,
	// and a NUL cannot be passed to git as an argument at all.
	if strings.ContainsAny(query, "\n\r\x00") {
		return nil, errors.New("a search must be a single line")
	}
	changes, err := m.requestedChanges(ctx, threadID, req)
	if err != nil {
		return nil, err
	}

	hit := map[string]bool{}
	if changes.Base != "" {
		// QuoteMeta's escapes are valid POSIX extended syntax, which is what
		// -G compiles, so the query is matched literally.
		args := []string{"diff", "-M", "--name-only", "-z", "-i", "-G", regexp.QuoteMeta(query), changes.Base}
		if changes.Head != "" {
			args = append(args, changes.Head)
		}
		raw, err := runGit(ctx, changes.Root, args...)
		if err != nil {
			return nil, err
		}
		for _, path := range splitNUL(string(raw)) {
			hit[path] = true
		}
	}
	needle := []byte(strings.ToLower(query))
	paths := []string{}
	for _, f := range changes.Files {
		if f.Untracked && !f.Binary {
			if untrackedContains(filepath.Join(changes.Root, f.Path), needle) {
				paths = append(paths, f.Path)
			}
			continue
		}
		if hit[f.Path] {
			paths = append(paths, f.Path)
		}
	}
	return paths, nil
}

// untrackedContains reports whether a file git has never seen holds needle,
// which is already lower case. Every line of such a file is an added line, so
// the whole file is in scope, up to the read cap. Only regular files are read:
// a symlink's target is not part of the checkout's changes.
func untrackedContains(path string, needle []byte) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxFindFileBytes))
	if err != nil || bytes.IndexByte(body, 0) >= 0 {
		return false
	}
	return bytes.Contains(bytes.ToLower(body), needle)
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
	turnID  string
	from    string
	to      string
	warning string
}

// diffScope works out where to run Git and what to compare against. Failure to
// find a base branch is not an error: comparing against HEAD still shows
// everything uncommitted, which is most of what a live thread has.
func (m *Manager) diffScope(ctx context.Context, threadID string, req DiffRequest) (diffScope, error) {
	mode := req.Mode
	// The turn id becomes part of a ref name, so it is checked before anything
	// reaches git: a stray `..` or `:` would name some other ref, or none.
	if mode == DiffTurn {
		if err := validTurnID(req.TurnID); err != nil {
			return diffScope{}, err
		}
	}
	// Commits are named by full object id and nothing else, for the same
	// reason: a ref, a `~` or a range would be read by git as something other
	// than one commit. Any commit in the repository is accepted, not only the
	// ones the branch list offered; it is the thread's own checkout.
	if mode == DiffCommit {
		for _, sha := range []string{req.From, req.To} {
			if err := validCommitSHA(sha); err != nil {
				return diffScope{}, err
			}
		}
	}
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return diffScope{}, err
	}
	if meta.Cwd == "" {
		return diffScope{warning: "this thread has no checkout"}, nil
	}

	top, err := runGit(ctx, meta.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return diffScope{warning: "this thread's directory is not a Git repository"}, nil
	}
	if mode == "" {
		mode = DiffUncommitted
	}
	if mode != DiffUncommitted && mode != DiffBranch && mode != DiffPullRequest && mode != DiffTurn && mode != DiffCommit {
		return diffScope{}, fmt.Errorf("unknown diff mode %q", mode)
	}
	scope := diffScope{root: strings.TrimSpace(string(top)), mode: mode}

	if b, err := runGit(ctx, scope.root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		scope.branch = strings.TrimSpace(string(b))
	}

	if mode == DiffTurn {
		return turnScope(ctx, scope, threadID, req.TurnID), nil
	}
	if mode == DiffCommit {
		return commitScope(ctx, scope, req.From, req.To)
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
		delete(m.diffPR, threadID)
		m.diffMu.Unlock()
		pr, reason := m.ThreadPR(ctx, threadID)
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
		m.diffPR[threadID] = diffRange{base: scope.base, head: scope.head}
		m.diffMu.Unlock()
		return scope, nil
	}

	if ref, base := m.branchBase(ctx, scope.root, meta); base != "" {
		scope.baseRef, scope.base = ref, base
	}
	return scope, nil
}

// branchBase is where the checkout's branch left its base branch: the merge
// base with the first candidate that has one. Both are empty when no candidate
// shares history with HEAD.
func (m *Manager) branchBase(ctx context.Context, root string, meta store.ThreadMeta) (ref, base string) {
	for _, candidate := range m.baseCandidates(ctx, meta) {
		merged, err := runGit(ctx, root, "merge-base", candidate, "HEAD")
		if err != nil {
			continue
		}
		return candidate, strings.TrimSpace(string(merged))
	}
	return "", ""
}

// turnScope compares the snapshot taken as a turn started with the one taken
// as it ended. A missing snapshot is an ordinary answer, not a failure: a turn
// that ran before checkpoints existed, or that was cut short before its end
// was taken, has none, and dropping a thread's checkpoints removes them all.
func turnScope(ctx context.Context, scope diffScope, threadID, turnID string) diffScope {
	scope.turnID = turnID
	oids := make([]string, 0, 2)
	for _, edge := range []string{"base", "end"} {
		raw, err := runGit(ctx, scope.root, "rev-parse", "--verify", "--quiet", checkpointRef(threadID, turnID, edge)+"^{commit}")
		if err != nil {
			scope.warning = "this turn's snapshots are gone, or were never taken, so its changes cannot be shown"
			return scope
		}
		oids = append(oids, strings.TrimSpace(string(raw)))
	}
	scope.base, scope.head = oids[0], oids[1]
	return scope
}

// validTurnID admits only characters a ref name can carry unescaped, since
// the id becomes part of one. Real turn ids are UUIDs, well inside that.
func validTurnID(turnID string) error {
	if turnID == "" {
		return errors.New("a turn comparison needs a turn id")
	}
	for _, r := range turnID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("%q is not a turn id", turnID)
		}
	}
	return nil
}

// baseCandidates is what a branch might have been cut from, best guess first:
// the folder's configured base branch, then the remote's default, then the
// conventional names.
func (m *Manager) baseCandidates(ctx context.Context, meta store.ThreadMeta) []string {
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
		if _, f, err := m.folder(ctx, meta.ProjectID, meta.FolderID); err == nil {
			add(f.BaseBranch)
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

	cmd := gitCmd(ctx, dir, args...)
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
