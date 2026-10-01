package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// File is connections.json: the user's MCP servers and command-line
// sign-ins. It sits beside config.json rather than inside it, because the
// settings page saves config.json whole from what it last read, which would
// wipe anything it does not know about.
type File struct {
	Version int      `json:"version"`
	Servers []Server `json:"servers"`
	CLIs    []CLI    `json:"clis"`
}

// Draft is a server as the user types it: values included. An env variable
// or header present with an empty value keeps whatever is stored for it.
type Draft struct {
	Name    string            `json:"name"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
	Off     []string          `json:"off,omitempty"`
}

// Secret keys under a server's id in the secret store.
const (
	headerKey = "header."
	envKey    = "env."
	// OAuthKey holds the server's OAuth tokens and client registration.
	OAuthKey = "oauth"
)

// ReservedName is the server omniplex runs itself.
const ReservedName = "omniplex"

var (
	nameRule   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)
	envRule    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerRule = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// CheckName refuses a server name or CLI id outside the naming rule.
func CheckName(name string) error {
	if !nameRule.MatchString(name) {
		return fmt.Errorf("%q: use lowercase letters, digits, - and _, starting with a letter or digit, at most 48 characters", name)
	}
	return nil
}

// Store keeps connections.json and the secret store beside it. Every write
// goes through one mutex, so two devices saving at once cannot interleave.
type Store struct {
	path    string
	secrets *provider.SecretStore
	mu      sync.Mutex
}

// Dir is the omniplex directory: the one config.json lives in, which
// OMNIPLEX_CONFIG moves.
func Dir() (string, error) {
	p, err := userconfig.Path()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// OpenStore opens the store in dir, creating the secret store if needed.
func OpenStore(dir string) (*Store, error) {
	secrets, err := provider.OpenSecretStoreAt(filepath.Join(dir, "mcp-secrets"))
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "connections.json"), secrets: secrets}, nil
}

// Secrets is the store server credentials and tokens live in.
func (s *Store) Secrets() *provider.SecretStore { return s.secrets }

// Read returns the file; a missing one is empty.
func (s *Store) Read() (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

func (s *Store) readLocked() (File, error) {
	f := File{Version: 1}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", s.path, err)
	}
	if f.Version != 1 {
		return f, fmt.Errorf("%s: unsupported version %d", s.path, f.Version)
	}
	return f, nil
}

// update reads, changes and writes the file under the lock.
func (s *Store) update(fn func(*File) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readLocked()
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	if f.Servers == nil {
		f.Servers = []Server{}
	}
	if f.CLIs == nil {
		f.CLIs = []CLI{}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, append(b, '\n'))
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".connections-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Server returns one server by name.
func (s *Store) Server(name string) (Server, bool, error) {
	f, err := s.Read()
	if err != nil {
		return Server{}, false, err
	}
	i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == name })
	if i < 0 {
		return Server{}, false, nil
	}
	return f.Servers[i], true, nil
}

// ErrServerChanged refuses a token for a server that was removed, renamed or
// given a new URL while it was being signed in.
var ErrServerChanged = errors.New("the server changed while signing in. Sign in again")

// whileCurrent runs write under the store's lock if name still names a
// server at url, else returns ErrServerChanged. Holding the lock keeps a
// rename or removal from slipping in between the check and the write.
func (s *Store) whileCurrent(name, url string, write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readLocked()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(f.Servers, func(x Server) bool { return x.Name == name && x.URL == url }) {
		return write()
	}
	return ErrServerChanged
}

// Values reads a server's env and header values out of the secret store.
func (s *Store) Values(srv Server) (env, headers map[string]string) {
	env, headers = map[string]string{}, map[string]string{}
	for _, n := range srv.EnvNames {
		if v, ok := s.secrets.Get(srv.Name, envKey+n); ok {
			env[n] = v
		}
	}
	for _, n := range srv.HeaderNames {
		if v, ok := s.secrets.Get(srv.Name, headerKey+n); ok {
			headers[n] = v
		}
	}
	return env, headers
}

// checkDraft validates a draft's shape, returning its env and header names
// sorted.
func checkDraft(d Draft) (envNames, headerNames []string, err error) {
	if err := CheckName(d.Name); err != nil {
		return nil, nil, err
	}
	if d.Name == ReservedName {
		return nil, nil, fmt.Errorf("%q is the name of omniplex's own server; choose another", d.Name)
	}
	switch {
	case d.URL != "" && d.Command != "":
		return nil, nil, errors.New("give either a URL or a command, not both")
	case d.URL == "" && d.Command == "":
		return nil, nil, errors.New("give the server's URL or the command that runs it")
	case d.URL != "":
		u, err := url.Parse(d.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, nil, fmt.Errorf("%q is not an http or https URL", d.URL)
		}
		if len(d.Env) > 0 {
			return nil, nil, errors.New("environment variables only apply to a server run by a command")
		}
		if len(d.Args) > 0 {
			return nil, nil, errors.New("arguments only apply to a server run by a command")
		}
	default:
		if len(d.Headers) > 0 {
			return nil, nil, errors.New("headers only apply to a server at a URL")
		}
	}
	for n := range d.Env {
		if !envRule.MatchString(n) {
			return nil, nil, fmt.Errorf("%q is not a valid environment variable name", n)
		}
		envNames = append(envNames, n)
	}
	seen := map[string]bool{}
	for n := range d.Headers {
		if !headerRule.MatchString(n) {
			return nil, nil, fmt.Errorf("%q is not a valid header name", n)
		}
		if seen[strings.ToLower(n)] {
			return nil, nil, fmt.Errorf("header %q is given twice", n)
		}
		seen[strings.ToLower(n)] = true
		headerNames = append(headerNames, n)
	}
	sort.Strings(envNames)
	sort.Strings(headerNames)
	return envNames, headerNames, nil
}

// SaveServer adds a server, or replaces the one named previous (which may be
// renamed). Values travel to the secret store: an empty value keeps the
// stored one, and a name left out is deleted. Renaming moves the secrets;
// changing the URL drops the OAuth sign-in, which was for the old one.
func (s *Store) SaveServer(d Draft, previous string) (Server, error) {
	d.Name = strings.TrimSpace(d.Name)
	d.URL = strings.TrimSpace(d.URL)
	d.Command = strings.TrimSpace(d.Command)
	envNames, headerNames, err := checkDraft(d)
	if err != nil {
		return Server{}, err
	}
	srv := Server{
		Name: d.Name, URL: d.URL, Command: d.Command, Args: d.Args,
		EnvNames: envNames, HeaderNames: headerNames, Off: cleanList(d.Off),
	}
	err = s.update(func(f *File) error {
		oldIdx := -1
		if previous != "" {
			oldIdx = slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == previous })
			if oldIdx < 0 {
				return fmt.Errorf("no MCP server named %q", previous)
			}
		}
		if previous != d.Name && slices.ContainsFunc(f.Servers, func(x Server) bool { return x.Name == d.Name }) {
			return fmt.Errorf("there is already an MCP server named %q", d.Name)
		}
		var old Server
		if oldIdx >= 0 {
			old = f.Servers[oldIdx]
		}
		if err := s.moveSecrets(old, srv, d); err != nil {
			return err
		}
		if oldIdx >= 0 {
			f.Servers[oldIdx] = srv
		} else {
			f.Servers = append(f.Servers, srv)
		}
		return nil
	})
	if err != nil {
		return Server{}, err
	}
	return srv, nil
}

// moveSecrets writes the new server's values, reading kept ones from the old
// server (empty Name means there was none), and removes what the new one no
// longer has.
func (s *Store) moveSecrets(old, srv Server, d Draft) error {
	if old.Name == "" {
		// Nothing left behind by an earlier server of this name survives.
		if err := s.secrets.Purge(srv.Name); err != nil {
			return err
		}
	}
	put := func(key, value string, had bool) error {
		if value == "" && had {
			if v, ok := s.secrets.Get(old.Name, key); ok {
				if old.Name == srv.Name {
					return nil
				}
				value = v
			}
		}
		return s.secrets.Put(srv.Name, key, value)
	}
	for _, n := range srv.EnvNames {
		if err := put(envKey+n, d.Env[n], slices.Contains(old.EnvNames, n)); err != nil {
			return err
		}
	}
	for _, n := range srv.HeaderNames {
		if err := put(headerKey+n, d.Headers[n], slices.Contains(old.HeaderNames, n)); err != nil {
			return err
		}
	}
	keepOAuth := old.Name != "" && old.URL != "" && old.URL == srv.URL
	if old.Name != "" && old.Name != srv.Name {
		if keepOAuth {
			if v, ok := s.secrets.Get(old.Name, OAuthKey); ok {
				if err := s.secrets.Put(srv.Name, OAuthKey, v); err != nil {
					return err
				}
			}
		}
		return s.secrets.Purge(old.Name)
	}
	if old.Name == "" {
		return nil
	}
	for _, n := range old.EnvNames {
		if !slices.Contains(srv.EnvNames, n) {
			if err := s.secrets.Delete(old.Name, envKey+n); err != nil {
				return err
			}
		}
	}
	for _, n := range old.HeaderNames {
		if !slices.Contains(srv.HeaderNames, n) {
			if err := s.secrets.Delete(old.Name, headerKey+n); err != nil {
				return err
			}
		}
	}
	if !keepOAuth {
		return s.secrets.Delete(old.Name, OAuthKey)
	}
	return nil
}

// SetOff changes only which harnesses do not get a server.
func (s *Store) SetOff(name string, off []string) (Server, error) {
	var out Server
	err := s.update(func(f *File) error {
		i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == name })
		if i < 0 {
			return fmt.Errorf("no MCP server named %q", name)
		}
		f.Servers[i].Off = cleanList(off)
		out = f.Servers[i]
		return nil
	})
	return out, err
}

// RemoveServer deletes a server and every secret it has.
func (s *Store) RemoveServer(name string) error {
	return s.update(func(f *File) error {
		i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == name })
		if i < 0 {
			return fmt.Errorf("no MCP server named %q", name)
		}
		f.Servers = slices.Delete(f.Servers, i, i+1)
		return s.secrets.Purge(name)
	})
}

// SaveCLI adds a CLI, or replaces the one with id previous (which may be
// renamed). An existing CLI keeps its accounts; a new one starts with the
// accounts it was given.
func (s *Store) SaveCLI(c CLI, previous string) (CLI, error) {
	if err := CheckName(c.ID); err != nil {
		return CLI{}, err
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		c.Name = c.ID
	}
	if strings.TrimSpace(c.StatusCommand) == "" {
		return CLI{}, errors.New("give the command that checks whether an account is signed in")
	}
	if strings.TrimSpace(c.SignInCommand) == "" {
		return CLI{}, errors.New("give the command that signs an account in")
	}
	if c.SignedInPattern != "" {
		if _, err := regexp.Compile(c.SignedInPattern); err != nil {
			return CLI{}, fmt.Errorf("signed-in pattern: %v", err)
		}
	}
	for n := range c.AccountEnv {
		if !envRule.MatchString(n) {
			return CLI{}, fmt.Errorf("%q is not a valid environment variable name", n)
		}
	}
	err := s.update(func(f *File) error {
		oldIdx := -1
		if previous != "" {
			oldIdx = slices.IndexFunc(f.CLIs, func(x CLI) bool { return x.ID == previous })
			if oldIdx < 0 {
				return fmt.Errorf("no sign-in named %q", previous)
			}
		}
		if previous != c.ID && slices.ContainsFunc(f.CLIs, func(x CLI) bool { return x.ID == c.ID }) {
			return fmt.Errorf("there is already a sign-in named %q", c.ID)
		}
		if oldIdx >= 0 {
			c.Accounts = f.CLIs[oldIdx].Accounts
			f.CLIs[oldIdx] = c
			return nil
		}
		for _, a := range c.Accounts {
			if err := CheckName(a.Name); err != nil {
				return err
			}
		}
		f.CLIs = append(f.CLIs, c)
		return nil
	})
	if err != nil {
		return CLI{}, err
	}
	if c.Accounts == nil {
		c.Accounts = []Account{}
	}
	return c, nil
}

// RemoveCLI deletes a CLI. Its accounts' own credential folders are the
// tool's, and stay.
func (s *Store) RemoveCLI(id string) error {
	return s.update(func(f *File) error {
		i := slices.IndexFunc(f.CLIs, func(x CLI) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("no sign-in named %q", id)
		}
		f.CLIs = slices.Delete(f.CLIs, i, i+1)
		return nil
	})
}

// AddAccount adds an account to a CLI, its env made from the CLI's
// AccountEnv with {account} replaced by the account's name.
func (s *Store) AddAccount(id, name string) (CLI, error) {
	if err := CheckName(name); err != nil {
		return CLI{}, err
	}
	return s.changeCLI(id, func(c *CLI) error {
		if slices.ContainsFunc(c.Accounts, func(a Account) bool { return a.Name == name }) {
			return fmt.Errorf("%s already has an account named %q", c.Name, name)
		}
		acct := Account{Name: name}
		if len(c.AccountEnv) > 0 {
			acct.Env = map[string]string{}
			for k, v := range c.AccountEnv {
				acct.Env[k] = strings.ReplaceAll(v, "{account}", name)
			}
		}
		c.Accounts = append(c.Accounts, acct)
		return nil
	})
}

// RemoveAccount removes one account from a CLI.
func (s *Store) RemoveAccount(id, name string) (CLI, error) {
	return s.changeCLI(id, func(c *CLI) error {
		i := slices.IndexFunc(c.Accounts, func(a Account) bool { return a.Name == name })
		if i < 0 {
			return fmt.Errorf("%s has no account named %q", c.Name, name)
		}
		c.Accounts = slices.Delete(c.Accounts, i, i+1)
		return nil
	})
}

func (s *Store) changeCLI(id string, fn func(*CLI) error) (CLI, error) {
	var out CLI
	err := s.update(func(f *File) error {
		i := slices.IndexFunc(f.CLIs, func(x CLI) bool { return x.ID == id })
		if i < 0 {
			return fmt.Errorf("no sign-in named %q", id)
		}
		if err := fn(&f.CLIs[i]); err != nil {
			return err
		}
		out = f.CLIs[i]
		return nil
	})
	if out.Accounts == nil {
		out.Accounts = []Account{}
	}
	return out, err
}

// cleanList sorts and dedupes, dropping empties.
func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
