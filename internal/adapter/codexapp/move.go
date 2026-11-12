package codexapp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
)

var _ adapter.ConversationMover = (*Adapter)(nil)

// rolloutDirs are the folders under a Codex home that hold rollouts, each
// filed by date as YYYY/MM/DD/rollout-<timestamp>-<thread id>.jsonl.
var rolloutDirs = []string{"sessions", "archived_sessions"}

// MoveConversation hands a thread from one Codex account to another. Codex
// keeps a thread's history as rollout files under its CODEX_HOME, and resume
// reads them from the running account's home, so moving every rollout of the
// thread is the switch. Each keeps its path relative to the home, date
// folders included.
//
// Nothing is ever overwritten: a rollout the other account already has stops
// the move, and the ones moved before it are put back, so the thread is never
// split across two homes. Two accounts sharing a home share the thread
// already, and a thread with no rollout yet has nothing to move.
func (a *Adapter) MoveConversation(from, to map[string]string, cwd, id string) error {
	if id == "" {
		return nil
	}
	src, dst := filepath.Clean(codexHome(from)), filepath.Clean(codexHome(to))
	if src == dst {
		return nil
	}
	rollouts, err := findRollouts(src, id)
	if err != nil || len(rollouts) == 0 {
		return err
	}
	for _, rel := range rollouts {
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			return fmt.Errorf("the other account already has the Codex rollout %s; not overwriting it", rel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	var moved []string
	for _, rel := range rollouts {
		if err := move(filepath.Join(src, rel), filepath.Join(dst, rel)); err != nil {
			for _, back := range moved {
				_ = move(filepath.Join(dst, back), filepath.Join(src, back))
			}
			return fmt.Errorf("move the Codex rollout %s: %w", rel, err)
		}
		moved = append(moved, rel)
	}
	return nil
}

// findRollouts returns every rollout of thread id under home, relative to it.
// The id ends the file name, so a suffix match is exact.
func findRollouts(home, id string) ([]string, error) {
	suffix := "-" + id + ".jsonl"
	var out []string
	for _, dir := range rolloutDirs {
		root := filepath.Join(home, dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == root && errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			name := d.Name()
			if d.Type().IsRegular() && strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, suffix) {
				rel, err := filepath.Rel(home, path)
				if err != nil {
					return err
				}
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// move moves one rollout without ever replacing anything. Across filesystems
// accounthome.Move copies and leaves the source aside under a name Codex
// never reads; once the copy is whole the rollout has moved, so the leftover
// goes.
func move(src, dst string) error {
	aside, err := accounthome.Move(src, dst)
	if aside != "" {
		_ = os.RemoveAll(aside)
	}
	return err
}
