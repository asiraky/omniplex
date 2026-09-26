package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestMCPPublishSendsFolderAsTarWithoutJunkOrLinks(t *testing.T) {
	dir := t.TempDir()
	proto := filepath.Join(dir, "proto")
	os.MkdirAll(filepath.Join(proto, "assets"), 0o755)
	os.MkdirAll(filepath.Join(proto, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(proto, "index.html"), []byte("<h1>"), 0o644)
	os.WriteFile(filepath.Join(proto, "assets", "a.css"), []byte("b{}"), 0o644)
	os.WriteFile(filepath.Join(proto, "node_modules", "x", "i.js"), []byte("junk"), 0o644)
	os.Symlink("/etc/passwd", filepath.Join(proto, "leak"))

	var got []string
	var auth, name, note string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, name, note = r.Header.Get("Authorization"), r.URL.Query().Get("name"), r.URL.Query().Get("note")
		tr := tar.NewReader(r.Body)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			got = append(got, h.Name)
		}
		json.NewEncoder(w).Encode(map[string]any{"name": name, "version": 3, "entry": "index.html", "files": len(got)})
	}))
	defer srv.Close()
	t.Setenv("OMNIPLEX_URL", srv.URL)
	t.Setenv("OMNIPLEX_AGENT_TOKEN", "tok")

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"publish_artefact","arguments":{"path":"` + proto + `","note":"first cut"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"publish_artefact","arguments":{"path":"` + dir + `/missing"}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := runMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
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
	sort.Strings(got)
	if strings.Join(got, ",") != "assets/a.css,index.html" {
		t.Fatalf("tar held %v", got)
	}
	if auth != "Bearer tok" || name != "proto" || note != "first cut" {
		t.Fatalf("auth=%q name=%q note=%q", auth, name, note)
	}
	if !strings.Contains(lines[1], `version 3`) || strings.Contains(lines[1], `isError`) {
		t.Fatalf("publish reply: %s", lines[1])
	}
	if !strings.Contains(lines[2], `"isError":true`) {
		t.Fatalf("missing path must be a tool error: %s", lines[2])
	}
}
