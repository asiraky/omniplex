package piapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rawMap(pairs map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range pairs {
		out[k] = json.RawMessage(v)
	}
	return out
}

func TestSupportedThinkingLevels(t *testing.T) {
	cases := []struct {
		name string
		m    piModel
		want []string
	}{
		{
			name: "non-reasoning models only get off",
			m:    piModel{Reasoning: false},
			want: []string{"off"},
		},
		{
			name: "reasoning without a map gets the base ladder, extended levels excluded",
			m:    piModel{Reasoning: true},
			want: []string{"off", "minimal", "low", "medium", "high"},
		},
		{
			name: "explicit nulls remove levels, explicit xhigh opts in",
			m: piModel{Reasoning: true, ThinkingLevelMap: rawMap(map[string]string{
				"minimal": "null",
				"xhigh":   `"ultra"`,
			})},
			want: []string{"off", "low", "medium", "high", "xhigh"},
		},
	}
	for _, c := range cases {
		if got := supportedThinkingLevels(c.m); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMapModels(t *testing.T) {
	models := []piModel{
		{ID: "claude-x", Name: "Claude X", Provider: "anthropic", Reasoning: true},
		{ID: "openai/gpt-x", Name: "GPT X", Provider: "openrouter", Reasoning: false},
	}
	current := &piModel{ID: "claude-x", Provider: "anthropic"}
	got, err := mapModels(models, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].ID != "anthropic/claude-x" || !got[0].Default || got[0].Label != "Claude X" {
		t.Errorf("row 0 wrong: %+v", got[0])
	}
	// A model id containing a slash must survive the provider/id join: only
	// the first slash is the separator.
	if got[1].ID != "openrouter/openai/gpt-x" || got[1].Default {
		t.Errorf("row 1 wrong: %+v", got[1])
	}
	if !reflect.DeepEqual(got[1].Efforts, []string{"off"}) {
		t.Errorf("non-reasoning efforts wrong: %v", got[1].Efforts)
	}

	if _, err := mapModels(nil, nil); err == nil {
		t.Error("an empty catalogue must be an error, not an empty list")
	}
}

func TestListModels(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q/args
while IFS= read -r line; do
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"get_state"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"x\",\"model\":{\"provider\":\"anthropic\",\"id\":\"claude-x\"}}}"
      ;;
    *'"type":"get_available_models"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"id\":\"claude-x\",\"name\":\"Claude X\",\"provider\":\"anthropic\",\"reasoning\":true},{\"id\":\"small\",\"name\":\"Small\",\"provider\":\"openrouter\",\"reasoning\":false}]}}"
      ;;
  esac
done
`, dir)
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := New(bin).ListModels(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 || got[0].ID != "anthropic/claude-x" || !got[0].Default || got[1].ID != "openrouter/small" {
		t.Fatalf("catalogue wrong: %+v", got)
	}

	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "--no-session") {
		t.Errorf("the catalogue probe must not create a session; argv:\n%s", args)
	}
}

func TestListModelsEmptyCatalogue(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"x\"}}"
      ;;
    *'"type":"get_available_models"'*)
      printf '%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[]}}"
      ;;
  esac
done
`
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(bin).ListModels(context.Background(), nil); err == nil {
		t.Fatal("no credentials means no models; expected an error for the fallback path")
	}
}
