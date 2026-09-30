package piapp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/skills"
)

// piSkillPrefix is how pi names a skill command: skills register as
// /skill:<name> to keep them apart from extension commands and prompt
// templates, which share the same catalogue and the same / trigger.
const piSkillPrefix = "skill:"

// piCommand is one entry from get_commands. Pi returns extension commands,
// prompt templates and skills in a single list, told apart by Source.
//
// SourceInfo is not what docs/rpc.md describes — the doc still shows flat
// location and path fields, while pi (0.84) sends this nested object. The
// struct follows the wire, and every field is optional so a shape change
// costs an origin label rather than the whole catalogue.
type piCommand struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Source      string        `json:"source"` // extension | prompt | skill
	SourceInfo  *piSourceInfo `json:"sourceInfo"`
}

type piSourceInfo struct {
	Path   string `json:"path"`
	Scope  string `json:"scope"`  // user | project | temporary
	Origin string `json:"origin"` // package | top-level
}

// ComposerItems asks the live pi process what this session can invoke. It has
// to be the live process: skills are discovered relative to the session cwd,
// under the instance's own PI_CODING_AGENT_DIR, and project skills load only
// when that process trusted the project.
func (s *session) ComposerItems(ctx context.Context) ([]adapter.ComposerItem, error) {
	var response struct {
		Commands []piCommand `json:"commands"`
	}
	if err := s.call(ctx, map[string]any{"type": "get_commands"}, &response); err != nil {
		return nil, s.classify(err)
	}
	return piComposerItems(response.Commands), nil
}

// DraftComposerItems answers for a thread that does not exist yet, from the
// skill directories pi reads. Prompt templates and extension commands are only
// known to a running pi, so they arrive with the live catalogue.
func (a *Adapter) DraftComposerItems(_ context.Context, env map[string]string, cwd string) ([]adapter.ComposerItem, error) {
	home, _ := os.UserHomeDir()
	found, _ := skills.Discover(skills.DefaultRoots(home, env, cwd))
	commands := make([]piCommand, 0, len(found))
	for _, skill := range found {
		if !piSees(skill) || skill.Name == "" {
			continue
		}
		// Discover's scopes are pi's own words for them. A name found twice is
		// dropped by piComposerItems, which keeps the narrower scope: Discover
		// lists that one first.
		commands = append(commands, piCommand{
			Name:        piSkillPrefix + skill.Name,
			Description: skill.Description,
			Source:      "skill",
			SourceInfo:  &piSourceInfo{Path: filepath.Join(skill.Dir, "SKILL.md"), Scope: skill.Scope},
		})
	}
	return piComposerItems(commands), nil
}

func piSees(skill skills.Skill) bool {
	for _, h := range skill.Harnesses {
		if h == skills.Pi {
			return true
		}
	}
	return false
}

// piComposerItems maps pi's catalogue onto composer entries. Every entry is
// prompt text: pi expands /skill:name and /template itself when the prompt
// arrives, so omniplex sends the line as typed and never interprets it.
//
// It expands them only as the first thing in the prompt. Anywhere else the
// model reads the text as typed, and can act on it only for a skill pi told it
// about — one that does not disable model invocation. That is what Inline
// says, and pi's catalogue does not, so it is read from the skill's own file.
func piComposerItems(commands []piCommand) []adapter.ComposerItem {
	items := make([]adapter.ComposerItem, 0, len(commands))
	seen := make(map[string]bool)
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		// What pi accepts is the full name, prefix and all.
		insert := "/" + name
		kind := "command"
		inline := false
		var aliases []string
		if strings.EqualFold(strings.TrimSpace(command.Source), "skill") {
			kind = "skill"
			inline = command.SourceInfo != nil && command.SourceInfo.Path != "" && !skills.UserOnly(command.SourceInfo.Path)
			// Display the bare skill name — "merge" reads better in the list
			// than "skill:merge" — but keep the qualified name as an alias so
			// typing /skill:merge still finds it.
			if short := strings.TrimPrefix(name, piSkillPrefix); short != name && short != "" {
				aliases = []string{name}
				name = short
			}
		}
		id := kind + ":" + name
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, adapter.ComposerItem{
			ID:          id,
			Name:        name,
			Description: strings.TrimSpace(command.Description),
			Kind:        kind,
			Trigger:     "/",
			InsertText:  insert,
			Origin:      piCommandOrigin(command.SourceInfo),
			Behavior:    adapter.ComposerPrompt,
			Aliases:     aliases,
			Inline:      inline,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// piCommandOrigin turns pi's scope into the same vocabulary the other
// adapters use for the bracketed label beside a composer entry.
func piCommandOrigin(info *piSourceInfo) string {
	if info == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(info.Origin), "package") {
		return "package"
	}
	switch strings.ToLower(strings.TrimSpace(info.Scope)) {
	case "user":
		return "personal"
	case "project":
		return "project"
	case "temporary":
		// Loaded for this run only: pi's own inline extensions, and anything
		// a --skill or --extension path pulled in.
		return "built-in"
	default:
		return "other"
	}
}
