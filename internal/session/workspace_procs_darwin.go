//go:build darwin

package session

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// scanCWDs reads every working directory out of lsof.
//
// macOS has no /proc, and the syscall that answers this (proc_pidinfo) is only
// reachable through cgo. lsof ships with the system, already knows how to ask,
// and is only run when a workspace is about to be deleted — a third of a
// second, once, against a delete that otherwise wedges the worktree. Without
// this the check silently found nothing on the machine most of this project is
// developed on, which is the same as not having it.
//
// -F is lsof's stable, parseable output: one field per line, tagged by its
// first byte. "p" opens a process (and its "c" name), and every following line
// belongs to it until the next "p".
func scanCWDs() []procCWD {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// -d cwd: working directories only. -w: no warnings about the file systems
	// it could not stat, which are not our business here.
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-F", "pcn", "-w").Output()
	// A non-zero exit still carries the processes it did manage to read —
	// lsof reports partial failures that way — so the output is parsed either
	// way and only a total absence is given up on.
	if len(out) == 0 && err != nil {
		return nil
	}

	self := os.Getpid()
	var found []procCWD
	var cur procCWD
	flush := func() {
		if cur.PID != 0 && cur.PID != self && cur.CWD != "" {
			// macOS does not mark an unlinked working directory the way Linux
			// does, so it is inferred: the path lsof reports no longer
			// resolving to anything means the directory it names is gone. A
			// directory since recreated at the same path reads as live, which
			// is the safe way round — it counts, and a delete is refused.
			if _, statErr := os.Stat(cur.CWD); statErr != nil && os.IsNotExist(statErr) {
				cur.Deleted = true
			}
			found = append(found, cur)
		}
		cur = procCWD{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			flush()
			pid, convErr := strconv.Atoi(value)
			if convErr != nil {
				continue
			}
			cur.PID = pid
		case 'c':
			cur.Name = value
		case 'n':
			cur.CWD = value
		}
	}
	flush()
	return found
}
