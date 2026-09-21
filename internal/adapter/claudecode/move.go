package claudecode

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// MoveConversation hands a conversation from one Claude account to another.
// Claude Code keeps each conversation under its config directory, at
// projects/<project key>/<id>.jsonl, with the conversation's subagent
// transcripts in a sibling <id>/ directory, and --resume only looks under the
// running account's directory. Moving both is the whole switch: the next
// process, started with the other account's env, resumes the same id.
//
// The file is found by id rather than by recomputing the project key: Claude
// derives that key from the cwd with rules of its own (long paths are
// shortened and hashed), and the id is unique. The key directory is reused as
// found, which is the name Claude will look for under the new account too.
//
// Two accounts sharing a config directory — an API-key account beside a
// subscription one — share the conversation already, so nothing moves. Neither
// does a conversation that was never written (a session with no turns yet).
func (a *Adapter) MoveConversation(from, to map[string]string, cwd, id string) error {
	if id == "" {
		return nil
	}
	src, dst := claudeConfigDir(cwd, from), claudeConfigDir(cwd, to)
	if src == dst {
		return nil
	}
	file, err := findConversation(src, cwd, id)
	if err != nil || file == "" {
		return err
	}
	key := filepath.Base(filepath.Dir(file))
	destDir := filepath.Join(dst, "projects", key)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("prepare the other account's Claude projects directory: %w", err)
	}
	destFile := filepath.Join(destDir, id+".jsonl")
	if _, err := os.Lstat(destFile); err == nil {
		return fmt.Errorf("the other account already has a Claude conversation %s; not overwriting it", id)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := move(file, destFile); err != nil {
		return fmt.Errorf("move the Claude conversation: %w", err)
	}
	// Subagent transcripts are part of the conversation but not needed to
	// resume it, so a failure here is rolled back rather than half-applied:
	// the conversation stays whole under one account.
	side := filepath.Join(filepath.Dir(file), id)
	if info, err := os.Lstat(side); err == nil && info.IsDir() {
		destSide := filepath.Join(destDir, id)
		if _, err := os.Lstat(destSide); err == nil {
			_ = move(destFile, file)
			return fmt.Errorf("the other account already has Claude subagent transcripts for %s; not overwriting them", id)
		}
		if err := move(side, destSide); err != nil {
			_ = move(destFile, file)
			return fmt.Errorf("move the Claude subagent transcripts: %w", err)
		}
	}
	return nil
}

// findConversation returns the path of conversation id under configDir, or ""
// when there is none. The cwd's own project directory is checked first; a
// conversation filed under another key (the cwd moved, or Claude shortened
// the key) is found by scanning.
func findConversation(configDir, cwd, id string) (string, error) {
	name := id + ".jsonl"
	projects := filepath.Join(configDir, "projects")
	direct := filepath.Join(projects, nonAlphanumeric.ReplaceAllString(filepath.Clean(cwd), "-"), name)
	if info, err := os.Stat(direct); err == nil && info.Mode().IsRegular() {
		return direct, nil
	}
	matches, err := filepath.Glob(filepath.Join(projects, "*", name))
	if err != nil {
		return "", err
	}
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.Mode().IsRegular() {
			return m, nil
		}
	}
	return "", nil
}

// nonAlphanumeric is how Claude Code turns a cwd into its project key.
var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

// move renames, falling back to a copy when the two accounts' config
// directories sit on different filesystems. The source is first renamed aside
// (same filesystem, so atomic): a failed copy puts it straight back, and once
// the copy is whole the conversation has moved — the leftover is under a name
// Claude never reads, so failing to delete it cannot split the history.
func move(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	aside := filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")
	_ = os.RemoveAll(aside) // a leftover from an earlier move
	if err := os.Rename(src, aside); err != nil {
		return err
	}
	if err := copyTree(aside, dst); err != nil {
		_ = os.RemoveAll(dst)
		if back := os.Rename(aside, src); back != nil {
			return fmt.Errorf("%w (and restoring %s failed: %v)", err, src, back)
		}
		return err
	}
	_ = os.RemoveAll(aside)
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
