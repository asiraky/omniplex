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
	cmd.SysProcAttr.Setpgid = true
	return pgidGroup{cmd}
}

func (g pgidGroup) Kill() {
	if g.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
}
