package procgroup

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	cgroupRoot = "/sys/fs/cgroup"
	// prefix marks the cgroups this package owns, so Sweep can tell them from
	// anything else that might appear under the server's cgroup. The owner's
	// pid follows it: two servers sharing a cgroup (the tests, a dev server
	// started beside another) must not sweep each other's live sessions.
	prefix = "omniplex-"
)

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
	root, ok := ownCgroup()
	if !ok {
		return nil, false
	}
	// A suffix keeps a fresh session from colliding with the cgroup of one
	// with the same name that is still being torn down.
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	dir := filepath.Join(root, prefix+strconv.Itoa(owner)+"-"+sanitize(name)+"-"+hex.EncodeToString(suffix[:]))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, false
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

func sweepCgroups() {
	root, ok := ownCgroup()
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
