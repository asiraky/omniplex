package procgroup

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startTree runs a shell with two sleeping children, one of them detached
// with setsid so no process group covers it, and returns once all are up.
func startTree(t *testing.T, attach func(*exec.Cmd) Group) (*exec.Cmd, Group) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "setsid sleep 300 & sleep 300 & echo ready; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	g := attach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("shell did not report ready: %q %v", line, err)
	}
	return cmd, g
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func waitGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var left []int
		for _, pid := range pids {
			if alive(pid) {
				left = append(left, pid)
			}
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("still alive after Kill: %v", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCgroupKillEndsDetachedDescendants(t *testing.T) {
	var g Group
	cmd, _ := startTree(t, func(c *exec.Cmd) Group {
		cg, ok := attachCgroup(c, "test/it")
		if !ok {
			t.Skip("cgroup v2 not writable here")
		}
		g = cg
		return cg
	})
	dir := g.(*cgroupGroup).dir
	if base := filepath.Base(dir); !strings.HasPrefix(base, prefix+strconv.Itoa(os.Getpid())+"-test_it-") {
		t.Fatalf("cgroup name %q not sanitised under prefix", base)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, f := range strings.Fields(string(raw)) {
		pid, _ := strconv.Atoi(f)
		pids = append(pids, pid)
	}
	// The shell plus two sleeps; the setsid one must be in here too.
	if len(pids) < 3 {
		t.Fatalf("expected the whole tree in the cgroup, got %v", pids)
	}
	if cmd.Process.Pid != pids[0] && !contains(pids, cmd.Process.Pid) {
		t.Fatalf("child %d not in its cgroup %v", cmd.Process.Pid, pids)
	}

	g.Kill()
	_ = cmd.Wait() // reap the shell; a zombie still answers kill(pid, 0)
	waitGone(t, pids)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cgroup %s not removed after kill", dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
	g.Kill() // idempotent after teardown
}

// deadPid is a pid no process holds: that of a child already reaped.
func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

func TestSweepKillsGroupsOfDeadOwnersOnly(t *testing.T) {
	var stale, live Group
	staleCmd, _ := startTree(t, func(c *exec.Cmd) Group {
		cg, ok := attachCgroupOwned(c, deadPid(t), "stale")
		if !ok {
			t.Skip("cgroup v2 not writable here")
		}
		stale = cg
		return cg
	})
	liveCmd, _ := startTree(t, func(c *exec.Cmd) Group {
		cg, _ := attachCgroup(c, "live")
		live = cg
		return cg
	})

	Sweep()

	_ = staleCmd.Wait()
	waitGone(t, []int{staleCmd.Process.Pid})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(stale.(*cgroupGroup).dir); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweep left the dead owner's group %s behind", stale.(*cgroupGroup).dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !alive(liveCmd.Process.Pid) {
		t.Fatal("sweep killed a group whose owner is alive")
	}
	if _, err := os.Stat(live.(*cgroupGroup).dir); err != nil {
		t.Fatalf("sweep removed the live owner's group: %v", err)
	}
}

func TestPgidKillEndsOrdinaryChildren(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 300 & echo ready; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	g := attachPgid(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Kill(); _ = cmd.Wait() })
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("shell did not report ready: %q", line)
	}
	g.Kill()
	_ = cmd.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(-cmd.Process.Pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("process group still has members after Kill")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}
