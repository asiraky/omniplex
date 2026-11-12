package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/procgroup"
)

// FactVersion is the Availability fact every adapter reports its harness
// CLI's version under: the bare version number, so a UI can show it without
// knowing which harness printed it.
const FactVersion = "version"

// Executable is implemented by an adapter whose harness is a program on disk.
// It names the binary a session would run right now, found the same way a
// session finds it. The core watches it: a binary that changed under a
// running server has a different catalogue than the one cached.
type Executable interface {
	Executable() (string, bool)
}

// Updater is implemented by an adapter whose harness CLI can update itself in
// place. Nobody logs in to a server that only runs harnesses through omniplex,
// and at least Claude Code's own updater only runs from its interactive UI, so
// without this the CLI stays at whatever version was installed by hand.
type Updater interface {
	// Update runs the harness's own updater under an instance's environment
	// overlay (nil means ambient) and reports the version before and after.
	// It must leave running sessions alone: a process already started keeps
	// the binary it started with. An updater that fails returns an error
	// that says why in terms a person can act on.
	Update(ctx context.Context, env map[string]string) (UpdateResult, error)
}

// UpdateResult is one update run. From and To are equal when there was
// nothing newer to install.
type UpdateResult struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Changed says whether the run installed a different version.
func (r UpdateResult) Changed() bool { return r.From != r.To }

// FileIdentity is a binary's identity on disk: the path after symlinks, its
// size, and its modification time. One stat, cheap enough to check on every
// listing, and it changes whenever an install replaces the file or repoints a
// link at a new version. Empty when the file is not there.
func FileIdentity(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
}

// versionPattern is a dotted version with at least a major and a minor, and
// any semver prerelease or build suffix after it.
var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)*(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)

// VersionNumber is the bare version in what a CLI prints for --version:
// "2.1.300 (Claude Code)" and "codex-cli 0.162.1" both carry their name in
// different places, and neither name is any use beside a harness that is
// already labelled. Output with no version in it comes back trimmed, so
// something odd still shows as itself rather than as nothing.
func VersionNumber(s string) string {
	if v := versionPattern.FindString(s); v != "" {
		return v
	}
	return strings.TrimSpace(s)
}

// UpdateTimeout bounds an update run. A download over a slow link takes
// minutes; an updater that has not finished in ten is waiting on something
// that is not coming, and it must not hold the driver's start gate forever.
const UpdateTimeout = 10 * time.Minute

// failureTail caps how much of an updater's output an error carries: enough
// for the line that says what went wrong, short enough to show in a UI.
const failureTail = 300

// RunUpdate runs a harness's own updater, `bin args...`, and reports the
// version on either side of it. name is what the harness is called in an
// error: bin may resolve to a versioned file whose basename says nothing.
// version prints the harness's version as its
// --version does, or nothing when it cannot; it is called fresh each time
// because an install that repoints a link at a new file is only visible to
// a caller that looks the binary up again.
//
// The updater runs with no stdin, so one that wants to ask something fails
// instead of waiting for an answer nobody will give, under env (nil means
// ambient), and with its whole process tree killed if it outlives
// UpdateTimeout or an earlier deadline on ctx. A failure carries the end of
// what the updater printed, which is where updaters say why.
func RunUpdate(ctx context.Context, env []string, name, bin string, args []string, version func(context.Context) string) (UpdateResult, error) {
	label := strings.Join(append([]string{name}, args...), " ")

	from := VersionNumber(version(ctx))
	if from == "" {
		return UpdateResult{}, fmt.Errorf("could not read the %s version before updating", name)
	}

	runCtx, cancel := context.WithTimeout(ctx, UpdateTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	tree := procgroup.Attach(cmd, "update-"+name)
	defer tree.Kill()
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	// An installer's children can hold the output pipe open after it is
	// killed; without a bound on that, Wait would outlive the deadline.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()

	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		// The caller gave up, the server is likely shutting down; that is not
		// the updater's failure to report.
		return UpdateResult{From: from}, ctx.Err()
	case runCtx.Err() != nil:
		stuck := label + " did not finish in time"
		if tail := outputTail(out.String()); tail != "" {
			stuck += ": " + tail
		}
		return UpdateResult{From: from}, errors.New(stuck)
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if tail := outputTail(out.String()); tail != "" {
				return UpdateResult{From: from}, fmt.Errorf("%s failed: %s", label, tail)
			}
			return UpdateResult{From: from}, fmt.Errorf("%s exited with status %d", label, exit.ExitCode())
		}
		return UpdateResult{From: from}, fmt.Errorf("%s could not run: %w", label, err)
	}

	to := VersionNumber(version(ctx))
	if to == "" {
		return UpdateResult{From: from}, fmt.Errorf("could not read the %s version after updating", name)
	}
	return UpdateResult{From: from, To: to}, nil
}

// ansiEscape matches the colour and cursor sequences an updater prints even
// when nobody is watching.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// outputTail is the last few non-blank lines of an updater's output, on one
// line and capped at failureTail. Progress bars redraw with a carriage
// return, so those split lines too: only the last frame says anything.
func outputTail(output string) string {
	output = ansiEscape.ReplaceAllString(output, "")
	lines := strings.FieldsFunc(output, func(r rune) bool { return r == '\n' || r == '\r' })
	var tail []string
	size := 0
	for i := len(lines) - 1; i >= 0 && len(tail) < 3; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if size > 0 && size+len(line)+1 > failureTail {
			break
		}
		tail = append([]string{line}, tail...)
		size += len(line) + 1
	}
	msg := strings.Join(tail, " ")
	if r := []rune(msg); len(r) > failureTail {
		msg = string(r[:failureTail-1]) + "…"
	}
	return msg
}
