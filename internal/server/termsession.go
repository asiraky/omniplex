package server

import (
	"os"
	"os/exec"
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
// only an explicit close, the process exiting, or a long idle spell ends it.

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
	changed  chan struct{}
	ended    bool
	attached int
	// idle is the pending kill while nothing is attached. idleGen tells a
	// timer that fired late, after an attach and another detach, that it is
	// no longer the current one.
	idle    *time.Timer
	idleGen int

	cleanup sync.Once
}

func termKey(device, id string) string { return device + "\x00" + id }

// termAttach finds the device's terminal by id and counts a new attachment on
// it. A terminal opened for another target is treated as absent: the id is
// the client's, and reusing one for a different checkout is not a reattach.
func (s *Server) termAttach(device, id, target string) *termSession {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	sess := s.terms[termKey(device, id)]
	if sess == nil || sess.target != target {
		return nil
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.ended {
		return nil
	}
	sess.attached++
	sess.idleGen++
	if sess.idle != nil {
		sess.idle.Stop()
		sess.idle = nil
	}
	return sess
}

// termSpawn starts cmd on a pty and registers it under the id, already
// counting the caller's attachment.
func (s *Server) termSpawn(device, id, target string, cmd *exec.Cmd) (*termSession, error) {
	// A shell in a checkout starts dev servers and leaves them; end them with
	// the terminal rather than with the machine.
	// pty.Start makes the shell a thread leader; say so up front so the
	// process-group fallback does not also ask for setpgid, which cannot
	// combine with setsid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	tree := procgroup.Attach(cmd, "term-"+target)
	tty, err := pty.Start(cmd)
	if err != nil {
		tree.Kill()
		return nil, err
	}
	sess := &termSession{
		key: termKey(device, id), device: device, target: target,
		cmd: cmd, tty: tty, tree: tree,
		ring: newTermRing(termRingBytes), changed: make(chan struct{}),
		attached: 1,
	}

	s.termMu.Lock()
	old := s.terms[sess.key]
	s.terms[sess.key] = sess
	s.termMu.Unlock()
	// Same id, other target: the old one can no longer be reached.
	if old != nil {
		s.termEnd(old)
	}

	go s.termPump(sess)
	return sess, nil
}

// termPump copies pty output into the ring. It never touches a socket, so a
// stalled phone cannot stall the shell or the other devices watching it.
// Reading fails once the process has exited or the tty was closed, and that
// is what ends the session.
func (s *Server) termPump(sess *termSession) {
	buf := make([]byte, 32*1024)
	for {
		n, err := sess.tty.Read(buf)
		if n > 0 {
			sess.mu.Lock()
			sess.ring.Write(buf[:n])
			close(sess.changed)
			sess.changed = make(chan struct{})
			sess.mu.Unlock()
		}
		if err != nil {
			s.termEnd(sess)
			return
		}
	}
}

// termDetach counts an attachment gone, and starts the idle clock when it
// was the last.
func (s *Server) termDetach(sess *termSession) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.attached--
	if sess.attached > 0 || sess.ended {
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
			s.termEnd(sess)
		}
	})
}

// termKill ends the device's terminal by id, if it has one.
func (s *Server) termKill(device, id string) {
	s.termMu.Lock()
	sess := s.terms[termKey(device, id)]
	s.termMu.Unlock()
	if sess != nil {
		s.termEnd(sess)
	}
}

// termEnd removes a session and kills its process. Idempotent: the pump, the
// idle timer, an explicit close and a revocation can all race to it.
// Attached writers see ended, drain what is left in the ring, and close their
// sockets.
func (s *Server) termEnd(sess *termSession) {
	s.termMu.Lock()
	if s.terms[sess.key] == sess {
		delete(s.terms, sess.key)
	}
	s.termMu.Unlock()

	sess.mu.Lock()
	if !sess.ended {
		sess.ended = true
		if sess.idle != nil {
			sess.idle.Stop()
			sess.idle = nil
		}
		close(sess.changed)
		sess.changed = make(chan struct{})
	}
	sess.mu.Unlock()

	sess.cleanup.Do(func() {
		_ = sess.tty.Close()
		_ = sess.cmd.Process.Kill()
		sess.tree.Kill()
		_, _ = sess.cmd.Process.Wait()
	})
}

// termEndDevice ends every terminal a device opened.
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
		s.termEnd(sess)
	}
}
