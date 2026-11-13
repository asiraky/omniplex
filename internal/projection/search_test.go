package projection

import (
	"slices"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func msg(id, role, contentKind, text string) Item {
	return Item{ID: id, Kind: ItemMessage, Role: role, ContentKind: contentKind, Text: text}
}

func matchIDs(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Only what the reader sees as a row of conversation is searched: user and
// agent text, and plans. Thinking, tools, notices and anything folded under a
// parent are not.
func TestSearchMatchesConversationAndPlansOnly(t *testing.T) {
	s := New("s")
	child := msg("child", "agent", "text", "the login inside a subagent")
	child.ParentID = "task"
	s.Items = []Item{
		msg("user", "user", "text", "Fix the LOGIN page"),
		msg("thought", "agent", "thought", "the login is broken"),
		{ID: "task", Kind: ItemTool, Title: "login", Input: []byte(`{"q":"login"}`)},
		child,
		msg("agent", "agent", "text", "Login fixed."),
		{ID: "notice", Kind: ItemNotice, Text: "login account switched"},
		{ID: "step", Kind: ItemPlan, Steps: []proto.PlanStep{{Text: "read"}, {Text: "patch logIn"}}},
		{ID: "why", Kind: ItemPlan, Explanation: "the login flow changed", Steps: []proto.PlanStep{{Text: "ship"}}},
		{ID: "plain-plan", Kind: ItemPlan, Steps: []proto.PlanStep{{Text: "nothing here"}}},
		msg("empty", "user", "text", ""),
	}
	s.reindex()

	got, total := s.Search("lOgIn", 0)
	want := []string{"user", "agent", "step", "why"}
	if !slices.Equal(matchIDs(got), want) || total != len(want) {
		t.Fatalf("matches = %v total %d, want %v", matchIDs(got), total, want)
	}
	for _, m := range got {
		if s.Items[m.Index].ID != m.ID {
			t.Fatalf("match %s has index %d, which holds %s", m.ID, m.Index, s.Items[m.Index].ID)
		}
	}
}

func TestSearchFoldsCaseBeyondASCII(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		msg("a", "user", "text", "ÜBER grüße"),
		msg("b", "agent", "text", "uber"),
		msg("c", "agent", "text", "naïve"),
	}
	s.reindex()
	if got, _ := s.Search("über", 0); !slices.Equal(matchIDs(got), []string{"a"}) {
		t.Fatalf("über matched %v", matchIDs(got))
	}
	if got, _ := s.Search("NAÏ", 0); !slices.Equal(matchIDs(got), []string{"c"}) {
		t.Fatalf("NAÏ matched %v", matchIDs(got))
	}
	// Case folds; accents do not.
	if got, _ := s.Search("NAI", 0); len(got) != 0 {
		t.Fatalf("NAI matched %v", matchIDs(got))
	}
	if got, total := s.Search("zz", 0); len(got) != 0 || total != 0 || got == nil {
		t.Fatalf("no match = %#v total %d, want an empty list", got, total)
	}
}

// Past the cap the newest matches are kept, oldest first, and total still
// counts every one.
func TestSearchCapKeepsNewestWithTrueTotal(t *testing.T) {
	s := New("s")
	for i := range 10 {
		id := string(rune('a' + i))
		text := "miss"
		if i%2 == 0 {
			text = "hit"
		}
		s.Items = append(s.Items, msg(id, "user", "text", text))
	}
	s.reindex()
	// Hits sit at a c e g i; the newest three are e g i.
	want := []string{"e", "g", "i"}
	got, total := s.Search("hit", 3)
	if !slices.Equal(matchIDs(got), want) || total != 5 {
		t.Fatalf("capped = %v total %d, want %v total 5", matchIDs(got), total, want)
	}
	if got[0].Index != 4 || got[2].Index != 8 {
		t.Fatalf("indices = %+v", got)
	}
	if got, total := s.Search("hit", 0); len(got) != 5 || total != 5 {
		t.Fatalf("uncapped = %v total %d", matchIDs(got), total)
	}
}

// Text the client never draws as text is not searched: a prompt's
// attached-files trailer (drawn as tiles) and the prompt of a turn the server
// started to recover or to continue past a usage limit (drawn as a fixed
// label). The same tag mid-prompt is the
// person's own words.
func TestSearchSkipsTextTheClientHides(t *testing.T) {
	s := New("s")
	attached := msg("attached", "user", "text",
		"look at this\n\n<attached-files>\n- report.pdf (application/pdf, 2 KB, artefact a1): /tmp/report.pdf\n</attached-files>\n")
	quoted := msg("quoted", "user", "text", "what does <attached-files>report</attached-files> mean")
	resumed := msg("resumed", "user", "text", "pick the report back up")
	resumed.TurnID = "t2"
	asked := msg("asked", "user", "text", "write the report")
	asked.TurnID = "t1"
	continued := msg("continued", "user", "text", "finish the report")
	continued.TurnID = "t3"
	scheduled := msg("scheduled", "user", "text", "the weekly report")
	scheduled.TurnID = "t4"
	s.Turns = []Turn{{ID: "t1"}, {ID: "t2", Recovery: &proto.TurnRecovery{}}, {ID: "t3"}, {ID: "t4"}}
	s.Scheduled = []proto.ScheduledPrompt{
		{ID: "q1", Kind: proto.ScheduleLimit, Status: "sent", TurnID: "t3"},
		// One a person scheduled is their own words.
		{ID: "q2", Status: "sent", TurnID: "t4"},
	}
	s.Items = []Item{attached, quoted, asked, resumed, continued, scheduled}

	got, _ := s.Search("report", 0)
	if ids := matchIDs(got); !slices.Equal(ids, []string{"quoted", "asked", "scheduled"}) {
		t.Fatalf("report matched %v", ids)
	}
	got, _ = s.Search("look at", 0)
	if ids := matchIDs(got); !slices.Equal(ids, []string{"attached"}) {
		t.Fatalf("the prompt's own words matched %v", ids)
	}
}
