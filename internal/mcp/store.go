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

// Member finds a server or one of its accounts (see Server.members) by the
// name sessions know it by, with the server it belongs to.
func (s *Store) Member(name string) (member, parent Server, ok bool, err error) {
	f, err := s.Read()
	if err != nil {
		return Server{}, Server{}, false, err
	}
	for _, srv := range f.Servers {
		for _, m := range srv.members() {
			if m.Name == name {
				return m, srv, true, nil
			}
		}
	}
	return Server{}, Server{}, false, nil
}

// Values reads a server's env and header values out of the secret store. An
// account gets the server's, with its own in their place.
func (s *Store) Values(srv Server) (env, headers map[string]string) {
	env, headers = map[string]string{}, map[string]string{}
	ids := []string{srv.Name}
	if srv.parent != "" {
		ids = []string{srv.parent, srv.Name}
	}
	for _, id := range ids {
		for _, n := range srv.EnvNames {
			if v, ok := s.secrets.Get(id, envKey+n); ok {
				env[n] = v
			}
		}
		for _, n := range srv.HeaderNames {
			if v, ok := s.secrets.Get(id, headerKey+n); ok {
				headers[n] = v
			}
		}
	}
	return env, headers
}

// taken lists every name sessions see, servers and accounts alike, leaving
// out the server at index skip.
func taken(f *File, skip int) map[string]bool {
	out := map[string]bool{}
	for i, srv := range f.Servers {
		if i == skip {
			continue
		}
		for _, m := range srv.members() {
			out[m.Name] = true
		}
	}
	return out
}

// carry moves what is stored under one secret id to another (which may be
// the same id), keeping only the env and header values named and, with
// oauth, the sign-in. Anything else stored under either id is dropped.
func (s *Store) carry(from, to string, env, headers []string, oauth bool) error {
	kept := map[string]string{}
	keys := make([]string, 0, len(env)+len(headers)+1)
	for _, n := range env {
		keys = append(keys, envKey+n)
	}
	for _, n := range headers {
		keys = append(keys, headerKey+n)
	}
	if oauth {
		keys = append(keys, OAuthKey)
	}
	for _, k := range keys {
		if v, ok := s.secrets.Get(from, k); ok {
			kept[k] = v
		}
	}
	if err := s.secrets.Purge(from); err != nil {
		return err
	}
	if err := s.secrets.Purge(to); err != nil {
		return err
	}
	for k, v := range kept {
		if err := s.secrets.Put(to, k, v); err != nil {
			return err
		}
	}
	return nil
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
		others := taken(f, oldIdx)
		if others[d.Name] {
			return fmt.Errorf("there is already an MCP server named %q", d.Name)
		}
		var old Server
		if oldIdx >= 0 {
			old = f.Servers[oldIdx]
		}
		// The accounts come along, keeping their own values for the
		// names the server still has.
		for _, a := range old.Accounts {
			name := AccountName(srv.Name, a.Label)
			if err := CheckName(name); err != nil {
				return fmt.Errorf("account %s: %w", a.Label, err)
			}
			if others[name] {
				return fmt.Errorf("account %s would be named %q, which another server has", a.Label, name)
			}
			a.EnvNames = keepOnly(a.EnvNames, srv.EnvNames)
			a.HeaderNames = keepOnly(a.HeaderNames, srv.HeaderNames)
			srv.Accounts = append(srv.Accounts, a)
		}
		if err := s.moveSecrets(old, srv, d); err != nil {
			return err
		}
		keepOAuth := old.URL != "" && old.URL == srv.URL
		for _, a := range srv.Accounts {
			if err := s.carry(AccountName(old.Name, a.Label), AccountName(srv.Name, a.Label), a.EnvNames, a.HeaderNames, keepOAuth); err != nil {
				return err
			}
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

// RemoveServer deletes a server, its accounts, and every secret they have.
func (s *Store) RemoveServer(name string) error {
	return s.update(func(f *File) error {
		i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == name })
		if i < 0 {
			return fmt.Errorf("no MCP server named %q", name)
		}
		members := f.Servers[i].members()
		f.Servers = slices.Delete(f.Servers, i, i+1)
		for _, m := range members {
			if err := s.secrets.Purge(m.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

// AccountDraft is an account of a server as the user types it. A value
// given is the account's own; a name present with an empty value keeps the
// account's own value if it has one; a name left out uses the server's.
type AccountDraft struct {
	Label   string            `json:"label"`
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
}

// SaveServerAccount adds an account to a server, or replaces the one
// labelled previous (which may be relabelled, keeping its sign-in).
func (s *Store) SaveServerAccount(server string, d AccountDraft, previous string) (Server, error) {
	d.Label = strings.TrimSpace(d.Label)
	if err := CheckName(d.Label); err != nil {
		return Server{}, err
	}
	var out Server
	err := s.update(func(f *File) error {
		i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == server })
		if i < 0 {
			return fmt.Errorf("no MCP server named %q", server)
		}
		srv := &f.Servers[i]
		prev := -1
		if previous != "" {
			prev = slices.IndexFunc(srv.Accounts, func(a ServerAccount) bool { return a.Label == previous })
			if prev < 0 {
				return fmt.Errorf("%s has no account %q", server, previous)
			}
		}
		name := AccountName(server, d.Label)
		if err := CheckName(name); err != nil {
			return fmt.Errorf("the account would be named %q, which is too long; use a shorter label", name)
		}
		if previous != d.Label {
			if slices.ContainsFunc(srv.Accounts, func(a ServerAccount) bool { return a.Label == d.Label }) {
				return fmt.Errorf("%s already has an account %q", server, d.Label)
			}
			if taken(f, -1)[name] {
				return fmt.Errorf("there is already an MCP server named %q", name)
			}
		}
		var old ServerAccount
		oldName := name
		if prev >= 0 {
			old = srv.Accounts[prev]
			oldName = AccountName(server, previous)
		}
		acct := ServerAccount{Label: d.Label}
		values := map[string]string{}
		pick := func(prefix string, given map[string]string, allowed, had []string, kind string) ([]string, error) {
			var names []string
			for n, v := range given {
				if !slices.Contains(allowed, n) {
					return nil, fmt.Errorf("%s has no %s %q", server, kind, n)
				}
				if v == "" {
					stored, ok := s.secrets.Get(oldName, prefix+n)
					if !ok || !slices.Contains(had, n) {
						continue
					}
					v = stored
				}
				values[prefix+n] = v
				names = append(names, n)
			}
			sort.Strings(names)
			return names, nil
		}
		var err error
		if acct.EnvNames, err = pick(envKey, d.Env, srv.EnvNames, old.EnvNames, "environment variable"); err != nil {
			return err
		}
		if acct.HeaderNames, err = pick(headerKey, d.Headers, srv.HeaderNames, old.HeaderNames, "header"); err != nil {
			return err
		}
		if prev >= 0 {
			if v, ok := s.secrets.Get(oldName, OAuthKey); ok {
				values[OAuthKey] = v
			}
		}
		if err := s.secrets.Purge(oldName); err != nil {
			return err
		}
		if err := s.secrets.Purge(name); err != nil {
			return err
		}
		for k, v := range values {
			if err := s.secrets.Put(name, k, v); err != nil {
				return err
			}
		}
		if prev >= 0 {
			srv.Accounts[prev] = acct
		} else {
			srv.Accounts = append(srv.Accounts, acct)
		}
		out = *srv
		return nil
	})
	return out, err
}

// RemoveServerAccount removes one account of a server and its secrets.
func (s *Store) RemoveServerAccount(server, label string) (Server, error) {
	var out Server
	err := s.update(func(f *File) error {
		i := slices.IndexFunc(f.Servers, func(x Server) bool { return x.Name == server })
		if i < 0 {
			return fmt.Errorf("no MCP server named %q", server)
		}
		j := slices.IndexFunc(f.Servers[i].Accounts, func(a ServerAccount) bool { return a.Label == label })
		if j < 0 {
			return fmt.Errorf("%s has no account %q", server, label)
		}
		f.Servers[i].Accounts = slices.Delete(f.Servers[i].Accounts, j, j+1)
		out = f.Servers[i]
		return s.secrets.Purge(AccountName(server, label))
	})
	return out, err
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

// keepOnly is names, less any not in allowed.
func keepOnly(names, allowed []string) []string {
	var out []string
	for _, n := range names {
		if slices.Contains(allowed, n) {
			out = append(out, n)
		}
	}
	return out
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
