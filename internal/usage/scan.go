package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Source is one directory a harness writes its session transcripts under,
// for one account: Claude's <config>/projects, Codex's <home>/sessions, Pi's
// <agent>/sessions. Usage is read from these rather than from omniplex's own
// event log because the transcripts are the complete record: they hold every
// subagent's requests, they outlive deleted threads, and they cover work
// started outside omniplex on the same account.
type Source struct {
	Provider string // the harness driver: claude | codex | pi
	Root     string
}

// Scanner reads usage records out of harness transcripts and keeps what it
// has parsed, so a report re-reads only what was appended since the last one.
// Transcripts are append-only JSONL; a file that shrank is parsed afresh.
type Scanner struct {
	mu    sync.Mutex
	files map[string]*scannedFile
}

type scannedFile struct {
	size    int64
	modTime time.Time
	// offset is how far the file has been consumed: through the last
	// complete line, so a line still being written is read again whole.
	offset  int64
	parser  lineParser
	records []Record
}

// lineParser turns one transcript line into a usage record. It is stateful
// per file: Codex names the model and the fork origin in earlier lines than
// the usage itself.
type lineParser interface {
	parse(line []byte) (Record, bool)
}

func NewScanner() *Scanner {
	return &Scanner{files: map[string]*scannedFile{}}
}

// Records returns every usage record the sources hold from `from` on. Files
// last written before `from` cannot hold a record inside the window and are
// not opened.
func (s *Scanner) Records(ctx context.Context, sources []Source, from time.Time) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type job struct {
		path     string
		provider string
		info     fs.FileInfo
	}
	var jobs []job
	present := map[string]bool{}
	seen := map[Source]bool{}
	for _, src := range sources {
		if newParser(src.Provider) == nil {
			continue
		}
		// A transcript directory moved to another disk and symlinked back
		// is still the harness's directory; WalkDir would not enter it.
		root, err := filepath.EvalSymlinks(src.Root)
		if err != nil {
			continue // not created yet: this account has no transcripts
		}
		// Resolved, so two accounts reaching one directory by different
		// paths read it once.
		key := Source{src.Provider, root}
		if seen[key] {
			continue
		}
		seen[key] = true
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable subtree is skipped, not fatal: one bad
				// directory must not blank the whole page.
				if d != nil && d.IsDir() && path != root {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			present[path] = true
			if info.ModTime().Before(from) {
				return nil
			}
			jobs = append(jobs, job{path: path, provider: src.Provider, info: info})
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	for path := range s.files {
		if !present[path] {
			delete(s.files, path)
		}
	}

	// Parse in parallel; each file belongs to exactly one worker.
	work := make(chan job)
	var wg sync.WaitGroup
	var cacheMu sync.Mutex
	for range max(1, runtime.GOMAXPROCS(0)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				cacheMu.Lock()
				f := s.files[j.path]
				cacheMu.Unlock()
				f = refresh(f, j.path, j.provider, j.info)
				cacheMu.Lock()
				if f == nil {
					delete(s.files, j.path)
				} else {
					s.files[j.path] = f
				}
				cacheMu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		work <- j
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fromMs := from.UnixMilli()
	var out []Record
	for _, j := range jobs {
		f := s.files[j.path]
		if f == nil {
			continue
		}
		for _, r := range f.records {
			if r.Timestamp >= fromMs {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// refresh brings one file's parsed state up to date with the file on disk.
// An unchanged file is returned as is; a grown one is read from where the
// last read stopped; anything else is parsed from the start.
func refresh(f *scannedFile, path, provider string, info fs.FileInfo) *scannedFile {
	if f != nil && f.size == info.Size() && f.modTime.Equal(info.ModTime()) {
		return f
	}
	if f == nil || info.Size() < f.offset {
		f = &scannedFile{parser: newParser(provider)}
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return nil
	}
	r := bufio.NewReaderSize(fh, 1<<20)
	for {
		line, err := readLine(r)
		complete := err == nil
		if len(line) > 0 && (complete || json.Valid(line)) {
			// A final line with no newline is consumed only once it is
			// whole JSON; otherwise it is still being written.
			f.offset += int64(len(line))
			if complete {
				f.offset++
			}
			if rec, ok := f.parser.parse(line); ok {
				f.records = append(f.records, rec)
			}
		} else if complete {
			f.offset++ // a blank line
		}
		if err != nil {
			break
		}
	}
	f.size, f.modTime = info.Size(), info.ModTime()
	return f
}

// readLine reads one newline-terminated line without its newline, however
// long it is: a transcript line holding a large tool result runs to
// megabytes. It returns io.EOF with whatever trailed the last newline.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == nil {
		return line[:len(line)-1], nil
	}
	if !errors.Is(err, bufio.ErrBufferFull) {
		return bytes.Clone(line), err
	}
	buf := bytes.Clone(line)
	for {
		line, err = r.ReadSlice('\n')
		if err == nil {
			return append(buf, line[:len(line)-1]...), nil
		}
		buf = append(buf, line...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return buf, err
		}
	}
}

func newParser(provider string) lineParser {
	switch provider {
	case "claude":
		return &claudeParser{}
	case "codex":
		return &codexParser{}
	case "pi":
		return &piParser{}
	}
	return nil
}

// parseTime reads an RFC 3339 timestamp as epoch milliseconds.
func parseTime(s string) (int64, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// ---- Claude: <config>/projects/**/*.jsonl ----

// claudeParser reads the assistant lines of a Claude transcript. Every API
// response's usage is on its assistant lines — the main conversation's and,
// under <session>/subagents/, each subagent's. Input there is already the
// uncached share.
type claudeParser struct{}

var (
	claudeAssistant = []byte(`"assistant"`)
	claudeUsage     = []byte(`"usage"`)
)

func (*claudeParser) parse(line []byte) (Record, bool) {
	if !bytes.Contains(line, claudeAssistant) || !bytes.Contains(line, claudeUsage) {
		return Record{}, false
	}
	var l struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		RequestID string `json:"requestId"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				Input         int64 `json:"input_tokens"`
				Output        int64 `json:"output_tokens"`
				CacheRead     int64 `json:"cache_read_input_tokens"`
				CacheCreation int64 `json:"cache_creation_input_tokens"`
				Detail        struct {
					Hour int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "assistant" || l.Message.Usage == nil {
		return Record{}, false
	}
	// <synthetic> lines are the CLI's own stand-ins, never an API call.
	if l.Message.Model == "<synthetic>" {
		return Record{}, false
	}
	ts, ok := parseTime(l.Timestamp)
	if !ok {
		return Record{}, false
	}
	u := l.Message.Usage
	rec := Record{
		Timestamp: ts,
		Provider:  "claude",
		Model:     l.Message.Model,
		Counts: Counts{
			Input:        u.Input,
			Output:       u.Output,
			CacheRead:    u.CacheRead,
			CacheWrite:   u.CacheCreation,
			CacheWrite1h: u.Detail.Hour,
		},
	}
	if l.Message.ID != "" || l.RequestID != "" {
		rec.Key = "claude:" + l.Message.ID + ":" + l.RequestID
	}
	return rec, rec.Counts != (Counts{})
}

// ---- Codex: <home>/sessions/**/*.jsonl and <home>/archived_sessions ----

// codexParser reads a Codex rollout. Each response logs a token_count event
// whose last_token_usage is that response's own usage; the model comes from
// the turn_context before it.
type codexParser struct {
	model string
	// prevTotal is the running total at the previous token_count. Codex
	// re-emits an unchanged count on some events; an unchanged total means
	// no new response.
	prevTotal codexTokens
	// A forked or spawned rollout opens by replaying its parent's history,
	// restamped to the fork moment, and those responses are already counted
	// in the parent's file. While replaying, token_counts arriving within a
	// second of the previous line are copies.
	replaying  bool
	replayLast int64
}

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

var (
	codexTokenCount  = []byte(`"token_count"`)
	codexTurnContext = []byte(`"turn_context"`)
	codexSessionMeta = []byte(`"session_meta"`)
)

func (p *codexParser) parse(line []byte) (Record, bool) {
	isCount := bytes.Contains(line, codexTokenCount)
	if !isCount && !bytes.Contains(line, codexTurnContext) && !bytes.Contains(line, codexSessionMeta) {
		return Record{}, false
	}
	var l struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil {
		return Record{}, false
	}
	ts, ok := parseTime(l.Timestamp)
	if !ok {
		return Record{}, false
	}
	switch l.Type {
	case "session_meta":
		var m struct {
			ForkedFrom string          `json:"forked_from_id"`
			Source     json.RawMessage `json:"source"`
		}
		_ = json.Unmarshal(l.Payload, &m)
		if m.ForkedFrom != "" || bytes.Contains(m.Source, []byte(`"parent_thread_id"`)) {
			p.replaying, p.replayLast = true, ts
		}
		return Record{}, false
	case "turn_context":
		var c struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(l.Payload, &c) == nil && c.Model != "" {
			p.model = c.Model
		}
		return Record{}, false
	case "event_msg":
	default:
		return Record{}, false
	}
	var e struct {
		Type string `json:"type"`
		Info *struct {
			Total codexTokens `json:"total_token_usage"`
			Last  codexTokens `json:"last_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(l.Payload, &e) != nil || e.Type != "token_count" || e.Info == nil {
		return Record{}, false
	}
	if p.replaying {
		if ts-p.replayLast < 1000 {
			p.replayLast = ts
			p.prevTotal = e.Info.Total
			return Record{}, false
		}
		p.replaying = false
	}
	if e.Info.Total == p.prevTotal {
		return Record{}, false
	}
	p.prevTotal = e.Info.Total
	last := e.Info.Last
	// Codex's input includes the cached and cache-written share.
	c := Counts{
		Input:      max(0, last.Input-last.Cached-last.CacheWrite),
		Output:     last.Output,
		CacheRead:  last.Cached,
		CacheWrite: last.CacheWrite,
	}
	if c == (Counts{}) {
		return Record{}, false
	}
	return Record{Timestamp: ts, Provider: "codex", Model: p.model, Counts: c}, true
}

// ---- Pi: <agent>/sessions/**/*.jsonl ----

// piParser reads Pi session entries. Pi records billed usage on assistant
// messages, on tool results whose tool called a model, and on the summaries
// it writes when compacting a session or leaving a branch. Pi prices each call itself, per provider, so its own
// cost is used rather than the catalogue whenever it has one.
type piParser struct {
	// model is the session's current model. Summary entries do not name the
	// model that wrote them; it is the one the session was running.
	model string
}

var (
	piUsage       = []byte(`"usage"`)
	piModelChange = []byte(`"model_change"`)
)

type piUsageFields struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

func (p *piParser) parse(line []byte) (Record, bool) {
	if !bytes.Contains(line, piUsage) && !bytes.Contains(line, piModelChange) {
		return Record{}, false
	}
	var l struct {
		Type      string         `json:"type"`
		ID        string         `json:"id"`
		Timestamp string         `json:"timestamp"`
		ModelID   string         `json:"modelId"`
		Usage     *piUsageFields `json:"usage"`
		Message   struct {
			Role       string         `json:"role"`
			Model      string         `json:"model"`
			ResponseID string         `json:"responseId"`
			Usage      *piUsageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil {
		return Record{}, false
	}
	// A forked Pi session copies its parent's entries into a new file, ids
	// and timestamps intact. The response id is the strongest key; not every
	// provider sets one, and the entry id alone is too short to be unique
	// across every session on the machine.
	var key string
	if l.ID != "" {
		key = "pi:" + l.ID + ":" + l.Timestamp
	}
	var u *piUsageFields
	switch l.Type {
	case "model_change":
		if l.ModelID != "" {
			p.model = l.ModelID
		}
		return Record{}, false
	case "message":
		switch l.Message.Role {
		case "assistant":
			if l.Message.Model != "" {
				p.model = l.Message.Model
			}
			if l.Message.ResponseID != "" {
				key = "pi:" + l.Message.ResponseID
			}
		case "toolResult":
		default:
			return Record{}, false
		}
		u = l.Message.Usage
	case "compaction", "branch_summary":
		u = l.Usage
	}
	if u == nil {
		return Record{}, false
	}
	ts, ok := parseTime(l.Timestamp)
	if !ok {
		return Record{}, false
	}
	rec := Record{
		Timestamp: ts,
		Provider:  "pi",
		Model:     p.model,
		Counts:    Counts{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite},
		// A zero cost on a call that used tokens is Pi not knowing the price,
		// not the call being free: the catalogue gets a turn.
		Cost:    u.Cost.Total,
		HasCost: u.Cost.Total > 0,
		Key:     key,
	}
	return rec, rec.Counts != (Counts{})
}
