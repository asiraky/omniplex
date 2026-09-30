package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPShowSendsAbsolutePathAndReportsRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var got []map[string]string
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		auth = append(auth, r.Header.Get("Authorization"))
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
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a reply per request and none for the notification, got %q", lines)
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
	if len(got) != 2 || got[0]["path"] != filepath.Join(dir, "proto") || got[0]["title"] != "Proto" || got[0]["note"] != "first cut" {
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
