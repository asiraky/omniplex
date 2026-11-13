package thread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// What one turn changed is a different question from what the thread changed,
// and Git cannot answer it after the fact: by the time a turn ends, the only
// record of where it started is gone. So each turn is bracketed by a snapshot.
//
// A snapshot is a real commit written to a hidden ref, built through a
// throwaway index so the person's own staged work is never touched. Two of them
// bound a turn, and the diff between them is what that turn did — formatters,
// codemods and stray `sed` included, because it asks the worktree rather than
// the event log.

// checkpointRefPrefix keeps snapshots out of `git branch`, `git log` and every
// other place a person looks, while still being ordinary refs that `git diff`
// and `git restore` understand.
const checkpointRefPrefix = "refs/omniplex/checkpoints"

// A turn diff is bounded like any other: a diff nobody can read is not worth
// the memory to hold it.
const maxTurnDiffFiles = 500

// TurnChanges is what one turn did, as the file list a card can render.
type TurnChanges struct {
	Files     []ChangedFile `json:"files"`
	Additions int           `json:"additions"`
	Deletions int           `json:"deletions"`
	Truncated bool          `json:"truncated,omitempty"`
}

// checkpointRef names the snapshot bounding one end of a turn. The turn id is
// already unique, so no counter has to be kept in step with the log.
func checkpointRef(threadID, turnID, edge string) string {
	return fmt.Sprintf("%s/%s/%s/%s", checkpointRefPrefix, threadID, turnID, edge)
}

// scratchIndexConfig keeps a scratch index that started as a copy of the
// person's from reaching back into it. A split index would write and expire
// the sharedindex files the real one still points at, and an fsmonitor token
// vouches for the index it was written into, not for a copy of it.
var scratchIndexConfig = []string{
	"-c", "core.splitIndex=false",
	"-c", "splitIndex.sharedIndexExpire=never",
	"-c", "core.fsmonitor=false",
}

// captureCheckpoint writes the worktree — tracked, staged, unstaged and
// untracked alike — to a commit under ref, and answers with its object id.
func captureCheckpoint(ctx context.Context, root, ref string) (string, error) {
	return writeCheckpoint(ctx, root, ref, true)
}

// writeCheckpoint is captureCheckpoint with the seeding made optional, so a
// seeded snapshot can be held up against one built from nothing.
//
// It runs through GIT_INDEX_FILE so that `git add -A` stages into a scratch
// index instead of the person's own. Staging into the real index would make a
// snapshot visible as staged work, which is a side effect no one asked for.
func writeCheckpoint(ctx context.Context, root, ref string, seed bool) (string, error) {
	index, err := os.CreateTemp("", "omniplex-checkpoint-index-*")
	if err != nil {
		return "", err
	}
	// git wants to create the index itself; an existing empty file is not a
	// valid index, so only the name is borrowed.
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)

	env := append(gitEnv(),
		"GIT_INDEX_FILE="+indexPath,
		// A snapshot is the server's own bookkeeping. Borrowing the person's
		// identity for it would put their name on commits they never made,
		// and a repository with no configured user would fail outright.
		"GIT_AUTHOR_NAME=omniplex",
		"GIT_AUTHOR_EMAIL=omniplex@localhost",
		"GIT_COMMITTER_NAME=omniplex",
		"GIT_COMMITTER_EMAIL=omniplex@localhost",
	)
	scratch := func(args ...string) ([]byte, error) {
		return runGitEnv(ctx, root, env, slices.Concat(scratchIndexConfig, args)...)
	}

	// A repository with no commits yet has nothing to seed the index from, and
	// that is not an error: everything in it is simply new.
	if _, err := runGit(ctx, root, "rev-parse", "--verify", "HEAD"); err == nil {
		if !seed || !seedScratchIndex(ctx, root, indexPath, scratch) {
			// Whatever a failed seed left behind is not a starting point.
			_ = os.Remove(indexPath)
			if _, err := scratch("read-tree", "HEAD"); err != nil {
				return "", err
			}
		}
	}
	if _, err := scratch("add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := scratch("write-tree")
	if err != nil {
		return "", err
	}
	commit, err := runGitEnv(ctx, root, env, "commit-tree", strings.TrimSpace(string(tree)),
		"-m", "omniplex checkpoint "+ref)
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(commit))
	if _, err := runGitEnv(ctx, root, env, "update-ref", ref, oid); err != nil {
		return "", err
	}
	return oid, nil
}

// seedScratchIndex starts the scratch index as a copy of the worktree's own.
// An index read from a tree carries no stat data, so `git add -A` would have
// to hash every file in the checkout to learn that almost none of them
// changed; the person's index already knows, and with it git only rehashes
// what has been touched since. It answers false when the copy cannot be
// trusted to give the same snapshot, and the caller builds from HEAD instead.
func seedScratchIndex(ctx context.Context, root, indexPath string, scratch func(...string) ([]byte, error)) bool {
	out, err := runGit(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return false
	}
	own := strings.TrimSpace(string(out))
	info, err := os.Stat(own)
	if err != nil {
		return false
	}
	if err := copyFile(own, indexPath); err != nil {
		return false
	}
	// Git trusts an entry's stat data only when the file is older than the
	// index it sits in; anything as new is "racily clean" and gets hashed. A
	// copy is newer than the original, which would vouch for files edited in
	// the same second the person's index was written. Wearing the original's
	// timestamp, rounded down, keeps every entry it doubted doubtful.
	stamp := info.ModTime().Truncate(time.Second)
	if os.Chtimes(indexPath, stamp, stamp) != nil {
		return false
	}
	// The person's staged work is theirs, not the turn's: every snapshot
	// starts from HEAD exactly as an unseeded one would. A single-tree reset
	// keeps the stat data of each entry that already matches HEAD.
	if _, err := scratch("read-tree", "--reset", "HEAD"); err != nil {
		return false
	}
	// The reset rewrote the file at the current time.
	if os.Chtimes(indexPath, stamp, stamp) != nil {
		return false
	}
	return !hasFlaggedEntries(ctx, scratch)
}

// hasFlaggedEntries reports whether any entry is marked assume-unchanged or
// skip-worktree. Git takes either flag as a promise not to look at the file,
// so `add -A` would miss an edit to one and keep one a sparse checkout left
// off disk, where a snapshot from HEAD records both. Those checkouts give up
// the speed rather than the snapshot.
func hasFlaggedEntries(ctx context.Context, scratch func(...string) ([]byte, error)) bool {
	out, err := scratch("ls-files", "-v", "-z")
	if err != nil {
		return true
	}
	// Each record is a one-letter tag, a space and a path. A plain cached
	// entry is "H"; skip-worktree is "S", and assume-unchanged lowers either.
	for record := range bytes.SplitSeq(out, []byte{0}) {
		if len(record) > 0 && record[0] != 'H' {
			return true
		}
	}
	return false
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// diffCheckpoints is the file list between two snapshots. Both sides are real
// commits, so a rename is a rename and an untracked file that the turn created
// is simply an addition — none of the special cases the working tree needs.
func diffCheckpoints(ctx context.Context, root, from, to string) (TurnChanges, error) {
	out := TurnChanges{Files: []ChangedFile{}}

	nameStatus, err := runGit(ctx, root, "diff", "-M", "--name-status", "-z", from, to)
	if err != nil {
		return TurnChanges{}, err
	}
	numstat, err := runGit(ctx, root, "diff", "-M", "--numstat", "-z", from, to)
	if err != nil {
		return TurnChanges{}, err
	}

	files := parseNameStatus(string(nameStatus))
	counts := parseNumstat(string(numstat))
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = c.additions, c.deletions, c.binary
		}
	}

	// Totals are summed over everything before the list is cut, so a truncated
	// card still reports honest numbers.
	for _, f := range files {
		out.Additions += f.Additions
		out.Deletions += f.Deletions
	}
	if len(files) > maxTurnDiffFiles {
		files = files[:maxTurnDiffFiles]
		out.Truncated = true
	}
	if files != nil {
		out.Files = files
	}
	return out, nil
}

// dropCheckpoints deletes every snapshot belonging to a thread. Refs are cheap
// but they are not free, and a thread that has been closed will never be
// diffed again.
func dropCheckpoints(ctx context.Context, root, threadID string) error {
	prefix := fmt.Sprintf("%s/%s/", checkpointRefPrefix, threadID)
	listed, err := runGit(ctx, root, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return err
	}
	var failed error
	for _, ref := range strings.Fields(string(listed)) {
		if _, err := runGit(ctx, root, "update-ref", "-d", ref); err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

// purgeCheckpoints removes a thread's snapshots when there is no checkpointer
// to do it — a workspace being cleaned up, or a thread closed while nothing
// was running. It has to happen while the checkout still exists: the refs live
// in the repository the worktree belongs to, and they outlive the worktree.
func purgeCheckpoints(ctx context.Context, cwd, threadID string, logf func(string, ...any)) {
	root := repoRoot(ctx, cwd)
	if root == "" {
		return
	}
	if err := dropCheckpoints(ctx, root, threadID); err != nil {
		logf("dropping checkpoints for %s: %v", threadID, err)
	}
}

// repoRoot is the top of the checkout a thread works in, or "" when it is not
// a repository at all — which is not a failure, only a thread that will have
// no turn cards.
func repoRoot(ctx context.Context, cwd string) string {
	if cwd == "" {
		return ""
	}
	top, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(top))
}

func runGitEnv(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := gitCmd(ctx, dir, args...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// errNoBaseCheckpoint is a restore asked of a turn whose starting snapshot was
// never taken: one from before snapshots, one whose baseline did not settle in
// time, or one a composer action started without one.
var errNoBaseCheckpoint = errors.New("there is no snapshot of the files from when this turn started, so they cannot be restored")

// checkpointExists reports whether ref names a snapshot commit.
func checkpointExists(ctx context.Context, root, ref string) bool {
	_, err := runGit(ctx, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// restoreCheckpoint puts the checkout back the way it was when turnID
// started: every file in that turn's base snapshot gets its content and mode
// back, and every file the snapshot did not have is deleted, unless the
// snapshot's own ignore rules cover it: ignored files were never captured, so
// they are left alone. The checkout as it stood is
// saved first under the turn's pre-rewind ref, which is what makes a restore
// done by mistake recoverable with git.
//
// Only the files that differ are written, so an untouched file keeps its
// mtime and a build does not see the whole tree as new. Like writeCheckpoint
// it goes through a scratch index, and the person's own index, staged work
// and HEAD are left exactly as they were: afterwards their staged changes
// still say what they said, measured against files that may have moved.
func restoreCheckpoint(ctx context.Context, root, threadID, turnID string) error {
	base := checkpointRef(threadID, turnID, "base")
	if !checkpointExists(ctx, root, base) {
		return errNoBaseCheckpoint
	}
	pre, err := captureCheckpoint(ctx, root, checkpointRef(threadID, turnID, "pre-rewind"))
	if err != nil {
		return fmt.Errorf("saving the files as they are before restoring them: %w", err)
	}
	// Plumbing, not `git diff`: no external diff driver or textconv the
	// person configured gets a say in which files are written.
	out, err := runGit(ctx, root, "diff-tree", "-r", "-z", "--no-renames", "--name-status", pre, base)
	if err != nil {
		return err
	}
	var gone, write []string
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "D" {
			gone = append(gone, fields[i+1])
		} else {
			write = append(write, fields[i+1])
		}
	}

	index, err := os.CreateTemp("", "omniplex-restore-index-*")
	if err != nil {
		return err
	}
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)
	env := append(gitEnv(), "GIT_INDEX_FILE="+indexPath)
	if _, err := runGitEnv(ctx, root, env, slices.Concat(scratchIndexConfig, []string{"read-tree", base})...); err != nil {
		return err
	}

	// A file the base does not have is only the turn's if the base could
	// have had it. One the base's ignore rules cover was left out of the
	// snapshot, not absent from the checkout, and stays where it is.
	if gone, err = dropIgnoredByBase(ctx, root, env, gone); err != nil {
		return fmt.Errorf("checking which files the snapshot ignored: %w", err)
	}

	// Deletions first: a file that was a directory then, or a directory
	// that was a file, has to be out of the way before the other is written.
	for _, rel := range gone {
		if err := removeCheckoutFile(root, rel); err != nil {
			return err
		}
	}
	if len(write) == 0 {
		return nil
	}

	cmd := gitCmd(ctx, root, slices.Concat(scratchIndexConfig, []string{"checkout-index", "-f", "-z", "--stdin"})...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(strings.Join(write, "\x00") + "\x00")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout-index: %w: %s", err, strings.TrimSpace(string(msg)))
	}
	return nil
}

// dropIgnoredByBase leaves out of paths every one the base snapshot's own
// ignore rules cover. Ignore rules are files in the tree, so they can change
// between two snapshots: a `.env` ignored when the base was taken is missing
// from it, and once a turn stops ignoring it, it is in the later snapshot and
// would look like a file the turn created.
//
// The rules are asked of the base, not of the checkout as it stands: its
// ignore files are written into a throwaway work tree, and check-ignore runs
// there against the repository's own git directory, so info/exclude and
// core.excludesFile still count. env carries the scratch index already
// holding the base.
func dropIgnoredByBase(ctx context.Context, root string, env, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return paths, nil
	}
	gitDir, err := runGit(ctx, root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	rules, err := runGitEnv(ctx, root, env, "ls-files", "-z", "--", ".gitignore", "*/.gitignore")
	if err != nil {
		return nil, err
	}
	tree, err := os.MkdirTemp("", "omniplex-restore-ignores-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tree)
	if len(rules) > 0 {
		cmd := gitCmd(ctx, root, slices.Concat(scratchIndexConfig,
			[]string{"checkout-index", "-f", "-z", "--stdin", "--prefix=" + tree + string(filepath.Separator)})...)
		cmd.Env = env
		cmd.Stdin = bytes.NewReader(rules)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git checkout-index: %w: %s", err, strings.TrimSpace(string(msg)))
		}
	}

	cmd := gitCmd(ctx, tree, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Env = append(slices.Clone(env),
		"GIT_DIR="+strings.TrimSpace(string(gitDir)),
		"GIT_WORK_TREE="+tree,
	)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		// Nothing is ignored.
		return paths, nil
	case errors.As(err, &ee):
		return nil, fmt.Errorf("git check-ignore: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	case err != nil:
		return nil, err
	}
	ignored := make(map[string]bool)
	for _, rel := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		ignored[rel] = true
	}
	return slices.DeleteFunc(paths, func(rel string) bool { return ignored[rel] }), nil
}

// removeCheckoutFile deletes one file a restore takes away, and then every
// directory it leaves empty, up to but never including root. A path that is
// already gone is not an error.
func removeCheckoutFile(root, rel string) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if !within(full, root) {
		return fmt.Errorf("refusing to delete %q: it is outside the checkout", rel)
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for dir := filepath.Dir(full); within(dir, root); dir = filepath.Dir(dir) {
		// Fails on a directory with anything left in it, ignored files
		// included, which is where the walk stops.
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}
