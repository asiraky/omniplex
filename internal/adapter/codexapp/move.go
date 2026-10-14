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

// RolloutRoots are the folders of a Codex home that hold conversations: live
// rollouts under sessions/YYYY/MM/DD/ and archived ones.
var RolloutRoots = []string{"sessions", "archived_sessions"}

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
	rel, err := FindRollout(src, id)
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

// FindRollout returns the path, relative to home, of the rollout of
// conversation id, or "" when home has none.
func FindRollout(home, id string) (string, error) {
	suffix := "-" + id + ".jsonl"
	var found string
	for _, root := range RolloutRoots {
		err := filepath.WalkDir(filepath.Join(home, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			name := d.Name()
			if d.Type().IsRegular() && strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, suffix) {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if found != "" {
			return filepath.Rel(home, found)
		}
	}
	return "", nil
}
