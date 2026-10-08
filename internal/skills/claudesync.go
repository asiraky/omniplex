package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Claude Code's two switches for whole sets of skills in the user settings.
// syncKey is for the skills enabled on a claude.ai account: only false means
// anything, true is the same as leaving it out. bundledKey is for the skills
// that ship inside the CLI: only true means anything.
const (
	syncKey    = "syncClaudeAiSkills"
	bundledKey = "disableBundledSkills"
)

func claudeSettingsPath(r Roots) string {
	return filepath.Join(r.ClaudeConfigDir, "settings.json")
}

// claudeSetting is a top-level key of the user settings as raw JSON, "" when
// it is not there or the file cannot be read.
func claudeSetting(r Roots, key string) string {
	if r.ClaudeConfigDir == "" {
		return ""
	}
	data, err := os.ReadFile(claudeSettingsPath(r))
	if err != nil {
		return ""
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return string(settings[key])
}

// setClaudeSetting writes a top-level key of the user settings, or takes it
// out when value is nil. The rest of the file is left as it was, and a
// settings.json that is a link is written through to what it points at.
func setClaudeSetting(r Roots, key string, value *string) error {
	if r.ClaudeConfigDir == "" {
		return errors.New("no Claude config dir")
	}
	return editSettings(claudeSettingsPath(r), func(content string) (string, error) {
		return setTopKey(content, key, value)
	})
}

// ClaudeSync reports whether Claude Code loads the skills it syncs from the
// claude.ai account: on unless the user settings say false.
func ClaudeSync(r Roots) bool {
	return claudeSetting(r, syncKey) != "false"
}

// SetClaudeSync turns the sync off by writing false into the user settings,
// or on by taking the key out.
func SetClaudeSync(r Roots, on bool) error {
	var value *string
	if !on {
		f := "false"
		value = &f
	}
	return setClaudeSetting(r, syncKey, value)
}

// ClaudeBundled reports whether Claude Code loads the skills it ships with:
// on unless the user settings say disableBundledSkills is true. Off beats
// every skillOverrides entry, so a bundled skill set on there stays off.
func ClaudeBundled(r Roots) bool {
	return claudeSetting(r, bundledKey) != "true"
}

// SetClaudeBundled turns the CLI's own skills off by writing true into the
// user settings, or on by taking the key out. Per-skill entries in
// skillOverrides are left alone, so turning the set back on restores them.
func SetClaudeBundled(r Roots, on bool) error {
	var value *string
	if !on {
		t := "true"
		value = &t
	}
	return setClaudeSetting(r, bundledKey, value)
}

// editSettings edits a JSON settings file in place. A missing file is
// created only when the edit has something to say.
func editSettings(path string, edit func(string) (string, error)) error {
	return editThrough(path, func(content string, exists bool) (string, error) {
		if !exists {
			next, err := edit("{}\n")
			if err != nil || next == "{}\n" {
				return "", err
			}
			return next, nil
		}
		next, err := edit(content)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
		}
		return next, nil
	})
}

// readThrough reads a file, following a symlink to what it points at. real
// is the file to write back to; it is set even when the file is not there.
func readThrough(path string) (content, real string, err error) {
	real = path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		real = r
	} else if target, err := os.Readlink(path); err == nil {
		// A dangling link: write where it points.
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		real = target
	}
	data, err := os.ReadFile(real)
	return string(data), real, err
}

// editMu serialises config edits. Commands run concurrently, and two edits
// that read the same file would otherwise each write back without the other's
// change.
var editMu sync.Mutex

// editThrough rewrites a config file with edit, through a symlink to its
// target. A missing file reads as empty and is only created if edit returns
// something; nothing is written when the content does not change.
func editThrough(path string, edit func(content string, exists bool) (string, error)) error {
	editMu.Lock()
	defer editMu.Unlock()
	content, real, err := readThrough(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next, err := edit(content, exists)
	if err != nil {
		return err
	}
	if next == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		return err
	}
	return writeAtomic(real, []byte(next))
}

// entry is one top-level member of a JSON object, by byte offset: prev is
// where the member before it (or the opening brace) ends, key where its key's
// quote starts, from/to its value.
type entry struct {
	name                string
	prev, key, from, to int
}

// parseObject finds the top-level members of the JSON object in content.
// open is the offset just after its opening brace.
func parseObject(content string) (open int, entries []entry, err error) {
	dec := json.NewDecoder(strings.NewReader(content))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, nil, errors.New("not a JSON object")
	}
	open = int(dec.InputOffset())
	for dec.More() {
		prev := int(dec.InputOffset())
		t, err := dec.Token()
		if err != nil {
			return 0, nil, err
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, nil, err
		}
		to := int(dec.InputOffset())
		entries = append(entries, entry{
			name: key,
			prev: prev,
			key:  prev + strings.IndexByte(content[prev:], '"'),
			from: to - len(raw),
			to:   to,
		})
	}
	if _, err := dec.Token(); err != nil {
		return 0, nil, err
	}
	return open, entries, nil
}

// setTopKey sets a top-level key of the JSON object in content to the raw
// JSON value, or removes it when value is nil, touching nothing else.
func setTopKey(content, name string, value *string) (string, error) {
	return setMember(content, name, value, "")
}

// setMember is setTopKey for an object that may itself be nested: outer is
// the indentation of the line the object opens on, so a member added to an
// empty object sits one level in from it.
func setMember(content, name string, value *string, outer string) (string, error) {
	open, entries, err := parseObject(content)
	if err != nil {
		return "", err
	}
	out := content
	found := false
	// Last first, so the offsets of the ones before stay good.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.name != name {
			continue
		}
		found = true
		switch {
		case value != nil:
			out = out[:e.from] + *value + out[e.to:]
		case i > 0:
			// Take the comma before it along with it.
			out = out[:e.prev] + out[e.to:]
		case len(entries) > 1:
			// The first of several: take the comma after it, and the space
			// up to the next key, so that key sits where this one did.
			out = out[:e.key] + strings.TrimLeft(out[e.to:], " \t\r\n,")
		default:
			out = out[:open] + out[e.to:]
		}
	}
	if found || value == nil {
		return out, nil
	}
	member := jsonString(name) + ": " + *value
	if len(entries) == 0 {
		return content[:open] + "\n" + outer + "  " + member + "\n" + outer + strings.TrimLeft(content[open:], " \t\r\n"), nil
	}
	// First in the object, indented like the member that was first.
	indent := content[open:entries[0].key]
	return content[:open] + indent + member + "," + content[open:], nil
}

// setNestedKey sets outer.inner in the JSON object in content, or removes it
// when value is nil, and takes outer away too once nothing is left in it.
// Every other byte stays where it was.
func setNestedKey(content, outer, inner string, value *string) (string, error) {
	open, entries, err := parseObject(content)
	if err != nil {
		return "", err
	}
	at := -1
	for i, e := range entries {
		if e.name == outer {
			at = i
		}
	}
	if at < 0 {
		if value == nil {
			return content, nil
		}
		// Indented like the first member, or on one line like the file.
		indent := "  "
		if len(entries) > 0 {
			gap := content[open:entries[0].key]
			nl := strings.LastIndexByte(gap, '\n')
			if nl < 0 {
				obj := "{" + jsonString(inner) + ": " + *value + "}"
				return setTopKey(content, outer, &obj)
			}
			indent = gap[nl+1:]
		}
		obj := "{\n" + indent + indent + jsonString(inner) + ": " + *value + "\n" + indent + "}"
		return setTopKey(content, outer, &obj)
	}
	e := entries[at]
	raw := content[e.from:e.to]
	if !strings.HasPrefix(raw, "{") {
		return "", fmt.Errorf("%s is not an object", outer)
	}
	line := content[strings.LastIndexByte(content[:e.key], '\n')+1 : e.key]
	next, err := setMember(raw, inner, value, indentOf(line))
	if err != nil {
		return "", err
	}
	if next == raw {
		return content, nil
	}
	if value == nil {
		if _, left, err := parseObject(next); err == nil && len(left) == 0 {
			return setTopKey(content, outer, nil)
		}
	}
	return content[:e.from] + next + content[e.to:], nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
