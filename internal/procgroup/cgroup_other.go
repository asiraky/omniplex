//go:build !linux

package procgroup

import "os/exec"

func attachCgroup(*exec.Cmd, string) (Group, bool) { return nil, false }
func sweepCgroups()                                {}
