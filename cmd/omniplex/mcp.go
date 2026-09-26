package main

import (
	"archive/tar"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// The omniplex MCP server: `omniplex mcp`, run by a harness as a stdio MCP
// server beside each session. It gives the agent omniplex's own tools. Today
// that is one, publish_artefact.
//
// It is configured entirely by environment, set by the server that started
// the harness: where that server is, and a token that lets this process
// publish into its own session and nowhere else. It reads the files itself,
// with the harness's permissions, and streams them to the server as a tar, so
// the server never opens a path an agent names.

const mcpPublishTool = "publish_artefact"

const publishDescription = `Publish a file or folder as an artefact: something the user will open and read, look at, or pass on. The user may not be at your machine and cannot browse its files, so publishing is how they see what you made. It appears as a card in the conversation and opens in omniplex's viewer: HTML runs in a sandboxed mini browser, markdown renders with a source toggle, and PDF, images, SVG, audio, video, CSV, JSON and code all preview. Other types can be downloaded. The user can share it by link.

What counts as an artefact. Deliverables meant for a person: reports, write-ups, specs, plans, briefs, research summaries, comparisons, mockups and clickable prototypes, diagrams, charts, data exports, drafts of emails or documents, generated images or audio. Not artefacts: scratch or intermediate files, logs, test or build output, dependencies, secrets or .env files, or anything the user did not need to see.

In a git repository. When the working directory is a git repository, the user reviews your changes to the project as a diff, and that diff is the deliverable of a coding task. Never publish source changes, config, tests or the repository to show your work. Decide by what the user wants back:
- A change to keep or merge (a fix, a feature, a refactor, a doc that belongs in the repo): edit the project. Do not publish.
- Something to read, look at or decide on (explain, investigate, write up, compare options, draft, mock up, prototype before building, diagram): make an artefact.
Write an artefact's files outside the repository, in a scratch directory such as one under the system temp directory, so they never show up in the diff or get committed. Put them in the project only if the user asked for the file to live there. A document the user asked you to add to the project (an ADR, a README, a docs page) is a project change first; publish it as well only if they want to read or share it rendered.

Outside a git repository. There is no diff to review. The files you make for the user are the output: publish the ones they should see, once they are ready.

Versions. Publishing a name again adds a new version of that artefact, and the user can switch between versions. Pick a clear, stable name ("Onboarding research summary", not "output.md"), reuse it every time you revise that thing, and say what changed in the note. Use a new name only for a separate deliverable. Publish when a version is worth looking at, not after every small edit. To revise a file the user attached, publish your edited copy under its name.

Formats. Prefer what the viewer renders: markdown for documents, HTML for anything interactive or visually designed, CSV for tables, SVG or PNG for diagrams and charts. For a multi-file HTML prototype, publish its folder: index.html is the entry, and relative links, scripts, styles and images work. Keep it self-contained, with no build step and no server. One version is capped at 200 MB and 2000 files.`

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
				"name":        mcpPublishTool,
				"description": publishDescription,
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []string{"path"},
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "File or folder to publish. Relative paths resolve against the working directory. A folder is published as one bundle; symlinks, .git and node_modules are left out."},
						"name": map[string]any{"type": "string", "description": "Title the user sees, and the key for versions: the same name adds a version, a new name makes a new artefact. Defaults to the file or folder name, so pass a readable one."},
						"note": map[string]any{"type": "string", "description": "One line on what this version is or, for a revision, what changed."},
					},
				},
			}}}, nil)
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Path string `json:"path"`
					Name string `json:"name"`
					Note string `json:"note"`
				} `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil || p.Name != mcpPublishTool {
				reply(m.ID, nil, &rpcErr{Code: -32602, Message: "unknown tool"})
				continue
			}
			text, err := publish(base, token, p.Arguments.Path, p.Arguments.Name, p.Arguments.Note)
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

// skipDirs are never part of a published bundle.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".DS_Store": true}

func publish(base, token, p, name, note string) (string, error) {
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
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("cannot publish %s: %w", p, err)
	}
	if name == "" {
		name = filepath.Base(abs)
	}

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeTar(pw, abs, info)) }()

	q := url.Values{"name": {name}}
	if note != "" {
		q.Set("note", note)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/api/agent/artefacts?"+q.Encode(), pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-tar")
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
		return "", fmt.Errorf("omniplex refused the artefact (%d): %s", res.StatusCode, e.Error)
	}
	var pub struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		Entry   string `json:"entry"`
		Files   int    `json:"files"`
	}
	json.Unmarshal(body, &pub)
	what := pub.Entry
	if pub.Files > 1 {
		what = fmt.Sprintf("%d files, opens on %s", pub.Files, pub.Entry)
	}
	return fmt.Sprintf("Published %q version %d (%s). It is attached to the session and the user can open it from the conversation.", pub.Name, pub.Version, what), nil
}

// writeTar writes a file, or a directory's regular files, as a tar. Symlinks
// are skipped: following one could publish something outside the folder.
func writeTar(w io.Writer, root string, info fs.FileInfo) error {
	tw := tar.NewWriter(w)
	add := func(p, rel string, fi fs.FileInfo) error {
		h := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}
	if !info.IsDir() {
		if err := add(root, filepath.Base(root), info); err != nil {
			return err
		}
		return tw.Close()
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		return add(p, rel, fi)
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
