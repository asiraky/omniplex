// Package project defines the portable project configuration edited by omniplex.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ConfigPath = ".omniplex/project.json"

type Defaults struct {
	Harness    string                     `json:"harness,omitempty"`
	Harnesses  map[string]HarnessDefaults `json:"harnesses,omitempty"`
	Workspace  string                     `json:"workspace,omitempty"`
	BaseBranch string                     `json:"baseBranch,omitempty"`
	// Legacy scalar agent defaults are read only long enough to move existing
	// version-1 project files into the selected harness's profile.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

type HarnessDefaults struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

type Workspace struct {
	SuggestedRoot             string `json:"suggestedRoot,omitempty"`
	Provision                 string `json:"provision,omitempty"`
	Deprovision               string `json:"deprovision,omitempty"`
	ProvisionTimeoutSeconds   int    `json:"provisionTimeoutSeconds,omitempty"`
	DeprovisionTimeoutSeconds int    `json:"deprovisionTimeoutSeconds,omitempty"`
}

type Config struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	Defaults  Defaults  `json:"defaults"`
	Workspace Workspace `json:"workspace"`
}

type Project struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	Config    Config `json:"config"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func DefaultConfig(root string) Config {
	return Config{Version: 1, Name: filepath.Base(root), Defaults: Defaults{
		Harness: "codex", Workspace: "local",
	}, Workspace: Workspace{SuggestedRoot: ".worktrees", ProvisionTimeoutSeconds: 1800, DeprovisionTimeoutSeconds: 600}}
}

func Load(root string) (Config, error) {
	cfg := DefaultConfig(root)
	b, err := os.ReadFile(filepath.Join(root, ConfigPath))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", ConfigPath, err)
	}
	return Normalize(root, cfg)
}

func Normalize(root string, cfg Config) (Config, error) {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return cfg, fmt.Errorf("unsupported project config version %d", cfg.Version)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = filepath.Base(root)
	}
	if cfg.Defaults.Workspace == "" {
		cfg.Defaults.Workspace = "local"
	}
	if cfg.Defaults.Harnesses == nil {
		cfg.Defaults.Harnesses = make(map[string]HarnessDefaults)
	}
	if cfg.Defaults.Harness != "" && (cfg.Defaults.Model != "" || cfg.Defaults.Effort != "" || cfg.Defaults.Mode != "") {
		if _, exists := cfg.Defaults.Harnesses[cfg.Defaults.Harness]; !exists {
			cfg.Defaults.Harnesses[cfg.Defaults.Harness] = HarnessDefaults{
				Model: cfg.Defaults.Model, Effort: cfg.Defaults.Effort, Mode: cfg.Defaults.Mode,
			}
		}
		cfg.Defaults.Model, cfg.Defaults.Effort, cfg.Defaults.Mode = "", "", ""
	}
	if cfg.Workspace.SuggestedRoot == "" {
		cfg.Workspace.SuggestedRoot = ".worktrees"
	}
	if cfg.Workspace.ProvisionTimeoutSeconds <= 0 {
		cfg.Workspace.ProvisionTimeoutSeconds = 1800
	}
	if cfg.Workspace.DeprovisionTimeoutSeconds <= 0 {
		cfg.Workspace.DeprovisionTimeoutSeconds = 600
	}
	for _, hook := range []string{cfg.Workspace.Provision, cfg.Workspace.Deprovision} {
		if hook == "" {
			continue
		}
		if filepath.IsAbs(hook) || strings.HasPrefix(filepath.Clean(hook), "..") {
			return cfg, fmt.Errorf("hook paths must stay inside the project")
		}
	}
	return cfg, nil
}

func Save(root string, cfg Config) (Config, error) {
	cfg, err := Normalize(root, cfg)
	if err != nil {
		return cfg, err
	}
	for name, hook := range map[string]string{"provision": cfg.Workspace.Provision, "deprovision": cfg.Workspace.Deprovision} {
		if hook != "" {
			if _, err := ResolveHook(root, hook); err != nil {
				return cfg, fmt.Errorf("%s hook: %w", name, err)
			}
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return cfg, err
	}
	dir := filepath.Join(root, ".omniplex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cfg, err
	}
	tmp, err := os.CreateTemp(dir, "project-*.json")
	if err != nil {
		return cfg, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return cfg, err
	}
	if err := tmp.Close(); err != nil {
		return cfg, err
	}
	if err := os.Rename(name, filepath.Join(root, ConfigPath)); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func ResolveHook(root, rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	abs := filepath.Join(root, filepath.Clean(rel))
	inside, err := filepath.Rel(root, abs)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hook path escapes project")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is not a script file", rel)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if info.Mode()&0o111 == 0 && ext != ".ts" && ext != ".mts" && ext != ".js" && ext != ".mjs" && ext != ".cjs" && ext != ".sh" {
		return "", fmt.Errorf("%s must be executable or use a supported script extension", rel)
	}
	return abs, nil
}
