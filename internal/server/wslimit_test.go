package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/store"
)

// pairedClient pairs a device and returns an HTTP client carrying its cookie,
// so a test can open a socket the gate accepts.
func pairedClient(t *testing.T, ts *httptest.Server, guard *auth.Guard) *http.Client {
	t.Helper()

	code, err := guard.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"code": auth.FormatCode(code), "label": "test device"})
	res, err := client.Post(ts.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pairing returned %d, want 200", res.StatusCode)
	}
	return client
}

// A pasted prompt is routinely larger than the library's 32 KiB default read
// limit, and overrunning that limit is not a per-message failure: the server
// tears down the whole socket with 1009 before the frame is ever dispatched,
// and the client re-sends the same frame on reconnect and kills the new socket
// too. So the guarantee under test is that a large prompt is answered on a
// socket that stays up — an ack, not a close.
func TestLargePromptIsDispatchedRatherThanClosingTheSocket(t *testing.T) {
	handler, guard := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	client := pairedClient(t, ts, guard)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("a paired device could not open a socket: %v", err)
	}
	defer conn.CloseNow()
	// The server's own frames are far larger than the client's; read them all.
	conn.SetReadLimit(maxWSMessageBytes * 8)

	// Comfortably past the old 32 KiB ceiling, and past it by enough that a
	// stray envelope byte cannot be what makes this pass.
	const promptBytes = 512 * 1024
	args, err := json.Marshal(map[string]any{
		"sessionId": "no-such-session",
		"text":      strings.Repeat("x", promptBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(map[string]any{
		"type":      "command",
		"commandId": "large-prompt-1",
		"command":   "prompt",
		"args":      json.RawMessage(args),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) <= 32768 {
		t.Fatalf("the test frame is %d bytes, which the old limit would have allowed", len(frame))
	}

	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatalf("writing a %d-byte prompt failed: %v", len(frame), err)
	}

	// The session does not exist, so the command fails — that is fine and not
	// what is being checked. What matters is that it was dispatched at all and
	// answered by its command id, which can only happen on a live socket.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusMessageTooBig {
				t.Fatalf("the server closed the socket with 1009 on a %d-byte prompt", len(frame))
			}
			t.Fatalf("the socket did not survive a %d-byte prompt: %v", len(frame), err)
		}
		var got struct {
			Type      string `json:"type"`
			CommandID string `json:"commandId"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			continue
		}
		if got.Type == "ack" && got.CommandID == "large-prompt-1" {
			return
		}
	}
}

// The terminal carries pastes too, and it is a separate Accept call, so the
// cap has to be proven there independently rather than assumed to be shared.
func TestLargeTerminalPasteDoesNotCloseTheSocket(t *testing.T) {
	handler, guard, st := testServerWithStore(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	// A terminal needs a session with a real checkout to root the shell in.
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	if err := st.CreateSession(context.Background(), store.SessionMeta{
		ID: "term-session", Cwd: dir, Harness: "claudecode",
		Title: "terminal paste", CreatedAt: now, UpdatedAt: now, Phase: "idle",
	}); err != nil {
		t.Fatal(err)
	}

	client := pairedClient(t, ts, guard)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, res, err := websocket.Dial(ctx, wsURL+"/api/term?session=term-session", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		code := 0
		if res != nil {
			code = res.StatusCode
		}
		t.Fatalf("could not open a terminal socket (status %d): %v", code, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxWSMessageBytes * 8)

	// Past the old 32 KiB ceiling. Newline-free, so the shell buffers it as a
	// single unexecuted line rather than trying to run 256 KB of commands.
	paste, err := json.Marshal(map[string]any{
		"type": "input", "data": strings.Repeat("x", 256*1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paste) <= 32768 {
		t.Fatalf("the test frame is %d bytes, which the old limit would have allowed", len(paste))
	}
	if err := conn.Write(ctx, websocket.MessageText, paste); err != nil {
		t.Fatalf("writing a %d-byte paste failed: %v", len(paste), err)
	}

	// A shell echoes what it is fed, so a readable frame proves the socket
	// outlived the paste. A 1009 close is the regression.
	readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
	defer readCancel()
	if _, _, err := conn.Read(readCtx); err != nil {
		if websocket.CloseStatus(err) == websocket.StatusMessageTooBig {
			t.Fatalf("the terminal socket closed with 1009 on a %d-byte paste", len(paste))
		}
		if readCtx.Err() != nil {
			t.Fatalf("the terminal socket went quiet after a %d-byte paste", len(paste))
		}
		t.Fatalf("the terminal socket did not survive a %d-byte paste: %v", len(paste), err)
	}
}
