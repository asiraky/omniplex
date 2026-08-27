package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/asiraky/omniplex/internal/auth"
)

// The terminal surface: one WebSocket per open terminal tab, carrying a pty
// bound to the session's checkout. It is its own endpoint rather than a set of
// sync-protocol commands because a terminal is a stream, not a request — and
// because its lifetime is the tab's, not the session's. The gate covers it
// like every other route: an unpaired device never reaches the shell.
//
// Wire format: the client sends text frames of JSON — {"type":"input","data"}
// to type, {"type":"resize","cols","rows"} on layout change — and receives the
// pty's raw output as binary frames.

type termClientFrame struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (s *Server) serveTerm(w http.ResponseWriter, r *http.Request) {
	// Two things run here: the user's shell in a session's checkout, or a
	// harness's own sign-in flow for one provider instance. Both are a pty the
	// user types into; only what it runs differs.
	sessionID, login := r.URL.Query().Get("session"), r.URL.Query().Get("login")
	var cmd *exec.Cmd
	switch {
	case login != "":
		argv, env, err := s.mgr.LoginCommand(r.Context(), login)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Dir, _ = os.UserHomeDir()
		cmd.Env = append(env, "TERM=xterm-256color")
	case sessionID != "":
		root, err := s.mgr.SessionWorkspaceRoot(r.Context(), sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The user's own shell, as a login-ish interactive shell in the checkout.
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.Command(shell)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	default:
		http.Error(w, "session or login is required", http.StatusBadRequest)
		return
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
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Registered by device, so revoking the device cuts this shell too — same
	// contract as /ws.
	device, _ := auth.DeviceFrom(r.Context())
	s.termMu.Lock()
	s.termLive[ws] = device.ID
	s.termMu.Unlock()
	defer func() {
		s.termMu.Lock()
		delete(s.termLive, ws)
		s.termMu.Unlock()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	tty, err := pty.Start(cmd)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "pty failed")
		return
	}
	defer func() {
		_ = tty.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// pty -> socket. Reading the pty after the shell exits returns an error,
	// which is what ends this loop and the connection with it.
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := tty.Read(buf)
			if n > 0 {
				writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				werr := ws.Write(writeCtx, websocket.MessageBinary, buf[:n])
				writeCancel()
				if werr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
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
			if _, err := tty.Write([]byte(f.Data)); err != nil {
				return
			}
		case "resize":
			if f.Cols > 0 && f.Rows > 0 {
				_ = pty.Setsize(tty, &pty.Winsize{Cols: f.Cols, Rows: f.Rows})
			}
		}
	}
}
