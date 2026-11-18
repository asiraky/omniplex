package server

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newBoxRig is a term rig whose box shell has a tmux server of its own, in a
// $HOME of its own, ended when the test is. Its socket lives in the test's
// temp dir too: tmux leaves the file behind when its server exits.
func newBoxRig(t *testing.T) (*termRig, string) {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	rig := newTermRig(t, time.Minute)
	rig.srv.boxSocket = boxSocketName(t.TempDir())
	sock := rig.srv.boxSocket
	t.Cleanup(func() { _ = exec.Command(tmux, "-L", sock, "kill-server").Run() })
	return rig, home
}

// dialBox opens a socket to the box shell and gives it a screen size, as the
// client does on mount.
func (rig *termRig) dialBox(t *testing.T, ctx context.Context, client *http.Client, query string) *termReader {
	t.Helper()
	tr := &termReader{conn: rig.dialQuery(t, ctx, client, "box=1&"+query)}
	if err := tr.conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatal(err)
	}
	return tr
}

// removeAll drops every terminal the server holds, as a restart would.
func (rig *termRig) removeAll() {
	rig.srv.termMu.Lock()
	var all []*termSession
	for _, sess := range rig.srv.terms {
		all = append(all, sess)
	}
	rig.srv.termMu.Unlock()
	for _, sess := range all {
		rig.srv.termRemove(sess)
	}
}

func TestBoxShellStartsInHomeAndFollowsCd(t *testing.T) {
	rig, home := newBoxRig(t)
	ctx := testCtx(t)

	tr := rig.dialBox(t, ctx, rig.client, "id=box")
	tr.input(t, ctx, "echo he\"\"llo\n")
	tr.until(t, ctx, "hello")
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.cwd(t, ctx); got.Data != real || got.Root != home {
		t.Fatalf("at start got %+v, want cwd %q and root %q", got, real, home)
	}

	tr.input(t, ctx, "mkdir sub && cd sub && echo in\"\"side\n")
	tr.until(t, ctx, "inside")
	if got, want := tr.cwd(t, ctx), filepath.Join(real, "sub"); got.Data != want || got.Root != home {
		t.Fatalf("after cd got %+v, want cwd %q and root %q", got, want, home)
	}
}

// The point of tmux: a restart takes every terminal session with it, and the
// shell, with what the user left in it, is still there for the new server.
func TestBoxShellSurvivesARestart(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	first := rig.dialBox(t, ctx, rig.client, "id=box")
	first.input(t, ctx, "X=42; echo se\"\"t\n")
	first.until(t, ctx, "set")
	client := rig.session("box").cmd.Process.Pid
	rig.removeAll()
	eventually(t, "the tmux client to die", func() bool { return processGone(client) })

	// A second server on the same state directory is the restarted one.
	restarted := newTermRig(t, time.Minute)
	restarted.srv.boxSocket = rig.srv.boxSocket
	again := restarted.dialBox(t, ctx, restarted.client, "id=box")
	again.input(t, ctx, "echo \"val=$X.\"\n")
	again.until(t, ctx, "val=42.")
}

// A screen left open across a restart reconnects with attach=1, for a client
// the new server never had. The shell is still running, so it gets a new
// client onto it, not word that the shell has ended.
func TestBoxReattachAfterARestartFindsTheShell(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	first := rig.dialBox(t, ctx, rig.client, "id=box")
	first.input(t, ctx, "X=42; echo se\"\"t\n")
	first.until(t, ctx, "set")
	rig.removeAll()

	restarted := newTermRig(t, time.Minute)
	restarted.srv.boxSocket = rig.srv.boxSocket
	again := restarted.dialBox(t, ctx, restarted.client, "id=box&attach=1&since=500")
	again.input(t, ctx, "echo \"val=$X.\"\n")
	again.until(t, ctx, "val=42.")
	if len(again.syncs) == 0 || again.syncs[0] != 0 {
		t.Fatalf("syncs %v, want the new client's stream from 0", again.syncs)
	}
}

// Once the shell has ended, a reattach is told so rather than starting one.
func TestBoxReattachAfterTheShellEndedIsGone(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	first := rig.dialBox(t, ctx, rig.client, "id=box")
	first.input(t, ctx, "echo se\"\"t\n")
	first.until(t, ctx, "set")
	rig.srv.killBox(ctx)
	rig.removeAll()

	again := &termReader{conn: rig.dialQuery(t, ctx, rig.client, "box=1&id=box&attach=1&since=500")}
	if code := again.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("closed %d, want 4404", code)
	}
	if rig.srv.boxRunning(ctx) {
		t.Fatal("the reattach started a new shell")
	}
}

// A build started on the laptop is the same shell the phone opens.
func TestBoxShellIsSharedAcrossDevices(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	laptop := rig.dialBox(t, ctx, rig.client, "id=laptop")
	laptop.input(t, ctx, "Y=7; echo se\"\"t\n")
	laptop.until(t, ctx, "set")

	phone := rig.dialBox(t, ctx, pairedClient(t, rig.ts, rig.guard), "id=phone")
	phone.input(t, ctx, "echo \"y=$Y.\"\n")
	phone.until(t, ctx, "y=7.")
	// Its output reaches the laptop too: one shell, two clients.
	laptop.until(t, ctx, "y=7.")
}

// Without tmux the client is told what to install, not that something broke.
func TestBoxShellWithoutTmuxSaysSo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	rig := newTermRig(t, time.Minute)
	rig.srv.boxSocket = boxSocketName(t.TempDir())
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dialQuery(t, ctx, rig.client, "box=1&id=box")}
	var err error
	for err == nil {
		err = tr.next(ctx)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != termStatusFailed || ce.Reason != errNoTmux.Error() {
		t.Fatalf("closed with %v, want 4400 %q", err, errNoTmux.Error())
	}
	if n := rig.count(); n != 0 {
		t.Fatalf("%d terminals registered after a failed start", n)
	}
}

// Ending the box shell on purpose ends it for everyone, and the next open is
// a new shell.
func TestBoxDeleteEndsTheShell(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	mine := rig.dialBox(t, ctx, rig.client, "id=box")
	mine.input(t, ctx, "X=42; echo se\"\"t\n")
	mine.until(t, ctx, "set")
	theirs := rig.dialBox(t, ctx, pairedClient(t, rig.ts, rig.guard), "id=box")
	theirs.input(t, ctx, "echo \"val=$X.\"\n")
	theirs.until(t, ctx, "val=42.")

	req, _ := http.NewRequest(http.MethodDelete, rig.ts.URL+"/api/term?id=box&box=1", nil)
	res, err := rig.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE returned %d, want 204", res.StatusCode)
	}
	if code := mine.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("the deleting device's socket closed %d, want 4404", code)
	}
	if code := theirs.closed(t, ctx); code != termStatusExited {
		t.Fatalf("another device's socket closed %d, want 4000", code)
	}

	fresh := rig.dialBox(t, ctx, rig.client, "id=box&fresh=1")
	fresh.input(t, ctx, "echo \"val=$X.\"\n")
	fresh.until(t, ctx, "val=.")
}

// exit ends the shell like any other, and a fresh open starts another.
func TestBoxShellExitClosesWith4000(t *testing.T) {
	rig, _ := newBoxRig(t)
	ctx := testCtx(t)

	tr := rig.dialBox(t, ctx, rig.client, "id=box")
	tr.input(t, ctx, "X=42; exit\n")
	if code := tr.closed(t, ctx); code != termStatusExited {
		t.Fatalf("close status %d, want 4000", code)
	}

	again := rig.dialBox(t, ctx, rig.client, "id=box&fresh=1")
	again.input(t, ctx, "echo \"val=$X.\"\n")
	again.until(t, ctx, "val=.")
}

func TestBoxRejectsAnotherTarget(t *testing.T) {
	rig := newTermRig(t, time.Minute)

	for _, q := range []string{"box=1&thread=term-thread", "box=1&login=claude"} {
		res, err := rig.client.Get(rig.ts.URL + "/api/term?id=box&" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400", q, res.StatusCode)
		}
	}
}
