package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMCPShowSendsAbsolutePathAndReportsRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var mu sync.Mutex
	got := map[string]map[string]string{}
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got[filepath.Base(body["path"])] = body
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.HasSuffix(body["path"], "secret") {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "that is outside this project"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"name": "Proto", "entry": "index.html", "files": 2})
	}))
	defer srv.Close()
	t.Setenv("OMNIPLEX_URL", srv.URL)
	t.Setenv("OMNIPLEX_AGENT_TOKEN", "tok")

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"show_file","arguments":{"path":"proto","title":"Proto","note":"first cut"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"show_file","arguments":{"path":"/etc/secret"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"show_file","arguments":{}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := runMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	// Calls run concurrently, so replies come in any order: index them by id.
	byID := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m struct {
			ID json.RawMessage `json:"id"`
		}
		json.Unmarshal([]byte(line), &m)
		byID[string(m.ID)] = line
	}
	lines := []string{byID["1"], byID["2"], byID["3"], byID["4"]}
	if len(byID) != 4 || slices.Contains(lines, "") {
		t.Fatalf("want a reply per request and none for the notification, got %q", out.String())
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[0]), &init)
	if init.Result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("protocol version not echoed: %s", lines[0])
	}

	// The server does not know the agent's working directory: the tool
	// resolves relative paths before sending them.
	if p := got["proto"]; len(got) != 2 || p["path"] != filepath.Join(dir, "proto") || p["title"] != "Proto" || p["note"] != "first cut" {
		t.Fatalf("server got %v", got)
	}
	if auth[0] != "Bearer tok" {
		t.Fatalf("auth = %q", auth[0])
	}
	if strings.Contains(lines[1], "isError") || !strings.Contains(lines[1], "2 files") {
		t.Fatalf("show reply: %s", lines[1])
	}
	// A refusal reaches the agent as a tool error carrying the server's reason,
	// so it can move the file and try again.
	if !strings.Contains(lines[2], `"isError":true`) || !strings.Contains(lines[2], "outside this project") {
		t.Fatalf("refusal: %s", lines[2])
	}
	if !strings.Contains(lines[3], `"isError":true`) {
		t.Fatalf("missing path must be a tool error: %s", lines[3])
	}
}

// mcpConn runs the server over pipes, as a harness does, so a test can
// interleave requests with what is still in flight.
type mcpConn struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
	done  chan error
}

func startMCP(t *testing.T, url string) *mcpConn {
	t.Helper()
	t.Setenv("OMNIPLEX_URL", url)
	t.Setenv("OMNIPLEX_AGENT_TOKEN", "tok")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &mcpConn{t: t, in: inW, lines: make(chan string, 64), done: make(chan error, 1)}
	go func() {
		err := runMCP(inR, outW)
		outW.Close()
		c.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *mcpConn) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

type mcpReply struct {
	ID     json.RawMessage `json:"id"`
	Result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *rpcErr `json:"error"`
}

func (r mcpReply) text() string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	return r.Result.Content[0].Text
}

// next is the next reply, failing after a while without one.
func (c *mcpConn) next() mcpReply {
	c.t.Helper()
	select {
	case line, ok := <-c.lines:
		if !ok {
			c.t.Fatal("server closed its output")
		}
		var r mcpReply
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			c.t.Fatalf("reply %q: %v", line, err)
		}
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatal("no reply")
	}
	return mcpReply{}
}

// close ends the input and returns every reply still to come.
func (c *mcpConn) close() []mcpReply {
	c.t.Helper()
	c.in.Close()
	var rest []mcpReply
	for line := range c.lines {
		var r mcpReply
		json.Unmarshal([]byte(line), &r)
		rest = append(rest, r)
	}
	if err := <-c.done; err != nil {
		c.t.Fatal(err)
	}
	return rest
}

func TestMCPAgentToolForwardsToOmniplex(t *testing.T) {
	type seen struct{ path, auth, body string }
	got := make(chan seen, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- seen{r.URL.Path, r.Header.Get("Authorization"), string(body)}
		if strings.HasSuffix(r.URL.Path, "/remove_skill") {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "no skill named ghost"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "Saved linear for this project. Claude: now."})
	}))
	defer srv.Close()
	c := startMCP(t, srv.URL)

	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_mcp_server","arguments":{"config":"https://mcp.linear.app/mcp","scope":"project"}}}`)
	r := c.next()
	if r.Result.IsError || r.text() != "Saved linear for this project. Claude: now." {
		t.Fatalf("reply %+v", r)
	}
	s := <-got
	if s.path != "/api/agent/tools/add_mcp_server" || s.auth != "Bearer tok" {
		t.Fatalf("server saw %+v", s)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(s.body), &args); err != nil || args["config"] != "https://mcp.linear.app/mcp" || args["scope"] != "project" {
		t.Fatalf("arguments reached omniplex as %q", s.body)
	}

	// A refusal is a tool error carrying omniplex's reason, not a protocol
	// error: the agent reads it and can try again.
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remove_skill","arguments":{"name":"ghost"}}}`)
	r = c.next()
	if r.Error != nil || !r.Result.IsError || r.text() != "no skill named ghost" {
		t.Fatalf("reply %+v", r)
	}
	<-got

	// A tool with no arguments still sends an object.
	c.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_skills"}}`)
	c.next()
	if s := <-got; s.path != "/api/agent/tools/list_skills" || s.body != "{}" {
		t.Fatalf("server saw %+v", s)
	}

	c.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"drop_tables","arguments":{}}}`)
	if r := c.next(); r.Error == nil {
		t.Fatalf("unknown tool answered %+v", r)
	}
	c.close()
}

// A write waits on a person. While it does, the harness's pings and other
// calls are answered.
func TestMCPSlowCallDoesNotBlockTheConnection(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/add_account") {
			<-release
			json.NewEncoder(w).Encode(map[string]string{"text": "saved"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "listed"})
	}))
	defer srv.Close()
	c := startMCP(t, srv.URL)

	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add_account","arguments":{"cli":"gws","name":"work"}}}`)
	c.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if r := c.next(); string(r.ID) != "2" {
		t.Fatalf("first reply is %s, want the ping", r.ID)
	}
	c.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_sign_ins","arguments":{}}}`)
	if r := c.next(); string(r.ID) != "3" || r.text() != "listed" {
		t.Fatalf("reply %+v, want the listing", r)
	}
	close(release)
	if r := c.next(); string(r.ID) != "1" || r.text() != "saved" {
		t.Fatalf("reply %+v, want the write", r)
	}
	c.close()
}

// notifications/cancelled drops the request to omniplex, which is how the
// server learns the agent stopped waiting, and the call gets no reply.
func TestMCPCancelledCallDropsItsRequest(t *testing.T) {
	started, gone := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Go notices a client gone only once the body has been read, as
		// omniplex's own handler reads it.
		io.ReadAll(r.Body)
		close(started)
		<-r.Context().Done()
		close(gone)
	}))
	defer srv.Close()
	c := startMCP(t, srv.URL)

	c.send(`{"jsonrpc":"2.0","id":"call-7","method":"tools/call","params":{"name":"install_skill","arguments":{"source":"owner/repo"}}}`)
	<-started
	c.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"call-7","reason":"timed out"}}`)
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the request to omniplex outlived the cancellation")
	}
	c.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if r := c.next(); string(r.ID) != "8" {
		t.Fatalf("reply to %s, want only the ping", r.ID)
	}
	if rest := c.close(); len(rest) != 0 {
		t.Fatalf("a cancelled call was answered: %+v", rest)
	}
}
