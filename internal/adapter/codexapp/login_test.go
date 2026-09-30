package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// fakeCodex stands in for the CLI: `login status` prints what it is given and
// exits with the given code, anything else prints a version.
func fakeCodex(t *testing.T, status string, code int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nif [ \"$1\" = login ] && [ \"$2\" = status ]; then echo '" + status + "'; exit " + string(rune('0'+code)) + "; fi\necho codex-cli 0.1.0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeReportsSignedOutCodex(t *testing.T) {
	a := New(fakeCodex(t, "Not logged in", 1))
	got := a.Probe(context.Background(), nil)
	if got.OK() {
		t.Fatalf("signed-out codex reported ready: %+v", got)
	}
	var login bool
	for _, r := range got.Remedy {
		login = login || r.Action == adapter.RemedyLogin
	}
	if !login {
		t.Fatalf("signed-out codex offers no sign-in: %+v", got.Remedy)
	}
}

func TestProbeTrustsSignedInCodex(t *testing.T) {
	if got := New(fakeCodex(t, "Logged in using ChatGPT", 0)).Probe(context.Background(), nil); !got.OK() {
		t.Fatalf("signed-in codex reported unavailable: %+v", got)
	}
}

// An older CLI that fails the command for some other reason must not block
// sessions on a question it cannot answer.
func TestProbeIgnoresAnUnreadableLoginStatus(t *testing.T) {
	if got := New(fakeCodex(t, "error: unrecognized subcommand", 2)).Probe(context.Background(), nil); !got.OK() {
		t.Fatalf("unreadable status treated as signed out: %+v", got)
	}
}
