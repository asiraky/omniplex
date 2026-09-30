package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/skills"
)

type claudeCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Aliases      []string `json:"aliases"`
}

func (s *session) ComposerItems(ctx context.Context) ([]adapter.ComposerItem, error) {
	var response struct {
		Commands []claudeCommand `json:"commands"`
	}
	if err := s.conn.Call(ctx, "supportedCommands", map[string]any{}, &response); err != nil {
		return nil, err
	}
	disk := claudeDiskSkills(s.configDir, s.cwd)
	items := claudeComposerItems(response.Commands, disk)
	s.commands.remember(s.configDir, items, disk)
	return items, nil
}

// DraftComposerItems answers for a thread that does not exist yet. The skills
// are read from disk, where cwd and the instance's config dir put them. What
// the disk cannot say — Claude's own commands, and which skills it declines to
// list — is what the last live session under the same config dir reported, so
// until one has been asked the built-ins are missing and every skill shows.
func (a *Adapter) DraftComposerItems(_ context.Context, env map[string]string, cwd string) ([]adapter.ComposerItem, error) {
	configDir := claudeConfigDir(cwd, env)
	disk := claudeDiskSkills(configDir, cwd)
	known := a.commands.recall(configDir)
	items := make([]adapter.ComposerItem, 0, len(disk)+len(known.extra))
	for key, skill := range disk {
		if known.hidden[key] {
			continue
		}
		items = append(items, claudeComposerItem(
			claudeCommand{Name: skill.name, Description: skill.description},
			"skill", skill.origin, !skill.userOnly,
		))
	}
	for _, item := range known.extra {
		if _, onDisk := disk[strings.ToLower(item.Name)]; !onDisk {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// claudeComposerItems maps the SDK's command list, which is authoritative
// about what exists, onto composer entries. The disk says what the SDK does
// not: which of them are skills, where those came from, and whether the model
// may invoke them itself.
func claudeComposerItems(commands []claudeCommand, disk map[string]claudeSkill) []adapter.ComposerItem {
	items := make([]adapter.ComposerItem, 0, len(commands))
	seen := make(map[string]bool)
	for _, command := range commands {
		command.Name = strings.TrimSpace(command.Name)
		key := strings.ToLower(command.Name)
		if command.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		kind, origin, inline := "command", "built-in", false
		if skill, ok := disk[key]; ok {
			kind, origin, inline = "skill", skill.origin, !skill.userOnly
		} else if inferred, ok := claudeDescriptionOrigin(command.Description); ok {
			kind, origin = "skill", inferred
		}
		items = append(items, claudeComposerItem(command, kind, origin, inline))
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// Claude expands /name only as the first thing in a prompt. Anywhere else the
// text reaches the model as typed, and the model can act on it only for a
// skill it is allowed to invoke — which is what inline records.
func claudeComposerItem(command claudeCommand, kind, origin string, inline bool) adapter.ComposerItem {
	return adapter.ComposerItem{
		ID:          kind + ":" + command.Name,
		Name:        command.Name,
		Description: strings.TrimSpace(command.Description),
		Kind:        kind,
		Trigger:     "/",
		InsertText:  "/" + command.Name,
		ArgsHint:    strings.TrimSpace(command.ArgumentHint),
		Origin:      origin,
		Behavior:    adapter.ComposerPrompt,
		Aliases:     command.Aliases,
		Inline:      inline,
	}
}

// commandCache keeps, per config dir, what the last live catalogue said that
// the disk does not: the entries no file lists — Claude's built-in commands
// and the skills it bundles — and the skills on disk that Claude left out, as
// it does those written for another of its products. One config dir is one
// account's installation, so both hold for any folder under it.
type commandCache struct {
	mu          sync.Mutex
	byConfigDir map[string]remembered
}

type remembered struct {
	extra  []adapter.ComposerItem
	hidden map[string]bool
}

func (c *commandCache) remember(configDir string, items []adapter.ComposerItem, disk map[string]claudeSkill) {
	if c == nil {
		return
	}
	kept := remembered{hidden: make(map[string]bool)}
	listed := make(map[string]bool, len(items))
	for _, item := range items {
		key := strings.ToLower(item.Name)
		listed[key] = true
		// A project entry belongs to the folder that session ran in, not to
		// the one a draft is about to start in.
		if _, onDisk := disk[key]; onDisk || item.Origin == "project" {
			continue
		}
		kept.extra = append(kept.extra, item)
	}
	for key, skill := range disk {
		if !listed[key] && skill.origin != "project" {
			kept.hidden[key] = true
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byConfigDir == nil {
		c.byConfigDir = make(map[string]remembered)
	}
	c.byConfigDir[configDir] = kept
}

func (c *commandCache) recall(configDir string) remembered {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byConfigDir[configDir]
}

func claudeConfigDir(cwd string, env map[string]string) string {
	configured, overridden := env["CLAUDE_CONFIG_DIR"]
	if !overridden {
		configured = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if configured = strings.TrimSpace(configured); configured != "" {
		if filepath.IsAbs(configured) {
			return filepath.Clean(configured)
		}
		return filepath.Join(cwd, configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(cwd, ".claude")
	}
	return filepath.Join(home, ".claude")
}

type claudeSkill struct {
	name, description, origin string
	// userOnly is the skill's disable-model-invocation.
	userOnly bool
}

// claudeDiskSkills is every skill Claude would load for a session in cwd,
// keyed by the lowercased name it is invoked by. A plugin's skills are
// namespaced by the plugin, the way Claude lists them.
func claudeDiskSkills(configDir, cwd string) map[string]claudeSkill {
	found, _ := skills.Discover(skills.Roots{ClaudeConfigDir: configDir, ProjectRoot: cwd})
	out := make(map[string]claudeSkill, len(found))
	for _, skill := range found {
		if !seenBy(skill, skills.Claude) || skill.Name == "" {
			continue
		}
		name, origin := skill.Name, "other"
		switch skill.Scope {
		case skills.ScopeProject:
			origin = "project"
		case skills.ScopeUser:
			origin = "personal"
		case skills.ScopePlugin:
			name, origin = skill.Plugin+":"+skill.Name, "plugin"
		}
		// Discover lists project skills first, and a project skill shadows a
		// personal one of the same name in Claude too.
		key := strings.ToLower(name)
		if _, taken := out[key]; !taken {
			out[key] = claudeSkill{name: name, description: skill.Description, origin: origin, userOnly: skill.UserOnly}
		}
	}
	return out
}

func seenBy(skill skills.Skill, harness skills.Harness) bool {
	for _, h := range skill.Harnesses {
		if h == harness {
			return true
		}
	}
	return false
}

// Current Claude Code appends source labels to discovered skill descriptions.
// They are fallback enrichment only: a matching filesystem entry above is
// preferred because this presentation suffix is not part of the SDK type.
func claudeDescriptionOrigin(description string) (string, bool) {
	description = strings.TrimSpace(description)
	switch {
	case strings.HasSuffix(description, "(user)"):
		return "personal", true
	case strings.HasSuffix(description, "(project)"), strings.HasSuffix(description, "(local)"):
		return "project", true
	case strings.HasSuffix(description, "(dynamic workflow)"):
		return "other", true
	default:
		return "", false
	}
}
