package homemigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/adapter/claudecode"
	"github.com/asiraky/omniplex/internal/adapter/codexapp"
	"github.com/asiraky/omniplex/internal/adapter/piapp"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// fixture is a temp HOME laid out like a machine that has used Claude Code
// and Codex both from the terminal and through Omniplex.
type fixture struct {
	t      *testing.T
	home   string
	claude string
	codex  string
	st     *store.Store
}

const (
	claudeOwn     = "projects/-work-app/0001-own.jsonl"
	claudeOwnDir  = "projects/-work-app/0001-own"
	claudeThread  = "projects/-work-app/0002-thread.jsonl"
	claudeCLI     = "projects/-work-app/0003-cli.jsonl"
	claudeCLIDir  = "projects/-work-app/0003-cli"
	claudeSDKCLI  = "projects/-work-app/0004-sdkcli.jsonl"
	claudeLogin   = "projects/-work-app/0005-login.jsonl"
	codexOwn      = "sessions/2026/10/01/rollout-2026-10-01T10-00-00-c-own.jsonl"
	codexArchived = "archived_sessions/rollout-2026-09-01T10-00-00-c-arch.jsonl"
	codexExec     = "sessions/2026/10/01/rollout-2026-10-01T11-00-00-c-exec.jsonl"
)

func claudeLine(entrypoint, id string) string {
	return `{"type":"queue-operation","operation":"enqueue"}` + "\n" +
		`{"type":"user","entrypoint":"` + entrypoint + `","sessionId":"` + id + `","message":{"content":"hi"}}` + "\n" +
		`{"type":"assistant","entrypoint":"` + entrypoint + `","sessionId":"` + id + `"}` + "\n"
}

func codexLine(id, originator string) string {
	return `{"timestamp":"2026-10-01T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","originator":"` + originator + `","cwd":"/work/app"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message"}}` + "\n"
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", "")
	f := &fixture{t: t, home: home, claude: filepath.Join(home, ".claude"), codex: filepath.Join(home, ".codex")}

	f.write(f.claude, ".credentials.json", `{"claudeAiOauth":"secret"}`)
	f.write(f.claude, ".claude.json", `{"oauthAccount":"me"}`)
	f.write(f.claude, "settings.json", `{"theme":"dark"}`)
	f.write(f.claude, "CLAUDE.md", "be terse")
	f.write(f.claude, "skills/x/SKILL.md", "skill")
	f.write(f.claude, claudeOwn, claudeLine("sdk-ts", "0001-own"))
	f.write(f.claude, claudeOwnDir+"/subagents/agent-a.jsonl", `{"type":"user"}`+"\n")
	f.write(f.claude, claudeOwnDir+"/tool-results/r1.txt", "output")
	f.write(f.claude, claudeThread, claudeLine("sdk-ts", "0002-thread"))
	f.write(f.claude, claudeCLI, claudeLine("cli", "0003-cli"))
	f.write(f.claude, claudeCLIDir+"/subagents/agent-b.jsonl", `{"type":"user"}`+"\n")
	f.write(f.claude, claudeSDKCLI, claudeLine("sdk-cli", "0004-sdkcli"))
	// An Omniplex conversation with a login file in its sidecar: never moved.
	f.write(f.claude, claudeLogin, claudeLine("sdk-ts", "0005-login"))
	f.write(f.claude, "projects/-work-app/0005-login/.credentials.json", "secret")

	f.write(f.codex, "auth.json", `{"tokens":"secret"}`)
	f.write(f.codex, "config.toml", `model = "gpt-5"`)
	f.write(f.codex, codexOwn, codexLine("c-own", "omniplex"))
	f.write(f.codex, codexArchived, codexLine("c-arch", "omniplex"))
	f.write(f.codex, codexExec, codexLine("c-exec", "codex_exec"))
	f.age(f.claude, time.Hour)
	f.age(f.codex, time.Hour)

	f.config(`{"id":"claude","driver":"claude","displayName":"Aaron","enabled":true,"model":"opus"}`)

	st, err := store.Open(filepath.Join(home, ".omniplex", "omniplex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f.st = st
	f.thread("t-claude", "claude", "", "0002-thread", false)
	f.thread("t-codex", "codex", "", "c-own", true)
	return f
}

func (f *fixture) write(root, rel, content string) {
	f.t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// age dates everything under root d ago, so nothing is recent.
func (f *fixture) age(root string, d time.Duration) {
	f.t.Helper()
	old := time.Now().Add(-d)
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) touch(path string) {
	f.t.Helper()
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) config(entries ...string) {
	f.t.Helper()
	cfg := userconfig.Default()
	for _, e := range entries {
		cfg.Providers = append(cfg.Providers, json.RawMessage(e))
	}
	if _, err := userconfig.Save(cfg); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) configBytes() []byte {
	f.t.Helper()
	path, err := userconfig.Path()
	if err != nil {
		f.t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

// thread records a thread on an account resuming a harness conversation,
// optionally folded into a snapshot as the thread manager leaves them.
func (f *fixture) thread(id, harness, instance, session string, snapshot bool) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.st.CreateThread(ctx, store.ThreadMeta{ID: id, Cwd: "/work/app", Harness: harness, ProviderInstance: instance, Title: id}); err != nil {
		f.t.Fatal(err)
	}
	ev, err := f.st.Append(ctx, id, proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{HarnessSessionID: session}))
	if err != nil {
		f.t.Fatal(err)
	}
	if snapshot {
		s := projection.New(id)
		s.Apply(ev)
		if err := f.st.PutSnapshot(ctx, id, ev.Seq, s); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) adapters() []adapter.Adapter {
	return []adapter.Adapter{claudecode.New(""), codexapp.New("codex"), piapp.New("pi")}
}

func (f *fixture) opts(mod ...func(*Options)) Options {
	o := Options{UserHome: f.home, Adapters: f.adapters(), Threads: f.st}
	for _, m := range mod {
		m(&o)
	}
	return o
}

func (f *fixture) run(o Options) (*Plan, *Report) {
	f.t.Helper()
	p, err := NewPlan(context.Background(), o)
	if err != nil {
		f.t.Fatal(err)
	}
	rep, err := Execute(context.Background(), p)
	if err != nil {
		f.t.Fatal(err)
	}
	return p, rep
}

func (f *fixture) target(id string) string {
	return filepath.Join(f.home, ".omniplex", "instances", id)
}

// tree is every path under root with its kind, mode and content hash.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "link " + target
		case d.IsDir():
			out[rel] = "dir " + info.Mode().Perm().String()
		default:
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			out[rel] = "file " + info.Mode().Perm().String() + " " + hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var diffs []string
	for k, v := range before {
		if after[k] != v {
			diffs = append(diffs, k+": "+v+" -> "+after[k])
		}
	}
	for k, v := range after {
		if _, ok := before[k]; !ok {
			diffs = append(diffs, k+": new "+v)
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("%s changed:\n%s", what, strings.Join(diffs, "\n"))
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func providers(t *testing.T) map[string]map[string]any {
	t.Helper()
	cfg, err := userconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, raw := range cfg.Providers {
		var e map[string]any
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out[e["id"].(string)] = e
	}
	return out
}

func envValue(e map[string]any, name string) string {
	env, _ := e["env"].([]any)
	for _, item := range env {
		m := item.(map[string]any)
		if m["name"] == name {
			v, _ := m["value"].(string)
			return v
		}
	}
	return ""
}

func account(t *testing.T, rep *Report, id string) AccountResult {
	t.Helper()
	for _, a := range rep.Accounts {
		if a.Instance == id {
			return a
		}
	}
	t.Fatalf("no result for %s in %+v", id, rep.Accounts)
	return AccountResult{}
}

func TestMigrateMovesOnlyOmniplexConversationsWithTheirSidecars(t *testing.T) {
	f := newFixture(t)
	f.thread("t-gone", "claude", "", "0009-gone", false)
	terminal := map[string]string{}
	for _, rel := range []string{claudeCLI, claudeCLIDir + "/subagents/agent-b.jsonl", claudeSDKCLI} {
		terminal[filepath.Join(f.claude, rel)] = ""
	}
	terminal[filepath.Join(f.codex, codexExec)] = ""
	for _, rel := range []string{".credentials.json", ".claude.json"} {
		terminal[filepath.Join(f.claude, rel)] = ""
	}
	terminal[filepath.Join(f.codex, "auth.json")] = ""
	for path := range terminal {
		_, sum, err := hashFile(path)
		if err != nil {
			t.Fatal(err)
		}
		terminal[path] = sum
	}
	ownDirBefore := tree(t, filepath.Join(f.claude, claudeOwnDir))

	_, rep := f.run(f.opts())

	claudeHome, codexHome := f.target("claude"), f.target("codex")
	for _, rel := range []string{claudeOwn, claudeThread} {
		if exists(filepath.Join(f.claude, rel)) || !exists(filepath.Join(claudeHome, rel)) {
			t.Errorf("%s did not move to %s", rel, claudeHome)
		}
	}
	sameTree(t, "the moved sidecar", ownDirBefore, tree(t, filepath.Join(claudeHome, claudeOwnDir)))
	if exists(filepath.Join(f.claude, claudeOwnDir)) {
		t.Errorf("the sidecar is still in the machine's home")
	}
	for _, rel := range []string{codexOwn, codexArchived} {
		if exists(filepath.Join(f.codex, rel)) || !exists(filepath.Join(codexHome, rel)) {
			t.Errorf("%s did not move to the same place under %s", rel, codexHome)
		}
	}
	for path, sum := range terminal {
		if _, got, err := hashFile(path); err != nil || got != sum {
			t.Errorf("%s changed or moved (%v)", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(claudeHome, ".credentials.json"), filepath.Join(claudeHome, ".claude.json"), filepath.Join(codexHome, "auth.json"),
		filepath.Join(claudeHome, claudeLogin), filepath.Join(claudeHome, "projects/-work-app/0005-login"),
	} {
		if exists(path) {
			t.Errorf("%s reached the new home", path)
		}
	}
	if !exists(filepath.Join(f.claude, claudeLogin)) || !exists(filepath.Join(f.claude, "projects/-work-app/0005-login/.credentials.json")) {
		t.Errorf("the conversation holding a login file was moved")
	}

	c := account(t, rep, "claude")
	if !c.Migrated || c.Moved != 2 || c.Conflicts != 1 || len(c.Missing) != 1 || c.Missing[0].SessionID != "0009-gone" {
		t.Errorf("claude result = %+v", c)
	}
	if x := account(t, rep, "codex"); !x.Migrated || x.Moved != 2 {
		t.Errorf("codex result = %+v", x)
	}

	// The account's config names its new home and keeps what it had.
	p := providers(t)
	if got := p["claude"]; got["displayName"] != "Aaron" || got["model"] != "opus" || envValue(got, "CLAUDE_CONFIG_DIR") != claudeHome {
		t.Errorf("claude entry = %v", got)
	}
	if got := p["codex"]; got["driver"] != "codex" || got["displayName"] != codexapp.New("codex").Meta().Name || envValue(got, "CODEX_HOME") != codexHome {
		t.Errorf("codex entry = %v", got)
	}
	// The user's own configuration is shared into the new home.
	if target, err := os.Readlink(filepath.Join(claudeHome, "settings.json")); err != nil || target != filepath.Join(f.claude, "settings.json") {
		t.Errorf("settings.json in the new home = %q, %v", target, err)
	}

	// Every file the manifest lists is at its destination with its hash.
	m, err := ReadManifest(rep.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, mc := range m.Conversations {
		if mc.Status != StatusMoved {
			continue
		}
		for _, mf := range mc.Files {
			files++
			size, sum, err := hashFile(mf.Dst)
			if err != nil || size != mf.Size || sum != mf.SHA256 {
				t.Errorf("%s: %d %s, manifest says %d %s (%v)", mf.Dst, size, sum, mf.Size, mf.SHA256, err)
			}
		}
	}
	if files != 6 {
		t.Errorf("manifest lists %d moved files, want 6", files)
	}

	// A second run finds nothing to do and changes nothing.
	before := tree(t, f.home)
	cfgBefore := f.configBytes()
	p2, rep2 := f.run(f.opts())
	if !p2.Nothing() || rep2.Manifest != "" {
		t.Errorf("second run did something: %+v", rep2)
	}
	sameTree(t, "the home on a second run", before, tree(t, f.home))
	if string(f.configBytes()) != string(cfgBefore) {
		t.Errorf("config changed on a second run")
	}
}

func TestMigrateNeverOverwritesADestination(t *testing.T) {
	f := newFixture(t)
	f.write(f.target("claude"), claudeOwn, "already here")

	_, rep := f.run(f.opts())

	if b, _ := os.ReadFile(filepath.Join(f.target("claude"), claudeOwn)); string(b) != "already here" {
		t.Errorf("the destination was overwritten: %q", b)
	}
	if !exists(filepath.Join(f.claude, claudeOwn)) || !exists(filepath.Join(f.claude, claudeOwnDir)) {
		t.Errorf("the conflicting conversation left the machine's home")
	}
	if c := account(t, rep, "claude"); !c.Migrated || c.Moved != 1 || c.Conflicts != 2 {
		t.Errorf("claude result = %+v", c)
	}
}

func TestMigrateLeavesARecentConversationForALaterRun(t *testing.T) {
	f := newFixture(t)
	recent := filepath.Join(f.claude, claudeOwn)
	f.touch(recent)

	_, rep := f.run(f.opts())
	if c := account(t, rep, "claude"); !c.Migrated || c.Moved != 1 || c.Recent != 1 {
		t.Fatalf("claude result = %+v", c)
	}
	if !exists(recent) || exists(filepath.Join(f.target("claude"), claudeOwn)) {
		t.Fatalf("the recently modified conversation moved")
	}

	// Still in use: the next run leaves it again.
	_, rep = f.run(f.opts())
	if c := account(t, rep, "claude"); c.Moved != 0 || c.Recent != 1 {
		t.Fatalf("second run result = %+v", c)
	}

	// Quiet now: the next run takes it, sidecar and all.
	f.age(recent, time.Hour)
	_, rep = f.run(f.opts())
	if c := account(t, rep, "claude"); !c.Leftover || c.Moved != 1 {
		t.Fatalf("leftover run result = %+v", c)
	}
	for _, rel := range []string{claudeOwn, claudeOwnDir} {
		if exists(filepath.Join(f.claude, rel)) || !exists(filepath.Join(f.target("claude"), rel)) {
			t.Errorf("%s did not move on the later run", rel)
		}
	}

	if p, _ := f.run(f.opts()); !p.Nothing() {
		t.Errorf("a run after the leftover moved still plans: %+v", p.Accounts)
	}
}

func TestRecentConversationOfAThreadMovesOnlyAtStartup(t *testing.T) {
	f := newFixture(t)
	f.touch(filepath.Join(f.claude, claudeThread))
	cfg := f.configBytes()

	// From the command line its thread could be running it: the account
	// is not migrated at all.
	_, rep := f.run(f.opts(func(o *Options) { o.Adapters = o.Adapters[:1] }))
	if c := account(t, rep, "claude"); c.Aborted == "" || c.Moved != 0 {
		t.Fatalf("command line result = %+v", c)
	}
	if string(f.configBytes()) != string(cfg) || !exists(filepath.Join(f.claude, claudeOwn)) {
		t.Fatalf("an aborted account changed something")
	}

	// At startup no thread is running yet, so it moves.
	_, rep = f.run(f.opts(func(o *Options) { o.ExemptReferenced = true }))
	if c := account(t, rep, "claude"); !c.Migrated || c.Moved != 2 {
		t.Fatalf("startup result = %+v", c)
	}
	if !exists(filepath.Join(f.target("claude"), claudeThread)) {
		t.Errorf("the thread's conversation did not move")
	}
}

func TestAThreadOnAConversationOutsideTheMoveSetAbortsItsAccount(t *testing.T) {
	f := newFixture(t)
	f.thread("t-terminal", "claude", "", "0004-sdkcli", false)
	before := tree(t, f.claude)
	cfg := f.configBytes()

	p, rep := f.run(f.opts(func(o *Options) { o.Adapters = o.Adapters[:1] }))

	var check *ThreadCheck
	for i, a := range p.Accounts[0].Threads {
		if a.SessionID == "0004-sdkcli" {
			check = &p.Accounts[0].Threads[i]
		}
	}
	if check == nil || check.Status != ThreadBlocks {
		t.Fatalf("thread checks = %+v", p.Accounts[0].Threads)
	}
	if c := account(t, rep, "claude"); c.Aborted == "" || c.Migrated {
		t.Errorf("claude result = %+v", c)
	}
	sameTree(t, "~/.claude", before, tree(t, f.claude))
	if string(f.configBytes()) != string(cfg) {
		t.Errorf("config.json changed")
	}
	if rep.Manifest != "" || exists(f.target("claude")) {
		t.Errorf("an aborted account wrote a manifest or a home")
	}
}

func TestEachAmbientAccountTakesTheConversationsItsThreadsResume(t *testing.T) {
	f := newFixture(t)
	f.config(`{"id":"claude","driver":"claude","displayName":"Aaron"}`, `{"id":"claude-work","driver":"claude","displayName":"Work","env":[{"name":"CLAUDE_CONFIG_DIR","value":"~/.claude"}]}`)
	f.thread("t-work", "claude", "claude-work", "0001-own", false)

	_, rep := f.run(f.opts())

	if !exists(filepath.Join(f.target("claude-work"), claudeOwn)) || !exists(filepath.Join(f.target("claude-work"), claudeOwnDir)) {
		t.Errorf("the work thread's conversation did not go to its account")
	}
	if !exists(filepath.Join(f.target("claude"), claudeThread)) {
		t.Errorf("the default account's conversation did not go to it")
	}
	if w := account(t, rep, "claude-work"); !w.Migrated || w.Moved != 1 {
		t.Errorf("work result = %+v", w)
	}
	if got := providers(t)["claude-work"]; got["displayName"] != "Work" || envValue(got, "CLAUDE_CONFIG_DIR") != f.target("claude-work") {
		t.Errorf("work entry = %v", got)
	}
}

func TestReverseRestoresTheTreeAndTheConfig(t *testing.T) {
	f := newFixture(t)
	f.touch(filepath.Join(f.claude, claudeOwn)) // a leftover, pending
	claudeBefore, codexBefore := tree(t, f.claude), tree(t, f.codex)
	providersBefore := providers(t)

	_, rep := f.run(f.opts())
	if account(t, rep, "claude").Moved == 0 {
		t.Fatalf("nothing moved: %+v", rep)
	}

	rr, err := Reverse(context.Background(), rep.Manifest, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if rr.Restored != 3 || len(rr.Conflicts) > 0 || len(rr.Missing) > 0 {
		t.Errorf("reverse = %+v", rr)
	}
	sameTree(t, "~/.claude after reverse", claudeBefore, tree(t, f.claude))
	sameTree(t, "~/.codex after reverse", codexBefore, tree(t, f.codex))
	after := providers(t)
	if b, _ := json.Marshal(after["claude"]); string(b) != mustJSON(t, providersBefore["claude"]) {
		t.Errorf("claude entry after reverse = %s", b)
	}
	// Codex was the implicit default, with threads and no entry: the entry
	// the run added stays, on the machine's home, so its threads keep an
	// account.
	if x, ok := after["codex"]; !ok || x["driver"] != "codex" || x["env"] != nil || len(after) != len(providersBefore)+1 {
		t.Errorf("providers after reverse = %v, before %v", after, providersBefore)
	}

	// On hold: the server does not migrate again, and the pending leftover
	// was forgotten.
	f.age(filepath.Join(f.claude, claudeOwn), time.Hour)
	if p, _ := f.run(f.opts(func(o *Options) { o.ExemptReferenced = true })); !p.Nothing() {
		t.Errorf("the server would migrate again after a reverse: %+v", p.Accounts)
	}
	sameTree(t, "~/.claude while on hold", claudeBefore, tree(t, f.claude))
	// An explicit run lifts the hold.
	if p, _ := f.run(f.opts(func(o *Options) { o.Retry = true })); p.Nothing() {
		t.Errorf("an explicit run after a reverse did nothing")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.touch(filepath.Join(f.claude, claudeOwn))
	before := tree(t, f.home)

	p, err := NewPlan(context.Background(), f.opts())
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.Print(&out)
	if p.Nothing() || out.Len() == 0 {
		t.Fatalf("the plan is empty")
	}
	sameTree(t, "the home after planning", before, tree(t, f.home))
}

func TestServerEnvironmentSettingTheHomeLeavesThatHarnessAlone(t *testing.T) {
	f := newFixture(t)
	env := func(name string) string {
		if name == "CLAUDE_CONFIG_DIR" {
			return "/elsewhere"
		}
		return ""
	}
	before := tree(t, f.claude)
	_, rep := f.run(f.opts(func(o *Options) { o.ServerEnv = env }))
	sameTree(t, "~/.claude", before, tree(t, f.claude))
	if x := account(t, rep, "codex"); !x.Migrated {
		t.Errorf("codex result = %+v", x)
	}
}

func TestOpenThreadsReadsTheLogWithoutWritingIt(t *testing.T) {
	f := newFixture(t)
	dbPath := filepath.Join(f.home, ".omniplex", "omniplex.db")
	// While a server has it open.
	if src, closeFn, err := OpenThreads(dbPath); err != nil {
		t.Fatal(err)
	} else if threads, err := loadThreads(context.Background(), src); err != nil || len(threads) != 2 {
		t.Fatalf("threads with the server running = %v (%v)", threads, err)
	} else {
		closeFn()
	}
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	before := tree(t, filepath.Dir(dbPath))

	src, closeFn, err := OpenThreads(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	threads, err := loadThreads(context.Background(), src)
	closeFn()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, th := range threads {
		got[th.ID] = th.Instance + "/" + th.SessionID
	}
	if got["t-claude"] != "claude/0002-thread" || got["t-codex"] != "codex/c-own" {
		t.Errorf("threads = %v", got)
	}
	sameTree(t, "the database folder", before, tree(t, filepath.Dir(dbPath)))

	if src, closeFn, err := OpenThreads(filepath.Join(f.home, "none.db")); err != nil {
		t.Fatal(err)
	} else if threads, err := loadThreads(context.Background(), src); err != nil || len(threads) != 0 {
		t.Errorf("a missing database has threads %v (%v)", threads, err)
	} else {
		closeFn()
	}
	if exists(filepath.Join(f.home, "none.db")) {
		t.Errorf("opening a missing database created it")
	}
}

func (f *fixture) plan(o Options) *Plan {
	f.t.Helper()
	p, err := NewPlan(context.Background(), o)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fixture) execute(p *Plan) *Report {
	f.t.Helper()
	rep, err := Execute(context.Background(), p)
	if err != nil {
		f.t.Fatal(err)
	}
	return rep
}

func manifestConv(t *testing.T, m *Manifest, id string) *ManifestConversation {
	t.Helper()
	for _, mc := range m.Conversations {
		if mc.ID == id {
			return mc
		}
	}
	t.Fatalf("%s not in the manifest", id)
	return nil
}

// An account whose run stops after some of its conversations moved ends as
// it started: they go back, the manifest says so, the config is untouched.
func TestAStoppedAccountPutsBackWhatItMoved(t *testing.T) {
	for _, stopAt := range []string{"a later conversation", "the config", "verify"} {
		t.Run(stopAt, func(t *testing.T) {
			f := newFixture(t)
			claudeBefore := tree(t, f.claude)
			p := f.plan(f.opts())
			switch stopAt {
			case "a later conversation":
				// The thread's conversation, which moves second, cannot.
				f.write(f.target("claude"), claudeThread, "someone else's")
			case "the config":
				f.config(`{"id":"claude","driver":"claude","displayName":"Aaron","enabled":true,"model":"sonnet"}`)
			case "verify":
				// An Omniplex conversation the plan never saw.
				f.write(f.claude, "projects/-work-app/0006-new.jsonl", claudeLine("sdk-ts", "0006-new"))
				claudeBefore = tree(t, f.claude)
			}
			cfgBefore := providers(t)["claude"]

			rep := f.execute(p)

			c := account(t, rep, "claude")
			if c.Stopped == "" || c.Migrated || c.Moved != 0 || len(c.Unrestored) != 0 {
				t.Fatalf("claude result = %+v", c)
			}
			sameTree(t, "~/.claude after a stopped run", claudeBefore, tree(t, f.claude))
			if exists(filepath.Join(f.target("claude"), claudeOwn)) {
				t.Errorf("a conversation is still in the new home")
			}
			if got := providers(t)["claude"]; mustJSON(t, got) != mustJSON(t, cfgBefore) {
				t.Errorf("claude entry = %v, want %v", got, cfgBefore)
			}
			m, err := ReadManifest(rep.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			if mc := manifestConv(t, m, "0001-own"); mc.Status != StatusRolledBack {
				t.Errorf("manifest says 0001-own is %s", mc.Status)
			}
			if x := account(t, rep, "codex"); !x.Migrated {
				t.Errorf("another account's stop affected codex: %+v", x)
			}
		})
	}
}

// A rollback never overwrites: a conversation whose old path is taken
// stays whole in the new home, and the run says so.
func TestARollbackLeavesAConversationWhoseOldPathIsTaken(t *testing.T) {
	f := newFixture(t)
	p := f.plan(f.opts())
	f.config(`{"id":"claude","driver":"claude","displayName":"Aaron","enabled":true,"model":"sonnet"}`)
	// Something takes the old path of 0001-own once it has moved.
	afterMove = func(mc *ManifestConversation) {
		if mc.ID == "0001-own" {
			f.write(f.claude, claudeOwn, "the terminal's")
		}
	}
	t.Cleanup(func() { afterMove = nil })
	rep := f.execute(p)

	c := account(t, rep, "claude")
	if c.Stopped == "" || len(c.Unrestored) != 1 || c.Unrestored[0] != "0001-own" {
		t.Fatalf("claude result = %+v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(f.claude, claudeOwn)); string(b) != "the terminal's" {
		t.Errorf("the file at the old path was replaced: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.target("claude"), claudeOwn)); string(b) != claudeLine("sdk-ts", "0001-own") {
		t.Errorf("the conversation is not whole in the new home: %q", b)
	}
	m, err := ReadManifest(rep.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if mc := manifestConv(t, m, "0001-own"); mc.Status != StatusMoved {
		t.Errorf("manifest says 0001-own is %s, but it is in the new home", mc.Status)
	}
}

// The implicit default account has threads but no config entry. When it is
// not migrated it gets an entry with no home variable, so its threads keep
// an account; a second run changes nothing.
func TestAnImplicitAccountThatIsNotMigratedKeepsAnAmbientEntry(t *testing.T) {
	f := newFixture(t)
	f.thread("t-exec", "codex", "", "c-exec", false) // not Omniplex's: blocks codex
	codexBefore := tree(t, f.codex)

	_, rep := f.run(f.opts())

	x := account(t, rep, "codex")
	if x.Aborted == "" || !x.KeptAmbient {
		t.Fatalf("codex result = %+v", x)
	}
	sameTree(t, "~/.codex", codexBefore, tree(t, f.codex))
	e, ok := providers(t)["codex"]
	if !ok || e["driver"] != "codex" || envValue(e, "CODEX_HOME") != "" {
		t.Fatalf("codex entry = %v (%v)", e, ok)
	}

	cfg := f.configBytes()
	_, rep = f.run(f.opts())
	if x := account(t, rep, "codex"); x.Aborted == "" || x.KeptAmbient {
		t.Errorf("second run codex result = %+v", x)
	}
	if string(f.configBytes()) != string(cfg) {
		t.Errorf("a second run changed the config")
	}
}

// A copy across filesystems cut short leaves the whole original aside and
// a partial destination. Reverse restores the original and keeps the
// partial copy, renamed aside.
func TestReverseRestoresTheOriginalOfACopyCutShort(t *testing.T) {
	f := newFixture(t)
	original, err := os.ReadFile(filepath.Join(f.codex, codexOwn))
	if err != nil {
		t.Fatal(err)
	}
	_, rep := f.run(f.opts())
	m, err := ReadManifest(rep.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	mc := manifestConv(t, m, "c-own")
	src, dst := mc.Units[0].Src, mc.Units[0].Dst
	// As the crash left it: the original aside, half a copy, the manifest
	// not yet updated.
	if err := os.Rename(dst, filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Dir(dst), filepath.Base(dst), string(original[:len(original)/2]))
	mc.Status, mc.Copied = StatusPending, true
	if err := m.save(); err != nil {
		t.Fatal(err)
	}

	rr, err := Reverse(context.Background(), rep.Manifest, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(src); string(b) != string(original) {
		t.Fatalf("restored %q, want the original", b)
	}
	partial := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".partial")
	if b, _ := os.ReadFile(partial); string(b) != string(original[:len(original)/2]) {
		t.Errorf("the partial copy was not kept aside: %q", b)
	}
	if exists(dst) || exists(filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")) {
		t.Errorf("left the partial copy under its name, or the original aside")
	}
	if len(rr.Conflicts) != 0 || rr.Restored != 4 {
		t.Errorf("reverse = %+v", rr)
	}
}

// A destination that does not match the manifest, with no original aside,
// is not put back as if it were the conversation.
func TestReverseNeverRestoresBytesThatDoNotMatch(t *testing.T) {
	f := newFixture(t)
	_, rep := f.run(f.opts())
	m, err := ReadManifest(rep.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	mc := manifestConv(t, m, "c-own")
	f.write(filepath.Dir(mc.Units[0].Dst), filepath.Base(mc.Units[0].Dst), "truncated")

	rr, err := Reverse(context.Background(), rep.Manifest, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if exists(mc.Units[0].Src) {
		t.Errorf("the mismatched copy was put back")
	}
	if len(rr.Conflicts) != 1 || !strings.Contains(rr.Conflicts[0], "c-own") {
		t.Errorf("conflicts = %v", rr.Conflicts)
	}
	m, _ = ReadManifest(rep.Manifest)
	if mc := manifestConv(t, m, "c-own"); mc.Status == StatusReversed {
		t.Errorf("marked reversed")
	}
}

// An account with a conversation that cannot go back keeps its new home:
// restoring the config would leave that conversation behind. A rerun once
// the way is clear finishes it.
func TestReverseKeepsTheConfigOfAnAccountItCouldNotFinish(t *testing.T) {
	f := newFixture(t)
	_, rep := f.run(f.opts())
	blocker := filepath.Join(f.claude, claudeThread)
	f.write(f.claude, claudeThread, "the terminal's")

	rr, err := Reverse(context.Background(), rep.Manifest, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Conflicts) != 1 {
		t.Fatalf("conflicts = %v", rr.Conflicts)
	}
	if got := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); got != f.target("claude") {
		t.Errorf("claude's config was restored with a conversation still in its new home: %q", got)
	}
	if got := envValue(providers(t)["codex"], "CODEX_HOME"); got != "" {
		t.Errorf("codex, which went back whole, still names %q", got)
	}
	if !exists(filepath.Join(f.claude, claudeOwn)) {
		t.Errorf("claude's other conversation did not go back")
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	rr, err = Reverse(context.Background(), rep.Manifest, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Conflicts) != 0 || rr.Restored != 1 {
		t.Errorf("rerun = %+v", rr)
	}
	if got := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); got != "" {
		t.Errorf("the rerun did not restore claude's config: %q", got)
	}
	if m, _ := ReadManifest(rep.Manifest); m.Reversed == nil {
		t.Errorf("the manifest is not marked reversed after the rerun")
	}
}

// A conversation copied across filesystems goes back from its aside, the
// untouched original, and its copy in the new home is set aside, not
// deleted, so a later run can move it again.
func TestARollbackOfACopyRestoresTheOriginal(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.codex, codexOwn)
	dst := filepath.Join(f.target("codex"), codexOwn)
	original := codexLine("c-own", "omniplex")
	if err := os.Rename(src, filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Dir(dst), filepath.Base(dst), original)
	mc := &ManifestConversation{ID: "c-own", Status: StatusMoved, Copied: true, Units: []ManifestUnit{{Src: src, Dst: dst}}}
	m := &Manifest{Version: 1}
	if err := m.create(f.home, time.Now()); err != nil {
		t.Fatal(err)
	}

	back, stuck, saveErr := rollBack([]*ManifestConversation{mc}, m)

	if back != 1 || len(stuck) != 0 || saveErr != nil || mc.Status != StatusRolledBack {
		t.Fatalf("rollBack = %d, %v, %v; status %s", back, stuck, saveErr, mc.Status)
	}
	if b, _ := os.ReadFile(src); string(b) != original {
		t.Errorf("source = %q", b)
	}
	if exists(dst) || !exists(filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".moving")) {
		t.Errorf("the copy was not set aside in the new home")
	}
	if exists(filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")) {
		t.Errorf("the original is still aside")
	}
}
