package projection

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

var markerRe = regexp.MustCompile(`\n\n\[… (\d+) characters truncated …\]\n\n`)

// splitPreview pulls a capped string apart into head, omitted count and tail.
func splitPreview(t *testing.T, s string) (string, int, string) {
	t.Helper()
	loc := markerRe.FindStringSubmatchIndex(s)
	if loc == nil {
		t.Fatalf("no truncation marker in %d-byte string", len(s))
	}
	n, err := strconv.Atoi(s[loc[2]:loc[3]])
	if err != nil {
		t.Fatal(err)
	}
	return s[:loc[0]], n, s[loc[1]:]
}

func toolUpdate(t *testing.T, seq int64, id string, content ...proto.ToolContent) proto.Event {
	t.Helper()
	return event(t, seq, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: id, Status: proto.StatusCompleted, Content: content,
	})
}

// A big tool result keeps its head and tail, and the marker between them says
// how much was left out. A diff's old and new text are capped the same way;
// its path is not output and is kept whole.
func TestBigToolResultIsCappedToHeadAndTail(t *testing.T) {
	text := strings.Repeat("h", 6000) + strings.Repeat("m", 10000) + strings.Repeat("t", 6000)
	path := strings.Repeat("p", 9000)
	s := New("s1")
	s.Apply(toolUpdate(t, 1, "c1",
		proto.ToolContent{Type: "text", Text: text},
		proto.ToolContent{Type: "diff", Path: path, Old: text, New: text},
	))

	it, _ := s.ItemByID("c1")
	if !it.Truncated {
		t.Fatal("item with a capped result is not marked truncated")
	}
	for _, got := range []string{it.Content[0].Text, it.Content[1].Old, it.Content[1].New} {
		head, n, tail := splitPreview(t, got)
		if head != text[:len(head)] || tail != text[len(text)-len(tail):] {
			t.Fatal("preview is not the original's head and tail")
		}
		if len(head)+n+len(tail) != len(text) {
			t.Fatalf("head %d + omitted %d + tail %d != original %d", len(head), n, len(tail), len(text))
		}
		if len(got) >= len(text)/2 {
			t.Fatalf("capped to %d bytes from %d", len(got), len(text))
		}
	}
	if it.Content[1].Path != path {
		t.Fatal("diff path was cut")
	}
}

// A result that fits is kept byte for byte, and the item is not marked.
func TestSmallToolResultIsUntouched(t *testing.T) {
	fits := strings.Repeat("x", previewHead+previewTail+previewSlack)
	s := New("s1")
	s.Apply(toolUpdate(t, 1, "c1", proto.ToolContent{Type: "text", Text: fits}))
	s.Apply(toolUpdate(t, 2, "c1", proto.ToolContent{Type: "text", Text: "ok"}))

	it, _ := s.ItemByID("c1")
	if it.Truncated || it.Content[0].Text != fits || it.Content[1].Text != "ok" {
		t.Fatalf("small results changed: truncated=%v lens=%d,%d", it.Truncated, len(it.Content[0].Text), len(it.Content[1].Text))
	}
}

// Cuts land on rune boundaries whatever the byte offsets: 3- and 4-byte
// characters never line up with the head and tail sizes, and the count in the
// marker is characters, not bytes.
func TestCapNeverSplitsARune(t *testing.T) {
	for _, r := range []string{"é", "€", "𝄞"} {
		// Offset by one ASCII byte so the 2-byte case is misaligned too.
		text := "a" + strings.Repeat(r, 20000/len(r))
		got, cut := capText(text)
		if !cut {
			t.Fatalf("%q: not cut", r)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("%q: preview is not valid UTF-8", r)
		}
		head, n, tail := splitPreview(t, got)
		if head != text[:len(head)] || tail != text[len(text)-len(tail):] {
			t.Fatalf("%q: preview is not the original's head and tail", r)
		}
		if total := utf8.RuneCountInString(head) + n + utf8.RuneCountInString(tail); total != utf8.RuneCountInString(text) {
			t.Fatalf("%q: head+omitted+tail = %d chars, original %d", r, total, utf8.RuneCountInString(text))
		}
	}
}

// Capping is idempotent: a capped state written to a snapshot and read back
// is unchanged. Without this every load would re-cut the preview's marker.
func TestCapIsIdempotentThroughSnapshot(t *testing.T) {
	big := strings.Repeat("ab€", 50000)
	s := New("s1")
	s.Apply(toolUpdate(t, 1, "c1",
		proto.ToolContent{Type: "text", Text: big},
		proto.ToolContent{Type: "diff", Path: "f.go", Old: big, New: "short"},
	))
	again, cut := capText(s.Items[0].Content[0].Text)
	if cut || again != s.Items[0].Content[0].Text {
		t.Fatal("capping a preview cut it again")
	}

	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Items, s.Items) {
		t.Fatal("snapshot of a capped state changed on load")
	}
}

// An item keeps a bounded number of content entries. Past the limit further
// entries are dropped, in arrival order, and the item is marked.
func TestToolContentEntriesAreBounded(t *testing.T) {
	batch := func(from int) []proto.ToolContent {
		var out []proto.ToolContent
		for i := from; i < from+10; i++ {
			out = append(out, proto.ToolContent{Type: "text", Text: strconv.Itoa(i)})
		}
		return out
	}
	s := New("s1")
	s.Apply(toolUpdate(t, 1, "c1", batch(0)...))
	if it, _ := s.ItemByID("c1"); it.Truncated {
		t.Fatal("marked truncated while under the entry limit")
	}
	s.Apply(toolUpdate(t, 2, "c1", batch(10)...))
	s.Apply(toolUpdate(t, 3, "c1", batch(20)...))

	it, _ := s.ItemByID("c1")
	if !it.Truncated || len(it.Content) != maxToolContent {
		t.Fatalf("truncated=%v entries=%d, want true/%d", it.Truncated, len(it.Content), maxToolContent)
	}
	for i, c := range it.Content {
		if c.Text != strconv.Itoa(i) {
			t.Fatalf("entry %d = %q: kept the wrong entries", i, c.Text)
		}
	}
}

// A snapshot written before the cap holds results whole. Loading it caps them,
// so the next snapshot written is small.
func TestFromSnapshotCapsAnOldSnapshot(t *testing.T) {
	big := strings.Repeat("y", 1<<20)
	old := New("s1")
	content := make([]proto.ToolContent, 20)
	for i := range content {
		content[i] = proto.ToolContent{Type: "text", Text: big}
	}
	old.Items = append(old.Items,
		Item{ID: "c1", Kind: ItemTool, Status: proto.StatusCompleted, Content: content},
		Item{ID: "c2", Kind: ItemTool, Status: proto.StatusCompleted, Content: []proto.ToolContent{{Type: "text", Text: "fine"}}},
	)
	blob, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}

	s, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := s.ItemByID("c1")
	if !c1.Truncated || len(c1.Content) != maxToolContent {
		t.Fatalf("c1 truncated=%v entries=%d", c1.Truncated, len(c1.Content))
	}
	for _, c := range c1.Content {
		if _, n, _ := splitPreview(t, c.Text); n == 0 {
			t.Fatal("entry not capped")
		}
	}
	if c2, _ := s.ItemByID("c2"); c2.Truncated || c2.Content[0].Text != "fine" {
		t.Fatalf("small item changed: %+v", c2)
	}

	next, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) > len(blob)/100 {
		t.Fatalf("re-written snapshot is %d bytes, from %d", len(next), len(blob))
	}
}

// Line output is cut between lines when a break is near, so neither half
// ends or starts with part of a line that reads like a whole one.
func TestPreviewCutsBetweenLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		b.WriteString(strconv.Itoa(i) + "\n")
	}
	text := b.String()
	got, cut := capText(text)
	if !cut {
		t.Fatal("not capped")
	}
	head, n, tail := splitPreview(t, got)
	if !strings.HasSuffix(head, "\n") || !strings.HasPrefix(text[len(text)-len(tail)-1:], "\n") {
		t.Fatalf("cut mid-line: head ends %q, tail starts %q", head[len(head)-8:], tail[:8])
	}
	if head != text[:len(head)] || len(head)+n+len(tail) != len(text) {
		t.Fatal("preview is not the original's head and tail")
	}
}

// Every string leaf in a JSON document is capped, at any depth, while keys,
// numbers as written, literals and key order come through untouched.
func TestCapJSONStringsCapsLeavesOnly(t *testing.T) {
	long := strings.Repeat("h", 6000) + strings.Repeat("m", 10000) + strings.Repeat("t", 6000)
	key := strings.Repeat("k", 9000)
	raw := json.RawMessage(`{"z":"` + long + `","a":[1.50,12345678901234567890,true,null,{"` + key + `":"` + long + `"}],"m":"<b>&"}`)

	got, cut := capJSONStrings(raw)
	if !cut {
		t.Fatal("not cut")
	}
	if !json.Valid(got) {
		t.Fatalf("capped input is not valid JSON")
	}
	// Order and numbers as written, read straight off the bytes.
	if !strings.HasPrefix(string(got), `{"z":"`) || !strings.Contains(string(got), `"a":[1.50,12345678901234567890,true,null,{"`+key+`":"`) ||
		!strings.HasSuffix(string(got), `,"m":"<b>&"}`) {
		t.Fatalf("structure changed: %.200s … %.200s", got, got[len(got)-200:])
	}
	var v struct {
		Z string `json:"z"`
		A []json.RawMessage
	}
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	var nested map[string]string
	if err := json.Unmarshal(v.A[4], &nested); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{v.Z, nested[key]} {
		head, n, tail := splitPreview(t, s)
		if head != long[:len(head)] || tail != long[len(long)-len(tail):] || len(head)+n+len(tail) != len(long) {
			t.Fatal("string is not the original's head and tail")
		}
	}

	again, cut := capJSONStrings(got)
	if cut || !bytes.Equal(again, got) {
		t.Fatal("capping capped input cut it again")
	}
}

// The budget is across the whole input: once it is spent, later long strings
// are reduced to the marker, while short ones — paths, flags — stay whole.
func TestCapJSONStringsBudgetsTheWholeInput(t *testing.T) {
	middling := strings.Repeat("m", 6000) // under the per-string cap
	fields := make([]string, 12)
	for i := range fields {
		fields[i] = `"f` + strconv.Itoa(i) + `":"` + middling + `"`
	}
	raw := json.RawMessage(`{` + strings.Join(fields, ",") + `,"path":"/a/b.go"}`)

	got, cut := capJSONStrings(raw)
	if !cut {
		t.Fatal("not cut")
	}
	var v map[string]string
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatal(err)
	}
	kept := 0
	for i := range fields {
		switch f := v["f"+strconv.Itoa(i)]; {
		case f == middling:
			kept++
		case !strings.Contains(f, "6000 characters truncated"):
			t.Fatalf("f%d is neither whole nor the marker: %.80s", i, f)
		}
	}
	if kept != maxInputText/len(middling) {
		t.Fatalf("kept %d whole, want %d", kept, maxInputText/len(middling))
	}
	if v["path"] != "/a/b.go" {
		t.Fatalf("short field = %q", v["path"])
	}
}

// Input with nothing to cut comes back as the same bytes, not a re-encoding;
// so does input that is not JSON at all.
func TestCapJSONStringsLeavesUncutInputAlone(t *testing.T) {
	many := make([]string, 2000)
	for i := range many {
		many[i] = strconv.Quote("short " + strconv.Itoa(i))
	}
	bigButFine := json.RawMessage(`{ "list" : [` + strings.Join(many, ", ") + `] }`)
	broken := json.RawMessage(`{"a":"` + strings.Repeat("x", 20000) + `"`)
	trailing := json.RawMessage(`{"a":"` + strings.Repeat("x", 20000) + `"} {}`)
	for _, raw := range []json.RawMessage{bigButFine, broken, trailing, json.RawMessage(`"hi"`), nil} {
		got, cut := capJSONStrings(raw)
		if cut || len(got) != len(raw) || (len(raw) > 0 && &got[0] != &raw[0]) {
			t.Fatalf("%.40s…: cut=%v, or not the original bytes", raw, cut)
		}
	}
}
