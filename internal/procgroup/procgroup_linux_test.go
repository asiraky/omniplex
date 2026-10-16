package procgroup

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestPgidAttachStartsAThreadLeader(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	g := attachPgid(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("setsid child failed to start: %v", err)
	}
	g.Kill()
	_ = cmd.Wait()
	waitGone(t, []int{cmd.Process.Pid})
}

// A plain go test run shares its cgroup with the shell that started it, so it
// cannot delegate the memory controller and this test skips. To run it, give
// the test binary a cgroup of its own:
//
//	go test -c -o /tmp/procgroup.test ./internal/procgroup
//	systemd-run --user --scope -p Delegate=yes /tmp/procgroup.test -test.run MemoryCap -test.v
func TestMemoryCapKillsOnlyTheLargestProcess(t *testing.T) {
	root, ok := baseCgroup()
	if !ok {
		t.Skip("no cgroup v2")
	}
	if err := delegateMemory(root, os.Getpid()); err != nil {
		t.Skipf("cannot delegate the memory controller here: %v", err)
	}
	memoryMax.Store(64 << 20)
	t.Cleanup(func() { memoryMax.Store(0) })

	// The sleep stands in for the harness. tail, buffering one endless line,
	// is the runaway compiler.
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $!; head -c 1G /dev/zero | tail; echo $?; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	g, ok := attachCgroup(cmd, "memcap")
	if !ok {
		t.Fatal("attach failed after delegation")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Kill(); _ = cmd.Wait() })

	r := bufio.NewReader(out)
	line, _ := r.ReadString('\n')
	sleeper, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("no sleeper pid: %q", line)
	}
	status, _ := r.ReadString('\n')
	if strings.TrimSpace(status) != "137" {
		t.Fatalf("hog exited %q, want 137 (SIGKILL)", strings.TrimSpace(status))
	}
	if !alive(sleeper) || !alive(cmd.Process.Pid) {
		t.Fatal("the OOM kill took more than the largest process")
	}
}

// TestHelperHog is not a test: TestPoolCap re-runs the test binary as this,
// to get a process that holds a known amount of memory. It touches every page
// of PROCGROUP_HOG_MB megabytes, says so, and waits to be killed.
func TestHelperHog(t *testing.T) {
	mb, err := strconv.Atoi(os.Getenv("PROCGROUP_HOG_MB"))
	if err != nil {
		t.Skip("helper process only")
	}
	buf := make([]byte, mb<<20)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(5 * time.Minute)
	runtime.KeepAlive(buf)
}

// startHog starts a hog of mb megabytes in a tree of its own and waits until
// it holds all of it. It returns the hog and its tree's cgroup.
func startHog(t *testing.T, mb int) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHog$")
	cmd.Env = append(os.Environ(), "PROCGROUP_HOG_MB="+strconv.Itoa(mb))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	g, ok := attachCgroup(cmd, "hog")
	if !ok {
		t.Fatal("attach failed after delegation")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Kill(); _ = cmd.Wait() })
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("hog of %d MB did not get its memory: %q", mb, line)
	}
	return cmd, g.(*cgroupGroup).dir
}

// memoryEvent reads one counter from a cgroup's memory.events.local.
func memoryEvent(t *testing.T, dir, key string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "memory.events.local"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	t.Fatalf("no %s in %s/memory.events.local", key, dir)
	return 0
}

// Each tree's cap alone let two type checks, each under it, fill the service
// between them. With the pool, two trees that only together go over its cap
// must lose the largest process, killed by the pool's limit and not their own.
//
// Only the first kill is asserted. A multi-threaded victim (a Go tsc, or this
// Go hog) leaves its cgroup a moment before its memory is uncharged, and an
// allocation landing in that moment kills the next largest process in the
// pool too. The server is outside the pool either way.
//
// Needs a cgroup of its own, like TestMemoryCapKillsOnlyTheLargestProcess:
//
//	go test -c -o /tmp/procgroup.test ./internal/procgroup
//	systemd-run --user --scope -p Delegate=yes /tmp/procgroup.test -test.run PoolCap -test.v
func TestPoolCapKillsTheLargestProcessAcrossTrees(t *testing.T) {
	root, ok := baseCgroup()
	if !ok {
		t.Skip("no cgroup v2")
	}
	if err := delegateMemory(root, os.Getpid()); err != nil {
		t.Skipf("cannot delegate the memory controller here: %v", err)
	}
	dir, err := makePool(root, 200<<20)
	if err != nil {
		t.Fatal(err)
	}
	treeParent.Store(&dir)
	memoryMax.Store(160 << 20)
	t.Cleanup(func() { treeParent.Store(nil); memoryMax.Store(0) })

	big, bigDir := startHog(t, 120)
	done := make(chan error, 1)
	go func() { done <- big.Wait() }()
	// The second hog may itself be killed before it reports ready; only what
	// happened to the first matters here.
	go func() {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHog$")
		cmd.Env = append(os.Environ(), "PROCGROUP_HOG_MB=90")
		g, _ := attachCgroup(cmd, "hog")
		if cmd.Start() == nil {
			t.Cleanup(func() { g.Kill(); _ = cmd.Wait() })
		}
	}()

	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("larger tree's hog ended with %v, want SIGKILL", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("trees over the pool cap together, and nothing was killed")
	}
	if n := memoryEvent(t, bigDir, "oom"); n != 0 {
		t.Fatalf("larger tree hit its own cap %d times; the test needs it under", n)
	}
	if n := memoryEvent(t, bigDir, "oom_kill"); n != 1 {
		t.Fatalf("larger tree lost %d processes to the OOM killer, want 1", n)
	}
}
