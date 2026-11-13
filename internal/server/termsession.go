package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/asiraky/omniplex/internal/procgroup"
)

// A terminal outlives its socket. On a phone the socket drops whenever the
// screen locks or the train enters a tunnel, and a shell that died with it
// would take the user's running build along. So the pty lives in a registry
// keyed by the client's terminal id, sockets attach to and detach from it, and
// only an explicit close or a long idle spell removes it.
//
// The process exiting ends the session but does not remove it: what it printed
// last may have landed while no socket was attached, so the session stays
// registered for replay until an attachment drains it or the idle clock runs
// out.

const (
	// termRingBytes is how much output a reattaching socket can be caught up
	// on. A few screens of scrollback; anything older the client either
	// already has or has lost to its own scrollback limit anyway.
	termRingBytes = 256 * 1024
	// termIdleTTL is how long a terminal with no socket waits for one to come
	// back. Long enough for a commute between two stations, short enough that
	// abandoned shells do not pile up.
	termIdleTTL = 30 * time.Minute
	// termFrameBytes caps one binary frame, so a catch-up after a long drop
	// goes out in pieces a slow link can interleave with pings.
	termFrameBytes = 32 * 1024
)

type termSession struct {
	key    string // device + id: the registry key
	device string
	target string // "thread:<id>" or "login:<instance>"

	cmd  *exec.Cmd
	tty  *os.File
	tree procgroup.Group

	mu   sync.Mutex
	ring *termRing
	// changed is closed and replaced whenever output arrives or the session
	// ends, so each attachment's writer can wait on it without the pty read
	// loop ever waiting on a socket.
	changed chan struct{}
	ended   bool
	// killed is an end the user (or a revocation) chose, as opposed to the
	// process exiting: an attached socket is told it is gone, not exited.
	killed   bool
	attached int
	// idle is the pending removal while nothing is attached. idleGen tells a
	// timer that fired late, after an attach and another detach, that it is
	// no longer the current one.
	idle    *time.Timer
	idleGen int

	cleanup sync.Once
}

// errTermClosed is a spawn for an id DELETE already closed.
var errTermClosed = errors.New("terminal closed")

func termKey(device, id string) string { return device + "\x00" + id }

// notify wakes every attachment's writer. The caller holds mu.
func (sess *termSession) notify() {
	close(sess.changed)
	sess.changed = make(chan struct{})
}

// stopIdle cancels the pending removal; the generation bump turns a timer
// that already fired into a no-op. The caller holds mu.
func (sess *termSession) stopIdle() {
	sess.idleGen++
	if sess.idle != nil {
		sess.idle.Stop()
		sess.idle = nil
	}
}

// isKilled reports whether the session was ended on purpose.
func (sess *termSession) isKilled() bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.killed
}

// cwd is the shell's working directory now, "" once it has ended: its pid may
// already belong to something else.
func (sess *termSession) cwd(ctx context.Context) string {
	sess.mu.Lock()
	ended := sess.ended
	sess.mu.Unlock()
	if ended {
		return ""
	}
	return asStartedIn(processCwd(ctx, sess.cmd.Process.Pid), sess.cmd.Dir)
}

// asStartedIn spells dir the way the shell's start directory is spelled. The
// kernel reports a cwd with its symlinks resolved, while the client compares
// it against the checkout path as the thread recorded it; through a symlinked
// checkout every path would otherwise look like it was outside it.
func asStartedIn(dir, start string) string {
	if dir == "" || start == "" {
		return dir
	}
	real, err := filepath.EvalSymlinks(start)
	if err != nil || real == start {
		return dir
	}
	if dir == real {
		return start
	}
	if rest, ok := strings.CutPrefix(dir, real+string(filepath.Separator)); ok {
		return filepath.Join(start, rest)
	}
	return dir
}

// termAttach finds the device's terminal by id and counts a new attachment on
// it. fresh is the user asking for a new shell under the id, after the last
// one ended.
func (s *Server) termAttach(device, id, target string, fresh bool) *termSession {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return s.termAttachLocked(termKey(device, id), target, fresh)
}

// termAttachLocked is termAttach for a caller holding termMu. A terminal
// opened for another target is treated as absent: the id is the client's, and
// reusing one for a different checkout is not a reattach. An ended terminal is
// still found, so its last output can be replayed — unless the user asked for
// a fresh one, which a live terminal still satisfies.
func (s *Server) termAttachLocked(key, target string, fresh bool) *termSession {
	sess := s.terms[key]
	if sess == nil || sess.target != target {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if fresh && sess.ended {
		return nil
	}
	sess.attached++
	sess.stopIdle()
	return sess
}

// termSpawn starts cmd on a pty and registers it under the id, already
// counting the caller's attachment. The registry lock is held across the
// start: two sockets for a new id arrive together whenever a phone reconnects
// twice in quick succession, and the second must join the first's shell, not
// replace it.
func (s *Server) termSpawn(device, id, target string, fresh bool, cmd *exec.Cmd) (*termSession, error) {
	key := termKey(device, id)
	s.termMu.Lock()
	if sess := s.termAttachLocked(key, target, fresh); sess != nil {
		s.termMu.Unlock()
		return sess, nil
	}
	// Closed while its first socket was still on the way: the user is done
	// with it before it ever ran. Asking for a fresh shell is the user
	// changing their mind, from a tab that was told it was closed elsewhere.
	if _, ok := s.termClosed[key]; ok && !fresh {
		s.termMu.Unlock()
		return nil, errTermClosed
	}
	delete(s.termClosed, key)
	old := s.terms[key]

	// A shell in a checkout starts dev servers and leaves them; end them with
	// the terminal rather than with the machine.
	// pty.Start makes the shell a thread leader; say so up front so the
	// process-group fallback does not also ask for setpgid, which cannot
	// combine with setsid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	tree := procgroup.Attach(cmd, "term-"+target)
	tty, err := pty.Start(cmd)
	if err != nil {
		s.termMu.Unlock()
		tree.Kill()
		return nil, err
	}
	sess := &termSession{
		key: key, device: device, target: target,
		cmd: cmd, tty: tty, tree: tree,
		ring: newTermRing(termRingBytes), changed: make(chan struct{}),
		attached: 1,
	}
	s.terms[key] = sess
	s.termMu.Unlock()

	// Same id, other target: the old one can no longer be reached. Same
	// target: it had ended, and a fresh shell replaces it.
	if old != nil && old.target == target {
		s.termRemove(old)
	} else if old != nil {
		s.termKillSession(old)
	}

	go sess.pump()
	return sess, nil
}

// pump copies pty output into the ring. It never touches a socket, so a
// stalled phone cannot stall the shell or the terminal's other sockets.
// Reading fails once the process has exited or the tty was closed, and that
// is what ends the session; it stays registered for replay.
func (sess *termSession) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := sess.tty.Read(buf)
		if n > 0 {
			sess.mu.Lock()
			sess.ring.Write(buf[:n])
			sess.notify()
			sess.mu.Unlock()
		}
		if err != nil {
			sess.end()
			return
		}
	}
}

// termDetach counts an attachment gone, and starts the idle clock when it
// was the last. An ended session gets the clock too: if nobody comes back to
// read its last output, it is removed all the same.
func (s *Server) termDetach(sess *termSession) {
	// Registry first, as everywhere: a removed session needs no clock, and
	// holding both keeps termRemove from slipping in between the check and
	// the timer.
	s.termMu.Lock()
	defer s.termMu.Unlock()
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.attached--
	if sess.attached > 0 || s.terms[sess.key] != sess {
		return
	}
	sess.idleGen++
	gen := sess.idleGen
	sess.idle = time.AfterFunc(s.termTTL, func() {
		// Unregistered under the registry lock, so an attach cannot find the
		// session between deciding it is idle and killing it.
		s.termMu.Lock()
		sess.mu.Lock()
		stale := sess.idleGen != gen || sess.attached > 0
		if !stale && s.terms[sess.key] == sess {
			delete(s.terms, sess.key)
		}
		sess.mu.Unlock()
		s.termMu.Unlock()
		if !stale {
			sess.end()
		}
	})
}

// termKill removes the device's terminal by id, and remembers the id as
// closed so a spawn still on its way for it does not start a shell nobody
// will ever reattach to. Only a fresh-shell request reuses a closed id.
func (s *Server) termKill(device, id string) {
	key := termKey(device, id)
	now := time.Now()
	s.termMu.Lock()
	sess := s.terms[key]
	for k, at := range s.termClosed {
		// A spawn is pending for as long as it takes to resolve a command;
		// a TTL is far longer than that.
		if now.Sub(at) > s.termTTL {
			delete(s.termClosed, k)
		}
	}
	s.termClosed[key] = now
	s.termMu.Unlock()
	if sess != nil {
		s.termKillSession(sess)
	}
}

// termKillSession removes a session the user (or a revocation) ended.
func (s *Server) termKillSession(sess *termSession) {
	sess.mu.Lock()
	sess.killed = true
	sess.mu.Unlock()
	s.termRemove(sess)
}

// termRemove unregisters a session, if it is still the registered one, and
// ends it. Sockets already attached keep their pointer and drain what is left.
func (s *Server) termRemove(sess *termSession) {
	s.termMu.Lock()
	if s.terms[sess.key] == sess {
		delete(s.terms, sess.key)
	}
	sess.mu.Lock()
	sess.stopIdle()
	sess.mu.Unlock()
	s.termMu.Unlock()
	sess.end()
}

// end marks a session ended and kills its process, leaving it registered.
// Idempotent: the pump, the idle timer, an explicit close and a revocation
// can all race to it. Attached writers see ended, drain what is left in the
// ring, and close their sockets.
func (sess *termSession) end() {
	sess.mu.Lock()
	if !sess.ended {
		sess.ended = true
		sess.notify()
	}
	sess.mu.Unlock()

	sess.cleanup.Do(func() {
		_ = sess.tty.Close()
		_ = sess.cmd.Process.Kill()
		sess.tree.Kill()
		_, _ = sess.cmd.Process.Wait()
	})
}

// termEndDevice removes every terminal a device opened.
func (s *Server) termEndDevice(device string) {
	s.termMu.Lock()
	var doomed []*termSession
	for _, sess := range s.terms {
		if sess.device == device {
			doomed = append(doomed, sess)
		}
	}
	s.termMu.Unlock()
	for _, sess := range doomed {
		s.termKillSession(sess)
	}
}
