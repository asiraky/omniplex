package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// syncKey is Claude Code's switch for the skills enabled on a claude.ai
// account. Only false means anything: true is the same as leaving it out.
const syncKey = "syncClaudeAiSkills"

func claudeSettingsPath(r Roots) string {
	return filepath.Join(r.ClaudeConfigDir, "settings.json")
}

// ClaudeSync reports whether Claude Code loads the skills it syncs from the
// claude.ai account: on unless the user settings say false.
func ClaudeSync(r Roots) bool {
	if r.ClaudeConfigDir == "" {
		return true
	}
	data, err := os.ReadFile(claudeSettingsPath(r))
	if err != nil {
		return true
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(data, &settings) != nil {
		return true
	}
	return string(settings[syncKey]) != "false"
}

// SetClaudeSync turns the sync off by writing false into the user settings,
// or on by taking the key out. The rest of the file is left as it was, and a
// settings.json that is a link is written through to what it points at.
func SetClaudeSync(r Roots, on bool) error {
	if r.ClaudeConfigDir == "" {
		return errors.New("no Claude config dir")
	}
	path := claudeSettingsPath(r)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if on {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeAtomic(path, []byte("{\n  \""+syncKey+"\": false\n}\n"))
	case err != nil:
		return err
	}
	var value *string
	if !on {
		f := "false"
		value = &f
	}
	next, err := setTopKey(string(data), syncKey, value)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
	}
	if next == string(data) {
		return nil
	}
	return writeAtomic(path, []byte(next))
}

// entry is one top-level member of a JSON object, by byte offset: prev is
// where the member before it (or the opening brace) ends, key where its key's
// quote starts, from/to its value.
type entry struct {
	name                string
	prev, key, from, to int
}

// setTopKey sets a top-level key of the JSON object in content to the raw
// JSON value, or removes it when value is nil, touching nothing else.
func setTopKey(content, name string, value *string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(content))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", errors.New("settings are not a JSON object")
	}
	open := int(dec.InputOffset())
	var entries []entry
	for dec.More() {
		prev := int(dec.InputOffset())
		t, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", err
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
	member := `"` + name + `": ` + *value
	if len(entries) == 0 {
		return content[:open] + "\n  " + member + "\n" + strings.TrimLeft(content[open:], " \t\r\n"), nil
	}
	// First in the object, indented like the member that was first.
	indent := content[open:entries[0].key]
	return content[:open] + indent + member + "," + content[open:], nil
}
