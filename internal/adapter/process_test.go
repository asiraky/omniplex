package adapter

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// start runs script under sh with stderr watched.
func start(t *testing.T, script string) (*exec.Cmd, *Process) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := WatchProcess(cmd, stderr, func(string, ...any) {}, "pi")
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = p.Wait() })
	return cmd, p
}

// A harness that dies mid-turn must leave its exit status and its last words
// on the turn, not just in the server log; and only the last few, so a chatty
// harness cannot flood the card.
func TestExitReasonQuotesStatusAndStderrTail(t *testing.T) {
	_, p := start(t, `for i in 1 2 3 4 5 6 7 8 9 10; do echo "line $i" >&2; done; exit 3`)

	got := p.ExitReason(nil)
	var want []string
	for i := 3; i <= 10; i++ {
		want = append(want, fmt.Sprintf("line %d", i))
	}
	if exp := "pi exited (exit status 3)\n" + strings.Join(want, "\n"); got != exp {
		t.Fatalf("ExitReason =\n%s\nwant\n%s", got, exp)
	}

	got = p.ExitReason(errors.New("pipe broke"))
	if !strings.HasPrefix(got, "pi exited (exit status 3); reading its output failed: pipe broke\nline 3") {
		t.Fatalf("ExitReason with a read error =\n%s", got)
	}
}

// A harness killed from outside (OOM killer, a stray kill) says so.
func TestExitReasonReportsSignal(t *testing.T) {
	cmd, p := start(t, `exec sleep 30`)
	_ = cmd.Process.Kill()
	if got := p.ExitReason(nil); got != "pi exited (signal: killed)" {
		t.Fatalf("ExitReason = %q", got)
	}
}

// A harness that closed its output but hangs on must not hang the turn with
// it: the reason says it is still running.
func TestExitReasonForHungProcess(t *testing.T) {
	t.Parallel()
	_, p := start(t, `echo bye >&2; exec 2>&-; exec sleep 30`)
	if got := p.ExitReason(nil); got != "pi closed its output but is still running\nbye" {
		t.Fatalf("ExitReason = %q", got)
	}
}

// Close, watchExit and ExitReason all reap the same process, from different
// goroutines; each must get the one result rather than a second-Wait error.
func TestWaitIsShared(t *testing.T) {
	_, p := start(t, `exit 5`)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = p.Wait() }()
	}
	wg.Wait()
	errs = append(errs, p.Wait())
	for i, err := range errs {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 5 || err != errs[0] {
			t.Fatalf("Wait #%d = %v, want the shared exit status 5", i, err)
		}
	}
}

// A runaway stderr line is cut to fit the card, and one past the read limit
// is dropped without stopping the drain: a harness blocked on a full stderr
// pipe hangs.
func TestStderrLongLines(t *testing.T) {
	_, p := start(t, `printf '%300s\n' x | tr ' ' a >&2; head -c 2000000 /dev/zero | tr '\0' b >&2; echo >&2; echo last >&2`)
	_ = p.Wait()
	lines := strings.Split(p.Tail(), "\n")
	if len(lines) != 2 || lines[1] != "last" {
		t.Fatalf("tail = %d lines, last %q", len(lines), lines[len(lines)-1])
	}
	if want := strings.Repeat("a", tailLineMax) + "…"; lines[0] != want {
		t.Fatalf("long line kept as %d bytes, want %d + ellipsis", len(lines[0]), tailLineMax)
	}
}
