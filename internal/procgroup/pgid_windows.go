package procgroup

import "os/exec"

type pgidGroup struct{ cmd *exec.Cmd }

func attachPgid(cmd *exec.Cmd) Group { return pgidGroup{cmd} }

func (g pgidGroup) Kill() {
	if g.cmd.Process != nil {
		_ = g.cmd.Process.Kill()
	}
}
