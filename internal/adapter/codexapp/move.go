package codexapp

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/convmove"
)

var _ adapter.ConversationMover = (*Adapter)(nil)

// rolloutRoots are the folders of a Codex home that hold conversations: live
// rollouts under sessions/YYYY/MM/DD/ and archived ones.
var rolloutRoots = []string{"sessions", "archived_sessions"}

// MoveConversation hands a Codex conversation from one account to another.
// Codex keeps each conversation as one rollout file under its home
// (CODEX_HOME), at sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl, and resumes
// a thread by finding that file under the running account's home. The file
// keeps its path relative to the home, so the other account finds it where
// Codex would have filed it.
//
// Two accounts sharing a home share the conversation already, and a thread
// that never wrote a rollout has nothing to move; neither is an error. An
// existing rollout under the other account is never overwritten.
func (a *Adapter) MoveConversation(from, to map[string]string, cwd, id string) error {
	if id == "" {
		return nil
	}
	src, dst := filepath.Clean(codexHome(from)), filepath.Clean(codexHome(to))
	if src == dst {
		return nil
	}
	rel, err := findRollout(src, id)
	if err != nil || rel == "" {
		return err
	}
	if _, err := convmove.Move(filepath.Join(src, rel), filepath.Join(dst, rel), false); err != nil {
		if errors.Is(err, convmove.ErrExists) {
			return fmt.Errorf("the other account already has the Codex conversation %s; not overwriting it", id)
		}
		return fmt.Errorf("move the Codex conversation: %w", err)
	}
	return nil
}

// findRollout returns the path, relative to home, of the rollout of
// conversation id, or "" when home has none.
func findRollout(home, id string) (string, error) {
	suffix := "-" + id + ".jsonl"
	var found string
	err := WalkRollouts(home, func(path string) bool {
		if strings.HasSuffix(filepath.Base(path), suffix) {
			found = path
			return false
		}
		return true
	})
	if err != nil || found == "" {
		return "", err
	}
	return filepath.Rel(home, found)
}

// WalkRollouts calls fn with the path of every rollout file under home's
// rollout roots, live ones first, until fn returns false. Hidden folders (a
// move's aside, anything else) are skipped, and a root that is not there
// has nothing in it.
func WalkRollouts(home string, fn func(path string) bool) error {
	for _, root := range rolloutRoots {
		top := filepath.Join(home, root)
		stop := false
		err := filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			name := d.Name()
			if d.IsDir() && strings.HasPrefix(name, ".") && path != top {
				return fs.SkipDir
			}
			if d.Type().IsRegular() && strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") && !fn(path) {
				stop = true
				return fs.SkipAll
			}
			return nil
		})
		if err != nil || stop {
			return err
		}
	}
	return nil
}
