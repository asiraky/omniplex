package piapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func routingEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"PI_CODING_AGENT_DIR": t.TempDir()}
}

func readFile(t *testing.T, env map[string]string) string {
	t.Helper()
	data, err := os.ReadFile(modelsFile(env))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetModelSettingWritesRouting(t *testing.T) {
	a, env := New(""), routingEnv(t)
	if err := a.SetModelSetting(env, "openrouter/anthropic/claude-sonnet-4", `{"only":["amazon-bedrock"]}`); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Providers struct {
			OpenRouter struct {
				ModelOverrides map[string]struct {
					Compat struct {
						OpenRouterRouting struct {
							Only []string `json:"only"`
						} `json:"openRouterRouting"`
					} `json:"compat"`
				} `json:"modelOverrides"`
			} `json:"openrouter"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, env)), &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.Providers.OpenRouter.ModelOverrides["anthropic/claude-sonnet-4"].Compat.OpenRouterRouting.Only
	if len(got) != 1 || got[0] != "amazon-bedrock" {
		t.Fatalf("routing = %v; want [amazon-bedrock]", got)
	}

	values, err := a.ModelSettings(env)
	if err != nil {
		t.Fatal(err)
	}
	if v := values["openrouter/anthropic/claude-sonnet-4"]; !strings.Contains(v, "amazon-bedrock") {
		t.Errorf("ModelSettings = %q; want the stored routing", v)
	}
}

// The file is the user's, not ours: a settings screen that dropped someone's
// hand-written provider config would be worse than no settings screen.
func TestSetModelSettingPreservesEverythingElse(t *testing.T) {
	a, env := New(""), routingEnv(t)
	original := `{
  "providers": {
    "ollama": {"baseUrl": "http://localhost:11434/v1", "models": [{"id": "llama3.1:8b"}]},
    "openrouter": {
      "modelOverrides": {
        "meta/llama": {"name": "Kept", "compat": {"supportsDeveloperRole": false}}
      }
    }
  },
  "futureKnob": 42
}`
	if err := os.WriteFile(modelsFile(env), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModelSetting(env, "openrouter/anthropic/claude-sonnet-4", `{"only":["anthropic"]}`); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(readFile(t, env)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["futureKnob"] != float64(42) {
		t.Errorf("unknown top-level key was dropped: %v", doc)
	}
	providers := doc["providers"].(map[string]any)
	if _, ok := providers["ollama"]; !ok {
		t.Error("another provider was dropped")
	}
	overrides := providers["openrouter"].(map[string]any)["modelOverrides"].(map[string]any)
	other, ok := overrides["meta/llama"].(map[string]any)
	if !ok {
		t.Fatal("another model's override was dropped")
	}
	if other["name"] != "Kept" {
		t.Errorf("another model's fields were rewritten: %v", other)
	}
	if compat := other["compat"].(map[string]any); compat["supportsDeveloperRole"] != false {
		t.Errorf("another model's compat was rewritten: %v", compat)
	}
}

// Clearing should leave the file as it was found, not a nest of empty objects.
func TestClearingRoutingFoldsEmptyContainersAway(t *testing.T) {
	a, env := New(""), routingEnv(t)
	if err := a.SetModelSetting(env, "openrouter/x/y", `{"only":["a"]}`); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModelSetting(env, "openrouter/x/y", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelsFile(env)); !os.IsNotExist(err) {
		t.Errorf("models.json should be gone once it holds nothing; stat err = %v", err)
	}

	// ...but a file with other content stays, minus only our key.
	if err := os.WriteFile(modelsFile(env), []byte(`{"futureKnob":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModelSetting(env, "openrouter/x/y", `{"only":["a"]}`); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModelSetting(env, "openrouter/x/y", ""); err != nil {
		t.Fatal(err)
	}
	body := readFile(t, env)
	if !strings.Contains(body, "futureKnob") {
		t.Errorf("unrelated content was removed: %s", body)
	}
	if strings.Contains(body, "openRouterRouting") || strings.Contains(body, "modelOverrides") {
		t.Errorf("cleared setting left scaffolding behind: %s", body)
	}
}

func TestRoutingRejectsNonObjects(t *testing.T) {
	a, env := New(""), routingEnv(t)
	for _, bad := range []string{`"amazon-bedrock"`, `["amazon-bedrock"]`, `42`, `{"only": [}`} {
		if err := a.SetModelSetting(env, "openrouter/x/y", bad); err == nil {
			t.Errorf("value %q was accepted", bad)
		}
	}
	if _, err := os.Stat(modelsFile(env)); !os.IsNotExist(err) {
		t.Error("a rejected value must not create the file")
	}
}

func TestRoutingRefusesNonOpenRouterModels(t *testing.T) {
	a, env := New(""), routingEnv(t)
	if err := a.SetModelSetting(env, "anthropic/claude-sonnet-4", `{"only":["anthropic"]}`); err == nil {
		t.Error("routing applies to OpenRouter models only")
	}
}

// A file we cannot parse is someone's hand-written config with a typo in it.
// Rewriting it would destroy their work.
func TestRoutingRefusesToRewriteUnparseableFile(t *testing.T) {
	a, env := New(""), routingEnv(t)
	if err := os.WriteFile(modelsFile(env), []byte(`{"providers": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.SetModelSetting(env, "openrouter/x/y", `{"only":["a"]}`); err == nil {
		t.Fatal("a malformed file must not be overwritten")
	}
	if body := readFile(t, env); body != `{"providers": ` {
		t.Errorf("file was modified: %q", body)
	}
}

func TestModelSettingsIsEmptyWithoutAFile(t *testing.T) {
	values, err := New("").ModelSettings(routingEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Errorf("values = %v; want none", values)
	}
}

// The default instance's setting must land in the agent directory pi itself
// reads, which is what makes a pin apply to the terminal too.
func TestModelsFileFollowsTheAgentDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := modelsFile(map[string]string{"PI_CODING_AGENT_DIR": dir}); got != filepath.Join(dir, "models.json") {
		t.Errorf("modelsFile = %q", got)
	}
}
