package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/skills"
)

type codexSkill struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"shortDescription"`
	Path             string `json:"path"`
	Scope            string `json:"scope"`
	Enabled          bool   `json:"enabled"`
	Interface        *struct {
		DisplayName      string `json:"displayName"`
		ShortDescription string `json:"shortDescription"`
	} `json:"interface"`
}

// ComposerItems asks the same live app-server that owns the thread. Passing
// the session cwd is load-bearing: Codex discovers repo-local .agents/skills
// relative to the requested workspace, not the server process.
func (s *session) ComposerItems(ctx context.Context) ([]adapter.ComposerItem, error) {
	items := codexBuiltinComposerItems()
	var response struct {
		Data []struct {
			Cwd    string       `json:"cwd"`
			Skills []codexSkill `json:"skills"`
		} `json:"data"`
	}
	if err := s.conn.Call(ctx, "skills/list", map[string]any{"cwds": []string{s.cwd}}, &response); err != nil {
		// The explicit commands do not depend on skill discovery. Keep them
		// usable if an older or temporarily unhealthy app-server cannot list
		// skills, and leave the provider failure in the server log.
		s.host.Logf("codex skills/list: %v", err)
		return items, nil
	}

	var skills []codexSkill
	for _, entry := range response.Data {
		if entry.Cwd == s.cwd {
			skills = entry.Skills
			break
		}
	}
	if skills == nil {
		for _, entry := range response.Data {
			skills = append(skills, entry.Skills...)
		}
	}

	for _, skill := range skills {
		if !skill.Enabled || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		name := strings.TrimSpace(skill.Name)
		description := strings.TrimSpace(skill.ShortDescription)
		if skill.Interface != nil && strings.TrimSpace(skill.Interface.ShortDescription) != "" {
			description = strings.TrimSpace(skill.Interface.ShortDescription)
		}
		if description == "" {
			description = strings.TrimSpace(skill.Description)
		}
		items = append(items, codexSkillItem(name, description, codexSkillOrigin(skill.Scope, skill.Path)))
	}
	return items, nil
}

// Codex resolves a $name mention wherever it appears in a prompt, so a skill
// is always inline.
func codexSkillItem(name, description, origin string) adapter.ComposerItem {
	return adapter.ComposerItem{
		ID:          "skill:" + name,
		Name:        name,
		Description: description,
		Kind:        "skill",
		Trigger:     "$",
		InsertText:  "$" + name,
		Origin:      origin,
		Behavior:    adapter.ComposerPrompt,
		Inline:      true,
	}
}

// DraftComposerItems answers for a thread that does not exist yet, from the
// skill directories Codex reads. The slash commands are left out: each is an
// action on a thread, and there is none.
func (a *Adapter) DraftComposerItems(_ context.Context, env map[string]string, cwd string) ([]adapter.ComposerItem, error) {
	home, _ := os.UserHomeDir()
	found, _ := skills.Discover(skills.DefaultRoots(home, env, cwd))
	items := make([]adapter.ComposerItem, 0, len(found))
	seen := make(map[string]bool)
	for _, skill := range found {
		if !codexSees(skill) || skill.Name == "" {
			continue
		}
		name, origin := skill.Name, "other"
		switch skill.Scope {
		case skills.ScopeProject:
			origin = "repo"
		case skills.ScopeUser:
			origin = "personal"
		case skills.ScopePlugin:
			// Codex namespaces a plugin's skills by the plugin.
			name, origin = skill.Plugin+":"+skill.Name, "plugin"
		case skills.ScopeSystem:
			origin = "system"
		}
		// Discover lists the narrowest scope first, and that is the one a
		// name resolves to.
		if seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, codexSkillItem(name, skill.Description, origin))
	}
	return items, nil
}

func codexSees(skill skills.Skill) bool {
	for _, h := range skill.Harnesses {
		if h == skills.Codex {
			return true
		}
	}
	return false
}

// codexBuiltinComposerItems is deliberately small. Codex app-server does not
// publish the TUI slash-command catalogue, so omniplex advertises only commands for
// which it has a real local surface or a real app-server operation.
func codexBuiltinComposerItems() []adapter.ComposerItem {
	return []adapter.ComposerItem{
		{
			ID: "command:status", Name: "status",
			Description: "Show the current model, approvals, and token usage",
			Kind:        "command", Trigger: "/", InsertText: "/status", Origin: "built-in",
			Behavior: adapter.ComposerClientAction, Action: "status",
		},
		{
			ID: "command:diff", Name: "diff",
			Description: "Open the current workspace changes",
			Kind:        "command", Trigger: "/", InsertText: "/diff", Origin: "built-in",
			Behavior: adapter.ComposerClientAction, Action: "diff",
		},
		{
			ID: "command:compact", Name: "compact",
			Description: "Compact the conversation context",
			Kind:        "command", Trigger: "/", InsertText: "/compact", Origin: "built-in",
			Behavior: adapter.ComposerAdapterAction, Action: "compact",
		},
		{
			ID: "command:review", Name: "review",
			Description: "Review the current workspace changes",
			Kind:        "command", Trigger: "/", InsertText: "/review", ArgsHint: "[instructions]", Origin: "built-in",
			Behavior: adapter.ComposerAdapterAction, Action: "review",
		},
	}
}

// RunComposerAction maps the small advertised command set onto app-server.
// The opaque action ids are interpreted only here, inside the Codex adapter.
func (s *session) RunComposerAction(ctx context.Context, in adapter.ComposerActionInput) (any, error) {
	method, params, err := codexComposerActionRequest(s.threadID, in.Action, in.Args)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	// Same collision guard as Prompt: codex may have started work on its own
	// (ensureTurn) between the actor's idle check and this call. Overwriting
	// that turn's id would orphan it — its completion would be dropped and the
	// session would read as working forever.
	if s.turnID != "" && s.turnID != in.TurnID {
		s.mu.Unlock()
		return nil, errors.New("the harness resumed work on its own; wait for it to finish")
	}
	s.turnID = in.TurnID
	s.mu.Unlock()
	if in.Action == "compact" {
		s.mu.Lock()
		s.manualCompact = true
		s.mu.Unlock()
	}

	var response map[string]any
	if err := s.conn.Call(ctx, method, params, &response); err != nil {
		if in.Action == "compact" {
			s.mu.Lock()
			s.manualCompact = false
			s.mu.Unlock()
		}
		s.mu.Lock()
		if s.turnID == in.TurnID {
			s.turnID = ""
		}
		s.mu.Unlock()
		return nil, err
	}
	return response, nil
}

func codexComposerActionRequest(threadID, action, args string) (string, map[string]any, error) {
	switch action {
	case "compact":
		if strings.TrimSpace(args) != "" {
			return "", nil, fmt.Errorf("/%s does not accept arguments", action)
		}
		return "thread/compact/start", map[string]any{"threadId": threadID}, nil
	case "review":
		target := map[string]any{"type": "uncommittedChanges"}
		if instructions := strings.TrimSpace(args); instructions != "" {
			target = map[string]any{"type": "custom", "instructions": instructions}
		}
		return "review/start", map[string]any{
			"threadId": threadID,
			"delivery": "inline",
			"target":   target,
		}, nil
	default:
		return "", nil, fmt.Errorf("unknown Codex composer action %q", action)
	}
}

func codexSkillOrigin(scope, path string) string {
	normalized := filepath.ToSlash(path)
	if strings.Contains(normalized, "/.codex/plugins/") || strings.Contains(normalized, "/.agents/plugins/") {
		return "plugin"
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "user":
		return "personal"
	case "repo":
		return "repo"
	case "system", "admin":
		return "system"
	default:
		return "other"
	}
}
