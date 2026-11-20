package thread

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/store"
)

// HeadCommit is the commit checked out in dir: what a delegated thread's
// fresh worktree branches from, so it starts where its parent is.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	out, err := runGitEnv(ctx, dir, gitEnv(), "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TurnDigest is one turn as another agent reads it: what it was asked and what it answered.
type TurnDigest struct {
	Index  int // 0-based position in State.Turns
	ID     string
	Prompt string
	// Reply is the turn's top-level agent text, its messages joined with a
	// blank line in order. Thoughts, tools and a subagent's own output are
	// left out: they are how the turn got there, not what it said.
	Reply      string
	StopReason string // empty while running
	Error      string
	Done       bool
}

// PendingQuestion is a question a thread is waiting on an answer to.
type PendingQuestion struct {
	RequestID string
	Prompt    string
	Schema    json.RawMessage
}

// PendingApproval is a permission request a thread is waiting on. Only the
// user may answer one.
type PendingApproval struct {
	RequestID, ToolName, Title string
}

// Digest is where a thread stands, small enough to hand another agent.
type Digest struct {
	Attention  string // as Actor.Attention()
	TurnFailed bool
	Turns      int         // len(State.Turns)
	Last       *TurnDigest // the latest turn, nil before the first
	// Questions are the pending elicitations, less omniplex's own cards: a
	// card is a proposal for the user to save or decline, not a question an
	// agent can answer.
	Questions []PendingQuestion
	Approvals []PendingApproval // pending permission requests
}

// Digest reads where the thread stands, on the actor loop and without
// copying the rest of its state.
func (a *Actor) Digest(ctx context.Context) (Digest, error) {
	v, err := a.read(ctx, func(s *projection.State) any {
		d := Digest{Attention: a.Attention(), TurnFailed: a.TurnFailed(), Turns: len(s.Turns)}
		if n := len(s.Turns); n > 0 {
			parts := replyParts(s, n-1)
			t := turnDigest(s, n-1, parts[0])
			d.Last = &t
		}
		for _, e := range s.Elicitations {
			if !e.IsCard() {
				d.Questions = append(d.Questions, PendingQuestion{RequestID: e.RequestID, Prompt: e.Prompt, Schema: slices.Clone(e.Schema)})
			}
		}
		for _, p := range s.Pending {
			d.Approvals = append(d.Approvals, PendingApproval{RequestID: p.RequestID, ToolName: p.ToolName, Title: p.Title})
		}
		return d
	})
	if err != nil {
		return Digest{}, err
	}
	return v.(Digest), nil
}

// Transcript returns turns from index since on, oldest first, stopping before
// maxChars of Prompt+Reply would be exceeded (always at least one turn when any
// remain); next is the index to pass as since to continue (== Turns when done).
// A single turn that alone exceeds maxChars keeps the tail of its reply,
// prefixed with "…", cut to fit. maxChars <= 0 means no limit.
func (a *Actor) Transcript(ctx context.Context, since, maxChars int) (turns []TurnDigest, next int, err error) {
	type page struct {
		turns []TurnDigest
		next  int
	}
	v, err := a.read(ctx, func(s *projection.State) any {
		since := max(since, 0)
		if since >= len(s.Turns) {
			return page{next: len(s.Turns)}
		}
		parts := replyParts(s, since)
		out := []TurnDigest{}
		used := 0
		i := since
		for ; i < len(s.Turns); i++ {
			t := turnDigest(s, i, parts[i-since])
			size := utf8.RuneCountInString(t.Prompt) + utf8.RuneCountInString(t.Reply)
			if maxChars > 0 && used+size > maxChars {
				if len(out) > 0 {
					break
				}
				t.Reply = KeepTail(t.Reply, maxChars-utf8.RuneCountInString(t.Prompt))
				size = maxChars
			}
			out = append(out, t)
			used += size
		}
		return page{turns: out, next: i}
	})
	if err != nil {
		return nil, 0, err
	}
	p := v.(page)
	return p.turns, p.next, nil
}

// replyParts collects, in one pass over the items, the top-level agent text
// of every turn from index since on: element k holds turn since+k's messages.
// They are the state's own strings, not copies, so collecting turns a caller
// ends up not reading costs nothing.
func replyParts(s *projection.State, since int) [][]string {
	parts := make([][]string, len(s.Turns)-since)
	at := make(map[string]int, len(parts))
	for k, t := range s.Turns[since:] {
		at[t.ID] = k
	}
	for _, it := range s.Items {
		if it.Kind != projection.ItemMessage || it.Role != "agent" || it.ContentKind != "text" || it.ParentID != "" || it.Text == "" {
			continue
		}
		if k, ok := at[it.TurnID]; ok {
			parts[k] = append(parts[k], it.Text)
		}
	}
	return parts
}

func turnDigest(s *projection.State, i int, parts []string) TurnDigest {
	t := s.Turns[i]
	return TurnDigest{Index: i, ID: t.ID, Prompt: t.Prompt, Reply: strings.Join(parts, "\n\n"), StopReason: t.StopReason, Error: t.Error, Done: t.Done}
}

// KeepTail cuts text to at most n runes, keeping its end behind a "…". The
// end is where a turn says what it found; the start is how it got there.
func KeepTail(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	if n <= 1 {
		return "…"
	}
	r := []rune(text)
	return "…" + string(r[len(r)-(n-1):])
}

// Descendants is every thread whose parent chain reaches id: its children,
// theirs, and so on, nearest first. One read of the thread list, and a chain
// that loops back on itself ends rather than spins.
func (m *Manager) Descendants(ctx context.Context, id string) ([]store.ThreadMeta, error) {
	all, err := m.store.ListThreads(ctx)
	if err != nil {
		return nil, err
	}
	children := map[string][]int{}
	for i, t := range all {
		if t.ParentID != "" {
			children[t.ParentID] = append(children[t.ParentID], i)
		}
	}
	seen := map[string]bool{id: true}
	out := []store.ThreadMeta{}
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		for _, i := range children[queue[0]] {
			if c := all[i]; !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c)
				queue = append(queue, c.ID)
			}
		}
	}
	return out, nil
}

// IsDescendant reports whether id's parent chain reaches ancestor. A thread
// is not its own descendant.
func (m *Manager) IsDescendant(ctx context.Context, ancestor, id string) (bool, error) {
	all, err := m.store.ListThreads(ctx)
	if err != nil {
		return false, err
	}
	parent := make(map[string]string, len(all))
	for _, t := range all {
		parent[t.ID] = t.ParentID
	}
	seen := map[string]bool{id: true}
	for cur := parent[id]; cur != ""; cur = parent[cur] {
		if cur == ancestor {
			return true, nil
		}
		if seen[cur] {
			return false, nil
		}
		seen[cur] = true
	}
	return false, nil
}
