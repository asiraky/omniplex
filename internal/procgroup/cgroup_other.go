//go:build !linux

package procgroup

import (
	"errors"
	"os/exec"
)

func attachCgroup(*exec.Cmd, string) (Group, bool) { return nil, false }
func sweepCgroups()                                {}

func limitMemory(int) (int64, error) {
	return 0, errors.New("per-tree memory caps need Linux cgroup v2")
}
