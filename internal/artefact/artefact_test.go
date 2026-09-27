package artefact

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tree writes files under root, creating folders as needed.
func tree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDescribeFile(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"Report (final).md": "# hi"})
	info, err := Describe(filepath.Join(dir, "Report (final).md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Dir || info.Entry != "Report (final).md" || info.Files != 1 || info.Size != 4 || !strings.HasPrefix(info.MediaType, "text/markdown") || info.ModifiedAt == 0 {
		t.Fatalf("info = %+v", info)
	}
}

func TestDescribeFolderLeavesOutHiddenFilesDependenciesAndLinks(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"proto/index.html":              "<h1>",
		"proto/assets/app.js":           "x",
		"proto/.env":                    "SECRET=1",
		"proto/.git/config":             "c",
		"proto/node_modules/x/index.js": "y",
	})
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "proto", "passwd")); err != nil {
		t.Fatal(err)
	}
	info, err := Describe(filepath.Join(dir, "proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Dir || info.Entry != "index.html" || info.Files != 2 || info.Size != 5 || !strings.HasPrefix(info.MediaType, "text/html") {
		t.Fatalf("info = %+v", info)
	}
}

func TestDescribeRefusesEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"empty.txt": "", "only-hidden/.env": "x"})
	for name, want := range map[string]error{"empty.txt": ErrEmpty, "only-hidden": ErrEmpty, "missing": ErrNotFound} {
		if _, err := Describe(filepath.Join(dir, name)); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
	}
}

func TestResolveServesOnlyWhatIsInsideTheArtefact(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{
		"proto/index.html": "<h1>",
		"proto/.env":       "SECRET=1",
		"secret.txt":       "s",
		"report.md":        "r",
	})
	proto := filepath.Join(dir, "proto")
	os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(proto, "escape.txt"))
	os.Symlink(filepath.Join(proto, "index.html"), filepath.Join(proto, "alias.html"))

	if p, err := Resolve(proto, true, "index.html"); err != nil || filepath.Base(p) != "index.html" {
		t.Fatalf("index: %q %v", p, err)
	}
	if _, err := Resolve(proto, true, "alias.html"); err != nil {
		t.Fatalf("a link that stays inside should resolve: %v", err)
	}
	for _, rel := range []string{"../secret.txt", "escape.txt", ".env", "", "/index.html", "missing.html"} {
		if _, err := Resolve(proto, true, rel); err == nil {
			t.Errorf("%q resolved", rel)
		}
	}
	// A single file serves itself and nothing beside it.
	report := filepath.Join(dir, "report.md")
	if _, err := Resolve(report, false, "report.md"); err != nil {
		t.Fatalf("file: %v", err)
	}
	if _, err := Resolve(report, false, "secret.txt"); err == nil {
		t.Fatal("a file artefact served its neighbour")
	}
}

func TestSnapshotIsACopyThatUpdatesBehindTheSameLink(t *testing.T) {
	s := New(t.TempDir())
	src := filepath.Join(t.TempDir(), "proto")
	tree(t, src, map[string]string{"index.html": "one", "old.css": "o", ".env": "SECRET=1"})
	now := time.UnixMilli(1_000_000)

	first, err := s.Snapshot("s", "a", src, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Entry != "index.html" || first.ExpiresAt != now.Add(ShareTTL).UnixMilli() {
		t.Fatalf("share = %+v", first)
	}
	// Editing the source after sharing does not reach the link.
	tree(t, src, map[string]string{"index.html": "two"})
	os.Remove(filepath.Join(src, "old.css"))
	if b := readShared(t, s, "index.html"); b != "one" {
		t.Fatalf("shared copy followed the edit: %q", b)
	}
	if _, err := s.OpenShared("s", "a", ".env"); err == nil {
		t.Fatal("hidden file was shared")
	}

	later := now.Add(time.Hour)
	second, err := s.Snapshot("s", "a", src, later)
	if err != nil {
		t.Fatal(err)
	}
	if second.Nonce != first.Nonce || second.ExpiresAt != later.Add(ShareTTL).UnixMilli() {
		t.Fatalf("update changed the link or kept the old expiry: %+v then %+v", first, second)
	}
	if b := readShared(t, s, "index.html"); b != "two" {
		t.Fatalf("update not copied: %q", b)
	}
	if _, err := s.OpenShared("s", "a", "old.css"); err == nil {
		t.Fatal("a file deleted before the update is still shared")
	}

	// Stopping forgets the link: sharing again makes a new one.
	if err := s.Unshare("s", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Share("s", "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("share after stop: %v", err)
	}
	third, _ := s.Snapshot("s", "a", src, later)
	if third.Nonce == first.Nonce {
		t.Fatal("sharing again revived the stopped link")
	}
}

func readShared(t *testing.T, s *Store, rel string) string {
	t.Helper()
	p, err := s.OpenShared("s", "a", rel)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestShareRefusesBadIDs(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Snapshot("../s", "a", t.TempDir(), time.Now()); !errors.Is(err, ErrBadPath) {
		t.Fatalf("bad session id: %v", err)
	}
	if err := s.PurgeSession(".."); !errors.Is(err, ErrBadPath) {
		t.Fatalf("purge ..: %v", err)
	}
}

func TestSaveUploadNumbersTakenNamesAndNeverHidesAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	a, err := SaveUpload(dir, "../../brief.pdf", strings.NewReader("a"))
	if err != nil || a != filepath.Join(dir, "brief.pdf") {
		t.Fatalf("first: %q %v", a, err)
	}
	b, _ := SaveUpload(dir, "brief.pdf", strings.NewReader("b"))
	if b != filepath.Join(dir, "brief (2).pdf") {
		t.Fatalf("second: %q", b)
	}
	if got, _ := os.ReadFile(a); string(got) != "a" {
		t.Fatalf("first overwritten: %q", got)
	}
	c, _ := SaveUpload(dir, ".bashrc", strings.NewReader("c"))
	if filepath.Base(c) != "bashrc" {
		t.Fatalf("hidden upload: %q", c)
	}
	if _, err := SaveUpload(dir, "empty.txt", strings.NewReader("")); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "empty.txt")); err == nil {
		t.Fatal("empty upload left a file")
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
	tok := s.Mint(Claims{Kind: KindShare, Session: "s", Artefact: "a", Nonce: "n1", ExpiresAt: 2_000_000})
	c, err := s.Check(tok, KindShare, now)
	if err != nil || c.Session != "s" || c.Artefact != "a" || c.Nonce != "n1" {
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
