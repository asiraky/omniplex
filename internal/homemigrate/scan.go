package homemigrate

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Conversation is one harness conversation in a config home: the transcript
// and anything that belongs with it, which move together or not at all.
type Conversation struct {
	ID string `json:"id"`
	// Origin is what the harness recorded about who started it (Claude's
	// entrypoint, Codex's originator); empty when it recorded nothing.
	Origin string `json:"origin,omitempty"`
	// Owned is a conversation Omniplex started.
	Owned bool `json:"owned"`
	// Units are paths relative to the home: the transcript first, then its
	// sidecar folder when there is one.
	Units    []Unit    `json:"units"`
	Bytes    int64     `json:"bytes"`
	Files    int       `json:"files"`
	Modified time.Time `json:"modified"`
	// Reason says why a conversation that would move is not moving.
	Reason string `json:"reason,omitempty"`
	// Threads are the Omniplex threads that resume it.
	Threads []string `json:"threads,omitempty"`
}

// Unit is one path of a conversation, relative to the config home.
type Unit struct {
	Rel string `json:"rel"`
	Dir bool   `json:"dir,omitempty"`
}

// credentials are the files that hold a login. They are never moved or
// copied, whatever folder they turn up in.
var credentials = map[string]bool{".credentials.json": true, ".claude.json": true, "auth.json": true}

// scanner lists the conversations under one config home.
type scanner func(home string) ([]*Conversation, error)

// scanClaude lists projects/<key>/<id>.jsonl with its sibling <id>/ folder
// (subagent transcripts, tool results). A conversation is Omniplex's when the
// first line that carries an entrypoint says sdk-ts, the Agent SDK that
// Omniplex's bridge drives; the terminal (cli) and claude -p (sdk-cli) are
// the user's.
func scanClaude(home string) ([]*Conversation, error) {
	keys, err := os.ReadDir(filepath.Join(home, "projects"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Conversation
	for _, key := range keys {
		if !key.IsDir() {
			continue
		}
		dir := filepath.Join("projects", key.Name())
		entries, err := os.ReadDir(filepath.Join(home, dir))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			c := &Conversation{ID: id, Units: []Unit{{Rel: filepath.Join(dir, name)}}}
			if info, err := os.Lstat(filepath.Join(home, dir, id)); err == nil && info.IsDir() {
				c.Units = append(c.Units, Unit{Rel: filepath.Join(dir, id), Dir: true})
			}
			if err := measure(home, c); err != nil {
				return nil, err
			}
			c.Origin = claudeEntrypoint(filepath.Join(home, c.Units[0].Rel))
			c.Owned = c.Origin == "sdk-ts"
			out = append(out, c)
		}
	}
	return out, nil
}

// claudeEntrypoint is the entrypoint of the first transcript line that has
// one. Early lines (queue operations, summaries) carry none.
func claudeEntrypoint(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var l struct {
				Entrypoint string `json:"entrypoint"`
			}
			if json.Unmarshal(line, &l) == nil && l.Entrypoint != "" {
				return l.Entrypoint
			}
		}
		if err != nil {
			return ""
		}
	}
}

// codexRoots hold Codex's rollouts: live ones under sessions/YYYY/MM/DD/ and
// archived ones.
var codexRoots = []string{"sessions", "archived_sessions"}

// scanCodex lists every rollout. A conversation is Omniplex's when its
// session_meta line (always the first) has originator "omniplex", the client
// name Omniplex gives the app server; codex exec, the TUI and probes are not.
func scanCodex(home string) ([]*Conversation, error) {
	var out []*Conversation
	for _, root := range codexRoots {
		err := filepath.WalkDir(filepath.Join(home, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return err
			}
			name := d.Name()
			if d.IsDir() && strings.HasPrefix(name, ".") && path != filepath.Join(home, root) {
				return fs.SkipDir // an aside left by a move, or anything hidden
			}
			if !d.Type().IsRegular() || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			rel, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			c := &Conversation{Units: []Unit{{Rel: rel}}}
			if err := measure(home, c); err != nil {
				return err
			}
			c.ID, c.Origin = codexMeta(path)
			c.Owned = c.ID != "" && c.Origin == "omniplex"
			out = append(out, c)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// codexMeta reads the id and originator from a rollout's session_meta line.
func codexMeta(path string) (id, originator string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, 1<<16).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", ""
	}
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			ID         string `json:"id"`
			Originator string `json:"originator"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "session_meta" {
		return "", ""
	}
	return l.Payload.ID, l.Payload.Originator
}

// measure fills a conversation's size, file count and newest modification
// time over every unit, including everything in a sidecar folder.
func measure(home string, c *Conversation) error {
	c.Bytes, c.Files, c.Modified = 0, 0, time.Time{}
	for _, u := range c.Units {
		err := filepath.WalkDir(filepath.Join(home, u.Rel), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.ModTime().After(c.Modified) {
				c.Modified = info.ModTime()
			}
			if info.Mode().IsRegular() {
				c.Bytes += info.Size()
				c.Files++
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// holdsCredentials reports a conversation with a file named like a login
// anywhere in it. It is never moved.
func holdsCredentials(home string, c *Conversation) bool {
	found := false
	for _, u := range c.Units {
		_ = filepath.WalkDir(filepath.Join(home, u.Rel), func(path string, d fs.DirEntry, err error) error {
			if err == nil && credentials[d.Name()] {
				found = true
				return fs.SkipAll
			}
			return nil
		})
	}
	return found
}

// present reports whether any unit of c exists under home.
func present(home string, c *Conversation) bool {
	for _, u := range c.Units {
		if _, err := os.Lstat(filepath.Join(home, u.Rel)); err == nil {
			return true
		}
	}
	return false
}
