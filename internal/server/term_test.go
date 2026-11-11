package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// termRig is a real server with a thread to root shells in, a paired client,
// and the *Server itself so a test can shorten the TTL and see the registry.
type termRig struct {
	srv    *Server
	ts     *httptest.Server
	guard  *auth.Guard
	client *http.Client
}

func newTermRig(t *testing.T, ttl time.Duration) *termRig {
	t.Helper()
	// Plain sh: the user's own shell and its rc files are not under test.
	t.Setenv("SHELL", "/bin/sh")

	st, err := store.Open(filepath.Join(t.TempDir(), "term.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	guard := auth.New(st, true, auth.DefaultPort)
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)

	now := time.Now().UnixMilli()
	if err := st.CreateThread(context.Background(), store.ThreadMeta{
		ID: "term-thread", Cwd: t.TempDir(), Harness: "claudecode",
		Title: "terminal", CreatedAt: now, UpdatedAt: now, Phase: "idle",
	}); err != nil {
		t.Fatal(err)
	}

	srv := New(Options{Manager: mgr, Store: st, Guard: guard})
	srv.termTTL = ttl
	ts := httptest.NewServer(asRemote(srv.Handler()))
	t.Cleanup(func() {
		ts.Close()
		srv.termMu.Lock()
		var all []*termSession
		for _, sess := range srv.terms {
			all = append(all, sess)
		}
		srv.termMu.Unlock()
		for _, sess := range all {
			srv.termEnd(sess)
		}
	})
	return &termRig{srv: srv, ts: ts, guard: guard, client: pairedClient(t, ts, guard)}
}

func (rig *termRig) dial(t *testing.T, ctx context.Context, client *http.Client, query string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(rig.ts.URL, "http") + "/api/term?thread=term-thread&" + query
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		code := 0
		if res != nil {
			code = res.StatusCode
		}
		t.Fatalf("dial %s: status %d: %v", query, code, err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func (rig *termRig) session(id string) *termSession {
	rig.srv.termMu.Lock()
	defer rig.srv.termMu.Unlock()
	for _, sess := range rig.srv.terms {
		if strings.HasSuffix(sess.key, "\x00"+id) {
			return sess
		}
	}
	return nil
}

// termReader follows one socket's stream the way a client would: sync frames
// set the offset, binary frames advance it.
type termReader struct {
	conn   *websocket.Conn
	offset int64
	syncs  []int64
	out    strings.Builder
}

// next reads one frame, returning the close error once the socket ends.
func (tr *termReader) next(ctx context.Context) error {
	typ, data, err := tr.conn.Read(ctx)
	if err != nil {
		return err
	}
	if typ == websocket.MessageText {
		var f termSyncFrame
		if json.Unmarshal(data, &f) == nil && f.Type == "sync" {
			tr.offset = f.Offset
			tr.syncs = append(tr.syncs, f.Offset)
		}
		return nil
	}
	tr.offset += int64(len(data))
	tr.out.Write(data)
	return nil
}

func (tr *termReader) until(t *testing.T, ctx context.Context, want string) {
	t.Helper()
	for !strings.Contains(tr.out.String(), want) {
		if err := tr.next(ctx); err != nil {
			t.Fatalf("waiting for %q, got %q then: %v", want, tr.out.String(), err)
		}
	}
}

// closed reads until the server closes the socket and returns its status.
func (tr *termReader) closed(t *testing.T, ctx context.Context) websocket.StatusCode {
	t.Helper()
	for {
		if err := tr.next(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatalf("socket was never closed: %v", err)
			}
			return websocket.CloseStatus(err)
		}
	}
}

func (tr *termReader) input(t *testing.T, ctx context.Context, s string) {
	t.Helper()
	frame, _ := json.Marshal(termClientFrame{Type: "input", Data: s})
	if err := tr.conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func attachedCount(sess *termSession) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.attached
}

func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The point of the change: a socket drop is not the end of the shell, and
// what it printed meanwhile is waiting on reattach.
func TestTermOutputWhileDetachedIsReplayedOnReattach(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	first := &termReader{conn: rig.dial(t, ctx, rig.client, "id=replay")}
	if err := first.next(ctx); err != nil || len(first.syncs) != 1 || first.syncs[0] != 0 {
		t.Fatalf("a fresh terminal should open with sync 0, got %v (%v)", first.syncs, err)
	}
	// Quoted so the echo of the typed line cannot be what matches.
	first.input(t, ctx, "sleep 1; echo do\"\"ne\n")
	first.until(t, ctx, "sleep 1")
	received := first.offset
	first.conn.Close(websocket.StatusNormalClosure, "")

	sess := rig.session("replay")
	eventually(t, "the drop to detach", func() bool { return attachedCount(sess) == 0 })
	eventually(t, "the command to finish while detached", func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		data, _ := sess.ring.Since(received, termRingBytes)
		return strings.Contains(string(data), "done")
	})

	second := &termReader{conn: rig.dial(t, ctx, rig.client, "id=replay&attach=1&since="+strconv.FormatInt(received, 10))}
	if err := second.next(ctx); err != nil || len(second.syncs) != 1 || second.syncs[0] != received {
		t.Fatalf("reattach should resume at %d, got syncs %v (%v)", received, second.syncs, err)
	}
	second.until(t, ctx, "done")
	if strings.Contains(second.out.String(), "sleep 1") {
		t.Fatalf("reattach resent output the client already had: %q", second.out.String())
	}
}

func TestTermAttachToUnknownIDClosesWith4404(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dial(t, ctx, rig.client, "id=nobody&attach=1")}
	if code := tr.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("close status %d, want 4404", code)
	}
	if rig.session("nobody") != nil {
		t.Fatal("attach=1 spawned a terminal")
	}
}

func TestTermIdleTTLKillsADetachedTerminal(t *testing.T) {
	rig := newTermRig(t, 50*time.Millisecond)
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dial(t, ctx, rig.client, "id=idle")}
	_ = tr.next(ctx)
	sess := rig.session("idle")
	pid := sess.cmd.Process.Pid
	tr.conn.Close(websocket.StatusNormalClosure, "")

	eventually(t, "the idle terminal to be reaped", func() bool { return rig.session("idle") == nil })
	eventually(t, "the shell to die", func() bool { return processGone(pid) })

	again := &termReader{conn: rig.dial(t, ctx, rig.client, "id=idle&attach=1")}
	if code := again.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("attach after expiry closed %d, want 4404", code)
	}
}

func TestTermReattachStopsTheIdleTimer(t *testing.T) {
	const ttl = 150 * time.Millisecond
	rig := newTermRig(t, ttl)
	ctx := testCtx(t)

	first := &termReader{conn: rig.dial(t, ctx, rig.client, "id=keep")}
	_ = first.next(ctx)
	sess := rig.session("keep")
	first.conn.Close(websocket.StatusNormalClosure, "")
	// Wait for the detach, so the timer really is running when we come back.
	eventually(t, "the drop to detach", func() bool { return attachedCount(sess) == 0 })

	second := &termReader{conn: rig.dial(t, ctx, rig.client, "id=keep&attach=1")}
	if err := second.next(ctx); err != nil || len(second.syncs) != 1 {
		t.Fatalf("reattach within the TTL failed: %v", err)
	}
	time.Sleep(3 * ttl)
	if rig.session("keep") != sess {
		t.Fatal("an attached terminal was reaped by the idle timer")
	}
	second.input(t, ctx, "echo al\"\"ive\n")
	second.until(t, ctx, "alive")
}

// One device, two sockets: the TTL only runs once both are gone.
func TestTermSurvivesWhileAnyAttachmentRemains(t *testing.T) {
	const ttl = 100 * time.Millisecond
	rig := newTermRig(t, ttl)
	ctx := testCtx(t)

	a := &termReader{conn: rig.dial(t, ctx, rig.client, "id=shared")}
	_ = a.next(ctx)
	b := &termReader{conn: rig.dial(t, ctx, rig.client, "id=shared&attach=1")}
	_ = b.next(ctx)
	sess := rig.session("shared")
	eventually(t, "both attached", func() bool { return attachedCount(sess) == 2 })

	// Input from either reaches the one shell; output reaches both.
	b.input(t, ctx, "echo fr\"\"om-b\n")
	a.until(t, ctx, "from-b")
	b.until(t, ctx, "from-b")

	a.conn.Close(websocket.StatusNormalClosure, "")
	eventually(t, "a to detach", func() bool { return attachedCount(sess) == 1 })
	time.Sleep(3 * ttl)
	if rig.session("shared") != sess {
		t.Fatal("the terminal was reaped with a socket still attached")
	}
}

func TestTermDeleteKillsTheTerminal(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dial(t, ctx, rig.client, "id=doomed")}
	_ = tr.next(ctx)
	pid := rig.session("doomed").cmd.Process.Pid

	del := func(id string) int {
		req, _ := http.NewRequest(http.MethodDelete, rig.ts.URL+"/api/term?id="+id, nil)
		res, err := rig.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := del("doomed"); code != http.StatusNoContent {
		t.Fatalf("DELETE returned %d, want 204", code)
	}
	if code := tr.closed(t, ctx); code != termStatusExited {
		t.Fatalf("attached socket closed %d, want 4000", code)
	}
	eventually(t, "the shell to die", func() bool { return processGone(pid) })

	again := &termReader{conn: rig.dial(t, ctx, rig.client, "id=doomed&attach=1")}
	if code := again.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("attach after DELETE closed %d, want 4404", code)
	}
	// Closing a tab the server already lost is not an error.
	if code := del("doomed"); code != http.StatusNoContent {
		t.Fatalf("DELETE of a gone terminal returned %d, want 204", code)
	}
}

func TestTermShellExitClosesWith4000AndForgetsIt(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dial(t, ctx, rig.client, "id=quitter")}
	_ = tr.next(ctx)
	tr.input(t, ctx, "echo by\"\"e; exit\n")
	if code := tr.closed(t, ctx); code != termStatusExited {
		t.Fatalf("close status %d, want 4000", code)
	}
	// Output written just before the exit is drained, not dropped.
	if !strings.Contains(tr.out.String(), "bye") {
		t.Fatalf("the last output was lost: %q", tr.out.String())
	}
	eventually(t, "the session to be forgotten", func() bool { return rig.session("quitter") == nil })
}

func TestTermIsPrivateToTheDeviceThatOpenedIt(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	mine := &termReader{conn: rig.dial(t, ctx, rig.client, "id=private")}
	_ = mine.next(ctx)

	other := pairedClient(t, rig.ts, rig.guard)
	theirs := &termReader{conn: rig.dial(t, ctx, other, "id=private&attach=1")}
	if code := theirs.closed(t, ctx); code != termStatusMissing {
		t.Fatalf("another device attached to the terminal: close %d, want 4404", code)
	}
	// Nor can it close it.
	req, _ := http.NewRequest(http.MethodDelete, rig.ts.URL+"/api/term?id=private", nil)
	res, err := other.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	mine.input(t, ctx, "echo st\"\"ill-here\n")
	mine.until(t, ctx, "still-here")
}

// A revoked phone loses its shells, including ones it is not attached to.
func TestTermRevokingTheDeviceKillsItsTerminals(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	tr := &termReader{conn: rig.dial(t, ctx, rig.client, "id=revoked")}
	_ = tr.next(ctx)
	sess := rig.session("revoked")
	tr.conn.Close(websocket.StatusNormalClosure, "")
	eventually(t, "the drop to detach", func() bool { return attachedCount(sess) == 0 })

	rig.srv.closeDevice(sess.device)
	if rig.session("revoked") != nil {
		t.Fatal("a revoked device's terminal is still registered")
	}
	eventually(t, "the shell to die", func() bool { return processGone(sess.cmd.Process.Pid) })
}

func TestTermRejectsAMissingOrInvalidID(t *testing.T) {
	rig := newTermRig(t, time.Minute)

	for _, q := range []string{"", "id=", "id=has%20space", "id=" + strings.Repeat("x", 65)} {
		res, err := rig.client.Get(rig.ts.URL + "/api/term?thread=term-thread&" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400", q, res.StatusCode)
		}
	}
}

// More output than the ring holds while detached: the client cannot get it
// all back, and the sync frame is how it learns where its copy resumes.
func TestTermReattachBehindTheRingResumesFromItsStart(t *testing.T) {
	rig := newTermRig(t, time.Minute)
	ctx := testCtx(t)

	first := &termReader{conn: rig.dial(t, ctx, rig.client, "id=flood")}
	_ = first.next(ctx)
	first.input(t, ctx, "sleep 1; head -c 400000 /dev/zero | tr '\\0' x; echo; echo fl\"\"ooded\n")
	first.until(t, ctx, "sleep 1")
	first.conn.Close(websocket.StatusNormalClosure, "")

	sess := rig.session("flood")
	eventually(t, "the flood to land while detached", func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		data, _ := sess.ring.Since(sess.ring.Total()-64, 64)
		return strings.Contains(string(data), "flooded")
	})

	second := &termReader{conn: rig.dial(t, ctx, rig.client, "id=flood&attach=1&since=0")}
	second.conn.SetReadLimit(termFrameBytes * 2)
	_ = second.next(ctx)
	if len(second.syncs) != 1 || second.syncs[0] <= 0 {
		t.Fatalf("since=0 behind the ring should resync past 0, got %v", second.syncs)
	}
	second.until(t, ctx, "flooded")
	if got := len(second.out.String()); got > termRingBytes {
		t.Fatalf("replayed %d bytes, more than the ring holds", got)
	}
}
