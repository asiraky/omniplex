// Find in thread. A search runs over the actor's whole timeline, not the
// window a client holds, and answers with positions only: the client pages to
// a match with one /items?from= fetch rather than downloading the transcript.
package projection

import (
	"strings"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

// SearchMatchCap is how many matches a search returns. The newest are kept,
// since the find bar opens on the newest; the reply's total counts them all.
const SearchMatchCap = 500

// Match is one item a search found, by id and by position in s.Items — the
// index space ItemsBefore and /items?from= use.
type Match struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
}

// Search finds the top-level items whose text holds q, case-insensitively:
// user and agent text messages, and plans (steps and explanation). Thinking,
// tool calls, notices, artefacts and cards are never searched, nor is a child
// item — the client never shows one as a row, so there is nothing to jump to.
// Nor is text the client hides: a prompt's attached-files trailer, which it
// draws as file tiles, and the prompt the server wrote to resume a recovered
// turn or continue past a usage limit, which it draws as a fixed label.
//
// When more than limit items match, the newest limit are returned; limit <= 0
// returns every match. Matches come oldest first; total is the true count.
func (s *State) Search(q string, limit int) (matches []Match, total int) {
	if q == "" {
		return []Match{}, 0
	}
	needle := strings.ToLower(q)
	serverPrompted := map[string]bool{}
	for _, t := range s.Turns {
		if t.Recovery != nil {
			serverPrompted[t.ID] = true
		}
	}
	// The continuation armed at a usage limit is the server's prompt too; the
	// schedule that sent it names its turn. The client's rule is recoveredTurns
	// in web/src/components/transcript/useTranscriptModel.ts.
	for _, p := range s.Scheduled {
		if p.Kind == proto.ScheduleLimit && p.Status == "sent" && p.TurnID != "" {
			serverPrompted[p.TurnID] = true
		}
	}
	var hits []int
	for i := range s.Items {
		if itemMatches(&s.Items[i], needle, serverPrompted) {
			hits = append(hits, i)
		}
	}
	total = len(hits)
	if limit > 0 && len(hits) > limit {
		hits = hits[len(hits)-limit:]
	}
	matches = make([]Match, len(hits))
	for k, i := range hits {
		matches[k] = Match{ID: s.Items[i].ID, Index: i}
	}
	return matches, total
}

// itemMatches reports whether it is a searchable item whose text holds
// needle, which is already lower-cased. serverPrompted holds the ids of turns
// the server started itself.
func itemMatches(it *Item, needle string, serverPrompted map[string]bool) bool {
	if it.ParentID != "" {
		return false
	}
	switch it.Kind {
	case ItemMessage:
		switch {
		case it.ContentKind != "text":
			return false
		case it.Role == "agent":
			return containsFold(it.Text, needle)
		case it.Role == "user":
			return !serverPrompted[it.TurnID] && containsFold(withoutAttachedFiles(it.Text), needle)
		}
	case ItemPlan:
		if containsFold(it.Explanation, needle) {
			return true
		}
		for _, st := range it.Steps {
			if containsFold(st.Text, needle) {
				return true
			}
		}
	}
	return false
}

// withoutAttachedFiles cuts the attached-files trailer (server/skills.go's
// attachedFiles) off a prompt, by the client's rule: the block closes the
// text and its opening tag starts a line.
func withoutAttachedFiles(text string) string {
	const open, closing = "<attached-files>", "</attached-files>"
	trimmed := strings.TrimRight(text, " \t\r\n")
	if !strings.HasSuffix(trimmed, closing) {
		return text
	}
	i := strings.LastIndex(trimmed, open)
	if i == -1 || (i > 0 && trimmed[i-1] != '\n') {
		return text
	}
	return trimmed[:i]
}

// containsFold reports whether s holds needle, ignoring case. needle is
// already lower-cased. An ASCII needle is matched byte by byte with ASCII
// folding, so the transcript is never copied: an ASCII byte cannot sit inside
// a multi-byte UTF-8 sequence, so this cannot match across a character. Any
// other needle lower-cases s.
func containsFold(s, needle string) bool {
	if !isASCII(needle) {
		// Lower-casing can change a character's byte length, so lengths
		// are only comparable after it.
		return strings.Contains(strings.ToLower(s), needle)
	}
	first := needle[0]
	for i := 0; i+len(needle) <= len(s); i++ {
		if lowerASCII(s[i]) != first {
			continue
		}
		j := 1
		for j < len(needle) && lowerASCII(s[i+j]) == needle[j] {
			j++
		}
		if j == len(needle) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
