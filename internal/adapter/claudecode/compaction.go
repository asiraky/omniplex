package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// An agent's compaction log shows what each epoch of its conversation started
// from: the summary Claude Code carried across a compaction and what it
// attached beside it (re-read files, invoked skills, CLAUDE.md). The stream
// says only that a compaction happened; the rest is in the transcript, which
// after a compact_boundary entry holds the summary as a user entry marked
// isCompactSummary and the attachments as attachment entries, up to the first
// assistant message of the new epoch. The first epoch starts the same way,
// minus the summary: the attachments in front of the conversation's first
// answer. Like restart.go this reads an undocumented format (seen on Claude
// Code 2.1.328), but here a surprise only leaves the record emptier.

// compactSettings is the inline settings object that hands file to Claude
// Code's compactor: a PreCompact command hook whose output, on success, is
// added to the compaction prompt. The hook reads the file each time it runs,
// so an edit applies at the next compaction with no restart. Claude Code adds
// these hooks to the ones in the user's own settings rather than replacing
// them, and caps the output at 10,000 characters.
func compactSettings(file string) json.RawMessage {
	if file == "" {
		return nil
	}
	settings := map[string]any{"hooks": map[string]any{
		"PreCompact": []any{map[string]any{"hooks": []any{map[string]any{
			"type":    "command",
			"command": "cat " + shellQuote(file),
		}}}},
	}}
	raw, _ := json.Marshal(settings)
	return raw
}

// minCompactWindow is the smallest auto-compact window Claude Code accepts;
// it reads anything under it as this.
const minCompactWindow = 100_000

// agentEnv is the environment an agent's session adds: where it compacts, no
// auto memory (its system prompt is its only memory), and the raw request log.
// The raw log needs nothing else switched on: Claude Code writes the bodies
// to files whether or not telemetry is exported, and only declines in its
// diskless mode.
func agentEnv(o adapter.CreateOptions) ([]string, error) {
	var env []string
	if o.CompactAt > 0 {
		env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.FormatInt(max(o.CompactAt, minCompactWindow), 10))
	}
	if o.NoMemory {
		env = append(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	}
	if o.RawRequestLogDir != "" {
		if err := os.MkdirAll(o.RawRequestLogDir, 0o700); err != nil {
			return nil, fmt.Errorf("make the raw request log folder: %w", err)
		}
		env = append(env, "OTEL_LOG_RAW_API_BODIES=file:"+o.RawRequestLogDir)
	}
	return env, nil
}

// reportEpochs reads what is due: the first epoch's start at the first result
// of a new conversation, and a compaction's new epoch at the epoch's first
// answer. The host files a compaction's detail on the latest compaction row,
// so it has to go out before the next compaction does; one long turn can
// compact more than once. By that answer the summary and attachments are in
// the transcript; the read at the next result is in case the answer beat
// them to the file. The reads run beside the stream, since a long
// conversation's transcript is many megabytes, one after another, so a later
// read never goes out before an earlier one.
func (s *session) reportEpochs(atResult bool) {
	s.mu.Lock()
	start := s.startDetail && atResult
	latest := s.compacted || (atResult && s.epochOpen)
	if start {
		s.startDetail = false
	}
	if latest {
		s.compacted, s.epochOpen = false, !atResult
	}
	if !start && !latest {
		s.mu.Unlock()
		return
	}
	id := parseConversation(s.harnessSessionID).id
	prev, done := s.epochRead, make(chan struct{})
	s.epochRead = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		if prev != nil {
			<-prev
		}
		file, err := findConversation(s.configDir, s.cwd, id)
		if err != nil || file == "" {
			s.logf("claude: no transcript to read the compaction detail from: %v", err)
			return
		}
		for _, first := range []bool{true, false} {
			if (first && !start) || (!first && !latest) {
				continue
			}
			d, ok, err := readEpochDetail(file, first)
			if err != nil {
				s.logf("claude: read the compaction detail: %v", err)
				return
			}
			if ok && (d.Summary != "" || len(d.Attachments) > 0) {
				s.emit(proto.Emit(proto.CompactionDetail, d))
			}
		}
	}()
}

func (s *session) logf(format string, args ...any) {
	if s.host != nil {
		s.host.Logf(format, args...)
	}
}

// shellQuote makes s one word for sh, whatever it holds.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// readEpochDetail reads the start of an epoch from transcript file. With start
// it is the conversation's first epoch, which ends at the first compaction if
// there is one; otherwise it is the epoch after the last compaction. ok is
// false when the transcript has no such epoch (no compaction yet).
func readEpochDetail(file string, start bool) (d proto.CompactionDetailPayload, ok bool, err error) {
	f, err := os.Open(file)
	if err != nil {
		return d, false, err
	}
	defer f.Close()
	d.Start = start
	ok = start
	// done is set once the epoch's first answer is reached: what follows
	// it belongs to the epoch's turns, not to what it started from.
	done := false
	// Lines run to megabytes (a tool result, a pasted image), so they are
	// read whole rather than through a Scanner's fixed limit.
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return d, false, rerr
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var e transcriptEntry
			if json.Unmarshal(line, &e) == nil && !e.IsSidechain {
				switch {
				case e.Type == "system" && e.Subtype == "compact_boundary":
					if start {
						return d, true, nil
					}
					d, ok, done = proto.CompactionDetailPayload{}, true, false
				case done:
				case e.Type == "assistant":
					done = true
				case e.Type == "user" && e.IsCompactSummary:
					d.Summary = messageText(e.Message)
				case e.Type == "attachment":
					d.Attachments = append(d.Attachments, epochAttachments(e.Attachment, e.Rendered)...)
				}
			}
		}
		if rerr != nil {
			return d, ok, nil
		}
	}
}

type transcriptEntry struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Message          json.RawMessage `json:"message"`
	Attachment       json.RawMessage `json:"attachment"`
	// Rendered is the attachment as Claude Code put it in front of the
	// model: a list of {content}, each a string or a list of blocks.
	Rendered json.RawMessage `json:"rendered"`
}

// messageText is the text of a transcript message, whose content is either a
// string or a list of blocks.
func messageText(raw json.RawMessage) string {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return contentText(m.Content)
}

func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// renderedText joins the parts of an attachment's rendered form.
func renderedText(raw json.RawMessage) string {
	var parts []struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if t := contentText(p.Content); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

// skippedAttachments carry nothing worth keeping beside a summary: a date,
// the model's name, account and session plumbing, and the pinned system
// prompt, which the log shows from its own revisions.
var skippedAttachments = map[string]bool{
	"date":                  true,
	"model":                 true,
	"credential_org":        true,
	"remote_session_change": true,
	"prompt_snapshot":       true,
}

// epochAttachments maps one attachment entry to what the compaction record
// lists. Most are one attachment, named for what they are; invoked skills and
// instructions are one per skill and per file, since those are what a reader
// is looking for. One with no text is dropped.
func epochAttachments(raw, rendered json.RawMessage) []proto.CompactionAttachment {
	var a struct {
		Type        string          `json:"type"`
		Filename    string          `json:"filename"`
		Path        string          `json:"path"`
		DisplayPath string          `json:"displayPath"`
		Description string          `json:"description"`
		Text        string          `json:"text"`
		Content     json.RawMessage `json:"content"`
		Skills      []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"skills"`
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Type == "" || skippedAttachments[a.Type] {
		return nil
	}
	var out []proto.CompactionAttachment
	add := func(kind, name, body string) {
		if body == "" {
			return
		}
		out = append(out, proto.CompactionAttachment{Kind: kind, Name: name, Chars: len(body), Body: body})
	}
	switch a.Type {
	case "invoked_skills":
		for _, s := range a.Skills {
			add("skill", s.Name, s.Content)
		}
		return out
	case "instructions":
		for _, f := range a.Files {
			add("instructions", f.Path, f.Content)
		}
		return out
	}
	body := renderedText(rendered)
	if body == "" {
		body = attachmentText(a.Type, a.Content, a.Text)
	}
	switch a.Type {
	case "file":
		add("file", a.Filename, body)
	case "skill_listing":
		add("skills", "skill listing", body)
	case "task_status":
		add("task", a.Description, body)
	default:
		name := a.Filename
		for _, n := range []string{a.DisplayPath, a.Path} {
			if name == "" {
				name = n
			}
		}
		if name == "" {
			name = strings.ReplaceAll(a.Type, "_", " ")
		}
		add(a.Type, name, body)
	}
	return out
}

// attachmentText is an attachment's own text, for an entry written without
// its rendered form.
func attachmentText(kind string, content json.RawMessage, text string) string {
	if kind == "file" {
		var c struct {
			File struct {
				Content string `json:"content"`
			} `json:"file"`
		}
		if json.Unmarshal(content, &c) == nil && c.File.Content != "" {
			return c.File.Content
		}
	}
	var s string
	if json.Unmarshal(content, &s) == nil && s != "" {
		return s
	}
	return text
}
