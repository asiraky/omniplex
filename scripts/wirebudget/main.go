// Command wirebudget measures what a client downloads to open Omniplex and a
// thread, for a fixed fixture, and fails when any of it grows past its budget.
// It is scripts/check-web-bundle.mjs for the wire: run it with
// `npm run check:wire`, as CI does.
//
// It seeds a temporary store, serves it with the real handler, and reads what
// a phone reads: the welcome frame (the sidebar), a thread's snapshot, and the
// page of older items above it. Each is measured decoded, which is what the
// phone parses and keeps, and compressed, which is what crosses the network.
// No harness adapters are installed, so the welcome frame's harness list is
// empty: what it holds depends on the machine.
package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/server"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// The fixture. Sidebar threads carry metadata only, which is all the welcome
// frame sends; the fixture thread has a long history of realistic turns, so
// its snapshot is a full window and there is a full page above it.
const (
	fixtureProjects = 4
	sidebarThreads  = 120
	fixtureTurns    = 60
	fixtureThread   = "fixture-thread"
)

// sizes is one payload in bytes: decoded is the JSON as parsed, wire is what
// gzip (HTTP) or permessage-deflate (WebSocket) sends.
type sizes struct{ decoded, wire int }

// The ceilings sit about 15% over what the fixture measured when they were
// set, noted beside each. Raising one is a decision: say in the PR why the
// payload has to be bigger.
var (
	welcomeBudget  = sizes{decoded: 53_000, wire: 8_000}   // 46.2 KB, 6.9 KB
	snapshotBudget = sizes{decoded: 211_000, wire: 52_000} // 183.6 KB, 45.2 KB
	pageBudget     = sizes{decoded: 189_000, wire: 49_000} // 164.0 KB, 42.6 KB
)

type payload struct {
	name        string
	got, budget sizes
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "wirebudget:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "omniplex-wirebudget-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	st, err := store.Open(filepath.Join(dir, "omniplex.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	if err := seed(ctx, st); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	mgr := thread.NewManager(st, func(string, ...any) {})
	defer mgr.Shutdown()
	srv := server.New(server.Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)})
	ts := httptest.NewServer(srv.Handler()) // loopback is trusted: no pairing
	defer ts.Close()

	welcome, err := measureWelcome(ctx, ts.URL)
	if err != nil {
		return fmt.Errorf("welcome: %w", err)
	}
	snap, body, err := measureHTTP(ts.URL + "/api/threads/" + fixtureThread)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	var state projection.State
	if err := json.Unmarshal(body, &state); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if state.ItemsBefore == 0 {
		return fmt.Errorf("snapshot holds the whole fixture thread; it is too short to have a page above it")
	}
	page, _, err := measureHTTP(fmt.Sprintf("%s/api/threads/%s/items?before=%d", ts.URL, fixtureThread, state.ItemsBefore))
	if err != nil {
		return fmt.Errorf("page: %w", err)
	}

	return check([]payload{
		{"welcome", welcome, welcomeBudget},
		{"snapshot", snap, snapshotBudget},
		{"page", page, pageBudget},
	})
}

func check(ps []payload) error {
	var over []string
	for _, p := range ps {
		fmt.Printf("%-9s decoded %7d B (budget %7d)   wire %6d B (budget %6d)\n", p.name, p.got.decoded, p.budget.decoded, p.got.wire, p.budget.wire)
		if p.got.decoded > p.budget.decoded || p.got.wire > p.budget.wire {
			over = append(over, p.name)
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("over budget: %s", strings.Join(over, ", "))
	}
	return nil
}

// measureWelcome reads the frame a client gets for its hello. The socket is
// opened uncompressed so the frame arrives as sent; its wire size is the frame
// deflated on its own at BestSpeed, which is what coder/websocket's
// permessage-deflate sends for the first message on a socket.
func measureWelcome(ctx context.Context, base string) (sizes, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws",
		&websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return sizes{}, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 20)
	hello, _ := json.Marshal(map[string]any{"type": "hello", "protocolVersion": server.ProtocolVersion, "clientId": "wirebudget"})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return sizes{}, err
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return sizes{}, err
		}
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &f) != nil || f.Type != "welcome" {
			continue
		}
		var buf bytes.Buffer
		w, _ := flate.NewWriter(&buf, flate.BestSpeed)
		w.Write(data)
		w.Close()
		return sizes{decoded: len(data), wire: buf.Len()}, nil
	}
}

// measureHTTP fetches url as the browser does, gzip accepted, and returns the
// sizes and the decoded body.
func measureHTTP(url string) (sizes, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return sizes{}, nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	// DisableCompression keeps the transport from unzipping it behind our back.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		return sizes{}, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return sizes{}, nil, err
	}
	if res.StatusCode != http.StatusOK {
		return sizes{}, nil, fmt.Errorf("status %d: %s", res.StatusCode, raw)
	}
	if res.Header.Get("Content-Encoding") != "gzip" {
		return sizes{}, nil, fmt.Errorf("response was not gzipped")
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return sizes{}, nil, err
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		return sizes{}, nil, err
	}
	return sizes{decoded: len(body), wire: len(raw)}, body, nil
}

// seed writes the fixture: projects with folders, a sidebar of threads across
// them, and one thread with a long history. Paths are fixed rather than under
// the temp dir, whose name varies in length from run to run; nothing reads
// them from disk.
func seed(ctx context.Context, st *store.Store) error {
	const home = "/home/dev/code"
	// Fixed timestamps for the rows we write. The store stamps events with the
	// clock, at a fixed width: decoded sizes never move between runs, and the
	// compressed ones move by a few bytes.
	const epoch = int64(1_760_000_000_000)
	for p := range fixtureProjects {
		id := fmt.Sprintf("project-%d", p)
		folder := filepath.Join(home, id)
		if err := st.CreateProject(ctx, project.Project{
			ID: id, Name: fmt.Sprintf("Project %d", p),
			Folders:   []project.Folder{project.NewFolder(id+"-main", folder)},
			CreatedAt: epoch, UpdatedAt: epoch,
		}); err != nil {
			return err
		}
	}

	r := rand.New(rand.NewPCG(379, 379))
	for i := range sidebarThreads {
		p := i % fixtureProjects
		if err := st.CreateThread(ctx, store.ThreadMeta{
			ID:        fmt.Sprintf("thread-%03d", i),
			Cwd:       filepath.Join(home, fmt.Sprintf("project-%d", p)),
			Harness:   []string{"claudecode", "codex", "pi"}[i%3],
			Title:     sentence(r, 4+r.IntN(6)),
			ProjectID: fmt.Sprintf("project-%d", p),
			FolderID:  fmt.Sprintf("project-%d-main", p),
			Branch:    fmt.Sprintf("issue/%d-%s", 100+i, strings.ReplaceAll(sentence(r, 3), " ", "-")),
			Model:     "claude-opus-5-5",
			Phase:     "idle",
			CreatedAt: epoch + int64(i)*60_000, UpdatedAt: epoch + int64(i)*60_000,
		}); err != nil {
			return err
		}
	}

	if err := st.CreateThread(ctx, store.ThreadMeta{
		ID: fixtureThread, Cwd: filepath.Join(home, "project-0"), Harness: "claudecode",
		Title: "The fixture thread", ProjectID: "project-0", FolderID: "project-0-main",
		Model: "claude-opus-5-5", Phase: "idle", CreatedAt: epoch, UpdatedAt: epoch,
	}); err != nil {
		return err
	}
	for turn := range fixtureTurns {
		for _, em := range turnEvents(r, turn) {
			if _, err := st.Append(ctx, fixtureThread, em); err != nil {
				return err
			}
		}
	}
	return nil
}

// turnEvents is one ordinary coding turn: a prompt, some thinking, a read, a
// search, an edit and a command, then the answer and what the turn changed.
// Every fifth turn reads a file far past the projection's caps on tool text,
// and every fourth spawns a subagent that makes calls of its own, so a cap
// loosened or a trim dropped shows up in the sizes.
func turnEvents(r *rand.Rand, turn int) []proto.Emission {
	tid := fmt.Sprintf("turn-%03d", turn)
	call := func(n int) string { return fmt.Sprintf("toolu_%03d_%d_%s", turn, n, word(r)) }
	path := fmt.Sprintf("internal/%s/%s.go", word(r), word(r))
	input := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	var ems []proto.Emission
	add := func(typ string, p any) { ems = append(ems, proto.Emit(typ, p)) }
	tool := func(id, kind, title string, in any, out []proto.ToolContent) {
		add(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: tid, ToolCallID: id, Kind: kind, Title: title, Status: proto.StatusInProgress, RawInput: input(in)})
		add(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: id, Status: proto.StatusCompleted, Content: out})
	}

	add(proto.TurnStarted, proto.TurnStartedPayload{TurnID: tid, Prompt: paragraph(r, 2)})
	add(proto.MessageChunk, proto.MessageChunkPayload{TurnID: tid, Role: "agent", Kind: "thought", BlockID: tid + "-think", Delta: paragraph(r, 4)})

	read := call(1)
	readLines := 80
	if turn%5 == 0 {
		readLines = 2000
	}
	tool(read, proto.KindRead, "Read "+path, map[string]any{"file_path": path},
		[]proto.ToolContent{{Type: "text", Text: code(r, readLines)}})
	search := call(2)
	tool(search, proto.KindSearch, "Grep "+word(r), map[string]any{"pattern": word(r), "path": "internal"},
		[]proto.ToolContent{{Type: "text", Text: lines(r, 15, func() string {
			return fmt.Sprintf("internal/%s/%s.go:%d: %s", word(r), word(r), r.IntN(900), sentence(r, 8))
		})}})
	edit := call(3)
	before, after := code(r, 6), code(r, 9)
	tool(edit, proto.KindEdit, "Edit "+path, map[string]any{"file_path": path, "old_string": before, "new_string": after},
		[]proto.ToolContent{{Type: "diff", Path: path, Old: before, New: after}})
	run := call(4)
	tool(run, proto.KindExecute, "go test ./internal/...", map[string]any{"command": "go test ./internal/...", "description": sentence(r, 6)},
		[]proto.ToolContent{{Type: "text", Text: lines(r, 40, func() string {
			return fmt.Sprintf("ok  \tgithub.com/asiraky/omniplex/internal/%s\t%d.%03ds", word(r), r.IntN(9), r.IntN(1000))
		})}})

	if turn%4 == 0 {
		task, job := call(5), fmt.Sprintf("job-%03d", turn)
		add(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: tid, ToolCallID: task, Kind: proto.KindAgent, Title: "Agent: " + sentence(r, 4), Status: proto.StatusInProgress,
			RawInput: input(map[string]any{"description": sentence(r, 4), "prompt": paragraph(r, 6), "subagent_type": "Explore"})})
		add(proto.JobStarted, proto.JobPayload{JobID: job, ToolCallID: task, Kind: proto.JobAgent, TaskType: "local_agent", Name: sentence(r, 4), Role: "Explore", Status: proto.JobRunning})
		for n := range 6 {
			child := fmt.Sprintf("%s_child_%d", task, n)
			add(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: tid, ToolCallID: child, Kind: proto.KindRead, Title: "Read " + path, Status: proto.StatusInProgress,
				RawInput: input(map[string]any{"file_path": path}), ParentToolCallID: task})
			add(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: child, Status: proto.StatusCompleted, ParentToolCallID: task,
				Content: []proto.ToolContent{{Type: "text", Text: code(r, 60)}}})
		}
		add(proto.JobFinished, proto.JobPayload{JobID: job, ToolCallID: task, Status: proto.JobCompleted})
		add(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: task, Status: proto.StatusCompleted,
			Content: []proto.ToolContent{{Type: "text", Text: paragraph(r, 10)}}})
	}

	for _, chunk := range strings.SplitAfter(paragraph(r, 8), ". ") {
		add(proto.MessageChunk, proto.MessageChunkPayload{TurnID: tid, Role: "agent", Kind: "text", BlockID: tid + "-answer", Delta: chunk})
	}
	add(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: tid, StopReason: proto.StopEndTurn})
	add(proto.TurnDiff, proto.TurnDiffPayload{TurnID: tid, Files: []proto.ChangedFile{{Path: path, Status: "modified", Additions: 9, Deletions: 6}}, Additions: 9, Deletions: 6})
	return ems
}

// Deterministic filler. Real prose and code compress about as well as text
// drawn from a few hundred words does; one repeated string would compress to
// nothing and make the wire budget meaningless.
var vocabulary = strings.Fields(`the a an of to in for on with by from that this it is be as at or and not
thread turn event snapshot page client server socket frame payload budget phone wire sidebar project
folder worktree branch harness adapter session store actor projection item tool call job agent message
prompt answer diff edit read search command output test build check lint type error value field state
window cursor before after head seq replay attach welcome reconnect resume fetch render mobile desktop
keep send drop trim cap measure count limit grow shrink fail pass fix add remove change move rename
because when while until since then so but if only every each some none all one two three first last
new old small large long short fast slow cheap real whole part full empty open closed running idle
return func struct map slice string int bool nil err ctx context json marshal decode encode write close`)

func word(r *rand.Rand) string { return vocabulary[r.IntN(len(vocabulary))] }

func sentence(r *rand.Rand, n int) string {
	ws := make([]string, n)
	for i := range ws {
		ws[i] = word(r)
	}
	return strings.Join(ws, " ")
}

func paragraph(r *rand.Rand, sentences int) string {
	ss := make([]string, sentences)
	for i := range ss {
		s := sentence(r, 8+r.IntN(14))
		ss[i] = strings.ToUpper(s[:1]) + s[1:] + "."
	}
	return strings.Join(ss, " ")
}

func lines(r *rand.Rand, n int, line func() string) string {
	ls := make([]string, n)
	for i := range ls {
		ls[i] = line()
	}
	return strings.Join(ls, "\n")
}

func code(r *rand.Rand, n int) string {
	return lines(r, n, func() string {
		indent := strings.Repeat("\t", r.IntN(3))
		switch r.IntN(4) {
		case 0:
			return fmt.Sprintf("%sif %s != nil {", indent, word(r))
		case 1:
			return fmt.Sprintf("%s%s := %s.%s(%s, %s)", indent, word(r), word(r), word(r), word(r), word(r))
		case 2:
			return fmt.Sprintf("%s// %s", indent, sentence(r, 6+r.IntN(6)))
		default:
			return fmt.Sprintf("%sreturn %s, %s", indent, word(r), word(r))
		}
	})
}
