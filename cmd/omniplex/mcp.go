package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The omniplex MCP server: `omniplex mcp`, run by a harness as a stdio MCP
// server beside each thread. It gives the agent omniplex's own tools:
// show_file, with the instructions for when to make an artefact, and the
// tools that list and propose MCP servers, skills and sign-ins.
//
// It is configured entirely by environment, set by the server that started
// the harness: where that server is, a token that lets this process act in
// its own thread and nowhere else, and the thread's home folder.

const mcpShowTool = "show_file"

// mcpToolTimeout is how long a harness waits on one of these tools. A write
// waits on a person tapping a card, maybe on a phone, and omniplex gives up
// waiting after 25 minutes; the harness must outlast that.
const mcpToolTimeout = 30 * time.Minute

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

Artefacts live in ` + where + `. Use markdown for documents, HTML for anything visual or interactive, CSV for tables, SVG for diagrams. An HTML artefact opens directly in a browser: one file with inline CSS and JS, or a folder with index.html when it needs images. Write it there, then present it with mcp__omniplex__` + mcpShowTool + `.

MCP servers, skills and CLI sign-ins are Omniplex's to manage: when the user asks to add or remove one, use the omniplex tools for it (list_mcp_servers, add_mcp_server, install_skill, add_sign_in and the rest) rather than editing config or skills folders, and load the omniplex skill first.`
}

// showDescription is how to present an artefact once the agent has chosen to
// make one. When to make one is in serverInstructions.
const showDescription = `Present an artefact to the user: a file or folder you made for them. It appears as a card in the conversation and opens in omniplex's viewer: HTML runs in a sandboxed mini browser, markdown renders, and PDF, images, SVG, audio, video, CSV, JSON and code all preview. Other types download.

The file stays where it is and the viewer reads it live. There are no copies and no versions. To revise it, edit the same file in place, then call show_file again with the same path and a note saying what changed: overwrite report.md rather than writing report-v2.md beside it.

For a folder, show the folder: it opens on its index.html, and relative links, scripts, styles and images work. Hidden files and node_modules are never part of a folder.

A file the user attached is already in front of them; show it again only after you have changed it.

Only the user shares. Sharing takes a snapshot for a link; your later edits reach it only when they update the link.`

// agentTool is one of the tools served by omniplex's /api/agent/tools/{name}
// route: the arguments go to it as they came, and its text comes back.
type agentTool struct {
	name        string
	description string
	properties  map[string]any
	required    []string
}

// writeNote ends every write tool's description.
const writeNote = ` Nothing changes until the user taps the card this raises, in every permission mode, bypass included; they can edit it first. The call waits for that tap, often minutes, and returns what the user did (saved, edited and saved, or declined) and when you can use the result. If the call times out, the card still works: check with the matching list_ tool before going on.`

// secretNote is for the writes that can carry credentials.
const secretNote = ` Pass a secret value only when the user has already given it to you; otherwise leave it out and the user types it into the card's field. Never ask the user to paste a secret into the chat.`

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strEnum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

const (
	scopeDesc       = "project: this thread's project only. everywhere: every thread in every project. Defaults to project in a project, else everywhere. Use everywhere only when the user asks for it."
	destinationDesc = "project: the project's own, private, never committed. repo: committed into the repository, for everyone who clones it. personal: everywhere, for every project. Defaults to project in a project, else personal."
)

var agentTools = []agentTool{
	{
		name:        "list_mcp_servers",
		description: "List the MCP servers Omniplex gives this thread's sessions: name, scope (this project or everywhere), status, which harnesses get each, and whether it is off in this project. Never shows header or env values.",
	},
	{
		name:        "list_skills",
		description: "List the skills Omniplex manages for this thread: name, where it lives (project, repo, personal), which harnesses load it, whether it is manual-only, and whether it is read-only.",
	},
	{
		name:        "list_sign_ins",
		description: "List the command-line tools Omniplex holds sign-ins for, each account and whether it is signed in, and how to use each account: the env to set on the commands you run, e.g. work: GOOGLE_WORKSPACE_CLI_CONFIG_DIR=… .",
	},
	{
		name:        "add_mcp_server",
		description: "Propose adding an MCP server to Omniplex, which gives it to every harness that can run it." + writeNote + secretNote,
		properties: map[string]any{
			"config": str("The server as the vendor's docs give it, in any of these forms: a `claude mcp add …` or `codex mcp add …` command, an mcpServers JSON snippet, or the server's URL."),
			"scope":  strEnum(scopeDesc, "project", "everywhere"),
		},
		required: []string{"config"},
	},
	{
		name:        "remove_mcp_server",
		description: "Propose removing an MCP server from Omniplex. Take the name and scope from list_mcp_servers." + writeNote,
		properties: map[string]any{
			"name":  str("The server's name."),
			"scope": strEnum("Which server of that name: project or everywhere. Defaults to project in a project, else everywhere.", "project", "everywhere"),
		},
		required: []string{"name"},
	},
	{
		name:        "install_skill",
		description: "Propose installing skills from a source. Omniplex fetches it now and the card shows the skills found, their files and where they go." + writeNote,
		properties: map[string]any{
			"source":      str("Where the skills are: owner/repo, a git URL, a URL to a folder in a repo, a local folder, or a `npx skills add …` command."),
			"skills":      strList("Names of the skills to install from the source. Omit to offer every skill found."),
			"destination": strEnum(destinationDesc, "project", "repo", "personal"),
		},
		required: []string{"source"},
	},
	{
		name:        "create_skill",
		description: "Propose a new skill written by you." + writeNote,
		properties: map[string]any{
			"name":        str("Lowercase letters, digits and single hyphens, up to 64 characters."),
			"description": str("What the skill does and when to use it; this is what makes an agent load it. Up to 1024 characters."),
			"content":     str("The whole SKILL.md, frontmatter included."),
			"destination": strEnum(destinationDesc, "project", "repo", "personal"),
		},
		required: []string{"name", "description", "content"},
	},
	{
		name:        "remove_skill",
		description: "Propose removing a skill. Take the name from list_skills; read-only skills cannot be removed." + writeNote,
		properties: map[string]any{
			"name": str("The skill's name."),
		},
		required: []string{"name"},
	},
	{
		name:        "add_sign_in",
		description: "Propose a new command-line tool for Omniplex to hold sign-ins for. Work the commands out from the tool's --help. Each command runs through sh -c with the account's env set. After saving, the user signs in to each account from the card." + writeNote + secretNote,
		properties: map[string]any{
			"definition": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":              str("Short id, e.g. gws. Defaults from the name."),
					"name":            str("The tool's name as the user knows it, e.g. Google Workspace."),
					"statusCommand":   str("Reports whether the account is signed in: exit status 0 means signed in, unless signedInPattern is set."),
					"signedInPattern": str("A Go regexp that must match the status command's output for signed in."),
					"signInCommand":   str("Signs in: usually prints a URL to open and waits for the browser to come back."),
					"prepareCommand":  str("Runs before signInCommand, e.g. to put a client secret into a new account's config folder."),
					"accountEnv":      map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Env that keeps each account apart, e.g. {\"GOOGLE_WORKSPACE_CLI_CONFIG_DIR\": \"~/.config/gws-{account}\"}. {account} becomes the account's name."},
					"accounts":        strList("Accounts to create, e.g. [\"work\", \"personal\"]."),
				},
				"required": []string{"name", "statusCommand", "signInCommand"},
			},
		},
		required: []string{"definition"},
	},
	{
		name:        "add_account",
		description: "Propose another account for a command-line tool Omniplex already holds sign-ins for. The user signs in to it from the card." + writeNote,
		properties: map[string]any{
			"cli":  str("The tool's id, from list_sign_ins."),
			"name": str("The account's name, e.g. work."),
		},
		required: []string{"cli", "name"},
	},
}

// mcpToolNames is every tool this server serves, for the harness to
// pre-approve: a write's own card is its approval.
func mcpToolNames() []string {
	names := []string{mcpShowTool}
	for _, t := range agentTools {
		names = append(names, t.name)
	}
	return names
}

func findAgentTool(name string) (agentTool, bool) {
	for _, t := range agentTools {
		if t.name == name {
			return t, true
		}
	}
	return agentTool{}, false
}

func toolList() []any {
	tools := []any{map[string]any{
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
	}}
	for _, t := range agentTools {
		schema := map[string]any{"type": "object", "properties": t.properties}
		if t.properties == nil {
			schema["properties"] = map[string]any{}
		}
		if len(t.required) > 0 {
			schema["required"] = t.required
		}
		tools = append(tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": schema})
	}
	return tools
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

// runMCP serves one harness over stdio until in ends. A tool call runs on its
// own, because a write can wait on a person for many minutes and pings and
// other calls must not queue behind it. notifications/cancelled ends the call
// it names, which drops the request to omniplex (the card stays); the call
// then gets no reply, as MCP asks. Calls still running when in ends are
// waited for.
func runMCP(in io.Reader, out io.Writer) error {
	base := os.Getenv("OMNIPLEX_URL")
	token := os.Getenv("OMNIPLEX_AGENT_TOKEN")
	home := os.Getenv("OMNIPLEX_HOME")

	var outMu sync.Mutex
	enc := json.NewEncoder(out)
	reply := func(id json.RawMessage, result any, e *rpcErr) {
		msg := map[string]any{"jsonrpc": "2.0", "id": id}
		if e != nil {
			msg["error"] = e
		} else {
			msg["result"] = result
		}
		outMu.Lock()
		defer outMu.Unlock()
		enc.Encode(msg)
	}

	var (
		callsMu sync.Mutex
		calls   = map[string]context.CancelFunc{}
		running sync.WaitGroup
	)
	defer running.Wait()

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m rpcMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if len(m.ID) == 0 {
			if m.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				json.Unmarshal(m.Params, &p)
				callsMu.Lock()
				cancel := calls[idKey(p.RequestID)]
				callsMu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
			continue
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
			reply(m.ID, map[string]any{"tools": toolList()}, nil)
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				reply(m.ID, nil, &rpcErr{Code: -32602, Message: "invalid params"})
				continue
			}
			if _, ok := findAgentTool(p.Name); !ok && p.Name != mcpShowTool {
				reply(m.ID, nil, &rpcErr{Code: -32602, Message: "unknown tool: " + p.Name})
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			key := idKey(m.ID)
			callsMu.Lock()
			calls[key] = cancel
			callsMu.Unlock()
			running.Add(1)
			go func(id json.RawMessage) {
				defer running.Done()
				text, err := callTool(ctx, base, token, p.Name, p.Arguments)
				callsMu.Lock()
				delete(calls, key)
				callsMu.Unlock()
				if ctx.Err() != nil {
					return // cancelled: the harness wants no reply
				}
				cancel()
				if err != nil {
					reply(id, map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}, nil)
					return
				}
				reply(id, map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}, nil)
			}(m.ID)
		default:
			reply(m.ID, nil, &rpcErr{Code: -32601, Message: "method not found: " + m.Method})
		}
	}
	return sc.Err()
}

// idKey is a request id as a map key: the id a cancellation names is the
// same JSON value, but not always the same bytes.
func idKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, id) != nil {
		return string(id)
	}
	return buf.String()
}

func callTool(ctx context.Context, base, token, name string, args json.RawMessage) (string, error) {
	if name == mcpShowTool {
		var a struct {
			Path  string `json:"path"`
			Title string `json:"title"`
			Note  string `json:"note"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("arguments: %w", err)
			}
		}
		return showFile(ctx, base, token, a.Path, a.Title, a.Note)
	}
	return agentCall(ctx, base, token, name, args)
}

// agentCall forwards a tool's arguments to omniplex and returns its text.
// There is no timeout here: a write waits on the user, omniplex bounds the
// wait, and the harness cancels the call when it gives up.
func agentCall(ctx context.Context, base, token, name string, args json.RawMessage) (string, error) {
	if base == "" || token == "" {
		return "", errors.New("omniplex did not configure this tool (OMNIPLEX_URL / OMNIPLEX_AGENT_TOKEN unset)")
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/agent/tools/"+name, bytes.NewReader(args))
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
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var got struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	json.Unmarshal(body, &got)
	if res.StatusCode != http.StatusOK {
		if got.Error == "" {
			got.Error = strings.TrimSpace(string(body))
		}
		if got.Error == "" {
			got.Error = res.Status
		}
		return "", errors.New(got.Error)
	}
	return got.Text, nil
}

func showFile(ctx context.Context, base, token, p, title, note string) (string, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/agent/artefacts", bytes.NewReader(reqBody))
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
