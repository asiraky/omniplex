package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// summaryTimeout bounds one summarisation. It is generous because the cost is
// a cold harness start against a small model, not a conversation: Codex and
// Claude both spend most of it launching. Nothing renders behind this — the UI
// shows a spinner — so a slow answer is better than a truncated one.
const summaryTimeout = 3 * time.Minute

// transcriptBudget caps the rendered transcript handed to the model, in bytes.
// A long session can be megabytes of tool output, and a fast model has a small
// context window; renderTranscript keeps the two ends and drops the middle,
// which is exactly the part a "what was this about, what happened" summary
// needs least.
const transcriptBudget = 60_000

// toolContentBudget caps one tool result. Whole file reads and long command
// output otherwise crowd out the conversation itself.
const toolContentBudget = 800

// SessionSummary is one generated summary, with enough provenance for a UI to
// say where it came from and to know when it has gone stale.
type SessionSummary struct {
	// Text is the model's answer, as Markdown.
	Text string `json:"text"`
	// Harness and Model name what produced it, so the summary can be
	// attributed rather than appearing from nowhere.
	Harness string `json:"harness"`
	Model   string `json:"model"`
	// Seq is the session head the summary was made from. A client caches
	// against it and knows the summary is stale once the session moves on.
	Seq int64 `json:"seq"`
	// GeneratedAt is millis, matching every other timestamp on the wire.
	GeneratedAt int64 `json:"generatedAt"`
}

// SummarizeSession asks the session's own harness to compress its transcript,
// under the operator's editable prompt.
//
// It runs against the harness that did the work, under that session's provider
// instance, so the summary is billed to the account that owns the conversation
// and needs no credential omniplex does not already hold. The adapter picks the
// model: "fastest thing this harness offers" is knowledge that belongs beside
// the harness, not here.
func (m *Manager) SummarizeSession(ctx context.Context, sessionID string) (SessionSummary, error) {
	var out SessionSummary

	meta, err := m.store.Session(ctx, sessionID)
	if err != nil {
		return out, err
	}

	reg, err := m.instanceFor(meta)
	if err != nil {
		return out, err
	}
	sum, ok := reg.ad.(adapter.Summarizer)
	if !ok {
		return out, fmt.Errorf("%s cannot summarise a session", reg.ad.Meta().Name)
	}

	// A live actor answers from inside its loop, so the projection is never
	// observed half-applied. An idle or closed session is folded straight out
	// of the log instead of through Get: summarising is a read, and it must
	// not be the thing that starts a harness process.
	var state *projection.State
	if actor, live := m.Peek(sessionID); live {
		state, err = actor.State(ctx)
	} else {
		state, err = loadState(ctx, m.store, sessionID)
	}
	if err != nil {
		return out, err
	}

	// Guard on the conversation, not on the rendered string: renderTranscript
	// always emits a header block, so a session that has never been prompted
	// would otherwise be sent to the model as four lines of metadata.
	if len(state.Turns) == 0 && len(state.Items) == 0 {
		return out, errors.New("this session has no transcript to summarise yet")
	}
	transcript := renderTranscript(state)

	cfg, err := userconfig.Load()
	if err != nil {
		return out, err
	}
	// Normalize has already substituted the default for an empty prompt, but
	// Load returns the default config unnormalised on a missing file; be
	// explicit rather than relying on that.
	system := strings.TrimSpace(cfg.SummaryPrompt)
	if system == "" {
		system = userconfig.DefaultSummaryPrompt
	}

	env, err := m.envFor(reg.inst)
	if err != nil {
		return out, err
	}

	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()

	res, err := sum.Summarize(ctx, env, adapter.SummaryRequest{System: system, Transcript: transcript})
	if err != nil {
		return out, err
	}
	text := strings.TrimSpace(res.Text)
	if text == "" {
		return out, errors.New("the summariser returned nothing")
	}

	return SessionSummary{
		Text:        text,
		Harness:     reg.ad.Meta().Name,
		Model:       res.Model,
		Seq:         state.Seq,
		GeneratedAt: proto.NowMillis(),
	}, nil
}

// renderTranscript flattens a projection into the plain text a model reads.
//
// It is deliberately lossy. The question being answered is "what was asked and
// what happened", so prompts and assistant prose are kept in full, tool calls
// are reduced to a titled line plus a clipped result, and the agent's private
// reasoning is dropped entirely — it is the bulkiest part of a transcript and
// the least informative about outcomes. Subagent items are folded away for the
// same reason the UI folds them: the parent tool call already says what was
// delegated.
func renderTranscript(state *projection.State) string {
	if state == nil {
		return ""
	}
	var b strings.Builder

	if state.Title != "" {
		fmt.Fprintf(&b, "Session title: %s\n", state.Title)
	}
	if state.Cwd != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", state.Cwd)
	}
	fmt.Fprintf(&b, "Harness: %s\n", state.Harness)
	if state.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", state.Model)
	}
	fmt.Fprintf(&b, "Status: %s\n\n", transcriptStatus(state))

	// Prompts come from the turn records rather than from user message items:
	// a turn always carries the text that started it, whereas a replayed user
	// message may not have been logged as an item at all.
	prompts := make(map[string]string, len(state.Turns))
	for _, t := range state.Turns {
		prompts[t.ID] = t.Prompt
	}

	seenTurn := make(map[string]bool, len(state.Turns))
	for _, it := range state.Items {
		// Subagent work is folded away; the spawning tool call stands for it.
		if it.ParentID != "" {
			continue
		}
		if it.TurnID != "" && !seenTurn[it.TurnID] {
			seenTurn[it.TurnID] = true
			if p := strings.TrimSpace(prompts[it.TurnID]); p != "" {
				fmt.Fprintf(&b, "## User\n%s\n\n", p)
			}
		}
		switch it.Kind {
		case projection.ItemMessage:
			// Thoughts are the agent talking to itself; they say little about
			// what it actually did and cost the most tokens.
			if it.ContentKind == "thought" {
				continue
			}
			text := strings.TrimSpace(it.Text)
			if text == "" {
				continue
			}
			if it.Role == "user" {
				// TurnStarted logs the prompt as an item as well as on the
				// turn record, and the turn record is where this renderer
				// takes it from. Printing both would spend the budget saying
				// the same thing twice.
				if text == strings.TrimSpace(prompts[it.TurnID]) {
					continue
				}
				fmt.Fprintf(&b, "## User\n%s\n\n", text)
				continue
			}
			fmt.Fprintf(&b, "## Agent\n%s\n\n", text)

		case projection.ItemTool:
			title := strings.TrimSpace(it.Title)
			if title == "" {
				title = it.ToolKind
			}
			fmt.Fprintf(&b, "### Tool (%s, %s): %s\n", it.ToolKind, it.Status, title)
			if body := clip(toolText(it), toolContentBudget); body != "" {
				fmt.Fprintf(&b, "%s\n", body)
			}
			b.WriteString("\n")

		case projection.ItemNotice:
			fmt.Fprintf(&b, "_(the conversation was compacted here)_\n\n")
		}
	}

	// Turn outcomes go last, as a compact ledger. A per-turn diff is the
	// single most useful fact for "did it change anything", and reading it
	// from the recorded diff rather than from tool calls means a formatter or
	// a codemod is counted too.
	var ledger strings.Builder
	for i, t := range state.Turns {
		line := fmt.Sprintf("- Turn %d", i+1)
		switch {
		case !t.Done:
			line += ": still running"
		case t.Error != "":
			line += ": failed — " + t.Error
		case t.StopReason != "" && t.StopReason != proto.StopEndTurn:
			line += ": " + t.StopReason
		default:
			line += ": completed"
		}
		switch {
		case t.Diff == nil:
			// No diff card at all. A turn that changed nothing is not
			// reported, and neither is one outside a git checkout, so silence
			// here means "not measured" and must not be read as "no changes".
		case t.Diff.Error != "":
			// An empty file list with an error is a failed measurement, not a
			// clean turn. Saying "changed no files" here would turn "could not
			// tell" into a claim.
			line += "; file changes could not be measured: " + t.Diff.Error
		case len(t.Diff.Files) > 0:
			names := make([]string, 0, len(t.Diff.Files))
			for _, f := range t.Diff.Files {
				names = append(names, fmt.Sprintf("%s (%s +%d/-%d)", f.Path, f.Status, f.Additions, f.Deletions))
			}
			line += fmt.Sprintf("; changed %d file(s): %s", len(t.Diff.Files), strings.Join(names, ", "))
		default:
			line += "; changed no files"
		}
		ledger.WriteString(line + "\n")
	}

	body := clipMiddle(b.String(), transcriptBudget)
	if ledger.Len() > 0 {
		// The ledger is appended after clipping so it always survives: it is
		// small, and it is the part that answers "did the agent change
		// anything" outright.
		body += "\n## Turn outcomes\n" +
			"(File changes are listed only where they were measured; a turn with no note about files was not measured, which is not the same as having changed nothing.)\n" +
			ledger.String()
	}
	return body
}

func transcriptStatus(state *projection.State) string {
	if state.Closed {
		return "closed"
	}
	if state.Phase == "turn" {
		return "still working"
	}
	return state.Phase
}

// toolText flattens a tool call's recorded content the way the transcript UI
// does, so the model reads what the operator would have seen.
func toolText(it projection.Item) string {
	parts := make([]string, 0, len(it.Content))
	for _, c := range it.Content {
		switch c.Type {
		case "diff":
			parts = append(parts, "--- "+c.Path+"\n"+c.Text)
		default:
			parts = append(parts, c.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func clip(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	return s[:budget] + "\n… (truncated)"
}

// clipMiddle keeps the head and the tail of an over-long transcript. The
// opening turn holds the request and the closing turns hold the outcome; the
// middle is where a long session repeats itself, so that is what goes.
func clipMiddle(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	head := budget / 2
	tail := budget - head
	// Cut on rune boundaries so the model is never handed a broken sequence.
	for head > 0 && !utf8Start(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !utf8Start(s[start]) {
		start++
	}
	return s[:head] + "\n\n… (a long middle section of this transcript was omitted) …\n\n" + s[start:]
}

// utf8Start reports whether a byte begins a UTF-8 sequence, i.e. is not a
// continuation byte.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
