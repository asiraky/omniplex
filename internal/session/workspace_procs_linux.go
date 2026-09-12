//go:build linux

package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanCWDs reads every working directory out of /proc.
//
// Cheap and exact: the kernel keeps the answer, and it even says which of them
// point at a directory that has already been unlinked. cwd is unreadable for
// processes belonging to another user or hidden by hidepid, and those are
// skipped rather than guessed at.
func scanCWDs() []procCWD {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	out := make([]procCWD, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil || pid == self {
			continue
		}
		cwd, linkErr := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if linkErr != nil {
			continue
		}
		// A deleted cwd reads back as "/path/to/dir (deleted)".
		trimmed, deleted := strings.CutSuffix(cwd, " (deleted)")
		out = append(out, procCWD{PID: pid, Name: procName(pid), CWD: trimmed, Deleted: deleted})
	}
	return out
}

func procName(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
