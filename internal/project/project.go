// Package project defines projects and the folders they point at. Both live
// in omniplex's database; nothing here is read from or written to a repo.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project is a named set of folders. Home is where omniplex puts new things
// for it (a new folder, a clone, an artefact), empty until first needed.
type Project struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Home      string   `json:"home,omitempty"`
	Defaults  Defaults `json:"defaults"`
	Folders   []Folder `json:"folders"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

// Defaults are what a new thread in the project starts with.
type Defaults struct {
	Harness   string                     `json:"harness,omitempty"`
	Harnesses map[string]HarnessDefaults `json:"harnesses,omitempty"`
	Workspace string                     `json:"workspace,omitempty"`
}

type HarnessDefaults struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// Folder is one directory a project points at. Git is found by looking, never
// stored: a folder can become a repo, or stop being one, behind omniplex's back.
type Folder struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Git        bool   `json:"git"`
	BaseBranch string `json:"baseBranch,omitempty"`
	// CopiesDir is where worktrees of this folder go, relative to it.
	CopiesDir                 string `json:"copiesDir"`
	Provision                 string `json:"provision,omitempty"`
	Deprovision               string `json:"deprovision,omitempty"`
	ProvisionTimeoutSeconds   int    `json:"provisionTimeoutSeconds"`
	DeprovisionTimeoutSeconds int    `json:"deprovisionTimeoutSeconds"`
}

// Folder returns the folder with this id.
func (p Project) Folder(id string) (Folder, bool) {
	for _, f := range p.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return Folder{}, false
}

// NewFolder is a folder with the defaults filled in.
func NewFolder(id, path string) Folder {
	return NormalizeFolder(Folder{ID: id, Path: path})
}

// NormalizeFolder fills in the defaults a folder is stored with.
func NormalizeFolder(f Folder) Folder {
	if strings.TrimSpace(f.CopiesDir) == "" {
		f.CopiesDir = ".worktrees"
	}
	if f.ProvisionTimeoutSeconds <= 0 {
		f.ProvisionTimeoutSeconds = 1800
	}
	if f.DeprovisionTimeoutSeconds <= 0 {
		f.DeprovisionTimeoutSeconds = 600
	}
	return f
}

// ValidateFolder refuses hook paths that point outside the folder. The hook
// scripts themselves live in the folder; only the pointers are stored. They
// need not exist yet: they are resolved when a copy is made. Copies may go
// anywhere, a sibling folder included.
func ValidateFolder(f Folder) error {
	for name, hook := range map[string]string{"provision": f.Provision, "deprovision": f.Deprovision} {
		if hook == "" {
			continue
		}
		if filepath.IsAbs(hook) || strings.HasPrefix(filepath.Clean(hook), "..") {
			return fmt.Errorf("%s hook must be a path inside %s", name, filepath.Base(f.Path))
		}
	}
	return nil
}

// NormalizeDefaults makes the harness map non-nil, so callers can index it.
func NormalizeDefaults(d Defaults) Defaults {
	if d.Workspace == "" {
		d.Workspace = "local"
	}
	if d.Harnesses == nil {
		d.Harnesses = map[string]HarnessDefaults{}
	}
	return d
}

// legacyFile is the .omniplex/project.json a project used to keep in its
// repo. It is read once, when a database from before folders is opened.
const legacyFile = ".omniplex/project.json"

type legacyConfig struct {
	Name     string `json:"name"`
	Defaults struct {
		Harness    string                     `json:"harness"`
		Harnesses  map[string]HarnessDefaults `json:"harnesses"`
		Workspace  string                     `json:"workspace"`
		BaseBranch string                     `json:"baseBranch"`
		Model      string                     `json:"model"`
		Effort     string                     `json:"effort"`
		Mode       string                     `json:"mode"`
	} `json:"defaults"`
	Workspace struct {
		SuggestedRoot             string `json:"suggestedRoot"`
		Provision                 string `json:"provision"`
		Deprovision               string `json:"deprovision"`
		ProvisionTimeoutSeconds   int    `json:"provisionTimeoutSeconds"`
		DeprovisionTimeoutSeconds int    `json:"deprovisionTimeoutSeconds"`
	} `json:"workspace"`
}

// Import turns an old project (its root and the settings the database cached
// for it) into a project with one folder. The repo's project.json wins over
// the cached copy when it is there, since it was the source of truth.
func Import(root string, cached []byte) (name string, d Defaults, f Folder) {
	var c legacyConfig
	_ = json.Unmarshal(cached, &c)
	if b, err := os.ReadFile(filepath.Join(root, legacyFile)); err == nil {
		var onDisk legacyConfig
		if json.Unmarshal(b, &onDisk) == nil {
			c = onDisk
		}
	}
	name = strings.TrimSpace(c.Name)
	if name == "" {
		name = filepath.Base(root)
	}
	d = Defaults{Harness: c.Defaults.Harness, Harnesses: c.Defaults.Harnesses, Workspace: c.Defaults.Workspace}
	d = NormalizeDefaults(d)
	// The oldest files kept one model for the default harness.
	if c.Defaults.Harness != "" && (c.Defaults.Model != "" || c.Defaults.Effort != "" || c.Defaults.Mode != "") {
		if _, ok := d.Harnesses[c.Defaults.Harness]; !ok {
			d.Harnesses[c.Defaults.Harness] = HarnessDefaults{Model: c.Defaults.Model, Effort: c.Defaults.Effort, Mode: c.Defaults.Mode}
		}
	}
	f = NormalizeFolder(Folder{
		Path:                      root,
		BaseBranch:                c.Defaults.BaseBranch,
		CopiesDir:                 c.Workspace.SuggestedRoot,
		Provision:                 c.Workspace.Provision,
		Deprovision:               c.Workspace.Deprovision,
		ProvisionTimeoutSeconds:   c.Workspace.ProvisionTimeoutSeconds,
		DeprovisionTimeoutSeconds: c.Workspace.DeprovisionTimeoutSeconds,
	})
	return name, d, f
}

// ResolveHook turns a hook path relative to a folder into an absolute script
// path, refusing one that escapes the folder or cannot be run.
func ResolveHook(root, rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("hook path escapes the folder")
	}
	abs := filepath.Join(root, filepath.Clean(rel))
	inside, err := filepath.Rel(root, abs)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("hook path escapes the folder")
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

// IsGit reports whether dir is inside a git working tree, by looking for a
// .git entry in it or any folder above it. A stat walk, not a git process,
// because it runs for every folder whenever the project list is sent.
func IsGit(dir string) bool {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}
