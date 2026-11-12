package claudecode

import (
	"strings"
	"testing"
)

// A server started from inside a Claude Code session carries that session's
// CLAUDECODE and CLAUDE_* variables; Claude threads must not inherit them,
// its config dir included (only the instance's overlay picks the account's
// home), but keep a server-wide OAuth token and anything the instance sets.
func TestClaudeEnv(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-ambient",
		"MY_CLAUDE_X=kept",
		"CLAUDECODEX=kept",
		"CLAUDECODE=1",
		"CLAUDE_EFFORT=max",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT=1",
		"CLAUDE_CODE_SESSION_ID=parent-session",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/parent.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=parent-token",
		"CLAUDE_CONFIG_DIR=/home/ambient/.claude",
		"CLAUDE_CODE_OAUTH_TOKEN=ambient-oauth",
	}
	dropped := []string{
		"CLAUDECODE",
		"CLAUDE_CODE_DISABLE_1M_CONTEXT",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
	}
	unset := append([]string{"CLAUDE_EFFORT", "CLAUDE_CONFIG_DIR"}, dropped...)

	for _, tc := range []struct {
		name    string
		overlay map[string]string
		want    map[string]string
		absent  []string
	}{
		{
			name: "nil overlay",
			want: map[string]string{
				"PATH":                    "/usr/bin",
				"ANTHROPIC_API_KEY":       "sk-ambient",
				"MY_CLAUDE_X":             "kept",
				"CLAUDECODEX":             "kept",
				"CLAUDE_CODE_OAUTH_TOKEN": "ambient-oauth",
			},
			absent: unset,
		},
		{
			name:    "empty overlay",
			overlay: map[string]string{},
			want: map[string]string{
				"PATH": "/usr/bin",
			},
			absent: unset,
		},
		{
			// The instance's own CLAUDE_* settings are applied after the
			// filter, so they survive it and replace the ambient value.
			name: "overlay wins",
			overlay: map[string]string{
				"CLAUDE_EFFORT":     "low",
				"CLAUDE_CONFIG_DIR": "/home/instance/.claude",
				"ANTHROPIC_API_KEY": "sk-instance",
				"OMNIPLEX_OVERLAY":  "yes",
			},
			want: map[string]string{
				"PATH":                    "/usr/bin",
				"CLAUDE_EFFORT":           "low",
				"CLAUDE_CONFIG_DIR":       "/home/instance/.claude",
				"ANTHROPIC_API_KEY":       "sk-instance",
				"OMNIPLEX_OVERLAY":        "yes",
				"CLAUDE_CODE_OAUTH_TOKEN": "ambient-oauth",
			},
			absent: dropped,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			for _, kv := range claudeEnv(base, tc.overlay) {
				k, v, _ := strings.Cut(kv, "=")
				if prev, dup := got[k]; dup {
					t.Errorf("%s appears twice: %q and %q", k, prev, v)
				}
				got[k] = v
			}
			for k, want := range tc.want {
				if v, ok := got[k]; !ok || v != want {
					t.Errorf("%s = %q (present %v), want %q", k, v, ok, want)
				}
			}
			for _, k := range tc.absent {
				if v, ok := got[k]; ok {
					t.Errorf("%s=%q reached Claude Code from the parent session", k, v)
				}
			}
		})
	}
}
