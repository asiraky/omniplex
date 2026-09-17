// Package procgroup gives each child the server starts a process tree that can
// be torn down as a whole.
//
// Why: a harness session is not one process. Claude Code forks shells, the
// shells start dev servers and headless browsers, and those double-fork and
// setsid until nothing links them to the session that started them. Killing
// the harness left every one of them behind, inside the server's cgroup, and
// on the always-on box they accumulated until the machine ran out of memory.
//
// On Linux under cgroup v2 each child is placed, atomically at clone time,
// into a cgroup of its own beneath the server's, and Kill writes cgroup.kill:
// one syscall that ends every process in the tree no matter how it detached.
// Memory stays charged to the server's cgroup, so the service's limits still
// cover the whole tree. Elsewhere, or when the cgroup filesystem is not
// writable, the child gets a process group and Kill signals that instead;
// that catches ordinary children but not anything that called setsid.
package procgroup

import (
	"os/exec"
)

// Group is one child's tree.
type Group interface {
	// Kill ends every process in the tree. It is safe to call more than once
	// and after the child has already exited.
	Kill()
}

// Attach prepares cmd so that its whole tree is killable through the returned
// Group. It must be called before cmd.Start. name distinguishes the group
// from its siblings; it need not be unique across restarts.
func Attach(cmd *exec.Cmd, name string) Group {
	if g, ok := attachCgroup(cmd, name); ok {
		return g
	}
	return attachPgid(cmd)
}

// Sweep kills whatever a previous server left behind: trees whose owner died
// without calling Kill (a crash, a SIGKILL). Call it once at startup, before
// starting any child.
func Sweep() {
	sweepCgroups()
}
