package artefact

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tarOf(t *testing.T, files map[string]string, extra ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	return &buf
}

func TestStageFileCommitsAndOpens(t *testing.T) {
	s := New(t.TempDir())
	st, err := s.StageFile("sess-1", "../../Report (final).md", strings.NewReader("# hi"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Entry != "Report (final).md" || st.Files != 1 || st.Size != 4 || !strings.HasPrefix(st.MediaType, "text/markdown") {
		t.Fatalf("meta = %+v", st.Meta)
	}
	if err := st.Commit("art-1", 1); err != nil {
		t.Fatal(err)
	}
	p, err := s.Open("sess-1", "art-1", 1, "Report (final).md")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "# hi" {
		t.Fatalf("read %q", b)
	}
	if v, err := s.Latest("sess-1", "art-1"); err != nil || v != 1 {
		t.Fatalf("latest = %d, %v", v, err)
	}
}

func TestStageRejectsEmptyAndOversize(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.StageFile("s", "a.txt", strings.NewReader("")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := s.StageTar("s", tarOf(t, nil)); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty tar: %v", err)
	}
	// Nothing staged survives a failure.
	entries, _ := os.ReadDir(filepath.Join(s.Dir(), "s"))
	if len(entries) != 0 {
		t.Fatalf("leftover staging: %v", entries)
	}
}

func TestStageTarBundlePicksEntryAndSkipsLinks(t *testing.T) {
	s := New(t.TempDir())
	buf := tarOf(t, map[string]string{
		"proto/assets/app.js": "x",
		"proto/about.html":    "<p>",
		"proto/index.html":    "<h1>",
		"notes.md":            "n",
	}, &tar.Header{Name: "evil", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	st, err := s.StageTar("s", buf)
	if err != nil {
		t.Fatal(err)
	}
	if st.Entry != "proto/index.html" || st.Files != 4 || !strings.HasPrefix(st.MediaType, "text/html") {
		t.Fatalf("meta = %+v", st.Meta)
	}
	st.Commit("a", 2)
	if _, err := s.Open("s", "a", 2, "evil"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("symlink was stored: %v", err)
	}
	if _, err := s.Open("s", "a", 2, "proto/assets/app.js"); err != nil {
		t.Fatal(err)
	}
}

func TestStageTarRefusesEscapes(t *testing.T) {
	s := New(t.TempDir())
	for _, name := range []string{"../x", "/abs", "a/../../x"} {
		if _, err := s.StageTar("s", tarOf(t, map[string]string{name: "x"})); !errors.Is(err, ErrBadPath) {
			t.Fatalf("%q: %v", name, err)
		}
	}
}

func TestOpenRefusesTraversalAndBadIDs(t *testing.T) {
	s := New(t.TempDir())
	st, _ := s.StageFile("s", "a.txt", strings.NewReader("x"))
	st.Commit("a", 1)
	for _, rel := range []string{"../../../etc/passwd", "", "..", "/a.txt"} {
		if _, err := s.Open("s", "a", 1, rel); err == nil {
			t.Fatalf("%q opened", rel)
		}
	}
	if _, err := s.Open("../s", "a", 1, "a.txt"); !errors.Is(err, ErrBadPath) {
		t.Fatalf("bad session id: %v", err)
	}
	if _, err := s.Open("s", "a", 0, "a.txt"); !errors.Is(err, ErrBadPath) {
		t.Fatalf("version 0: %v", err)
	}
}

func TestLatestFollowsHighestVersion(t *testing.T) {
	s := New(t.TempDir())
	for _, v := range []int{1, 3, 2} {
		st, _ := s.StageFile("s", "a.txt", strings.NewReader("x"))
		if err := st.Commit("a", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := s.Latest("s", "a"); v != 3 {
		t.Fatalf("latest = %d", v)
	}
	if _, err := s.Latest("s", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestPickEntry(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"only.bin"}, "only.bin"},
		{[]string{"a/index.html", "index.html"}, "index.html"},
		{[]string{"b.css", "page.html"}, "page.html"},
		{[]string{"docs/x.md", "README.md", "z.txt"}, "README.md"},
		{[]string{"b.bin", "c.bin"}, "b.bin"},
	}
	for _, c := range cases {
		if got := pickEntry(c.files); got != c.want {
			t.Errorf("pickEntry(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

func TestTokensRoundTripAndRefuseTampering(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	tok := s.Mint(Claims{Kind: KindShare, Session: "s", Artefact: "a", Version: 0, ExpiresAt: 2_000_000})
	c, err := s.Check(tok, KindShare, now)
	if err != nil || c.Session != "s" || c.Artefact != "a" || c.Version != 0 {
		t.Fatalf("claims = %+v, %v", c, err)
	}
	if _, err := s.Check(tok, KindPreview, now); err == nil {
		t.Fatal("share token accepted as preview")
	}
	if _, err := s.Check(tok, KindShare, time.UnixMilli(2_000_001)); err == nil {
		t.Fatal("expired token accepted")
	}
	other := NewSigner([]byte("fedcba9876543210fedcba9876543210"))
	if _, err := other.Check(tok, KindShare, now); err == nil {
		t.Fatal("token accepted under another key")
	}
	forged := s.Mint(Claims{Kind: KindShare, Session: "t", Artefact: "a", ExpiresAt: 2_000_000})
	enc, _, _ := strings.Cut(forged, ".")
	_, sig, _ := strings.Cut(tok, ".")
	if _, err := s.Check(enc+"."+sig, KindShare, now); err == nil {
		t.Fatal("payload swap accepted")
	}
	never := s.Mint(Claims{Kind: KindAgent, Session: "s"})
	if _, err := s.Check(never, KindAgent, time.UnixMilli(1<<50)); err != nil {
		t.Fatalf("non-expiring token: %v", err)
	}
}

func TestLoadSignerPersistsKey(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadSigner(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := LoadSigner(dir)
	tok := a.Mint(Claims{Kind: KindAgent, Session: "s"})
	if _, err := b.Check(tok, KindAgent, time.Now()); err != nil {
		t.Fatalf("reloaded key rejected token: %v", err)
	}
}
