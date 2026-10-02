package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The omniplex MCP server: `omniplex mcp`, run by a harness as a stdio MCP
// server beside each thread. It gives the agent omniplex's own tools. Today
// that is one, show_file, and the instructions for when to make an artefact.
//
// It is configured entirely by environment, set by the server that started
// the harness: where that server is, a token that lets this process show
// files in its own thread and nowhere else, and the thread's home folder.

const mcpShowTool = "show_file"

// serverInstructions go to the agent with the server, before any tool is
// loaded. Harnesses that defer MCP tools show only the tool's name until the
// agent searches for it, so the choice between an artefact and a code change
// has to be made here, not in the tool description. home is the project
// folder artefacts live in.
func serverInstructions(home string) string {
	where := "the project folder"
	if home != "" {
		where += ", " + home
	}
	return `You have two kinds of output: code changes and artefacts. An artefact is a self-contained file the user opens from the conversation: a report, plan, spec, research write-up, comparison, design, mockup, prototype, diagram, chart, data export or draft. Choose the kind before you start work.

- A request for something to read, look at or decide on: artefact.
- A request that changes how a codebase behaves (a fix, a feature, a refactor): code change.
- In a repository, a design request can be either a new look to explore (artefact) or a change to the app's UI (code change). Judge from the request, and ask the user when it could be either.

Artefacts live in ` + where + `. Use markdown for documents, HTML for anything visual or interactive, CSV for tables, SVG for diagrams. An HTML artefact opens directly in a browser: one file with inline CSS and JS, or a folder with index.html when it needs images. Write it there, then present it with mcp__omniplex__` + mcpShowTool + `.`
}

// showDescription is how to present an artefact once the agent has chosen to
// make one. When to make one is in serverInstructions.
const showDescription = `Present an artefact to the user: a file or folder you made for them. It appears as a card in the conversation and opens in omniplex's viewer: HTML runs in a sandboxed mini browser, markdown renders, and PDF, images, SVG, audio, video, CSV, JSON and code all preview. Other types download.

The file stays where it is and the viewer reads it live. There are no copies and no versions. To revise it, edit the same file in place, then call show_file again with the same path and a note saying what changed: overwrite report.md rather than writing report-v2.md beside it.

For a folder, show the folder: it opens on its index.html, and relative links, scripts, styles and images work. Hidden files and node_modules are never part of a folder.

A file the user attached is already in front of them; show it again only after you have changed it.

Only the user shares. Sharing takes a snapshot for a link; your later edits reach it only when they update the link.`

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func runMCP(in io.Reader, out io.Writer) error {
	base := os.Getenv("OMNIPLEX_URL")
	token := os.Getenv("OMNIPLEX_AGENT_TOKEN")
	home := os.Getenv("OMNIPLEX_HOME")
	enc := json.NewEncoder(out)
	reply := func(id json.RawMessage, result any, e *rpcErr) {
		msg := map[string]any{"jsonrpc": "2.0", "id": id}
		if e != nil {
			msg["error"] = e
		} else {
			msg["result"] = result
		}
		enc.Encode(msg)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m rpcMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if len(m.ID) == 0 {
			continue // a notification
		}
		switch m.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(m.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			reply(m.ID, map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "omniplex", "version": "0.1.0"},
				"instructions":    serverInstructions(home),
			}, nil)
		case "ping":
			reply(m.ID, map[string]any{}, nil)
		case "tools/list":
			reply(m.ID, map[string]any{"tools": []any{map[string]any{
				"name":        mcpShowTool,
				"description": showDescription,
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []string{"path"},
					"properties": map[string]any{
						"path":  map[string]any{"type": "string", "description": "File or folder to show. Relative paths resolve against the working directory. Showing a path again updates the same card."},
						"title": map[string]any{"type": "string", "description": "Title the user sees. Defaults to the file or folder name."},
						"note":  map[string]any{"type": "string", "description": "One line on what this is or, when showing it again, what changed."},
					},
				},
			}}}, nil)
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Path  string `json:"path"`
					Title string `json:"title"`
					Note  string `json:"note"`
				} `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil || p.Name != mcpShowTool {
				reply(m.ID, nil, &rpcErr{Code: -32602, Message: "unknown tool"})
				continue
			}
			text, err := showFile(base, token, p.Arguments.Path, p.Arguments.Title, p.Arguments.Note)
			if err != nil {
				reply(m.ID, map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}, nil)
				continue
			}
			reply(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}, nil)
		default:
			reply(m.ID, nil, &rpcErr{Code: -32601, Message: "method not found: " + m.Method})
		}
	}
	return sc.Err()
}

func showFile(base, token, p, title, note string) (string, error) {
	if base == "" || token == "" {
		return "", errors.New("omniplex did not configure this tool (OMNIPLEX_URL / OMNIPLEX_AGENT_TOKEN unset)")
	}
	if p == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	reqBody, _ := json.Marshal(map[string]string{"path": abs, "title": title, "note": note})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/api/agent/artefacts", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach omniplex: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return "", fmt.Errorf("omniplex could not show %s (%d): %s", p, res.StatusCode, e.Error)
	}
	var shown struct {
		Name  string `json:"name"`
		Entry string `json:"entry"`
		Files int    `json:"files"`
	}
	json.Unmarshal(body, &shown)
	what := shown.Entry
	if shown.Files > 1 {
		what = fmt.Sprintf("%d files, opens on %s", shown.Files, shown.Entry)
	}
	return fmt.Sprintf("Showing %q (%s). The user can open it from the conversation. To revise it, edit the file in place and show it again.", shown.Name, what), nil
}
