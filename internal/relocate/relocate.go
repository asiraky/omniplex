// Package relocate repairs hy's durable and harness-side state after a project
// directory has been moved while hy was stopped.
package relocate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/asiraky/omniplex/internal/store"
)

type Options struct {
	DBPath string
	// HomeDir selects ~/.omniplex/workspaces and the default Claude config root.
	HomeDir string
	// ClaudeConfigDirs holds CLAUDE_CONFIG_DIR values by provider instance.
	// Values may be absolute or relative to each thread's working directory.
	ClaudeConfigDirs map[string]string
	// ClaudeConfigDir is the ambient CLAUDE_CONFIG_DIR used when an instance
	// does not override it.
	ClaudeConfigDir string
	GitBin          string
}

type Report struct {
	Database          store.RelocateStats
	WorkspaceFiles    int
	ClaudeTranscripts int
	GitRepaired       bool
}

type fileChange struct {
	path string
	old  []byte
	new  []byte
	mode fs.FileMode
}

type move struct{ old, new string }

func Run(ctx context.Context, oldRoot, newRoot string, options Options) (Report, error) {
	oldRoot, err := filepath.Abs(oldRoot)
	if err != nil {
		return Report{}, fmt.Errorf("resolve old root: %w", err)
	}
	newRoot, err = filepath.Abs(newRoot)
	if err != nil {
		return Report{}, fmt.Errorf("resolve new root: %w", err)
	}
	oldRoot, newRoot = filepath.Clean(oldRoot), filepath.Clean(newRoot)
	if oldRoot == newRoot {
		return Report{}, errors.New("old and new roots are the same")
	}
	info, err := os.Stat(newRoot)
	if err != nil {
		return Report{}, fmt.Errorf("new root: %w", err)
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("new root is not a directory: %s", newRoot)
	}
	if _, err := os.Stat(oldRoot); err == nil {
		return Report{}, fmt.Errorf("old root still exists: %s", oldRoot)
	} else if !os.IsNotExist(err) {
		return Report{}, fmt.Errorf("inspect old root: %w", err)
	}
	mappings, err := relocationPaths(oldRoot, newRoot)
	if err != nil {
		return Report{}, err
	}
	if options.DBPath == "" {
		return Report{}, errors.New("database path is required")
	}
	if options.HomeDir == "" {
		return Report{}, errors.New("home directory is required")
	}
	if info, err := os.Stat(options.DBPath); err != nil {
		return Report{}, fmt.Errorf("database: %w", err)
	} else if info.IsDir() {
		return Report{}, fmt.Errorf("database path is a directory: %s", options.DBPath)
	}

	st, err := store.Open(options.DBPath)
	if err != nil {
		return Report{}, fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	projects, err := st.ListProjects(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list projects: %w", err)
	}
	projectIDs := map[string]bool{}
	for _, project := range projects {
		for _, folder := range project.Folders {
			if filepath.Clean(folder.Path) == oldRoot {
				projectIDs[project.ID] = true
			}
		}
	}
	if len(projectIDs) == 0 {
		return Report{}, fmt.Errorf("no project has the folder %s", oldRoot)
	}

	threads, err := st.ListThreads(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list threads: %w", err)
	}
	var projectThreads []store.ThreadMeta
	for _, thread := range threads {
		_, underOldRoot := replacePath(thread.Cwd, mappings)
		if projectIDs[thread.ProjectID] || underOldRoot {
			projectThreads = append(projectThreads, thread)
		}
	}

	files, err := planWorkspaceFiles(options.HomeDir, projectThreads, mappings)
	if err != nil {
		return Report{}, err
	}
	moves, err := planClaudeMoves(options, projectThreads, mappings)
	if err != nil {
		return Report{}, err
	}

	report := Report{}
	if report.GitRepaired, err = repairGit(ctx, options.GitBin, newRoot, projectThreads, mappings); err != nil {
		return Report{}, err
	}

	var written []fileChange
	var moved []move
	rollback := func() {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].new, moved[i].old)
		}
		for i := len(written) - 1; i >= 0; i-- {
			_ = atomicWrite(written[i].path, written[i].old, written[i].mode)
		}
	}
	for _, change := range files {
		if err := atomicWrite(change.path, change.new, change.mode); err != nil {
			rollback()
			return Report{}, fmt.Errorf("rewrite %s: %w", change.path, err)
		}
		written = append(written, change)
	}
	for _, change := range moves {
		if err := os.MkdirAll(filepath.Dir(change.new), 0o700); err != nil {
			rollback()
			return Report{}, fmt.Errorf("create Claude projects directory: %w", err)
		}
		if err := os.Rename(change.old, change.new); err != nil {
			rollback()
			return Report{}, fmt.Errorf("move Claude transcript %s: %w", change.old, err)
		}
		moved = append(moved, change)
	}

	report.Database, err = st.RelocateFolder(ctx, oldRoot, newRoot, mappings[1:]...)
	if err != nil {
		rollback()
		return Report{}, fmt.Errorf("rewrite database: %w", err)
	}
	report.WorkspaceFiles = len(files)
	report.ClaudeTranscripts = len(moves)
	return report, nil
}

func planWorkspaceFiles(home string, threads []store.ThreadMeta, mappings []store.RelocationPath) ([]fileChange, error) {
	var changes []fileChange
	for _, thread := range threads {
		dir := filepath.Join(home, ".omniplex", "workspaces", thread.ID)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read workspace state %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			blob, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read workspace state %s: %w", path, err)
			}
			next, changed, err := rewriteJSON(blob, mappings)
			if err != nil {
				return nil, fmt.Errorf("parse workspace state %s: %w", path, err)
			}
			if !changed {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			changes = append(changes, fileChange{path: path, old: blob, new: append(next, '\n'), mode: info.Mode()})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	return changes, nil
}

func planClaudeMoves(options Options, threads []store.ThreadMeta, mappings []store.RelocationPath) ([]move, error) {
	seen := map[string]bool{}
	var moves []move
	for _, thread := range threads {
		if thread.Harness != "claude" {
			continue
		}
		oldCwd := filepath.Clean(thread.Cwd)
		newCwd, ok := replacePath(oldCwd, mappings)
		if !ok {
			continue
		}
		instance := thread.ProviderInstance
		if instance == "" {
			instance = "claude"
		}
		configured, overridden := options.ClaudeConfigDirs[instance]
		if !overridden {
			configured = options.ClaudeConfigDir
		}
		oldConfig := claudeConfigDir(options.HomeDir, oldCwd, configured)
		// A relative or in-project config directory moved with the checkout.
		if relocated, ok := replacePath(oldConfig, mappings); ok {
			oldConfig = relocated
		}
		newConfig := claudeConfigDir(options.HomeDir, newCwd, configured)
		if relocated, ok := replacePath(newConfig, mappings); ok {
			newConfig = relocated
		}
		source := filepath.Join(oldConfig, "projects", claudeProjectKey(oldCwd))
		destination := filepath.Join(newConfig, "projects", claudeProjectKey(newCwd))
		key := source + "\x00" + destination
		if seen[key] || source == destination {
			continue
		}
		seen[key] = true
		if _, err := os.Stat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect Claude transcript %s: %w", source, err)
		}
		if _, err := os.Stat(destination); err == nil {
			return nil, fmt.Errorf("Claude transcript destination already exists: %s", destination)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect Claude transcript destination %s: %w", destination, err)
		}
		moves = append(moves, move{old: source, new: destination})
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].old < moves[j].old })
	return moves, nil
}

func repairGit(ctx context.Context, gitBin, root string, threads []store.ThreadMeta, mappings []store.RelocationPath) (bool, error) {
	if gitBin == "" {
		gitBin = "git"
	}
	probe := exec.CommandContext(ctx, gitBin, "-C", root, "rev-parse", "--git-dir")
	if err := probe.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, nil // Non-Git projects are valid hy projects.
		}
		return false, fmt.Errorf("inspect Git checkout: %w", err)
	}
	args := []string{"-C", root, "worktree", "repair"}
	seen := map[string]bool{}
	commonDirOutput, _ := exec.CommandContext(ctx, gitBin, "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	commonDir := strings.TrimSpace(string(commonDirOutput))
	if commonDir != "" {
		entries, _ := os.ReadDir(filepath.Join(commonDir, "worktrees"))
		for _, entry := range entries {
			gitdir, err := os.ReadFile(filepath.Join(commonDir, "worktrees", entry.Name(), "gitdir"))
			if err != nil {
				continue
			}
			oldPath := filepath.Dir(strings.TrimSpace(string(gitdir)))
			newPath, _ := replacePath(oldPath, mappings)
			if _, err := os.Stat(filepath.Join(newPath, ".git")); err == nil && !seen[newPath] {
				args = append(args, newPath)
				seen[newPath] = true
			}
		}
	}
	for _, thread := range threads {
		cwd, ok := replacePath(thread.Cwd, mappings)
		if !ok || cwd == root || seen[cwd] {
			continue
		}
		if info, err := os.Stat(filepath.Join(cwd, ".git")); err == nil && !info.IsDir() {
			args = append(args, cwd)
			seen[cwd] = true
		}
	}
	command := exec.CommandContext(ctx, gitBin, args...)
	if output, err := command.CombinedOutput(); err != nil {
		return false, fmt.Errorf("repair Git worktrees: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return true, nil
}

func claudeConfigDir(home, cwd, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(home, ".claude")
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	return filepath.Join(cwd, configured)
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func claudeProjectKey(path string) string {
	return nonAlphanumeric.ReplaceAllString(filepath.Clean(path), "-")
}

func relocationPaths(oldRoot, newRoot string) ([]store.RelocationPath, error) {
	mappings := []store.RelocationPath{{Old: oldRoot, New: newRoot}}
	canonicalOld, err := canonicalMissingPath(oldRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve old root aliases: %w", err)
	}
	canonicalNew, err := filepath.EvalSymlinks(newRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve new root aliases: %w", err)
	}
	canonicalOld, canonicalNew = filepath.Clean(canonicalOld), filepath.Clean(canonicalNew)
	if canonicalOld != oldRoot || canonicalNew != newRoot {
		mappings = append(mappings, store.RelocationPath{Old: canonicalOld, New: canonicalNew})
	}
	return mappings, nil
}

// canonicalMissingPath resolves the longest existing ancestor, then restores
// the absent suffix. This recovers the canonical spelling of a root after that
// root itself has already been moved away.
func canonicalMissingPath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func replacePath(path string, mappings []store.RelocationPath) (string, bool) {
	path = filepath.Clean(path)
	for _, mapping := range mappings {
		oldRoot := filepath.Clean(mapping.Old)
		if path == oldRoot {
			return mapping.New, true
		}
		prefix := oldRoot + string(filepath.Separator)
		if strings.HasPrefix(path, prefix) {
			return filepath.Join(mapping.New, strings.TrimPrefix(path, prefix)), true
		}
	}
	return path, false
}

func rewriteJSON(blob []byte, mappings []store.RelocationPath) ([]byte, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(blob))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, false, err
	}
	changed := rewriteValue(&value, mappings)
	if !changed {
		return blob, false, nil
	}
	next, err := json.MarshalIndent(value, "", "  ")
	return next, true, err
}

func rewriteValue(value *any, mappings []store.RelocationPath) bool {
	switch current := (*value).(type) {
	case string:
		if next, ok := replacePath(current, mappings); ok {
			*value = next
			return true
		}
	case []any:
		changed := false
		for i := range current {
			changed = rewriteValue(&current[i], mappings) || changed
		}
		return changed
	case map[string]any:
		changed := false
		for key, item := range current {
			if rewriteValue(&item, mappings) {
				current[key] = item
				changed = true
			}
		}
		return changed
	}
	return false
}

func atomicWrite(path string, blob []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".relocate-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode.Perm()); err != nil {
		return err
	}
	return os.Rename(name, path)
}
