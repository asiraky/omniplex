package codexapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

type sentCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func sentCalls(t *testing.T, dir string) []sentCall {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "lines"))
	if err != nil {
		t.Fatal(err)
	}
	var out []sentCall
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c sentCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		out = append(out, c)
	}
	return out
}

func methods(calls []sentCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Method)
	}
	return out
}

// A restart resumes the thread and then hands it the new instructions as a
// developer message, before the session is returned for a turn; a plain resume
// does not.
func TestRestartInjectsTheNewInstructionsAfterTheResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	for _, tc := range []struct {
		name         string
		restart      bool
		instructions string
	}{
		{"restart", true, "codeword FIG"},
		{"restart onto no instructions", true, ""},
		{"plain resume", false, "codeword FIG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := New(fakeAppServer(t, dir)).CreateSession(context.Background(), &elicitHost{}, adapter.CreateOptions{
				Cwd:              dir,
				Instructions:     tc.instructions,
				Resume:           true,
				Restart:          tc.restart,
				HarnessSessionID: "th-1",
			})
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			_ = s.Close()

			calls := sentCalls(t, dir)
			resume, inject := slices.Index(methods(calls), "thread/resume"), slices.Index(methods(calls), "thread/inject_items")
			if !tc.restart {
				if inject >= 0 {
					t.Fatalf("a plain resume injected items: %v", methods(calls))
				}
				return
			}
			if resume < 0 || inject < resume {
				t.Fatalf("instructions not injected after the resume: %v", methods(calls))
			}
			var p struct {
				ThreadID string `json:"threadId"`
				Items    []struct {
					Type    string `json:"type"`
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"items"`
			}
			if err := json.Unmarshal(calls[inject].Params, &p); err != nil {
				t.Fatal(err)
			}
			if p.ThreadID != "th-1" || len(p.Items) != 1 || p.Items[0].Role != "developer" || len(p.Items[0].Content) != 1 {
				t.Fatalf("inject_items params = %s", calls[inject].Params)
			}
			if text := p.Items[0].Content[0].Text; !strings.HasSuffix(text, tc.instructions) || text == tc.instructions {
				t.Fatalf("injected text = %q, want a note ending in %q", text, tc.instructions)
			}
		})
	}
}

// A restart that cannot resume the thread, or cannot hand it the new
// instructions, fails rather than carrying on: on a fresh thread, or on the
// old instructions.
func TestRestartFailsRatherThanLosingTheThreadOrItsInstructions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	for _, refuse := range []string{"thread/resume", "thread/inject_items"} {
		t.Run(refuse, func(t *testing.T) {
			dir := t.TempDir()
			s, err := New(fakeAppServer(t, dir)).CreateSession(context.Background(), &elicitHost{}, adapter.CreateOptions{
				Cwd:              dir,
				Env:              map[string]string{"REFUSE": refuse},
				Instructions:     "codeword FIG",
				Resume:           true,
				Restart:          true,
				HarnessSessionID: "th-1",
			})
			if err == nil {
				_ = s.Close()
				t.Fatal("restart succeeded")
			}
			if slices.Contains(methods(sentCalls(t, dir)), "thread/start") {
				t.Fatalf("restart fell back to a fresh thread: %v", methods(sentCalls(t, dir)))
			}
		})
	}
}
