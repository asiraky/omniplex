package setup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

var (
	ready   = adapter.Ready(nil)
	missing = adapter.Unavailable("missing")
)

func TestReadyNeedsGitAndOneHarness(t *testing.T) {
	cases := []struct {
		name          string
		git           adapter.Availability
		claude, codex adapter.Availability
		want          bool
	}{
		{"everything", ready, ready, ready, true},
		{"claude only", ready, ready, missing, true},
		{"codex only", ready, missing, ready, true},
		{"no harness", ready, missing, missing, false},
		{"no git", missing, ready, ready, false},
	}
	for _, c := range cases {
		r := Build("darwin", c.git, []Harness{
			{ID: "claude", Name: "Claude Code", Availability: c.claude},
			{ID: "codex", Name: "Codex", Availability: c.codex},
		})
		if r.Ready != c.want {
			t.Errorf("%s: ready=%v, want %v", c.name, r.Ready, c.want)
		}
	}
}

// A second account that works is as good as the default one: a user signed
// out of the default Codex but into another must not be sent back to setup.
func TestAWorkingSecondAccountIsEnough(t *testing.T) {
	r := Build("darwin", ready, []Harness{
		{ID: "claude", Name: "Claude Code", Availability: missing},
		{ID: "codex", Name: "Codex", Availability: missing, OtherInstanceOK: true},
	})
	if !r.Ready {
		t.Fatal("not ready with a working second Codex account")
	}
}

// A harness the screen does not ask about neither shows up as missing nor
// makes the machine count as ready.
func TestOtherHarnessesAreLeftOut(t *testing.T) {
	r := Build("linux", ready, []Harness{
		{ID: "pi", Name: "Pi", Availability: ready},
		{ID: "codex", Name: "Codex", Availability: missing},
	})
	if r.Ready {
		t.Fatal("pi alone made setup ready")
	}
	for _, c := range r.Checks {
		if c.ID == "pi" {
			t.Fatal("pi listed on the setup screen")
		}
	}
	if len(r.Checks) != 2 || r.Checks[0].ID != "git" || r.Checks[1].ID != "codex" {
		t.Fatalf("checks: %+v", r.Checks)
	}
}

func TestGitThatDoesNotRunIsMissing(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "git")
	// The macOS stub without command line tools: present, exits non-zero.
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := gitAt(context.Background(), stub, "darwin")
	if got.OK() || len(got.Remedy) == 0 {
		t.Fatalf("broken git reported %+v", got)
	}

	works := filepath.Join(dir, "git-ok")
	if err := os.WriteFile(works, []byte("#!/bin/sh\necho git version 2.44.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitAt(context.Background(), works, "darwin"); !got.OK() || got.Facts["gitVer"] != "git version 2.44.0" {
		t.Fatalf("working git reported %+v", got)
	}

	if got := gitAt(context.Background(), filepath.Join(dir, "nope"), "windows"); got.OK() {
		t.Fatal("absent git reported ready")
	}
}
