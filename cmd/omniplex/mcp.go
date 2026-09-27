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
// server beside each session. It gives the agent omniplex's own tools. Today
// that is one, show_file.
//
// It is configured entirely by environment, set by the server that started
// the harness: where that server is, a token that lets this process show
// files in its own session and nowhere else, and the session's home folder.

const mcpShowTool = "show_file"

// showDescription tells the agent what the tool is for. home is where the
// session keeps what it makes; cwd is where the agent works.
func showDescription(home, cwd string) string {
	var b strings.Builder
	b.WriteString(`Show the user a file or folder you made for them: a report, a write-up, a plan, a mockup, a clickable prototype, a diagram, a chart, a data export. The user may not be at your machine and cannot browse its files, so this is how they see your work. It appears as a card in the conversation and opens in omniplex's viewer: HTML runs in a sandboxed mini browser, markdown renders, and PDF, images, SVG, audio, video, CSV, JSON and code all preview. Other types download.

The file stays where it is and the viewer reads it live. There are no copies and no versions. To revise it, edit the same file in place, then call show_file again with the same path and a note saying what changed. Never make report-v2.md next to report.md: overwrite report.md.

`)
	if home != "" {
		fmt.Fprintf(&b, "Where to put it. Put what you make for the user in %s, the project's folder for these things, unless they asked for it somewhere else. Give each one a clear name, since that is the title the user sees.", home)
		if cwd != "" && !within(home, cwd) {
			b.WriteString(" Your working directory is a git repository or a copy of one. Files you make there show up in the user's diff and can get committed, so do not put deliverables in it.")
		}
		b.WriteString("\n\n")
	}
	b.WriteString(`What to show. Deliverables meant for a person: reports, specs, plans, briefs, research summaries, comparisons, mockups and prototypes, diagrams, charts, data exports, drafts of emails or documents, generated images or audio. Not: scratch or intermediate files, logs, test or build output, dependencies, secrets. When the task is a change to the code (a fix, a feature, a refactor), the diff is the deliverable: edit the project and show nothing. Show something when the user wants to read it, look at it or decide on it. A file the user attached is already in front of them; show it again only after you have changed it.

Formats. Prefer what the viewer renders: markdown for documents, HTML for anything interactive or visually designed, CSV for tables, SVG or PNG for diagrams and charts. A multi-file HTML prototype is a folder with index.html in it: show the folder, and relative links, scripts, styles and images work. Keep it self-contained, with no build step and no server. Hidden files and node_modules are never part of a folder.

Only the user shares. Sharing takes a snapshot for a link; your later edits reach it only when they update the link.`)
	return b.String()
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

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
	cwd, _ := os.Getwd()
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
			}, nil)
		case "ping":
			reply(m.ID, map[string]any{}, nil)
		case "tools/list":
			reply(m.ID, map[string]any{"tools": []any{map[string]any{
				"name":        mcpShowTool,
				"description": showDescription(home, cwd),
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
