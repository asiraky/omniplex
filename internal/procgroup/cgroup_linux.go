package procgroup

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	cgroupRoot = "/sys/fs/cgroup"
	// prefix marks the cgroups this package owns, so Sweep can tell them from
	// anything else that might appear under the server's cgroup. The owner's
	// pid follows it: two servers sharing a cgroup (the tests, a dev server
	// started beside another) must not sweep each other's live threads.
	prefix = "omniplex-"
	// leaf is where LimitMemory moves the server so its cgroup can hand the
	// memory controller to the trees. No prefix, so Sweep leaves it alone.
	leaf = "server"
)

var (
	baseOnce sync.Once
	baseDir  string
	baseOK   bool

	// memoryMax is the cap Attach gives each new tree, in bytes; 0 leaves
	// trees uncapped.
	memoryMax atomic.Int64
)

// baseCgroup is the cgroup the server started in. Trees go beneath it, not
// beneath wherever the server is now: LimitMemory moves the server into a leaf
// of its own, and a tree under that leaf would get no controllers.
func baseCgroup() (string, bool) {
	baseOnce.Do(func() { baseDir, baseOK = ownCgroup() })
	return baseDir, baseOK
}

type cgroupGroup struct {
	dir  string
	fd   *os.File
	once sync.Once
}

// ownCgroup is the server's own cgroup v2 directory.
func ownCgroup() (string, bool) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// v2 has exactly one line, "0::/path". A v1 mount lists controllers
		// per line and has no cgroup.kill; fall back to a process group there.
		if rest, ok := strings.CutPrefix(sc.Text(), "0::"); ok {
			return filepath.Join(cgroupRoot, rest), true
		}
	}
	return "", false
}

func attachCgroup(cmd *exec.Cmd, name string) (Group, bool) {
	return attachCgroupOwned(cmd, os.Getpid(), name)
}

func attachCgroupOwned(cmd *exec.Cmd, owner int, name string) (Group, bool) {
	root, ok := baseCgroup()
	if !ok {
		return nil, false
	}
	// A suffix keeps a fresh thread from colliding with the cgroup of one
	// with the same name that is still being torn down.
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	dir := filepath.Join(root, prefix+strconv.Itoa(owner)+"-"+sanitize(name)+"-"+hex.EncodeToString(suffix[:]))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, false
	}
	if limit := memoryMax.Load(); limit > 0 {
		// No swap: a tree over its cap is killed at once, rather than paging
		// the whole machine into the ground on its way there. memory.oom.group
		// stays 0, so the kernel kills only the tree's largest process, the
		// runaway compiler or test runner, and the harness that started it
		// lives to see the command fail. Best effort: a tree that cannot be
		// capped still runs.
		_ = os.WriteFile(filepath.Join(dir, "memory.swap.max"), []byte("0"), 0)
		_ = os.WriteFile(filepath.Join(dir, "memory.max"), []byte(strconv.FormatInt(limit, 10)), 0)
	}
	fd, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		_ = os.Remove(dir)
		return nil, false
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// clone3 with CLONE_INTO_CGROUP: the child is born in the group, so
	// nothing it forks in its first instant can escape into the parent's.
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(fd.Fd())
	return &cgroupGroup{dir: dir, fd: fd}, true
}

func (g *cgroupGroup) Kill() {
	g.once.Do(func() {
		_ = g.fd.Close()
		killCgroup(g.dir)
	})
}

// killCgroup ends every process in dir and removes it once they are gone.
// Removal is asynchronous: the kernel drops a task from its cgroup as it
// exits, which happens after the write returns, and a stuck exit must not
// block the caller.
func killCgroup(dir string) {
	_ = os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if err := os.Remove(dir); err == nil || os.IsNotExist(err) || time.Now().After(deadline) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}

func limitMemory(percent int) (int64, error) {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return 0, err
	}
	root, ok := baseCgroup()
	if !ok {
		return 0, errors.New("no cgroup v2 hierarchy")
	}
	if err := delegateMemory(root, os.Getpid()); err != nil {
		return 0, err
	}
	limit := int64(si.Totalram) * int64(si.Unit) / 100 * int64(percent)
	memoryMax.Store(limit)
	return limit, nil
}

// delegateMemory turns on the memory controller for root's children, so each
// tree can carry a memory.max of its own. The kernel allows that only while
// no process sits in root itself, so the server, self, first moves into
// root/server. It can only do that when it is root's sole process: a dev
// server or a test run sharing a cgroup with the shell that started it is left
// where it is, and its trees go uncapped.
func delegateMemory(root string, self int) error {
	ctl := filepath.Join(root, "cgroup.subtree_control")
	on, err := os.ReadFile(ctl)
	if err != nil {
		return err
	}
	for _, c := range strings.Fields(string(on)) {
		if c == "memory" {
			return nil
		}
	}
	procs, err := os.ReadFile(filepath.Join(root, "cgroup.procs"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(procs)) != strconv.Itoa(self) {
		return fmt.Errorf("%s holds processes besides this server", root)
	}
	dir := filepath.Join(root, leaf)
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(self)), 0); err != nil {
		return fmt.Errorf("move into %s: %w", dir, err)
	}
	if err := os.WriteFile(ctl, []byte("+memory"), 0); err != nil {
		return fmt.Errorf("enable memory controller in %s: %w", root, err)
	}
	return nil
}

func sweepCgroups() {
	root, ok := baseCgroup()
	if !ok {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !e.IsDir() || !ok {
			continue
		}
		owner, _, _ := strings.Cut(rest, "-")
		if pid, err := strconv.Atoi(owner); err == nil && processAlive(pid) {
			continue
		}
		killCgroup(filepath.Join(root, e.Name()))
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// sanitize keeps a name safe as a single path element.
func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, name)
}
