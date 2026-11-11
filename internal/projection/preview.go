package projection

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

// Tool output is capped in the projection. A long thread's tool results ran
// to hundreds of MB, held in memory for as long as the actor lives and
// rewritten whole with every snapshot. The projection keeps a head/tail
// preview of each result instead; the full text stays in the event log, which
// remains authoritative, and is read back from there on demand.
//
// The web client applies the same rule to the live events it folds, so a
// client holding a thread stays bounded too. It counts UTF-16 units where this
// counts bytes; the two previews can differ slightly, and nothing compares them.
const (
	previewHead = 4096
	previewTail = 4096
	// previewSlack keeps a string that is only just over head+tail whole:
	// cutting a few bytes to say a few bytes were cut helps nobody. It also
	// outweighs the marker, so a capped string is under the threshold and
	// capping it again is a no-op.
	previewSlack = 128
	// previewSnap is how far a cut may move to land on a line break, so the
	// preview never ends or starts on part of a line that reads like a whole
	// one.
	previewSnap = 512
	// maxToolContent is how many content entries one item keeps. Past it a
	// tool is streaming, and the log has the rest.
	maxToolContent = 16
)

// capText returns s as a head/tail preview, and whether anything was cut.
// Each cut moves inward to a line break when one is near, else to a rune
// boundary so neither half splits a character. The marker counts the omitted
// middle in characters.
func capText(s string) (string, bool) {
	if len(s) <= previewHead+previewTail+previewSlack {
		return s, false
	}
	h := previewHead
	if i := strings.LastIndexByte(s[h-previewSnap:h], '\n'); i >= 0 {
		h = h - previewSnap + i + 1
	} else {
		for h > 0 && !utf8.RuneStart(s[h]) {
			h--
		}
	}
	tailStart := len(s) - previewTail
	if i := strings.IndexByte(s[tailStart:tailStart+previewSnap], '\n'); i >= 0 {
		tailStart += i + 1
	} else {
		for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
			tailStart++
		}
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
// old backing array, and the text it holds, can be collected. It reports
// whether anything was cut.
func capItem(it *Item) bool {
	changed := false
	if len(it.Content) > maxToolContent {
		it.Content = append([]proto.ToolContent(nil), it.Content[:maxToolContent]...)
		it.Truncated = true
		changed = true
	}
	for i := range it.Content {
		c, cut := capToolContent(it.Content[i])
		if cut {
			it.Content[i] = c
			it.Truncated = true
			changed = true
		}
	}
	return changed
}

// capJSONStrings caps every string in a JSON document with capText: a tool's
// input is short fields around the odd long one, a file's whole text or a
// script. Keys are names and are left alone, numbers are carried as written,
// and keys keep their order. It reports whether anything was cut; when nothing
// was, or raw is not valid JSON, raw comes back as it was.
func capJSONStrings(raw json.RawMessage) (json.RawMessage, bool) {
	// Decoding never lengthens a string, so a document no longer than the
	// threshold cannot hold one worth cutting.
	if len(raw) <= previewHead+previewTail+previewSlack {
		return raw, false
	}
	w := &jsonCapper{dec: json.NewDecoder(bytes.NewReader(raw))}
	w.dec.UseNumber()
	w.enc = json.NewEncoder(&w.buf)
	w.enc.SetEscapeHTML(false)
	if err := w.value(); err != nil || !w.cut {
		return raw, false
	}
	if _, err := w.dec.Token(); err != io.EOF {
		return raw, false
	}
	return json.RawMessage(w.buf.Bytes()), true
}

// jsonCapper re-emits a JSON document token by token, capping strings on the
// way through.
type jsonCapper struct {
	dec *json.Decoder
	enc *json.Encoder
	buf bytes.Buffer
	cut bool
}

func (w *jsonCapper) value() error {
	tok, err := w.dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Delim:
		w.buf.WriteByte(byte(v))
		for first := true; w.dec.More(); first = false {
			if !first {
				w.buf.WriteByte(',')
			}
			if v == '{' {
				k, err := w.dec.Token()
				if err != nil {
					return err
				}
				w.str(k.(string))
				w.buf.WriteByte(':')
			}
			if err := w.value(); err != nil {
				return err
			}
		}
		end, err := w.dec.Token()
		if err != nil {
			return err
		}
		w.buf.WriteByte(byte(end.(json.Delim)))
	case string:
		s, cut := capText(v)
		w.cut = w.cut || cut
		w.str(s)
	case json.Number:
		w.buf.WriteString(v.String())
	case bool:
		w.buf.WriteString(strconv.FormatBool(v))
	case nil:
		w.buf.WriteString("null")
	}
	return nil
}

// str writes s as a JSON string. The encoder ends every value with a newline,
// which a string in the middle of a document must not have.
func (w *jsonCapper) str(s string) {
	_ = w.enc.Encode(s)
	w.buf.Truncate(w.buf.Len() - 1)
}
