// Package jsonrpc implements line-delimited JSON-RPC 2.0 over a subprocess's
// stdio, with bidirectional dispatch: we issue requests to the peer and also
// serve requests the peer issues to us.
package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Message is the union of request, response, notification, and error.
type Message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// Handler serves an inbound request from the peer. Returning an error becomes
// a JSON-RPC error response.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Notifier receives inbound notifications (no id).
type Notifier func(method string, params json.RawMessage)

// Conn is a bidirectional JSON-RPC connection over an io.Reader/io.Writer pair.
type Conn struct {
	w      io.Writer
	wmu    sync.Mutex
	nextID atomic.Int64

	pending sync.Map // string -> chan Message
	inbound sync.Map // compact id -> *inbound, one per request being served

	// life parents every inbound handler's context; it ends with the stream,
	// since nobody is left to read an answer to what the peer asked.
	life    context.Context
	endLife context.CancelFunc

	handler  Handler
	notifier Notifier

	logf      func(format string, args ...any)
	closeOnce sync.Once
	done      chan struct{}
	readErr   error
}

// maxLine is the longest message Conn reads; a longer one is skipped. A codex
// thread/resume answers with the whole rollout, and rollouts run to 95 MB.
const maxLine = 128 << 20

// NewConn starts reading r. logf, when not nil, hears about the lines Conn
// had to skip.
func NewConn(r io.Reader, w io.Writer, h Handler, n Notifier, logf func(format string, args ...any)) *Conn {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	c := &Conn{w: w, handler: h, notifier: n, logf: logf, done: make(chan struct{})}
	c.life, c.endLife = context.WithCancel(context.Background())
	go c.readLoop(r)
	return c
}

// Done is closed when the peer's stream ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is the read error that ended the peer's stream, or nil when it ended
// cleanly. It is only meaningful once Done is closed.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.readErr
	default:
		return nil
	}
}

func (c *Conn) readLoop(r io.Reader) {
	c.readErr = ReadLines(r, maxLine, func(line []byte) {
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			// A harness writing non-JSON on stdout is not fatal.
			c.logf("jsonrpc: skipped a line that is not JSON: %s", Brief(line))
			return
		}
		c.dispatch(m)
	}, c.skipped)
	c.endLife()

	// Done closes first, so a caller whose call fails here already sees the
	// connection as gone. Then fail every in-flight call so no caller waits on
	// a dead process.
	c.closeOnce.Do(func() { close(c.done) })
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan Message)
		select {
		case ch <- Message{Error: &RPCError{Code: -32000, Message: "connection closed"}}:
		default:
		}
		return true
	})
}

// skipped handles a message too large to read. When it is a response, the
// call waiting on it is failed rather than left to hang: codex writes the id
// first, so it is in the head.
func (c *Conn) skipped(head []byte, size int) {
	c.logf("jsonrpc: skipped a %d-byte message over the %d-byte limit: %s", size, maxLine, Brief(head))
	id := leadingID(head)
	if id == "" {
		return
	}
	if ch, ok := c.pending.LoadAndDelete(id); ok {
		select {
		case ch.(chan Message) <- Message{Error: &RPCError{Code: codeTooLarge, Message: fmt.Sprintf("response too large to read (%d bytes)", size)}}:
		default:
		}
	}
}

// codeTooLarge marks the error a call gets when its response was skipped. It
// is in the range JSON-RPC leaves to implementations, and never crosses the
// wire.
const codeTooLarge = -32099

// TooLarge reports whether err is a call failed because its response was
// over maxLine.
func TooLarge(err error) bool {
	var re *RPCError
	return errors.As(err, &re) && re.Code == codeTooLarge
}

// leadingID is the id of a response whose head is all there is, when the
// head reaches "result" or "error" with nothing but "jsonrpc" and "id" before
// it — the order codex and the claude bridge write. Anything else — a
// request, a notification, an id further in — gives "": a request's id is the
// peer's, and could match one of ours.
func leadingID(head []byte) string {
	dec := json.NewDecoder(bytes.NewReader(head))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ""
	}
	var id json.RawMessage
	for {
		key, err := dec.Token()
		if err != nil {
			return ""
		}
		switch key {
		case "jsonrpc":
			var v string
			if err := dec.Decode(&v); err != nil {
				return ""
			}
		case "id":
			if err := dec.Decode(&id); err != nil {
				return ""
			}
		case "result", "error":
			return string(id)
		default:
			return ""
		}
	}
}

// Brief is the start of b, for a log line.
func Brief(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

func (c *Conn) dispatch(m Message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		// Inbound request: serve it off the read loop so a slow handler
		// (a permission prompt awaiting a human) cannot stall the stream.
		// Its cancel is registered here, on the read loop, so a withdrawal
		// the peer sends right behind it always finds it.
		ctx, cancel := context.WithCancel(c.life)
		in := &inbound{cancel: cancel}
		c.inbound.Store(idKey(m.ID), in)
		go c.serve(ctx, in, m)
	case m.Method != "":
		if c.notifier != nil {
			c.notifier(m.Method, m.Params)
		}
	case len(m.ID) > 0:
		if ch, ok := c.pending.LoadAndDelete(string(m.ID)); ok {
			select {
			case ch.(chan Message) <- m:
			default:
			}
		}
	}
}

// inbound is a request of the peer's being served. A pointer, so a finished
// handler removes its own entry and not a later request that reused the id.
type inbound struct{ cancel context.CancelFunc }

func (c *Conn) serve(ctx context.Context, in *inbound, m Message) {
	defer in.cancel()
	defer c.inbound.CompareAndDelete(idKey(m.ID), in)
	if c.handler == nil {
		_ = c.send(Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32601, Message: "method not found"}})
		return
	}
	result, err := c.handler(ctx, m.Method, m.Params)
	if ctx.Err() != nil {
		// The peer withdrew the request, or went away: it is not waiting on
		// an answer, whatever the handler made of the cancel.
		return
	}
	if err != nil {
		_ = c.send(Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32000, Message: err.Error()}})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		_ = c.send(Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32603, Message: err.Error()}})
		return
	}
	_ = c.send(Message{JSONRPC: "2.0", ID: m.ID, Result: raw})
}

func (c *Conn) send(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.send(Message{JSONRPC: "2.0", Method: method, Params: raw})
}

// Call issues a request and waits for the response.
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	key := string(idRaw)

	ch := make(chan Message, 1)
	c.pending.Store(key, ch)
	defer c.pending.Delete(key)

	if err := c.send(Message{JSONRPC: "2.0", ID: idRaw, Method: method, Params: raw}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("connection closed before response")
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

// CancelInbound cancels the context of the handler serving the peer's
// request id, for a peer that settled the request on its own. No response is
// sent for it. Reports whether such a request was being served.
func (c *Conn) CancelInbound(id json.RawMessage) bool {
	v, ok := c.inbound.LoadAndDelete(idKey(id))
	if ok {
		v.(*inbound).cancel()
	}
	return ok
}

// idKey is a request id in a form both ends agree on, whatever whitespace the
// peer echoed it back with.
func idKey(id json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return string(id)
	}
	return b.String()
}

// Respond replies to an inbound request that was handled out of band.
func (c *Conn) Respond(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.send(Message{JSONRPC: "2.0", ID: id, Result: raw})
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}
