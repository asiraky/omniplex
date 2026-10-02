package server

import "testing"

func TestRootURLsPointAtTheSiteRoot(t *testing.T) {
	const root = "/p/tok/dist/"
	cases := []struct{ name, in, want string }{
		{"quoted attributes", `<link rel=stylesheet href="/a.css"><script src='/b.js'></script>`,
			`<link rel=stylesheet href="/p/tok/dist/a.css"><script src='/p/tok/dist/b.js'></script>`},
		{"unquoted, upper case", `<IMG SRC=/x.png><a HREF = /about/>`, `<IMG SRC=/p/tok/dist/x.png><a HREF = /p/tok/dist/about/>`},
		{"the bare root", `<a href="/">home</a>`, `<a href="/p/tok/dist/">home</a>`},
		{"other sites and relative paths", `<script src="//cdn.x/y.js"></script><a href="https://x/a">x</a><img src="a.png"><a href="#top">t</a>`,
			`<script src="//cdn.x/y.js"></script><a href="https://x/a">x</a><img src="a.png"><a href="#top">t</a>`},
		{"forms, video, objects", `<form action="/go"><button formaction="/b"></button></form><video poster="/p.jpg"></video><object data="/d.svg"></object>`,
			`<form action="/p/tok/dist/go"><button formaction="/p/tok/dist/b"></button></form><video poster="/p/tok/dist/p.jpg"></video><object data="/p/tok/dist/d.svg"></object>`},
		{"srcset", `<img srcset="/a.png 1x, /b.png 2x, https://c/d.png 3x">`,
			`<img srcset="/p/tok/dist/a.png 1x, /p/tok/dist/b.png 2x, https://c/d.png 3x">`},
		{"style element", `<STYLE>@import "/base.css"; body{background:url(/bg.png)}</style>`,
			`<STYLE>@import "/p/tok/dist/base.css"; body{background:url(/p/tok/dist/bg.png)}</style>`},
		{"style attribute", `<div style="background-image:url('/h.jpg')">`, `<div style="background-image:url('/p/tok/dist/h.jpg')">`},
		{"text the page shows", `<p>use url(/bg.svg) or <code> href="/x"</code></p><title>src="/t"</title><textarea> href="/y"</textarea>`,
			`<p>use url(/bg.svg) or <code> href="/x"</code></p><title>src="/t"</title><textarea> href="/y"</textarea>`},
		{"script and comments", `<script>const a = '<img src="/x">'; x.style = "url(/y)"</script><!-- <a href="/z"> -->`,
			`<script>const a = '<img src="/x">'; x.style = "url(/y)"</script><!-- <a href="/z"> -->`},
		{"a > inside a quoted value", `<a title="a > b" href="/x">`, `<a title="a > b" href="/p/tok/dist/x">`},
		{"an apostrophe in an unquoted value", `<a title=don't href="/x">t</a><a href="/y">`,
			`<a title=don't href="/p/tok/dist/x">t</a><a href="/p/tok/dist/y">`},
		{"a lone <", `<p>1 < 2</p><a href="/x">`, `<p>1 < 2</p><a href="/p/tok/dist/x">`},
	}
	for _, c := range cases {
		if got := string(rootURLsHTML([]byte(c.in), root)); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	css := `@import '/base.css'; @font-face{src:url( "/f.woff2" )} a{background:url(//cdn/x.png)} b{background:url(c.png)}`
	want := `@import '/p/tok/dist/base.css'; @font-face{src:url( "/p/tok/dist/f.woff2" )} a{background:url(//cdn/x.png)} b{background:url(c.png)}`
	if got := string(rootURLsCSS([]byte(css), root)); got != want {
		t.Errorf("stylesheet:\n got %s\nwant %s", got, want)
	}
	// A $ in the root is text, not a reference to a group.
	if got := string(rootURLsHTML([]byte(`<a href="/x">`), "/p/a$1/")); got != `<a href="/p/a$1/x">` {
		t.Errorf("root with $: %s", got)
	}
}
