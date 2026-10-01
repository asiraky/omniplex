// Package mcp keeps the MCP servers and command-line sign-ins ("connections")
// Omniplex manages for the user: their definitions, their credentials, signing
// in to them, and handing them to each agent session at start.
package mcp

import "errors"

// Server is one MCP server the user added to Omniplex. Exactly one of URL
// (a remote streamable-HTTP server) and Command (a local process) is set.
// Header and env values are credentials and live in the secret store, keyed
// by the server's name; only their names are here.
type Server struct {
	Name    string   `json:"name"`
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
	// Accounts are more sign-ins to the same server, say a work and a
	// personal Cloudflare. Each reaches the agent as a server of its own,
	// named by AccountName, so its tools are told apart by name. The server
	// itself stays the first account, under its own name.
	Accounts []ServerAccount `json:"accounts,omitempty"`

	// parent is set on an account as members gives it: the server whose
	// definition and values it shares.
	parent string
}

// ServerAccount is one more account of a server. It has its own OAuth
// tokens and, for the names listed, its own header and env values in place
// of the server's, kept in the secret store under its AccountName.
type ServerAccount struct {
	Label       string   `json:"label"`
	EnvNames    []string `json:"envNames,omitempty"`
	HeaderNames []string `json:"headerNames,omitempty"`
}

// AccountName is the name an account of a server goes to the agent under.
func AccountName(server, label string) string { return server + "-" + label }

// members is the server as sessions see it: itself, then one server per
// account, sharing its definition and switches.
func (s Server) members() []Server {
	out := make([]Server, 0, 1+len(s.Accounts))
	out = append(out, s)
	for _, a := range s.Accounts {
		m := s
		m.Name, m.Accounts, m.parent = AccountName(s.Name, a.Label), nil, s.Name
		out = append(out, m)
	}
	return out
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
