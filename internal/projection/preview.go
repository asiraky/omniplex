package projection

import (
	"strconv"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

// Tool output is capped in the projection. A long thread's tool results ran
// to hundreds of MB, held in memory for as long as the actor lives and
// rewritten whole with every snapshot. The projection keeps a head/tail
// preview of each result instead; the full text stays in the event log, which
// remains authoritative, and is read back from there on demand.
//
// The web client mirrors this exactly: both sides must cut the same bytes so a
// client's preview matches the server's.
const (
	previewHead = 4096
	previewTail = 4096
	// previewSlack keeps a string that is only just over head+tail whole:
	// cutting a few bytes to say a few bytes were cut helps nobody. It also
	// outweighs the marker, so a capped string is under the threshold and
	// capping it again is a no-op.
	previewSlack = 128
	// maxToolContent is how many content entries one item keeps. Past it a
	// tool is streaming, and the log has the rest.
	maxToolContent = 16
)

// capText returns s as a head/tail preview, and whether anything was cut.
// The cut points move back to rune boundaries so neither half splits a
// character, and the marker counts the omitted middle in characters.
func capText(s string) (string, bool) {
	if len(s) <= previewHead+previewTail+previewSlack {
		return s, false
	}
	h := previewHead
	for h > 0 && !utf8.RuneStart(s[h]) {
		h--
	}
	tailStart := len(s) - previewTail
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	omitted := utf8.RuneCountInString(s[h:tailStart])
	return s[:h] + "\n\n[… " + strconv.Itoa(omitted) + " characters truncated …]\n\n" + s[tailStart:], true
}

// capToolContent caps the free-text fields of one content entry. Path is a
// name, not output, and is left alone.
func capToolContent(c proto.ToolContent) (proto.ToolContent, bool) {
	var a, b, d bool
	c.Text, a = capText(c.Text)
	c.Old, b = capText(c.Old)
	c.New, d = capText(c.New)
	return c, a || b || d
}

// appendToolContent adds a tool update's content to the item, capped, and
// marks the item truncated if any of it was cut or dropped.
func appendToolContent(it *Item, add []proto.ToolContent) {
	if room := maxToolContent - len(it.Content); len(add) > room {
		add = add[:max(room, 0)]
		it.Truncated = true
	}
	for _, c := range add {
		c, cut := capToolContent(c)
		if cut {
			it.Truncated = true
		}
		it.Content = append(it.Content, c)
	}
}

// capItem applies the same caps to an item already built, for a snapshot
// written before they existed. Dropped entries go into a fresh slice so the
// old backing array, and the text it holds, can be collected.
func capItem(it *Item) {
	if len(it.Content) > maxToolContent {
		it.Content = append([]proto.ToolContent(nil), it.Content[:maxToolContent]...)
		it.Truncated = true
	}
	for i := range it.Content {
		c, cut := capToolContent(it.Content[i])
		if cut {
			it.Content[i] = c
			it.Truncated = true
		}
	}
}
