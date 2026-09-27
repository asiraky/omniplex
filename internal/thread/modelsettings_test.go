package session

import (
	"context"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
)

// settingsAdapter records the env it was handed, because *which* harness
// directory a setting lands in is the whole point of per-instance isolation.
type settingsAdapter struct {
	cfgAdapter
	values  map[string]string
	lastEnv map[string]string
}

func (s *settingsAdapter) ModelSettingsSchema() adapter.ModelSettingsSchema {
	return adapter.ModelSettingsSchema{Label: "Provider routing", Prefix: "openrouter/"}
}

func (s *settingsAdapter) ModelSettings(env map[string]string) (map[string]string, error) {
	s.lastEnv = env
	return s.values, nil
}

func (s *settingsAdapter) SetModelSetting(env map[string]string, modelID, value string) error {
	s.lastEnv = env
	if s.values == nil {
		s.values = map[string]string{}
	}
	if value == "" {
		delete(s.values, modelID)
	} else {
		s.values[modelID] = value
	}
	return nil
}

func settingsManager(t *testing.T) (*Manager, *settingsAdapter) {
	t.Helper()
	ad := &settingsAdapter{}
	return adminTestManagerWith(t, ad), ad
}

func TestModelSettingsRoundTrip(t *testing.T) {
	mgr, ad := settingsManager(t)
	if err := mgr.SetModelSetting("fake", "openrouter/x/y", `{"only":["a"]}`); err != nil {
		t.Fatal(err)
	}
	values, err := mgr.ModelSettings("fake")
	if err != nil {
		t.Fatal(err)
	}
	if values["openrouter/x/y"] != `{"only":["a"]}` {
		t.Fatalf("values = %v", values)
	}
	if err := mgr.SetModelSetting("fake", "openrouter/x/y", ""); err != nil {
		t.Fatal(err)
	}
	if values, _ = mgr.ModelSettings("fake"); len(values) != 0 {
		t.Fatalf("clearing left %v", values)
	}
	_ = ad
}

// The setting has to be written into the instance's own credential home, not
// the driver's ambient one — otherwise two accounts would fight over one file.
func TestModelSettingUsesTheInstanceEnvironment(t *testing.T) {
	mgr, ad := settingsManager(t)
	if err := mgr.AddProviderInstance(provider.Spec{
		ID: "fake-work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/homes/work"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetModelSetting("fake-work", "openrouter/x/y", `{"only":["a"]}`); err != nil {
		t.Fatal(err)
	}
	if ad.lastEnv["FAKE_HOME"] != "/homes/work" {
		t.Errorf("adapter got env %v; want the instance's home", ad.lastEnv)
	}
}

func TestModelSettingsUnknownInstance(t *testing.T) {
	mgr, _ := settingsManager(t)
	if _, err := mgr.ModelSettings("nope"); err == nil {
		t.Error("an unknown instance must be an error, not an empty answer")
	}
}

// A driver without the capability is not a bug in the caller: the read answers
// empty, and only the write refuses.
func TestModelSettingsAbsentCapability(t *testing.T) {
	mgr := adminTestManager(t)
	values, err := mgr.ModelSettings("fake")
	if err != nil || len(values) != 0 {
		t.Fatalf("values = %v, err = %v", values, err)
	}
	if err := mgr.SetModelSetting("fake", "openrouter/x/y", "{}"); err == nil {
		t.Error("a driver with no per-model settings must refuse the write")
	}
}

func TestHarnessAdvertisesTheSchema(t *testing.T) {
	mgr, _ := settingsManager(t)
	for _, h := range mgr.Harnesses(context.Background()) {
		if h.ID != "fake" {
			continue
		}
		if h.ModelSettings == nil || h.ModelSettings.Label != "Provider routing" {
			t.Fatalf("schema not advertised: %+v", h.ModelSettings)
		}
		return
	}
	t.Fatal("fake harness missing")
}
