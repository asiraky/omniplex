package server

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"time"
)

// processCwd is the working directory of the process pid, or "" when it
// cannot be read. macOS has no /proc, and its proc_pidinfo needs cgo, so this
// asks lsof, which ships with the OS and answers for one pid in milliseconds.
// The timeout is for the lsof that hangs on a stale network mount.
func processCwd(ctx context.Context, pid int) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	return lsofName(out)
}

// lsofName picks the name field out of lsof's -F output: one field per line,
// tagged by its first byte, the path being the line tagged n.
func lsofName(out []byte) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(line) > 1 && line[0] == 'n' && line[1] == '/' {
			return string(line[1:])
		}
	}
	return ""
}
