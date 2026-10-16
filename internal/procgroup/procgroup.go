// Package procgroup gives each child the server starts a process tree that can
// be torn down as a whole.
//
// Why: a harness session is not one process. Claude Code forks shells, the
// shells start dev servers and headless browsers, and those double-fork and
// setsid until nothing links them to the thread that started them. Killing
// the harness left every one of them behind, inside the server's cgroup, and
// on the always-on box they accumulated until the machine ran out of memory.
//
// On Linux under cgroup v2 each child is placed, atomically at clone time,
// into a cgroup of its own beneath the server's, and Kill writes cgroup.kill:
// one syscall that ends every process in the tree no matter how it detached.
// Memory stays charged to the server's cgroup, so the service's limits still
// cover the whole tree, and LimitMemory can cap each tree and all of them
// together on top. Elsewhere, or when the cgroup filesystem is not
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

// Limits are the caps LimitMemory set, in bytes; 0 is no cap.
type Limits struct {
	// PerTree caps each tree on its own.
	PerTree int64
	// AllTrees caps every tree together. The server is outside it.
	AllTrees int64
}

// LimitMemory caps every tree Attach starts from now on at perTree percent of
// the machine's RAM, with no swap, and all of them together at allTrees
// percent; 0 leaves that cap off. A tree over its cap, or any tree once all
// of them are over theirs, loses the largest process to the kernel's OOM
// killer, and nothing else: one runaway type check or test run fails on its
// own instead of dragging the machine into swap. The server itself is outside
// both caps and keeps a reserve the trees cannot reclaim, so it stays
// responsive while they are killed. Call it once at startup, after Sweep. It
// fails, leaving trees uncapped, off Linux and wherever the server shares its
// cgroup with other processes; under systemd the unit needs Delegate=yes.
func LimitMemory(perTree, allTrees int) (Limits, error) {
	return limitMemory(perTree, allTrees)
}
