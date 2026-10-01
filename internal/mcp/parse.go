package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// errUnrecognised is what a paste that is none of the known shapes gets.
var errUnrecognised = errors.New("paste a URL, a claude mcp add or codex mcp add command, or the server's JSON or TOML")

// Parse turns whatever the user pasted into a draft: a bare URL, a
// `claude mcp add` or `codex mcp add` command line, a JSON block (an
// mcpServers map or one server object), or a TOML [mcp_servers.x] block.
// The name comes from the input when it has one, else from the URL's host or
// the command.
func Parse(text string) (Draft, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "$ ")
	if text == "" {
		return Draft{}, errUnrecognised
	}
	var (
		d   Draft
		err error
	)
	switch {
	case strings.HasPrefix(text, "{") || strings.HasPrefix(text, `"`):
		d, err = parseJSON(text)
	case strings.HasPrefix(text, "[") || strings.HasPrefix(text, "mcp_servers."):
		d, err = parseTOML(text)
	default:
		d, err = parseCommandLine(text)
	}
	if err != nil {
		return Draft{}, err
	}
	return finish(d), nil
}

// finish fills the name and the empty maps, and turns an mcp-remote bridge
// into the remote server it bridges to, so omniplex signs in to it itself.
func finish(d Draft) Draft {
	if d.Env == nil {
		d.Env = map[string]string{}
	}
	if d.Headers == nil {
		d.Headers = map[string]string{}
	}
	if d.Command != "" {
		d = unbridge(d)
	}
	if d.Name != "" {
		d.Name = slugName(d.Name)
	}
	if d.Name == "" && d.URL != "" {
		d.Name = nameFromURL(d.URL)
	}
	if d.Name == "" && d.Command != "" {
		d.Name = nameFromCommand(d.Command, d.Args)
	}
	return d
}

// --- command lines ---

func parseCommandLine(text string) (Draft, error) {
	words, err := shellWords(text)
	if err != nil {
		return Draft{}, err
	}
	if len(words) == 0 {
		return Draft{}, errUnrecognised
	}
	if len(words) == 1 && isURL(words[0]) {
		return Draft{URL: words[0]}, nil
	}
	if len(words) >= 3 && words[1] == "mcp" {
		switch {
		case words[0] == "claude" && words[2] == "add":
			return parseClaudeAdd(words[3:])
		case words[0] == "claude" && words[2] == "add-json":
			return parseClaudeAddJSON(words[3:])
		case words[0] == "codex" && words[2] == "add":
			return parseCodexAdd(words[3:])
		}
	}
	if strings.HasPrefix(words[0], "-") || strings.ContainsAny(words[0], "{}[]") {
		return Draft{}, errUnrecognised
	}
	// A bare command line is a server run by that command.
	return Draft{Command: words[0], Args: words[1:]}, nil
}

var (
	envPair    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	headerPair = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+:")
)

// optValue splits "--opt=value" and fetches the next word for "--opt value".
func optValue(words []string, i int) (name, value string, next int, ok bool) {
	w := words[i]
	if strings.HasPrefix(w, "--") {
		if k, v, found := strings.Cut(w, "="); found {
			return k, v, i, true
		}
	}
	if i+1 >= len(words) {
		return w, "", i, false
	}
	return w, words[i+1], i + 1, true
}

func addEnv(d *Draft, pair string) {
	k, v, _ := strings.Cut(pair, "=")
	if d.Env == nil {
		d.Env = map[string]string{}
	}
	d.Env[k] = v
}

func addHeader(d *Draft, line string) {
	k, v, _ := strings.Cut(line, ":")
	if d.Headers == nil {
		d.Headers = map[string]string{}
	}
	d.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
}

// parseClaudeAdd reads `claude mcp add [options] <name> <commandOrUrl>
// [args...]`. -e and -H take several values, so a run of KEY=VALUE or
// "Name: value" words after one belongs to it.
func parseClaudeAdd(words []string) (Draft, error) {
	var d Draft
	var positional []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			positional = append(positional, words[i+1:]...)
			break
		}
		// Once there is a command, an option claude does not know is one of
		// the command's own arguments.
		if !strings.HasPrefix(w, "-") || (len(positional) >= 2 && !isClaudeOpt(w)) {
			positional = append(positional, w)
			continue
		}
		name, value, next, ok := optValue(words, i)
		switch name {
		case "-e", "--env":
			if !ok {
				return Draft{}, fmt.Errorf("%s needs KEY=value", name)
			}
			addEnv(&d, value)
			i = next
			for i+1 < len(words) && envPair.MatchString(words[i+1]) {
				i++
				addEnv(&d, words[i])
			}
		case "-H", "--header":
			if !ok {
				return Draft{}, fmt.Errorf("%s needs \"Name: value\"", name)
			}
			addHeader(&d, value)
			i = next
			for i+1 < len(words) && headerPair.MatchString(words[i+1]) && !strings.Contains(words[i+1], "://") {
				i++
				addHeader(&d, words[i])
			}
		case "-t", "--transport", "-s", "--scope", "--callback-port", "--client-id":
			i = next
		case "--client-secret":
		default:
			return Draft{}, fmt.Errorf("claude mcp add: unknown option %s", w)
		}
	}
	if len(positional) < 2 {
		return Draft{}, errors.New("claude mcp add needs a name and a command or URL")
	}
	d.Name = positional[0]
	if isURL(positional[1]) {
		d.URL = positional[1]
		return d, nil
	}
	d.Command, d.Args = positional[1], positional[2:]
	return d, nil
}

func isClaudeOpt(w string) bool {
	name, _, _ := strings.Cut(w, "=")
	switch name {
	case "-e", "--env", "-H", "--header", "-t", "--transport", "-s", "--scope", "--callback-port", "--client-id", "--client-secret":
		return true
	}
	return false
}

// parseClaudeAddJSON reads `claude mcp add-json <name> '<json>'`.
func parseClaudeAddJSON(words []string) (Draft, error) {
	var positional []string
	for i := 0; i < len(words); i++ {
		switch w := words[i]; {
		case w == "-s" || w == "--scope":
			i++
		case strings.HasPrefix(w, "-"):
		default:
			positional = append(positional, w)
		}
	}
	if len(positional) < 2 {
		return Draft{}, errors.New("claude mcp add-json needs a name and the server's JSON")
	}
	d, err := serverFromJSON([]byte(positional[1]))
	if err != nil {
		return Draft{}, err
	}
	d.Name = positional[0]
	return d, nil
}

// parseCodexAdd reads `codex mcp add <name> [--env K=V]... (--url <url> |
// -- <command> [args...])`.
func parseCodexAdd(words []string) (Draft, error) {
	var d Draft
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			rest := words[i+1:]
			if len(rest) == 0 {
				return Draft{}, errors.New("codex mcp add: nothing after --")
			}
			d.Command, d.Args = rest[0], rest[1:]
			break
		}
		if !strings.HasPrefix(w, "-") {
			if d.Name != "" {
				return Draft{}, fmt.Errorf("codex mcp add: unexpected %q", w)
			}
			d.Name = w
			continue
		}
		name, value, next, ok := optValue(words, i)
		if !ok {
			return Draft{}, fmt.Errorf("%s needs a value", name)
		}
		switch name {
		case "--env":
			addEnv(&d, value)
		case "--url":
			d.URL = value
		case "--bearer-token-env-var":
			// Names a variable in codex's environment; the token itself is
			// not in the paste, and omniplex signs in on its own.
		default:
			return Draft{}, fmt.Errorf("codex mcp add: unknown option %s", name)
		}
		i = next
	}
	if d.Name == "" {
		return Draft{}, errors.New("codex mcp add needs a name")
	}
	if d.URL == "" && d.Command == "" {
		return Draft{}, errors.New("codex mcp add needs --url or a command after --")
	}
	return d, nil
}

// shellWords splits a command line the way a POSIX shell would for the
// purposes of a paste: quotes, backslash escapes and line continuations.
func shellWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) {
				i++
				if s[i] == '\n' {
					continue
				}
				if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
					i++
					continue
				}
				cur.WriteByte(s[i])
				inWord = true
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unclosed ' in the command")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`\n", s[i+1]) >= 0 {
					i++
					if s[i] == '\n' {
						continue
					}
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New(`unclosed " in the command`)
			}
			inWord = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// --- JSON ---

// wrappers are the keys a client config keeps its server map under.
var wrappers = []string{"mcpServers", "servers", "mcp_servers", "context_servers"}

func parseJSON(text string) (Draft, error) {
	raw := []byte(text)
	if !json.Valid(raw) {
		// A fragment copied out of a bigger file: "name": { ... }
		wrapped := []byte("{" + strings.TrimSuffix(strings.TrimSpace(text), ",") + "}")
		if !json.Valid(wrapped) {
			return Draft{}, errors.New("that JSON does not parse")
		}
		raw = wrapped
	}
	for _, key := range wrappers {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return Draft{}, errUnrecognised
		}
		if inner, ok := obj[key]; ok {
			raw = inner
			break
		}
	}
	if looksLikeServer(raw) {
		return serverFromJSON(raw)
	}
	name, inner, err := firstEntry(raw)
	if err != nil {
		return Draft{}, err
	}
	if !looksLikeServer(inner) {
		return Draft{}, errUnrecognised
	}
	d, err := serverFromJSON(inner)
	if err != nil {
		return Draft{}, err
	}
	d.Name = name
	return d, nil
}

type jsonServer struct {
	Type      string            `json:"type"`
	URL       string            `json:"url"`
	ServerURL string            `json:"serverUrl"`
	HTTPURL   string            `json:"httpUrl"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Headers   map[string]string `json:"headers"`
}

func looksLikeServer(raw json.RawMessage) bool {
	var s map[string]json.RawMessage
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	for _, k := range []string{"url", "serverUrl", "httpUrl", "command"} {
		if _, ok := s[k]; ok {
			return true
		}
	}
	return false
}

func serverFromJSON(raw []byte) (Draft, error) {
	var s jsonServer
	if err := json.Unmarshal(raw, &s); err != nil {
		return Draft{}, fmt.Errorf("that server's JSON does not parse: %v", err)
	}
	d := Draft{Env: s.Env, Headers: s.Headers}
	switch {
	case s.URL != "":
		d.URL = s.URL
	case s.ServerURL != "":
		d.URL = s.ServerURL
	case s.HTTPURL != "":
		d.URL = s.HTTPURL
	case s.Command != "":
		d.Command, d.Args = s.Command, s.Args
	default:
		return Draft{}, errors.New("that server has neither a url nor a command")
	}
	if d.URL != "" {
		// A remote server's env means nothing to it.
		d.Env = nil
	}
	return d, nil
}

// firstEntry returns the first key of a JSON object and its value, in the
// order written: a pasted mcpServers map with several servers yields the
// first one.
func firstEntry(raw []byte) (string, json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", nil, errUnrecognised
	}
	t, err := dec.Token()
	if err != nil {
		return "", nil, errUnrecognised
	}
	key, ok := t.(string)
	if !ok {
		return "", nil, errors.New("there is no server in that JSON")
	}
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return "", nil, errUnrecognised
	}
	return key, v, nil
}

// --- TOML ---

type tomlServer struct {
	Command        string            `toml:"command"`
	Args           []string          `toml:"args"`
	Env            map[string]string `toml:"env"`
	URL            string            `toml:"url"`
	HTTPHeaders    map[string]string `toml:"http_headers"`
	EnvHTTPHeaders map[string]string `toml:"env_http_headers"`
}

func parseTOML(text string) (Draft, error) {
	var doc struct {
		Servers map[string]tomlServer `toml:"mcp_servers"`
	}
	md, err := toml.Decode(text, &doc)
	if err != nil {
		return Draft{}, fmt.Errorf("that TOML does not parse: %v", err)
	}
	name := ""
	for _, k := range md.Keys() {
		if len(k) >= 2 && k[0] == "mcp_servers" {
			name = k[1]
			break
		}
	}
	s, ok := doc.Servers[name]
	if !ok {
		return Draft{}, errors.New("there is no [mcp_servers.<name>] table in that TOML")
	}
	d := Draft{Name: name, URL: s.URL, Headers: s.HTTPHeaders}
	if d.URL == "" {
		if s.Command == "" {
			return Draft{}, fmt.Errorf("[mcp_servers.%s] has neither a url nor a command", name)
		}
		d.Command, d.Args, d.Env = s.Command, s.Args, s.Env
		return d, nil
	}
	// Headers codex reads from its environment: the value is not in the
	// paste, so the form asks for it.
	for h := range s.EnvHTTPHeaders {
		if d.Headers == nil {
			d.Headers = map[string]string{}
		}
		if _, ok := d.Headers[h]; !ok {
			d.Headers[h] = ""
		}
	}
	return d, nil
}

// --- mcp-remote ---

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// unbridge turns `npx mcp-remote <url> [--header "K: V"]` into the remote
// server itself. mcp-remote exists to give a local-only client a remote
// server and its OAuth; omniplex does both, and a bridge would hold a
// sign-in of its own that omniplex cannot see.
func unbridge(d Draft) Draft {
	switch path.Base(d.Command) {
	case "npx", "bunx", "pnpx", "pnpm", "yarn", "mcp-remote":
	default:
		return d
	}
	args := d.Args
	if path.Base(d.Command) != "mcp-remote" {
		i := 0
		for ; i < len(args); i++ {
			if a := args[i]; a == "mcp-remote" || strings.HasPrefix(a, "mcp-remote@") {
				break
			}
		}
		if i == len(args) {
			return d
		}
		args = args[i+1:]
	}
	if len(args) == 0 || !isURL(args[0]) {
		return d
	}
	out := Draft{Name: d.Name, URL: args[0], Headers: map[string]string{}, Env: map[string]string{}}
	for i := 1; i < len(args); i++ {
		if args[i] == "--header" && i+1 < len(args) {
			i++
			line := envRef.ReplaceAllStringFunc(args[i], func(ref string) string {
				m := envRef.FindStringSubmatch(ref)
				name := m[1] + m[2]
				if v, ok := d.Env[name]; ok {
					return v
				}
				return ref
			})
			addHeader(&out, line)
		}
	}
	return out
}

// --- names ---

var nonName = regexp.MustCompile(`[^a-z0-9_-]+`)

// slugName fits any string to the server name rule as best it can.
func slugName(s string) string {
	s = nonName.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-_")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-_")
	}
	return s
}

// nameFromURL names a server after its host, without the parts every host
// has: mcp.cloudflare.com is "cloudflare".
func nameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	// An address or localhost says nothing about what the server is; the
	// port at least tells two of them apart.
	if net.ParseIP(u.Hostname()) != nil || u.Hostname() == "localhost" {
		return slugName(strings.Join([]string{"local", u.Port()}, "-"))
	}
	labels := strings.Split(u.Hostname(), ".")
	if len(labels) > 1 {
		labels = labels[:len(labels)-1]
	}
	var keep []string
	for _, l := range labels {
		if l != "www" && l != "mcp" && l != "api" {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		keep = labels
	}
	return slugName(strings.Join(keep, "-"))
}

// runners are commands whose first argument, not themselves, names the
// server: npx -y @scope/server-github is "server-github".
var runners = map[string]bool{"npx": true, "bunx": true, "pnpx": true, "uvx": true, "pipx": true, "node": true, "python": true, "python3": true, "deno": true, "bun": true, "uv": true, "docker": true}

func nameFromCommand(command string, args []string) string {
	base := path.Base(command)
	if runners[base] {
		for _, a := range args {
			if strings.HasPrefix(a, "-") || a == "run" || a == "x" {
				continue
			}
			a = strings.TrimSuffix(path.Base(a), path.Ext(a))
			if at := strings.LastIndex(a, "@"); at > 0 {
				a = a[:at]
			}
			if n := slugName(a); n != "" {
				return n
			}
		}
	}
	return slugName(strings.TrimSuffix(base, path.Ext(base)))
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
