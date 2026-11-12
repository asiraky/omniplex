package homemigrate

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// What each harness records as having started a conversation, when it was
// Omniplex: the Claude Agent SDK's entrypoint (Omniplex's bridge; a terminal
// is "cli", `claude -p` is "sdk-cli"), and the originator Omniplex gives
// Codex's app-server (`codex exec` is "codex_exec").
const (
	claudeOmniplex = "sdk-ts"
	codexOmniplex  = "omniplex"
)

// headLimit bounds how much of a transcript is read to learn who started it.
// The first lines are small records, and the one that says is among the first
// few; a transcript that has not said by then is left where it is.
const (
	headLimit  = 64 << 20
	headValues = 64
)

// scan lists the Omniplex-started conversations in a machine home, and counts
// the rest, which stay.
func scan(driver, root string) (found []*Conversation, kept int, err error) {
	switch driver {
	case DriverClaude:
		return scanClaude(root)
	case DriverCodex:
		return scanCodex(root)
	}
	return nil, 0, nil
}

// scanClaude reads projects/<key>/<id>.jsonl, each with its <id>/ folder of
// subagent transcripts and tool results beside it.
func scanClaude(root string) ([]*Conversation, int, error) {
	projects := filepath.Join(root, "projects")
	keys, err := os.ReadDir(projects)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var found []*Conversation
	kept := 0
	for _, key := range keys {
		if !key.IsDir() {
			continue
		}
		dir := filepath.Join(projects, key.Name())
		names, err := os.ReadDir(dir)
		if err != nil {
			return nil, 0, err
		}
		for _, n := range names {
			name := n.Name()
			if !n.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			path := filepath.Join(dir, name)
			origin, err := claudeEntrypoint(path)
			if err != nil || origin != claudeOmniplex {
				kept++
				continue
			}
			c := &Conversation{Driver: DriverClaude, ID: strings.TrimSuffix(name, ".jsonl"), Origin: origin}
			side := filepath.Join(dir, c.ID)
			if info, err := os.Lstat(side); err == nil && info.IsDir() {
				c.Entries = append(c.Entries, &Entry{Src: side, Dir: true})
			}
			c.Entries = append(c.Entries, &Entry{Src: path})
			if err := c.collect(); err != nil {
				return nil, 0, err
			}
			found = append(found, c)
		}
	}
	return found, kept, nil
}

// claudeEntrypoint is the entrypoint the first record carrying one names,
// "" when none does within the bound.
func claudeEntrypoint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, headLimit))
	for i := 0; i < headValues; i++ {
		var rec struct {
			Entrypoint *string `json:"entrypoint"`
		}
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				return "", nil
			}
			return "", err
		}
		if rec.Entrypoint != nil {
			return *rec.Entrypoint, nil
		}
	}
	return "", nil
}

// scanCodex reads every rollout under sessions/ and archived_sessions/. The
// first line of one is its session_meta, which names the originator.
func scanCodex(root string) ([]*Conversation, int, error) {
	var found []*Conversation
	kept := 0
	for _, sub := range []string{"sessions", "archived_sessions"} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && path == filepath.Join(root, sub) {
					return filepath.SkipDir
				}
				return err
			}
			name := d.Name()
			if !d.Type().IsRegular() || !isRollout(name) {
				return nil
			}
			id, origin, err := codexMeta(path)
			if err != nil || origin != codexOmniplex {
				kept++
				return nil
			}
			c := &Conversation{Driver: DriverCodex, ID: id, Origin: origin, Entries: []*Entry{{Src: path}}}
			if err := c.collect(); err != nil {
				return err
			}
			found = append(found, c)
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return found, kept, nil
}

func isRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
}

// codexMeta reads a rollout's session_meta: the conversation id and its
// originator. A rollout that does not open with one has no originator.
func codexMeta(path string) (id, originator string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ID         string `json:"id"`
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(f, headLimit)).Decode(&rec); err != nil {
		return "", "", err
	}
	if rec.Type != "session_meta" {
		return "", "", nil
	}
	return rec.Payload.ID, rec.Payload.Originator, nil
}

// collect lists every regular file of the conversation with its size, and
// the newest modification time among them and its folders.
func (c *Conversation) collect() error {
	c.Files, c.Bytes = nil, 0
	for _, e := range c.Entries {
		err := filepath.WalkDir(e.Src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.ModTime().After(c.modified) {
				c.modified = info.ModTime()
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			c.Files = append(c.Files, &File{Src: path, Size: info.Size()})
			c.Bytes += info.Size()
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Modified is the newest modification time among the conversation's files.
func (c *Conversation) Modified() time.Time { return c.modified }

// locateSidecar finds a Claude conversation's <id>/ folder in a config home,
// "" when there is none.
func locateSidecar(driver, root, id string) string {
	if driver != DriverClaude || root == "" || id == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(root, "projects", "*", id))
	for _, m := range matches {
		if info, err := os.Lstat(m); err == nil && info.IsDir() {
			return m
		}
	}
	return ""
}

// locate finds conversation id in a config home, "" when it is not there.
func locate(driver, root, id string) string {
	if root == "" || id == "" {
		return ""
	}
	switch driver {
	case DriverClaude:
		matches, _ := filepath.Glob(filepath.Join(root, "projects", "*", id+".jsonl"))
		for _, m := range matches {
			if info, err := os.Lstat(m); err == nil && info.Mode().IsRegular() {
				return m
			}
		}
	case DriverCodex:
		var hit string
		for _, sub := range []string{"sessions", "archived_sessions"} {
			_ = filepath.WalkDir(filepath.Join(root, sub), func(path string, d fs.DirEntry, err error) error {
				if err != nil || hit != "" {
					return filepath.SkipDir
				}
				if d.Type().IsRegular() && isRollout(d.Name()) && strings.HasSuffix(d.Name(), "-"+id+".jsonl") {
					hit = path
					return filepath.SkipAll
				}
				return nil
			})
			if hit != "" {
				return hit
			}
		}
	}
	return ""
}
