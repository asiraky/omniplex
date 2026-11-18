package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The box shell: the user's $SHELL in $HOME on the machine Omniplex runs on,
// one per instance, shared by every device and kept across a server restart.
// A pty cannot outlive the process holding it, so tmux holds the shell and
// each box terminal is only a tmux client on a pty. The client is an ordinary
// terminal session, with the per-device registry, ring and idle TTL of any
// other; ending it detaches from the shell rather than ending it, and the next
// client tmux redraws for. Only DELETE with box=1, or the shell exiting, ends
// the shell itself.
//
// The tmux server is this instance's own: its socket name is derived from the
// state directory, so two instances on one machine do not share a shell, and
// the user's own tmux (default socket, ~/.tmux.conf) is never touched.

const (
	boxTarget  = "box"
	boxSession = "box"
)

// errNoTmux is a box terminal asked for on a machine without tmux: the one
// failure the user can fix, so the client is told which it is.
var errNoTmux = errors.New("tmux is not installed on the box")

// boxSocketName names this instance's tmux server after its state directory.
func boxSocketName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	sum := sha256.Sum256([]byte(dir))
	return "omniplex-box-" + hex.EncodeToString(sum[:4])
}

// boxEnv is the environment of every tmux invocation, and through the server
// the shell's. TMUX is dropped because a client refuses to attach from inside
// tmux, which is where Omniplex runs when someone starts it by hand.
func boxEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TMUX" || name == "TMUX_PANE" || name == "TERM" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color")
}

// boxArgs addresses args to this instance's tmux server. The empty config goes
// on every invocation, not only the first: a client whose server has just
// gone starts a new one, and that must not read the user's ~/.tmux.conf.
func (s *Server) boxArgs(args ...string) []string {
	return append([]string{"-L", s.boxSocket, "-f", os.DevNull}, args...)
}

func (s *Server) boxCmd(ctx context.Context, tmux string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, tmux, s.boxArgs(args...)...)
	cmd.Env = boxEnv()
	return cmd
}

// boxCommand is the tmux client a new box terminal runs, with the shell it
// attaches to already running. tmux is looked up on every call, so installing
// it takes effect without a restart.
func (s *Server) boxCommand(ctx context.Context) (*exec.Cmd, error) {
	if s.boxSocket == "" {
		return nil, errors.New("box shell is off: no state directory")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil, errNoTmux
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if err := s.ensureBox(ctx, tmux, home); err != nil {
		return nil, err
	}
	cmd := exec.Command(tmux, s.boxArgs("attach-session", "-t", boxSession)...)
	cmd.Dir, cmd.Env = home, boxEnv()
	return cmd, nil
}

// ensureBox starts the shell's tmux server unless it is already running.
//
// The server must not be a child of a terminal's pty process: that process
// sits in a procgroup tree, which is killed with the terminal. Nor should it
// sit in omniplex.service's cgroup, which a restart kills. systemd-run puts it
// in a transient scope of its own instead. Where there is no user bus to ask
// (CI, a server started by hand) it is started directly, which survives every
// terminal but not a restart under systemd.
func (s *Server) ensureBox(ctx context.Context, tmux, home string) error {
	// Serialised so two devices opening at once do not race to create the
	// session, the loser failing on a duplicate name.
	s.boxMu.Lock()
	defer s.boxMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if s.boxCmd(ctx, tmux, "has-session", "-t", boxSession).Run() == nil {
		return nil
	}

	// Options go before the session, so its first pane already has the
	// history limit. tmux is made invisible: no status line, no prefix key (so
	// Ctrl-b reaches the shell), no Esc delay, no mouse capture. It draws in
	// the normal screen rather than the alternate one, so what scrolls off
	// lands in xterm.js's own scrollback, which touch scrolling on a phone
	// depends on. The latest active device's size wins, so a phone attaching
	// reflows the shell to fit and the laptop's next keystroke takes it back.
	args := s.boxArgs(
		"start-server", ";",
		"set-option", "-g", "status", "off", ";",
		"set-option", "-g", "prefix", "None", ";",
		"unbind-key", "C-b", ";",
		"set-option", "-s", "escape-time", "0", ";",
		"set-option", "-g", "history-limit", "10000", ";",
		"set-option", "-g", "window-size", "latest", ";",
		"set-option", "-g", "mouse", "off", ";",
		"set-option", "-s", "terminal-overrides", "xterm*:smcup@:rmcup@", ";",
		"new-session", "-d", "-s", boxSession, "-c", home,
	)
	// Both ways run from $HOME: until the new shell has moved into the
	// directory asked for, tmux reports its pane's path as the server's own.
	started := false
	if run, err := exec.LookPath("systemd-run"); err == nil {
		scope := append([]string{"--user", "--scope", "--quiet", "--collect",
			"--description=Omniplex box shell", "--", tmux}, args...)
		cmd := exec.CommandContext(ctx, run, scope...)
		cmd.Dir, cmd.Env = home, boxEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s.logf("box_scope_failed err=%v out=%q", err, strings.TrimSpace(string(out)))
		} else {
			started = true
		}
	}
	if !started {
		cmd := exec.CommandContext(ctx, tmux, args...)
		cmd.Dir, cmd.Env = home, boxEnv()
		// Out of the server's process group, so nothing aimed at the
		// server's group reaches the shell.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if out, err := cmd.CombinedOutput(); err != nil {
			s.logf("box_start_failed err=%v out=%q", err, strings.TrimSpace(string(out)))
		}
	}
	// Checked rather than trusted: a shell that exits at once leaves no
	// session to attach to, whatever the start reported.
	if err := s.boxCmd(ctx, tmux, "has-session", "-t", boxSession).Run(); err != nil {
		return fmt.Errorf("box shell did not start: %w", err)
	}
	return nil
}

// termCwd is where a terminal's shell is now. A box terminal's process is a
// tmux client, whose own directory says nothing about the shell; the tmux
// server knows where its pane is.
func (s *Server) termCwd(ctx context.Context, sess *termSession) string {
	if sess.target != boxTarget {
		return sess.cwd(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := s.boxCmd(ctx, sess.cmd.Path, "display-message", "-p", "-t", boxSession, "#{pane_current_path}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// killBox ends the box shell for every device. Their clients exit with it,
// and their sockets are told the terminal exited.
func (s *Server) killBox(ctx context.Context) {
	if s.boxSocket == "" {
		return
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		// No tmux, no shell to end.
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out, err := s.boxCmd(ctx, tmux, "kill-server").CombinedOutput(); err != nil {
		s.logf("box_kill_failed err=%v out=%q", err, strings.TrimSpace(string(out)))
	}
}
