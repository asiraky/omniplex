package piapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
)

// This file stores OpenRouter provider routing per model.
//
// OpenRouter fans a model out to whoever serves it, and which one you land on
// changes latency, price and quantisation. Pi exposes the choice, but only
// through ~/.pi/agent/models.json — so pinning a model meant leaving the app,
// finding an undocumented file and hand-editing nested JSON. This puts the
// same value beside the account it applies to.
//
// The value is passed to pi verbatim and forwarded by pi to OpenRouter as the
// request's "provider" field. Omniplex validates that it is a JSON object and
// nothing else: the schema is OpenRouter's, and a build that second-guessed it
// would reject options added after it shipped.

// modelsFile is pi's custom-model config, in whichever agent directory this
// instance uses.
func modelsFile(env map[string]string) string {
	dir := agentDir(env)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "models.json")
}

// routingProvider is the pi provider whose models take this setting.
const routingProvider = "openrouter"

func (a *Adapter) ModelSettingsSchema() adapter.ModelSettingsSchema {
	return adapter.ModelSettingsSchema{
		Label:       "Provider routing",
		Description: "OpenRouter serves each model from one of several providers. Pin this model to one — or set any other routing preference — and Pi sends it with every request. Applies to new sessions.",
		Placeholder: `{"only": ["amazon-bedrock"]}`,
		DocsURL:     "https://openrouter.ai/docs/guides/routing/provider-selection",
		Prefix:      routingProvider + "/",
	}
}

// ModelSettings reads every stored routing value, keyed by the model id as a
// listing shows it ("openrouter/anthropic/claude-sonnet-4").
func (a *Adapter) ModelSettings(env map[string]string) (map[string]string, error) {
	doc, err := readModelsFile(env)
	if err != nil {
		return nil, err
	}
	overrides := descend(doc, "providers", routingProvider, "modelOverrides")
	out := map[string]string{}
	for model, raw := range overrides {
		var entry map[string]json.RawMessage
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		routing := descend(entry, "compat")["openRouterRouting"]
		if len(routing) == 0 {
			continue
		}
		out[routingProvider+"/"+model] = string(indent(routing))
	}
	return out, nil
}

// SetModelSetting writes one model's routing, or clears it when value is
// blank.
//
// Every level is round-tripped through map[string]json.RawMessage so that
// anything else in the file — other providers, other models, a compat key we
// have never heard of — comes back out exactly as it went in. A settings
// screen that quietly dropped a hand-written config would be worse than no
// settings screen.
func (a *Adapter) SetModelSetting(env map[string]string, modelID, value string) error {
	provider, model, ok := strings.Cut(modelID, "/")
	if !ok || provider != routingProvider {
		return fmt.Errorf("provider routing applies to OpenRouter models; %q is not one", modelID)
	}
	routing, err := parseRouting(value)
	if err != nil {
		return err
	}
	path := modelsFile(env)
	if path == "" {
		return errors.New("pi's agent directory could not be located")
	}
	doc, err := readModelsFile(env)
	if err != nil {
		return err
	}

	providers := descend(doc, "providers")
	openrouter := descend(providers, routingProvider)
	overrides := descend(openrouter, "modelOverrides")
	entry := descend(overrides, model)
	compat := descend(entry, "compat")

	if routing == nil {
		delete(compat, "openRouterRouting")
	} else {
		compat["openRouterRouting"] = routing
	}

	// Fold empty containers away on the way back up, so clearing the last
	// setting leaves the file as it was rather than a nest of empty objects.
	set(entry, "compat", compat)
	set(overrides, model, entry)
	set(openrouter, "modelOverrides", overrides)
	set(providers, routingProvider, openrouter)
	set(doc, "providers", providers)

	if len(doc) == 0 {
		// Nothing left to say. Remove the file rather than leave "{}" behind,
		// but only if we are the reason it is empty.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeModelsFile(path, doc)
}

// parseRouting validates a pasted value. Empty clears the setting; anything
// else must be a JSON object, because that is the only shape OpenRouter's
// "provider" field takes and a scalar would fail deep inside pi with a
// message about someone else's request body.
func parseRouting(value string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "{}" {
		return nil, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		var any any
		if json.Unmarshal([]byte(trimmed), &any) == nil {
			return nil, errors.New("provider routing must be a JSON object, like {\"only\": [\"amazon-bedrock\"]}")
		}
		return nil, fmt.Errorf("that is not valid JSON: %w", err)
	}
	// Re-marshal so the file gets canonical JSON rather than the paste's
	// whitespace, and so a trailing-garbage paste cannot survive.
	return json.Marshal(probe)
}

// readModelsFile loads models.json generically. A missing file is an empty
// document, not an error: most installs have never needed one.
func readModelsFile(env map[string]string) (map[string]json.RawMessage, error) {
	path := modelsFile(env)
	if path == "" {
		return map[string]json.RawMessage{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		// Refusing is the only safe answer: rewriting a file we cannot parse
		// would destroy whatever the user actually wrote in it.
		return nil, fmt.Errorf("%s could not be parsed, so it was left alone: %w", path, err)
	}
	return doc, nil
}

// writeModelsFile replaces the file atomically, so a crash mid-write cannot
// leave pi with a truncated config it then refuses to start on.
func writeModelsFile(path string, doc map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// descend returns the nested object at path, creating empty levels rather
// than nil so a caller can always write into what it gets back. A level that
// exists but is not an object is replaced, because there is no way to merge
// into a string.
func descend(doc map[string]json.RawMessage, path ...string) map[string]json.RawMessage {
	cur := doc
	for _, key := range path {
		next := map[string]json.RawMessage{}
		if raw, ok := cur[key]; ok {
			_ = json.Unmarshal(raw, &next)
		}
		cur = next
	}
	return cur
}

// set stores a child object under key, or removes the key when the child has
// nothing in it.
func set(parent map[string]json.RawMessage, key string, child map[string]json.RawMessage) {
	if len(child) == 0 {
		delete(parent, key)
		return
	}
	raw, err := json.Marshal(child)
	if err != nil {
		return
	}
	parent[key] = raw
}

// indent pretty-prints a stored value for display, so the box shows the same
// shape a user would paste rather than one long line.
func indent(raw json.RawMessage) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return raw
	}
	return buf.Bytes()
}
