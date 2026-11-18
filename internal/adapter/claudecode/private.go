package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/ephemeral"
)

// Claude Code keeps a conversation in its config folder, which is on disk,
// and resumes only what it finds there. A private thread still has to resume
// (after an account switch, a usage limit, a crash), so rather than keep no
// transcript, Claude's project folder for the thread's cwd is a link into the
// scratch folder: Claude writes and reads its transcript there, in memory.

// projectKey is the folder Claude Code files a cwd's conversations under.
func projectKey(cwd string) string {
	return nonAlphanumeric.ReplaceAllString(filepath.Clean(cwd), "-")
}

// privateTranscripts links Claude's project folder for cwd under configDir,
// and the session's environment folder, to the private thread's state, and reports whether cwd is a private thread's at
// all. Every account's config folder links to the same place, so the
// conversation is under each of them at once.
func privateTranscripts(configDir, cwd string) (bool, error) {
	id := ephemeral.ThreadOf(cwd)
	if id == "" {
		return false, nil
	}
	link := filepath.Join(configDir, "projects", projectKey(cwd))
	if err := ephemeral.Link(id, link, ephemeral.StateDir(id, "claude-projects")); err != nil {
		return true, err
	}
	// The session's environment folder is named for the conversation, so it
	// goes the same way.
	env := filepath.Join(configDir, "session-env", ephemeral.Bare(id))
	return true, ephemeral.Link(id, env, ephemeral.StateDir(id, "claude-session-env"))
}

// privateEnv is what keeps the rest of a private thread's Claude scratch in
// its state folder too: task output (CLAUDE_CODE_TMPDIR, otherwise the system
// temp folder) and MCP logs (XDG_CACHE_HOME, otherwise ~/.cache), both of
// which Claude files under the cwd's name with the session id inside. The
// folders are made. Nil for a folder that is no private thread's.
func privateEnv(cwd string) (map[string]string, error) {
	id := ephemeral.ThreadOf(cwd)
	if id == "" {
		return nil, nil
	}
	env := map[string]string{
		"CLAUDE_CODE_TMPDIR": ephemeral.StateDir(id, "claude-tmp"),
		"XDG_CACHE_HOME":     ephemeral.StateDir(id, "cache"),
	}
	for _, dir := range env {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// sweepPrivateLinks removes the links under configDir left by private threads
// of an earlier run of this server: its scratch folder was emptied when it
// started, so they point at nothing. Only a link into this server's scratch
// folder is touched. Best effort.
func sweepPrivateLinks(configDir string) {
	scratch := ephemeral.Dir() + string(filepath.Separator)
	projects, _ := filepath.Glob(filepath.Join(configDir, "projects", projectKey(filepath.Join(ephemeral.Dir(), "threads"))+"-*"))
	envs, _ := filepath.Glob(filepath.Join(configDir, "session-env", "*"))
	for _, m := range append(projects, envs...) {
		target, err := os.Readlink(m)
		if err != nil || !strings.HasPrefix(target, scratch) {
			continue
		}
		if _, err := os.Stat(m); errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(m)
		}
	}
}
