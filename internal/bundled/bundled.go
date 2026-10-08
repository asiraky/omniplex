// Package bundled is omniplex's own plugin: the omniplex skill, which tells
// an agent how to manage MCP servers, skills and sign-ins through omniplex's
// tools. It ships in the binary and is written out to omniplex's data
// directory at start, never into a user's or a project's skills library.
// Each session is pointed at it for that session alone.
package bundled

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

//go:embed all:plugin
var files embed.FS

// Extract writes the plugin to dir, laid out as a Claude plugin
// (.claude-plugin/plugin.json, skills/<name>/SKILL.md), and returns dir. A
// file whose content already matches is left alone; anything in dir the
// binary does not carry is removed, so a skill dropped from the binary leaves
// sessions too.
func Extract(dir string) (string, error) {
	sub, err := fs.Sub(files, "plugin")
	if err != nil {
		return "", err
	}
	want := map[string]bool{}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want[p] = true
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		return writeIfChanged(filepath.Join(dir, filepath.FromSlash(p)), data)
	})
	if err != nil {
		return "", err
	}
	return dir, prune(dir, want)
}

func writeIfChanged(dst string, data []byte) error {
	if have, err := os.ReadFile(dst); err == nil && bytes.Equal(have, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// A session can be reading the file: swap it in whole.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// prune removes every file under the plugin's own folders in dir that is not
// in want, then the folders left empty. Only those folders: dir is never
// emptied wholesale, whatever it turns out to be.
func prune(dir string, want map[string]bool) error {
	var dirs []string
	for _, top := range []string{".claude-plugin", "skills"} {
		err := filepath.WalkDir(filepath.Join(dir, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				dirs = append(dirs, p)
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			if !want[path.Clean(filepath.ToSlash(rel))] {
				return os.Remove(p)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	// Deepest first; a folder still holding files stays.
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
	return nil
}
