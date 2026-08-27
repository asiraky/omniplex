package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeClaude writes an executable that answers `auth status` with the given
// JSON, standing in for the CLI — exiting 1 as the real one does when signed
// out, so the answer must be read regardless of the exit code.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nif [ \"$1\" = auth ] && [ \"$2\" = status ]; then printf '%s' '" + body + "'; exit 1; fi\necho 1.0.0\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Whether Claude is signed in cannot be read off the disk: the credential may
// be a file, a keychain entry, or a token in one instance's env. The CLI is
// the only authority, and its answer is what the probe reports.
func TestAuthStatusReadsTheHarnessAnswer(t *testing.T) {
	ctx := context.Background()

	in := authStatus(ctx, fakeClaude(t, `{"loggedIn":true,"authMethod":"claude.ai","email":"a@b.c","subscriptionType":"max"}`), nil)
	if !in.known || !in.loggedIn || in.email != "a@b.c" || in.subscription != "max" {
		t.Fatalf("signed-in answer misread: %+v", in)
	}

	out := authStatus(ctx, fakeClaude(t, `{"loggedIn":false,"authMethod":"none"}`), nil)
	if !out.known || out.loggedIn {
		t.Fatalf("signed-out answer misread: %+v", out)
	}

	// An older CLI without the command says nothing useful; nothing is
	// claimed, so the session is still allowed to try.
	old := authStatus(ctx, fakeClaude(t, `Unknown command`), nil)
	if old.known {
		t.Fatalf("an unreadable answer was treated as known: %+v", old)
	}
}
