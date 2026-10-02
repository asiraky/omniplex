package server

import (
	"bytes"
	"regexp"
	"strings"
)

// A page written for the root of a site names its files from there:
// "/style.css", "/fonts/x.woff", "/". On a token route the root of the
// origin is omniplex, so left alone those URLs reach the app instead of the
// artefact: the page picks up omniplex's favicon, or gets the app's HTML for
// its stylesheet and renders unstyled. rootURLsHTML and rootURLsCSS point
// them at the artefact's own site root.
//
// They cover the places markup and CSS name a URL: an attribute, a srcset,
// url() and @import, in a tag, a <style> or a stylesheet. Text the page
// shows is left as written, and so is script: a URL a script builds at run
// time is not rewritten. "//host/x" is another site and is left alone.

var (
	rootAttr   = regexp.MustCompile(`(?i)(\s(?:href|src|action|formaction|poster|data|xlink:href)\s*=\s*["']?)/([^/])`)
	rootSrcset = regexp.MustCompile(`(?i)\s(?:image)?srcset\s*=\s*(?:"[^"]*"|'[^']*')`)
	srcsetURL  = regexp.MustCompile(`(["',])(\s*)/([^/])`)
	rootCSS    = regexp.MustCompile(`(?i)(url\(\s*["']?|@import\s+["'])/([^/])`)
)

// rawText are the elements whose content is not markup. Only <style>'s is
// rewritten; the rest pass through untouched.
var rawText = []string{"style", "script", "textarea", "title", "xmp", "noscript"}

// replacement escapes root for use in a regexp template.
func replacement(root string) string { return strings.ReplaceAll(root, "$", "$$") }

// rootURLsCSS rewrites a stylesheet's root-relative URLs to start at root,
// which ends in a slash.
func rootURLsCSS(css []byte, root string) []byte {
	return rootCSS.ReplaceAll(css, []byte("${1}"+replacement(root)+"${2}"))
}

// rootURLsHTML rewrites the root-relative URLs in a document's tags and
// <style> elements to start at root, which ends in a slash.
func rootURLsHTML(doc []byte, root string) []byte {
	out := make([]byte, 0, len(doc)+1024)
	for i := 0; i < len(doc); {
		lt := bytes.IndexByte(doc[i:], '<')
		if lt < 0 {
			out = append(out, doc[i:]...)
			break
		}
		out = append(out, doc[i:i+lt]...)
		i += lt
		switch {
		case bytes.HasPrefix(doc[i:], []byte("<!--")):
			n := len(doc) - i
			if end := bytes.Index(doc[i+4:], []byte("-->")); end >= 0 {
				n = 4 + end + 3
			}
			out = append(out, doc[i:i+n]...)
			i += n
		case i+1 < len(doc) && isASCIILetter(doc[i+1]):
			n := tagEnd(doc[i:])
			out = append(out, rootURLsTag(doc[i:i+n], root)...)
			name := tagName(doc[i : i+n])
			i += n
			for _, raw := range rawText {
				if name != raw {
					continue
				}
				body := len(doc) - i
				if end := indexFold(doc[i:], "</"+raw); end >= 0 {
					body = end
				}
				if raw == "style" {
					out = append(out, rootURLsCSS(doc[i:i+body], root)...)
				} else {
					out = append(out, doc[i:i+body]...)
				}
				i += body
			}
		default:
			out = append(out, '<')
			i++
		}
	}
	return out
}

func rootURLsTag(tag []byte, root string) []byte {
	r := replacement(root)
	tag = rootAttr.ReplaceAll(tag, []byte("${1}"+r+"${2}"))
	tag = rootCSS.ReplaceAll(tag, []byte("${1}"+r+"${2}")) // style="…url(/x)…"
	return rootSrcset.ReplaceAllFunc(tag, func(m []byte) []byte {
		return srcsetURL.ReplaceAll(m, []byte("${1}${2}"+r+"${3}"))
	})
}

// tagEnd is the length of the tag at the start of doc, through its '>'. A
// quote opens a value only after '=', so an apostrophe in an unquoted value
// does not swallow the rest of the page.
func tagEnd(doc []byte) int {
	var quote, prev byte
	for j := 1; j < len(doc); j++ {
		c := doc[j]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case (c == '"' || c == '\'') && prev == '=':
			quote = c
		case c == '>':
			return j + 1
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prev = c
		}
	}
	return len(doc)
}

func tagName(tag []byte) string {
	j := 1
	for j < len(tag) && (isASCIILetter(tag[j]) || tag[j] >= '0' && tag[j] <= '9' || tag[j] == '-') {
		j++
	}
	return strings.ToLower(string(tag[1:j]))
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// indexFold is bytes.Index for a lower-case ASCII needle, ignoring case.
func indexFold(s []byte, needle string) int {
	n := len(needle)
next:
	for i := 0; i+n <= len(s); i++ {
		for j := 0; j < n; j++ {
			if s[i+j]|0x20 != needle[j]|0x20 {
				continue next
			}
		}
		return i
	}
	return -1
}
