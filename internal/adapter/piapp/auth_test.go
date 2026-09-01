package piapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// fakeBridge writes a shell script standing in for `node authbridge.mjs
// <pkgRoot>`. With bridgeOverride the adapter appends the command and its
// arguments directly, so $1 is the command. Everything it reads from stdin is
// recorded under dir so tests can assert what crossed the pipe.
func fakeBridge(t *testing.T, dir, body string) *Adapter {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nDIR=%q\nprintf '%%s\\n' \"$@\" > \"$DIR/bridge_args\"\nprintf '%%s' \"$PI_TEST_MARKER\" > \"$DIR/bridge_env\"\n%s\n", dir, body)
	path := filepath.Join(dir, "bridge")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New("pi")
	a.bridgeOverride = []string{path}
	return a
}

// recordingInteraction collects notify events and answers every prompt with a
// fixed value.
type recordingInteraction struct {
	events  []adapter.AuthEvent
	prompts []adapter.AuthPrompt
	answer  string
}

func (r *recordingInteraction) Notify(ev adapter.AuthEvent) { r.events = append(r.events, ev) }
func (r *recordingInteraction) Prompt(ctx context.Context, p adapter.AuthPrompt) (string, error) {
	r.prompts = append(r.prompts, p)
	return r.answer, nil
}

func TestAuthMethods(t *testing.T) {
	dir := t.TempDir()
	a := fakeBridge(t, dir, `printf '%s\n' '{"type":"result","data":{"methods":[{"provider":"openrouter","type":"api_key","label":"OpenRouter API key"},{"provider":"anthropic","type":"oauth","label":"Anthropic (Claude Pro/Max)","loginLabel":"Sign in with Claude","subscription":true}]}}'`)

	got, err := a.AuthMethods(context.Background(), map[string]string{"PI_TEST_MARKER": "overlay"})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("methods = %+v", got)
	}
	if got[0].ID != "openrouter:api_key" || got[0].Kind != adapter.AuthKindSecret {
		t.Errorf("api_key method wrong: %+v", got[0])
	}
	if got[1].ID != "anthropic:oauth" || got[1].Kind != adapter.AuthKindOAuth || !got[1].Subscription || got[1].Description != "Sign in with Claude" {
		t.Errorf("oauth method wrong: %+v", got[1])
	}

	args, _ := os.ReadFile(filepath.Join(dir, "bridge_args"))
	if strings.TrimSpace(string(args)) != "methods" {
		t.Errorf("bridge argv wrong: %q", args)
	}
	if env, _ := os.ReadFile(filepath.Join(dir, "bridge_env")); string(env) != "overlay" {
		t.Errorf("env overlay was not applied to the bridge; got %q", env)
	}
}

func TestAuthStatuses(t *testing.T) {
	dir := t.TempDir()
	a := fakeBridge(t, dir, `printf '%s\n' '{"type":"result","data":{"statuses":[{"provider":"anthropic","connected":true,"type":"oauth","source":"OAuth","stored":true},{"provider":"openrouter","connected":true,"type":"api_key","source":"OPENROUTER_API_KEY","stored":false},{"provider":"google","connected":false}]}}'`)

	got, err := a.AuthStatuses(context.Background(), nil)
	if err != nil {
		t.Fatalf("AuthStatuses: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("statuses = %+v", got)
	}
	if got[0].MethodID != "anthropic:oauth" || got[0].State != adapter.AuthConnected || got[0].Source != adapter.SourceNative {
		t.Errorf("native status wrong: %+v", got[0])
	}
	if got[1].MethodID != "openrouter:api_key" || got[1].Source != adapter.SourceEnvironment || got[1].Detail != "OPENROUTER_API_KEY" {
		t.Errorf("environment status wrong: %+v", got[1])
	}
}

func TestBeginAuthFlow(t *testing.T) {
	dir := t.TempDir()
	// The flow narrates an auth URL, asks one secret question, and succeeds
	// only if the answer makes it back over stdin.
	a := fakeBridge(t, dir, `
printf '%s\n' '{"type":"notify","event":{"type":"auth_url","url":"https://example.test/auth","instructions":"Open this"}}'
printf '%s\n' '{"type":"notify","event":{"type":"device_code","userCode":"ABCD-1234","verificationUri":"https://example.test/device"}}'
printf '%s\n' '{"type":"prompt","id":1,"prompt":{"type":"secret","message":"Paste your API key","placeholder":"sk-or-..."}}'
IFS= read -r answer
printf '%s\n' "$answer" > "$DIR/answer"
case "$answer" in
  *'"value":"sk-test-123"'*) printf '%s\n' '{"type":"result","data":{}}' ;;
  *) printf '%s\n' '{"type":"error","message":"wrong answer"}' ;;
esac`)

	ia := &recordingInteraction{answer: "sk-test-123"}
	if err := a.BeginAuth(context.Background(), nil, "openrouter:api_key", ia); err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	if len(ia.events) != 2 || ia.events[0].Type != "auth_url" || ia.events[0].URL != "https://example.test/auth" {
		t.Errorf("events wrong: %+v", ia.events)
	}
	if ia.events[1].UserCode != "ABCD-1234" || ia.events[1].VerificationURI != "https://example.test/device" {
		t.Errorf("device code wrong: %+v", ia.events[1])
	}
	if len(ia.prompts) != 1 || !ia.prompts[0].Secret || ia.prompts[0].Placeholder != "sk-or-..." {
		t.Errorf("prompt wrong: %+v", ia.prompts)
	}

	args, _ := os.ReadFile(filepath.Join(dir, "bridge_args"))
	if strings.TrimSpace(string(args)) != "login\nopenrouter\napi_key" {
		t.Errorf("bridge argv wrong: %q", args)
	}
}

func TestBeginAuthFailure(t *testing.T) {
	dir := t.TempDir()
	a := fakeBridge(t, dir, `printf '%s\n' '{"type":"error","message":"login cancelled"}'`)
	err := a.BeginAuth(context.Background(), nil, "anthropic:oauth", &recordingInteraction{})
	if err == nil || !strings.Contains(err.Error(), "login cancelled") {
		t.Fatalf("expected the bridge error to surface, got %v", err)
	}
	if err := a.BeginAuth(context.Background(), nil, "not-a-method", &recordingInteraction{}); err == nil {
		t.Error("a malformed method id must be rejected before spawning anything")
	}
}

func TestLogout(t *testing.T) {
	dir := t.TempDir()
	a := fakeBridge(t, dir, `printf '%s\n' '{"type":"result","data":{}}'`)
	if err := a.Logout(context.Background(), nil, "anthropic:oauth"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "bridge_args"))
	if strings.TrimSpace(string(args)) != "logout\nanthropic" {
		t.Errorf("bridge argv wrong: %q", args)
	}
}

func TestBridgeExitWithoutResult(t *testing.T) {
	dir := t.TempDir()
	a := fakeBridge(t, dir, `exit 0`)
	if _, err := a.AuthMethods(context.Background(), nil); err == nil {
		t.Fatal("a bridge that dies silently must be an error")
	}
}

// The adapter must satisfy the optional capabilities the settings UI drives.
var (
	_ adapter.AuthFlows  = (*Adapter)(nil)
	_ adapter.Configurer = (*Adapter)(nil)
)
