//go:build !linux

package procgroup

import (
	"errors"
	"os/exec"
)

func attachCgroup(*exec.Cmd, string) (Group, bool) { return nil, false }
func sweepCgroups()                                {}

func limitMemory(int, int) (Limits, error) {
	return Limits{}, errors.New("memory caps need Linux cgroup v2")
}
