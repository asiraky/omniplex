package piapp

import (
	"context"
	"sort"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
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

// piComposerItems maps pi's catalogue onto composer entries. Every entry is
// prompt text: pi expands /skill:name and /template itself when the prompt
// arrives, so omniplex sends the line as typed and never interprets it.
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
		var aliases []string
		if strings.EqualFold(strings.TrimSpace(command.Source), "skill") {
			kind = "skill"
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
