package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// commandConn is a connection that can execute commands against a server
// over an empty database, with the home folder and user config in temp dirs.
func commandConn(t *testing.T) (*conn, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	return &conn{srv: New(Options{Manager: mgr, Store: st})}, home
}

func run(t *testing.T, c *conn, command string, args any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return c.execute(context.Background(), clientFrame{Command: command, Args: raw})
}

func TestSkillSwitchesOverTheWire(t *testing.T) {
	c, home := commandConn(t)
	type listed struct {
		Skills       []skills.Skill `json:"skills"`
		ClaudeSync   bool           `json:"claudeSync"`
		CodexBundled bool           `json:"codexBundled"`
		ProjectRoot  *string        `json:"projectRoot"`
	}
	created := call[skills.Skill](t, c, "create_skill", map[string]any{"name": "mine", "description": "Written here"})
	got := call[listed](t, c, "list_skills", map[string]any{})
	if len(got.Skills) != 1 || got.Skills[0].Mode != skills.ModeOn || !got.ClaudeSync || !got.CodexBundled || got.ProjectRoot != nil {
		t.Fatalf("listed = %+v", got)
	}

	for _, mode := range []string{skills.ModeOff, skills.ModeManual, skills.ModeOn} {
		if s := call[skills.Skill](t, c, "set_skill_mode", map[string]any{"dir": created.Dir, "mode": mode}); s.Mode != mode {
			t.Errorf("set %s, got %s", mode, s.Mode)
		}
		if s := call[listed](t, c, "list_skills", map[string]any{}).Skills[0]; s.Mode != mode {
			t.Errorf("set %s, listed %s", mode, s.Mode)
		}
	}
	if _, err := run(t, c, "set_skill_mode", map[string]any{"dir": created.Dir, "mode": "sometimes"}); err == nil {
		t.Error("set a mode that does not exist")
	}

	if got := call[map[string]bool](t, c, "set_codex_bundled", map[string]any{"on": false}); got["codexBundled"] {
		t.Errorf("set_codex_bundled off = %v", got)
	}
	if call[listed](t, c, "list_skills", map[string]any{}).CodexBundled {
		t.Error("listed as bundled after turning it off")
	}
	if got := call[map[string]bool](t, c, "set_claude_sync", map[string]any{"on": false}); got["claudeSync"] {
		t.Errorf("set_claude_sync off = %v", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); err != nil {
		t.Errorf("the bundled switch was not written to the temp home: %v", err)
	}
}

func TestClaudeBuiltinSwitchesOverTheWire(t *testing.T) {
	c, _ := commandConn(t)
	type listed struct {
		ClaudeBundled  bool                   `json:"claudeBundled"`
		ClaudeBuiltins []skills.ClaudeBuiltin `json:"claudeBuiltins"`
	}
	modeOf := func(l listed, name string) string {
		for _, b := range l.ClaudeBuiltins {
			if b.Name == name {
				return b.Mode
			}
		}
		return ""
	}
	// No probe: the names known without asking the CLI are still listed.
	got := call[listed](t, c, "list_skills", map[string]any{})
	if !got.ClaudeBundled || modeOf(got, "security-review") != skills.ModeOn {
		t.Fatalf("listed = %+v", got)
	}

	if b := call[skills.ClaudeBuiltin](t, c, "set_claude_builtin", map[string]any{"name": "security-review", "on": false}); b.Mode != skills.ModeOff {
		t.Errorf("set_claude_builtin off = %+v", b)
	}
	if got := call[map[string]bool](t, c, "set_claude_bundled", map[string]any{"on": false}); got["claudeBundled"] {
		t.Errorf("set_claude_bundled off = %v", got)
	}
	got = call[listed](t, c, "list_skills", map[string]any{})
	if got.ClaudeBundled || modeOf(got, "keybindings-help") != skills.ModeOff {
		t.Errorf("listed with the group off = %+v", got)
	}
	call[map[string]bool](t, c, "set_claude_bundled", map[string]any{"on": true})
	got = call[listed](t, c, "list_skills", map[string]any{})
	if modeOf(got, "security-review") != skills.ModeOff || modeOf(got, "keybindings-help") != skills.ModeOn {
		t.Errorf("the group came back without each skill's own switch: %+v", got)
	}
}
