// Package mcp keeps the MCP servers and command-line sign-ins ("connections")
// Omniplex manages for the user: their definitions, their credentials, signing
// in to them, and handing them to each agent session at start.
package mcp

import (
	"errors"
	"strings"
)

// Server is one MCP server the user added to Omniplex. Exactly one of URL
// (a remote streamable-HTTP server) and Command (a local process) is set.
// Header and env values are credentials and live in the secret store, keyed
// by the server's key; only their names are here.
type Server struct {
	Name string `json:"name"`
	// Project is the ID of the one project whose threads get the server;
	// empty means every thread. Names are unique within a project, and
	// among the servers that go everywhere.
	Project string   `json:"project,omitempty"`
	URL     string   `json:"url,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// EnvNames and HeaderNames list the variables and headers that have a
	// value in the secret store.
	EnvNames    []string `json:"envNames,omitempty"`
	HeaderNames []string `json:"headerNames,omitempty"`
	// Off lists the harnesses (adapter ids) that do not get the server.
	// Every other harness that implements adapter.MCPHost and runs this kind
	// of server does, including one added after the server was.
	Off []string `json:"off,omitempty"`
	// OffIn lists the projects that do not get a server that goes
	// everywhere. A project's own servers never have it.
	OffIn []string `json:"offIn,omitempty"`
}

// Key tells servers apart across scopes; secrets, sign-ins and checks are
// kept under it. A server that goes everywhere keys by its name alone, so
// the secrets it had before projects existed are still its.
func (s Server) Key() string { return ServerKey(s.Name, s.Project) }

// ServerKey is the key of the server named name in project ("" for one that
// goes everywhere): the name, or <project>/<name>.
func ServerKey(name, project string) string {
	if project == "" {
		return name
	}
	return project + "/" + name
}

// secretID is where a server key's secrets live in the secret store, whose
// ids cannot hold a slash. Names never hold a dot, so <project>.<name> can
// not be mistaken for a server that goes everywhere, or for another
// project's server.
func secretID(key string) string {
	return strings.Replace(key, "/", ".", 1)
}

// CLI is a command-line tool that holds its own sign-in, with one entry per
// account. Every command runs through sh -c with the account's env added.
type CLI struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// StatusCommand reports whether an account is signed in. With
	// SignedInPattern empty, exit status 0 means signed in; otherwise the
	// pattern (a Go regexp) must match its combined output.
	StatusCommand   string `json:"statusCommand"`
	SignedInPattern string `json:"signedInPattern,omitempty"`
	// SignInCommand prints a URL to open and, usually, waits on a localhost
	// port for the browser to come back to it.
	SignInCommand string `json:"signInCommand"`
	// PrepareCommand runs before SignInCommand, e.g. to put a client secret
	// into a new account's config folder.
	PrepareCommand string `json:"prepareCommand,omitempty"`
	// AccountEnv is the env a new account starts with; "{account}" in a
	// value is replaced by the account's name.
	AccountEnv map[string]string `json:"accountEnv,omitempty"`
	Accounts   []Account         `json:"accounts"`
}

// Account is one sign-in of a CLI.
type Account struct {
	Name string            `json:"name"`
	Env  map[string]string `json:"env,omitempty"`
}

// A sign-in flow talks to the person through adapter.AuthInteraction, so it
// runs under the existing auth flow engine and dialog.

// ErrSignInNeeded means a server or account has no usable credential.
var ErrSignInNeeded = errors.New("sign in needed")
