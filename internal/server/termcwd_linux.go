package server

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// processCwd is the working directory of the process pid, or "" when it
// cannot be read. /proc/<pid>/cwd is the process's own: a shell's cd moves it,
// and what the shell is running has no say.
func processCwd(_ context.Context, pid int) string {
	dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	// A directory removed from under the shell reads as "<path> (deleted)";
	// nothing resolves against it any more.
	if err != nil || strings.HasSuffix(dir, " (deleted)") {
		return ""
	}
	return dir
}
