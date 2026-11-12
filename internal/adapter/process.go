package adapter

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/jsonrpc"
)

// Process watches a started harness process so that, when it dies, the turn
// it took with it can say why: its exit status, and the last lines it wrote
// to stderr. Without it the only trace of a crash is the server log.
//
// Process owns cmd.Wait; the adapter must not call it directly.
type Process struct {
	cmd  *exec.Cmd
	name string

	stderrDone chan struct{}
	mu         sync.Mutex
	tail       []string

	waitOnce sync.Once
	exited   chan struct{}
	waitErr  error
}

const (
	tailLines   = 8
	tailLineMax = 240
	// stderrGrace is how long Wait lets stderr finish before reaping. The
	// pipe can outlive the harness — a child it started may hold it — and
	// reaping closes it, so this is what gets the dying words read first.
	stderrGrace = 500 * time.Millisecond
	// exitGrace is how long ExitReason waits for a process whose stdout has
	// closed to actually exit.
	exitGrace = 2 * time.Second
)

// WatchProcess starts draining stderr, logging each line and keeping the last
// few. name is the harness as the person reading the transcript knows it.
// cmd must already be started.
func WatchProcess(cmd *exec.Cmd, stderr io.Reader, logf func(format string, args ...any), name string) *Process {
	p := &Process{cmd: cmd, name: name, stderrDone: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		defer close(p.stderrDone)
		// A long stderr line must not stop the drain: a harness blocked on a
		// full stderr pipe hangs.
		_ = jsonrpc.ReadLines(stderr, 1<<20, func(line []byte) {
			// Leading space stays: it is how a stack trace reads.
			text := strings.TrimRight(string(line), " \t")
			if strings.TrimSpace(text) == "" {
				return
			}
			logf("%s stderr: %s", name, text)
			p.keep(text)
		}, func(head []byte, size int) {
			logf("%s stderr: skipped a %d-byte line: %s", name, size, jsonrpc.Brief(head))
		})
	}()
	return p
}

func (p *Process) keep(line string) {
	if len(line) > tailLineMax {
		line = line[:tailLineMax] + "…"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail = append(p.tail, line)
	if len(p.tail) > tailLines {
		p.tail = p.tail[1:]
	}
}

// Tail is the last few stderr lines, oldest first, one per line.
func (p *Process) Tail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.tail, "\n")
}

// Wait reaps the process and returns cmd.Wait's error. It is safe to call
// any number of times from any goroutine; every call waits for the one reap.
func (p *Process) Wait() error {
	p.waitOnce.Do(func() {
		go func() {
			select {
			case <-p.stderrDone:
			case <-time.After(stderrGrace):
			}
			p.waitErr = p.cmd.Wait()
			close(p.exited)
		}()
	})
	<-p.exited
	return p.waitErr
}

// ExitReason says why the harness is gone, for a turn that died with it.
// readErr is whatever broke its stdout, if anything did.
func (p *Process) ExitReason(readErr error) string {
	name := p.name
	go func() { _ = p.Wait() }()
	var status string
	select {
	case <-p.exited:
		status = exitStatus(name, p.waitErr)
	case <-time.After(exitGrace):
		status = name + " closed its output but is still running"
	}
	if readErr != nil {
		status += "; reading its output failed: " + readErr.Error()
	}
	if tail := p.Tail(); tail != "" {
		status += "\n" + tail
	}
	return status
}

func exitStatus(name string, err error) string {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return name + " exited (exit status 0)"
	case errors.As(err, &exit):
		// "exit status 1", "signal: killed".
		return fmt.Sprintf("%s exited (%s)", name, exit)
	default:
		return fmt.Sprintf("%s exited (%v)", name, err)
	}
}
