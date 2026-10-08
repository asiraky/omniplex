package skills

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/procgroup"
)

// Claude Code ships skills inside the CLI, with no files on disk to find.
// Their names are the union of three lists:
//
//  1. What the installed CLI says it ships: see BundledProbe.
//  2. claudeBundledExtra, the bundled skills that list misses.
//  3. Names in skillOverrides that are none of the user's own skills, so a
//     skill turned off here never drops off the page.
//
// A name the installed CLI does not have can show up through 2 or 3. That is
// harmless: switching it writes a skillOverrides entry Claude ignores.

// claudeBundledExtra is what the model can invoke, and both of Claude's
// switches turn off, but the init message's skills list leaves out:
// keybindings-help is model-only, security-review a command.
var claudeBundledExtra = []string{"keybindings-help", "security-review"}

// ClaudeBuiltin is one skill Claude Code ships with. Mode is on, manual or
// off, and off whenever the whole set is.
type ClaudeBuiltin struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// ClaudeBuiltins lists the skills Claude Code ships with, sorted by name.
// queried is the CLI's own list (BundledProbe.Names), found what Discover
// returned for the same roots.
func ClaudeBuiltins(r Roots, queried []string, found []Skill) []ClaudeBuiltin {
	p := readPolicy(r)
	own := map[string]bool{}
	for _, s := range found {
		if containsHarness(s.Harnesses, Claude) && s.Scope != ScopePlugin {
			own[s.Name] = true
			own[filepath.Base(s.Dir)] = true
		}
	}
	names := map[string]bool{}
	for _, n := range queried {
		names[n] = true
	}
	for _, n := range claudeBundledExtra {
		names[n] = true
	}
	for n := range p.claude {
		if !own[n] {
			names[n] = true
		}
	}
	bundled := ClaudeBundled(r)
	out := make([]ClaudeBuiltin, 0, len(names))
	for n := range names {
		if strings.TrimSpace(n) != "" {
			out = append(out, ClaudeBuiltin{Name: n, Mode: builtinMode(p, bundled, n)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func builtinMode(p policy, bundled bool, name string) string {
	switch {
	case !bundled || p.claude[name] == overrideOff:
		return ModeOff
	case p.claude[name] == overrideManualOnly:
		return ModeManual
	}
	return ModeOn
}

// SetClaudeBuiltin turns one of Claude Code's own skills off with a
// skillOverrides entry in the user settings, or on by taking away an entry
// that turns it off or makes it manual.
func SetClaudeBuiltin(r Roots, name string, on bool) (ClaudeBuiltin, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ClaudeBuiltin{}, fmt.Errorf("%w: a name is required", ErrInvalid)
	}
	if r.ClaudeConfigDir == "" {
		return ClaudeBuiltin{}, errors.New("no Claude config dir")
	}
	if err := editSettings(claudeSettingsPath(r), func(content string) (string, error) {
		return setClaudeOff(content, []string{name}, !on)
	}); err != nil {
		return ClaudeBuiltin{}, err
	}
	return ClaudeBuiltin{Name: name, Mode: builtinMode(readPolicy(r), ClaudeBundled(r), name)}, nil
}

// BundledProbe asks the installed Claude Code which skills it ships with.
//
// The only list of them is the skills field of the init message a `claude -p`
// run prints. Run in an empty directory with only project settings loaded,
// that list holds the bundled skills alone, with none of the user's skills,
// plugins or offs. It needs the user's sign-in (without it `schedule` is
// missing), and it is a whole CLI start, so it never runs when the Skills page
// asks: Watch runs it in the background once per installed version, and the
// answer is kept in a cache file keyed on that version.
type BundledProbe struct {
	cache  string
	claude func() (string, bool)
	// version and query run the CLI; a test swaps them.
	version func(ctx context.Context, claude string) (string, error)
	query   func(ctx context.Context, claude string) ([]string, error)

	mu     sync.Mutex
	last   bundledCache
	loaded bool
}

type bundledCache struct {
	Version string   `json:"version"`
	Skills  []string `json:"skills"`
}

// NewBundledProbe keeps its answer in the cache file and finds the CLI with
// claude, which reports false when there is none.
func NewBundledProbe(cache string, claude func() (string, bool)) *BundledProbe {
	return &BundledProbe{cache: cache, claude: claude, version: claudeVersion, query: queryBundled}
}

// Names is the last list the CLI gave, from whichever version gave it, or
// nil before any has. It reads the cache and never runs the CLI.
func (p *BundledProbe) Names() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.load()
	return slices.Clone(p.last.Skills)
}

func (p *BundledProbe) load() {
	if p.loaded {
		return
	}
	p.loaded = true
	if data, err := os.ReadFile(p.cache); err == nil {
		_ = json.Unmarshal(data, &p.last)
	}
}

// Refresh asks the CLI again when its version is not the one the cache is
// for. A failure leaves the last answer in place.
func (p *BundledProbe) Refresh(ctx context.Context) error {
	claude, ok := p.claude()
	if !ok {
		return nil
	}
	version, err := p.version(ctx, claude)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.load()
	known := p.last.Version == version
	p.mu.Unlock()
	if known {
		return nil
	}
	names, err := p.query(ctx, claude)
	if err != nil {
		return err
	}
	next := bundledCache{Version: version, Skills: names}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.cache), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(p.cache, append(data, '\n')); err != nil {
		return err
	}
	p.mu.Lock()
	p.last = next
	p.mu.Unlock()
	return nil
}

// Watch refreshes now and then every interval until ctx ends: Claude Code
// updates itself while the server runs.
func (p *BundledProbe) Watch(ctx context.Context, every time.Duration, logf func(string, ...any)) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := p.Refresh(ctx); err != nil && logf != nil && ctx.Err() == nil {
			logf("Claude Code built-in skills: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// claudeVersion is the version `claude --version` prints, without the
// "(Claude Code)" after it.
func claudeVersion(ctx context.Context, claude string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, claude, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("claude --version printed nothing")
	}
	return fields[0], nil
}

// queryBundled starts `claude -p` in an empty directory and reads the skills
// list off its init message. The init message comes before the model is
// asked anything, so the run is cut off as soon as it arrives.
func queryBundled(ctx context.Context, claude string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if signedOut(ctx, claude) {
		return nil, errors.New("Claude Code is not signed in")
	}
	dir, err := os.MkdirTemp("", "omniplex-claude-skills-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, claude, "-p",
		"--setting-sources", "project",
		"--strict-mcp-config",
		"--no-session-persistence",
		"--output-format", "stream-json", "--verbose",
		"Reply with OK.")
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tree := procgroup.Attach(cmd, "claude-skills")
	defer tree.Kill()
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var m struct {
			Type    string   `json:"type"`
			Subtype string   `json:"subtype"`
			Skills  []string `json:"skills"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == "system" && m.Subtype == "init" {
			tree.Kill()
			_ = cmd.Wait()
			return m.Skills, nil
		}
	}
	tree.Kill()
	err = cmd.Wait()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, fmt.Errorf("claude -p ended without an init message: %v", err)
}

// signedOut asks `claude auth status`. Only a clear "not logged in" counts:
// a CLI too old to answer is given the benefit of the doubt.
func signedOut(ctx context.Context, claude string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// It exits 1 when signed out, with the JSON that says so on stdout.
	out, _ := exec.CommandContext(ctx, claude, "auth", "status").Output()
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	return json.Unmarshal(out, &status) == nil && status.LoggedIn != nil && !*status.LoggedIn
}
