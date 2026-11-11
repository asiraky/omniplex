// Package jsonrpc implements line-delimited JSON-RPC 2.0 over a subprocess's
// stdio, with bidirectional dispatch: we issue requests to the peer and also
// serve requests the peer issues to us.
package jsonrpc

import (
	"bufio"
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

	closeOnce sync.Once
	done      chan struct{}
	readErr   error
}

func NewConn(r io.Reader, w io.Writer, h Handler, n Notifier) *Conn {
	c := &Conn{w: w, handler: h, notifier: n, done: make(chan struct{})}
	c.life, c.endLife = context.WithCancel(context.Background())
	go c.readLoop(r)
	return c
}

// Done is closed when the peer's stream ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) readLoop(r io.Reader) {
	defer c.closeOnce.Do(func() { close(c.done) })

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // a harness writing non-JSON on stdout is not fatal
		}
		c.dispatch(m)
	}
	c.readErr = sc.Err()
	c.endLife()

	// Fail every in-flight call so no caller waits on a dead process.
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan Message)
		select {
		case ch <- Message{Error: &RPCError{Code: -32000, Message: "connection closed"}}:
		default:
		}
		return true
	})
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
