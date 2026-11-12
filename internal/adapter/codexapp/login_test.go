package codexapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// fakeIDToken is an unsigned JWT carrying claims, the shape of auth.json's
// tokens.id_token.
func fakeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

func writeAuth(t *testing.T, home string, auth map[string]any) {
	t.Helper()
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The probe says who is signed in to the account's own CODEX_HOME, read from
// the login's ID token, and never passes a token on.
func TestProbeReportsTheSignedInEmail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	account := filepath.Join(home, ".omniplex", "instances", "work")
	idToken := fakeIDToken(t, map[string]any{"email": "me@work.example", "sub": "user-1"})
	writeAuth(t, account, map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token": idToken, "access_token": "secret-access", "refresh_token": "secret-refresh", "account_id": "acct-1",
		},
	})
	// A different login in the machine's own ~/.codex, and in a CODEX_HOME
	// the server inherited, must not show.
	writeAuth(t, filepath.Join(home, ".codex"), map[string]any{"tokens": map[string]any{"id_token": fakeIDToken(t, map[string]any{"email": "terminal@example"})}})
	ambient := t.TempDir()
	t.Setenv("CODEX_HOME", ambient)
	writeAuth(t, ambient, map[string]any{"tokens": map[string]any{"id_token": fakeIDToken(t, map[string]any{"email": "ambient@example"})}})

	got := New(fakeCodex(t, "Logged in using ChatGPT", 0)).Probe(context.Background(), map[string]string{"CODEX_HOME": account})
	if !got.OK() || got.Facts["account"] != "me@work.example" {
		t.Fatalf("probe = %+v, want the work account's email", got)
	}
	for k, v := range got.Facts {
		if strings.Contains(v, "secret") || strings.Contains(v, idToken) {
			t.Errorf("fact %s carries a token", k)
		}
	}

	if got := New(fakeCodex(t, "Logged in using ChatGPT", 0)).Probe(context.Background(), nil); got.Facts["account"] != "terminal@example" {
		t.Errorf("no CODEX_HOME = %q, want ~/.codex's login", got.Facts["account"])
	}
}

// An API-key login, a malformed token, or no auth.json at all names nobody.
func TestNoEmailWithoutAReadableIDToken(t *testing.T) {
	for name, auth := range map[string]map[string]any{
		"api key":    {"auth_mode": "apikey", "OPENAI_API_KEY": "sk-x"},
		"not a jwt":  {"tokens": map[string]any{"id_token": "garbage"}},
		"bad base64": {"tokens": map[string]any{"id_token": "a.!!!.c"}},
		"no email":   {"tokens": map[string]any{"id_token": fakeIDToken(t, map[string]any{"sub": "u"})}},
	} {
		home := t.TempDir()
		writeAuth(t, home, auth)
		if got := signedInEmail(map[string]string{"CODEX_HOME": home}); got != "" {
			t.Errorf("%s: email = %q", name, got)
		}
	}
	if got := signedInEmail(map[string]string{"CODEX_HOME": t.TempDir()}); got != "" {
		t.Errorf("no auth.json: email = %q", got)
	}
}
