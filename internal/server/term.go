package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/asiraky/omniplex/internal/auth"
)

// The terminal surface: a pty bound to a thread's checkout (or a harness's
// sign-in flow), reached over a WebSocket. It is its own endpoint rather than
// a set of sync-protocol commands because a terminal is a stream, not a
// request. The gate covers it like every other route: an unpaired device
// never reaches the shell.
//
// Lifetime: the pty belongs to a client-chosen terminal id, not to a socket
// (see termsession.go). A socket that drops detaches; the pty keeps running
// and its output keeps landing in a ring buffer. A socket that comes back with
// the same id reattaches and is caught up from the offset it already has. With
// nothing attached for termTTL the pty is killed, as it is by DELETE, by the
// device being revoked, or by its process exiting.
//
// GET /api/term?id=<id>&thread=<id>|login=<instance>[&since=<n>][&attach=1]
// upgrades to a WebSocket. attach=1 only ever reattaches; without it a missing
// terminal is spawned. since is the stream offset the client already holds.
//
// Wire format: the client sends text frames of JSON — {"type":"input","data"}
// to type, {"type":"resize","cols","rows"} on layout change. The server first
// sends {"type":"sync","offset":N}, N being the stream offset of the next
// binary byte, then the pty's raw output as binary frames. It sends another
// sync when the client has fallen so far behind that output was overwritten,
// which is the client's cue that the screen has a gap. Close 4000 "exited"
// means the terminal is gone; 4404 "no such terminal" answers an attach=1 for
// an id the server does not have.
//
// DELETE /api/term?id=<id> kills the terminal: closing a tab is an explicit
// end, not a drop to wait out.

const (
	termStatusExited  websocket.StatusCode = 4000
	termStatusMissing websocket.StatusCode = 4404
)

type termClientFrame struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type termSyncFrame struct {
	Type   string `json:"type"`
	Offset int64  `json:"offset"`
}

// validTermID keeps the id something that is safe to log and key on.
func validTermID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (s *Server) handleDeleteTerm(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validTermID(id) {
		http.Error(w, "a valid id is required", http.StatusBadRequest)
		return
	}
	device, _ := auth.DeviceFrom(r.Context())
	s.termKill(device.ID, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveTerm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if !validTermID(id) {
		http.Error(w, "a valid id is required", http.StatusBadRequest)
		return
	}
	// Two things run here: the user's shell in a thread's checkout, or a
	// harness's own sign-in flow for one provider instance. Both are a pty the
	// user types into; only what it runs differs.
	threadID, login := q.Get("thread"), q.Get("login")
	var target string
	switch {
	case login != "":
		target = "login:" + login
	case threadID != "":
		target = "thread:" + threadID
	default:
		http.Error(w, "thread or login is required", http.StatusBadRequest)
		return
	}
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	attachOnly := q.Get("attach") == "1"

	// Registered by device, so revoking the device ends this shell too, and
	// one device cannot reach another's by guessing its id.
	device, _ := auth.DeviceFrom(r.Context())
	sess := s.termAttach(device.ID, id, target)
	if sess == nil && !attachOnly {
		cmd, err := s.termCommand(r.Context(), threadID, login)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess, err = s.termSpawn(device.ID, id, target, cmd); err != nil {
			http.Error(w, "pty failed", http.StatusInternalServerError)
			return
		}
	}

	opts := &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}
	if s.allowAny {
		opts.InsecureSkipVerify = true
	}
	// A browser refuses the connection unless the server selects one of the
	// subprotocols it offered — same as /ws.
	if proto, ok := auth.TokenSubprotocol(r); ok {
		opts.Subprotocols = []string{proto}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		if sess != nil {
			s.termDetach(sess)
		}
		return
	}
	// A close code is what lets the client tell "gone, open a new one" from a
	// network drop worth retrying; an HTTP 404 would look like the latter.
	if sess == nil {
		_ = ws.Close(termStatusMissing, "no such terminal")
		return
	}
	defer s.termDetach(sess)
	// A paste into a shell is bounded by the same cap as a prompt; without it
	// the library's 32 KiB default drops the terminal mid-paste.
	ws.SetReadLimit(maxWSMessageBytes)
	defer ws.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		s.termStream(ctx, ws, sess, since)
	}()

	// A shell can sit silent for hours; ping keeps NAT from reaping the socket.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// socket -> pty.
	for {
		typ, data, readErr := ws.Read(ctx)
		if readErr != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var f termClientFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case "input":
			if _, err := sess.tty.Write([]byte(f.Data)); err != nil {
				return
			}
		case "resize":
			if f.Cols > 0 && f.Rows > 0 {
				_ = pty.Setsize(sess.tty, &pty.Winsize{Cols: f.Cols, Rows: f.Rows})
			}
		}
	}
}

// termCommand builds what a new terminal runs, without starting it.
func (s *Server) termCommand(ctx context.Context, threadID, login string) (*exec.Cmd, error) {
	if login != "" {
		argv, env, err := s.mgr.LoginCommand(ctx, login)
		if err != nil {
			return nil, err
		}
		// Same working directory as the probe, so a relative
		// CLAUDE_CONFIG_DIR lands the credentials where the probe looks.
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(env, "TERM=xterm-256color")
		return cmd, nil
	}
	root, err := s.mgr.ThreadWorkspaceRoot(ctx, threadID)
	if err != nil {
		return nil, err
	}
	// The user's own shell, as a login-ish interactive shell in the checkout.
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	return cmd, nil
}

// termStream sends one attachment the session's output from offset next,
// at its own pace. It reads from the ring rather than being handed output, so
// a slow socket falls behind on its own and is told so with a fresh sync.
// Returns when the socket fails or, after draining, when the session ends.
func (s *Server) termStream(ctx context.Context, ws *websocket.Conn, sess *termSession, next int64) {
	write := func(typ websocket.MessageType, p []byte) bool {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return ws.Write(wctx, typ, p) == nil
	}
	synced := false
	for {
		sess.mu.Lock()
		data, from := sess.ring.Since(next, termFrameBytes)
		ended, changed := sess.ended, sess.changed
		sess.mu.Unlock()

		if !synced || from != next {
			frame, _ := json.Marshal(termSyncFrame{Type: "sync", Offset: from})
			if !write(websocket.MessageText, frame) {
				return
			}
			synced = true
		}
		next = from
		if len(data) > 0 {
			if !write(websocket.MessageBinary, data) {
				return
			}
			next += int64(len(data))
			continue
		}
		if ended {
			_ = ws.Close(termStatusExited, "exited")
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}
