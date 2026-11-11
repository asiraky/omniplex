package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// peer is the far end of a Conn: it writes lines the Conn reads and reads
// the lines the Conn writes.
type peer struct {
	in  *io.PipeWriter
	out *bufio.Scanner
}

func (p peer) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (p peer) read(t *testing.T) Message {
	t.Helper()
	lines := make(chan Message, 1)
	go func() {
		if p.out.Scan() {
			var m Message
			_ = json.Unmarshal(p.out.Bytes(), &m)
			lines <- m
		}
	}()
	select {
	case m := <-lines:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no message from conn")
		return Message{}
	}
}

func newPair(t *testing.T, h Handler) (*Conn, peer) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := NewConn(inR, outW, h, nil)
	t.Cleanup(func() { _ = inW.Close(); _ = outR.Close() })
	return c, peer{in: inW, out: bufio.NewScanner(outR)}
}

// blocking is a handler that waits for its context, reports why it stopped,
// and answers anyway.
func blocking(started chan<- struct{}, stopped chan<- error) Handler {
	return func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "fast" {
			return "ok", nil
		}
		started <- struct{}{}
		<-ctx.Done()
		stopped <- ctx.Err()
		return "too late", nil
	}
}

func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func TestCancelInboundStopsTheHandlerAndSendsNoResponse(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	c, p := newPair(t, blocking(started, stopped))

	p.send(t, `{"jsonrpc":"2.0","id":7,"method":"approve"}`)
	wait(t, started)

	// The peer echoes the id back in its own formatting.
	if !c.CancelInbound(json.RawMessage(` 7 `)) {
		t.Fatal("CancelInbound did not find the request being served")
	}
	if err := wait(t, stopped); err != context.Canceled {
		t.Fatalf("handler stopped with %v, want context.Canceled", err)
	}

	// The next line the peer sees answers a later request, not the withdrawn one.
	p.send(t, `{"jsonrpc":"2.0","id":8,"method":"fast"}`)
	if m := p.read(t); string(m.ID) != "8" {
		t.Fatalf("got response for id %s, want only id 8", m.ID)
	}
	if c.CancelInbound(json.RawMessage(`7`)) {
		t.Fatal("a finished request is still cancellable")
	}
}

func TestCancelInboundKeepsStringAndNumberIDsApart(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	c, p := newPair(t, blocking(started, stopped))

	p.send(t, `{"jsonrpc":"2.0","id":"7","method":"approve"}`)
	wait(t, started)

	if c.CancelInbound(json.RawMessage(`7`)) {
		t.Fatal("number id 7 cancelled the request with string id \"7\"")
	}
	select {
	case err := <-stopped:
		t.Fatalf("handler stopped early: %v", err)
	default:
	}
	if !c.CancelInbound(json.RawMessage(`"7"`)) {
		t.Fatal("string id did not find its request")
	}
	wait(t, stopped)
}

func TestStreamEndCancelsHandlersInFlight(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	_, p := newPair(t, blocking(started, stopped))

	p.send(t, `{"jsonrpc":"2.0","id":"a","method":"approve"}`)
	wait(t, started)
	_ = p.in.Close()

	if err := wait(t, stopped); err != context.Canceled {
		t.Fatalf("handler stopped with %v, want context.Canceled", err)
	}
}

// A peer may withdraw a request in the line right behind it, before the
// handler has even started; the withdrawal must still find it.
func TestWithdrawalRightBehindItsRequestIsNotLost(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan error, 1)
	inR, inW := io.Pipe()
	var c *Conn
	ready := make(chan struct{})
	c = NewConn(inR, io.Discard, blocking(started, stopped), func(method string, params json.RawMessage) {
		<-ready
		var p struct{ ID json.RawMessage }
		_ = json.Unmarshal(params, &p)
		c.CancelInbound(p.ID)
	})
	close(ready)
	t.Cleanup(func() { _ = inW.Close() })

	_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":3,"method":"approve"}`+"\n"+`{"jsonrpc":"2.0","method":"withdraw","params":{"id":3}}`+"\n")
	if err := wait(t, stopped); err != context.Canceled {
		t.Fatalf("handler stopped with %v, want context.Canceled", err)
	}
}
