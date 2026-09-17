//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
)

type pgidGroup struct{ cmd *exec.Cmd }

func attachPgid(cmd *exec.Cmd) Group {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// A session leader (Setsid, as a pty child is) already leads its own
	// process group; asking for setpgid on top of setsid makes clone fail
	// with EPERM.
	if !cmd.SysProcAttr.Setsid {
		cmd.SysProcAttr.Setpgid = true
	}
	return pgidGroup{cmd}
}

func (g pgidGroup) Kill() {
	if g.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
}
