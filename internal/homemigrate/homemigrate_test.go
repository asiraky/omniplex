package homemigrate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

const projectKey = "-home-me-proj"

// fixture is a machine with a Claude account on ~/.claude, Codex's implicit
// default on ~/.codex, and a second Claude account already on its own home.
type fixture struct {
	t     *testing.T
	home  string
	st    *store.Store
	db    string
	now   time.Time
	claud string
	codex string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", "")
	f := &fixture{t: t, home: home, now: time.Now(), claud: filepath.Join(home, ".claude"), codex: filepath.Join(home, ".codex")}

	proj := filepath.Join(f.claud, "projects", projectKey)
	f.write(filepath.Join(proj, "s1.jsonl"), claudeTranscript("sdk-ts", "s1"))
	f.write(filepath.Join(proj, "s1", "subagents", "agent-a.jsonl"), `{"type":"user","entrypoint":"sdk-ts"}`+"\n")
	f.write(filepath.Join(proj, "s1", "tool-results", "r1.txt"), "tool output\n")
	f.write(filepath.Join(proj, "s2.jsonl"), claudeTranscript("sdk-ts", "s2"))
	f.write(filepath.Join(proj, "s2", "subagents", "agent-b.jsonl"), "{}\n")
	f.write(filepath.Join(proj, "c1.jsonl"), claudeTranscript("cli", "c1"))
	f.write(filepath.Join(proj, "c1", "subagents", "agent-c.jsonl"), "{}\n")
	f.write(filepath.Join(proj, "p1.jsonl"), claudeTranscript("sdk-cli", "p1"))
	f.write(filepath.Join(proj, "n1.jsonl"), `{"type":"summary","summary":"old"}`+"\n")
	f.write(filepath.Join(f.claud, ".credentials.json"), `{"claudeAiOauth":"terminal"}`)
	f.write(filepath.Join(home, ".claude.json"), `{"oauthAccount":"terminal"}`)
	f.write(filepath.Join(f.claud, "settings.json"), `{"theme":"dark"}`)
	f.write(filepath.Join(f.claud, "skills", "infra", "SKILL.md"), "# infra\n")

	day := filepath.Join(f.codex, "sessions", "2026", "10", "01")
	f.write(filepath.Join(day, "rollout-2026-10-01T00-00-00-x1.jsonl"), codexRollout("omniplex", "x1"))
	f.write(filepath.Join(day, "rollout-2026-10-01T00-00-01-x2.jsonl"), codexRollout("omniplex", "x2"))
	f.write(filepath.Join(day, "rollout-2026-10-01T00-00-02-e1.jsonl"), codexRollout("codex_exec", "e1"))
	f.write(filepath.Join(f.codex, "archived_sessions", "rollout-2026-09-01T00-00-00-x3.jsonl"), codexRollout("omniplex", "x3"))
	f.write(filepath.Join(f.codex, "auth.json"), `{"tokens":"terminal"}`)
	f.write(filepath.Join(f.codex, "config.toml"), "model = \"gpt\"\n")

	zero8 := accounthome.InstanceHome(home, "zero8")
	f.write(filepath.Join(zero8, "projects", projectKey, "z1.jsonl"), claudeTranscript("sdk-ts", "z1"))

	// Everything has been sitting for an hour.
	old := f.now.Add(-time.Hour)
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			_ = os.Chtimes(path, old, old)
		}
		return err
	})

	f.config(
		`{"id":"claude","driver":"claude","displayName":"Aaron","enabled":true,"env":[],"extra":{"kept":true}}`,
		`{"id":"zero8","driver":"claude","displayName":"zero8","enabled":true,"env":[{"name":"CLAUDE_CONFIG_DIR","value":"`+zero8+`"}]}`,
	)

	f.db = filepath.Join(home, ".omniplex", "omniplex.db")
	st, err := store.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f.st = st
	f.thread("t1", "claude", "", "s1")
	f.thread("t2", "codex", "", "x1")
	f.thread("t3", "claude", "zero8", "z1")
	f.thread("t4", "claude", "", "")
	return f
}

func claudeTranscript(entrypoint, id string) string {
	return `{"type":"queue-operation","operation":"enqueue","sessionId":"` + id + `"}` + "\n" +
		`{"type":"queue-operation","operation":"dequeue","sessionId":"` + id + `"}` + "\n" +
		`{"type":"user","entrypoint":"` + entrypoint + `","sessionId":"` + id + `","message":{"content":"hello"}}` + "\n" +
		`{"type":"assistant","entrypoint":"` + entrypoint + `","sessionId":"` + id + `"}` + "\n"
}

func codexRollout(originator, id string) string {
	return `{"timestamp":"2026-10-01T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","originator":"` + originator + `","cwd":"/home/me/proj"}}` + "\n" +
		`{"timestamp":"2026-10-01T00:00:01Z","type":"response_item","payload":{"type":"message"}}` + "\n"
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) config(entries ...string) {
	f.t.Helper()
	cfg := userconfig.Default()
	cfg.DefaultInstance = "claude"
	for _, e := range entries {
		cfg.Providers = append(cfg.Providers, json.RawMessage(e))
	}
	if _, err := userconfig.Save(cfg); err != nil {
		f.t.Fatal(err)
	}
}

// thread records a thread whose snapshot names a stale session and whose
// later event names the one it resumes, the way a live log looks.
func (f *fixture) thread(id, harness, instance, session string) {
	f.t.Helper()
	ctx := context.Background()
	if err := f.st.CreateThread(ctx, store.ThreadMeta{ID: id, Cwd: "/home/me/proj", Harness: harness, ProviderInstance: instance, Phase: "idle"}); err != nil {
		f.t.Fatal(err)
	}
	ev, err := f.st.Append(ctx, id, proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: "/home/me/proj", Harness: harness}))
	if err != nil {
		f.t.Fatal(err)
	}
	snap := projection.New(id)
	snap.Apply(ev)
	if session != "" {
		snap.HarnessSessionID = "stale-" + id
	}
	if err := f.st.PutSnapshot(ctx, id, ev.Seq, snap); err != nil {
		f.t.Fatal(err)
	}
	if session != "" {
		if _, err := f.st.Append(ctx, id, proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{HarnessSessionID: session})); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) opts(st Store) Options {
	return Options{
		Home:   f.home,
		Store:  st,
		Now:    f.now,
		MinAge: DefaultMinAge,
		Logf:   f.t.Logf,
		Drivers: []Driver{
			{ID: "claude", Name: "Claude Code", HomeEnv: "CLAUDE_CONFIG_DIR", DefaultHome: f.claud,
				SharedEntries: []accounthome.Entry{{Name: "settings.json"}, {Name: "skills", Dir: true}}},
			{ID: "codex", Name: "Codex", HomeEnv: "CODEX_HOME", DefaultHome: f.codex,
				SharedEntries: []accounthome.Entry{{Name: "config.toml"}, {Name: "skills", Dir: true}}},
			{ID: "pi", Name: "Pi", HomeEnv: "PI_CODING_AGENT_DIR", DefaultHome: filepath.Join(f.home, ".pi", "agent"),
				SharedEntries: []accounthome.Entry{{Name: "skills", Dir: true}}},
		},
	}
}

func (f *fixture) run() *Result {
	f.t.Helper()
	res, err := Run(context.Background(), f.opts(f.st))
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fixture) newClaude(rel ...string) string {
	return filepath.Join(append([]string{accounthome.InstanceHome(f.home, "claude")}, rel...)...)
}

func (f *fixture) newCodex(rel ...string) string {
	return filepath.Join(append([]string{accounthome.InstanceHome(f.home, "codex")}, rel...)...)
}

func (f *fixture) proj(rel ...string) string {
	return filepath.Join(append([]string{f.claud, "projects", projectKey}, rel...)...)
}

// tree is every non-folder entry under root: a file's content or a link's
// target, by path.
func tree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return filepath.SkipDir
			}
			if err != nil {
				return err
			}
			switch {
			case d.Type()&fs.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				out[path] = "link:" + target
			case d.Type().IsRegular():
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				out[path] = string(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func providers(t *testing.T) map[string]provider.Instance {
	t.Helper()
	cfg, err := userconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]provider.Instance{}
	for _, raw := range cfg.Providers {
		inst, err := provider.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		out[inst.ID] = inst
	}
	return out
}

func envValue(inst provider.Instance, name string) string {
	for _, v := range inst.Env {
		if v.Name == name {
			return v.Value
		}
	}
	return ""
}

func TestOnlyOmniplexConversationsMoveAndEachSidecarGoesWithIt(t *testing.T) {
	f := newFixture(t)
	before := tree(t, f.claud, f.codex)
	f.run()

	for _, rel := range []string{"s1.jsonl", "s1/subagents/agent-a.jsonl", "s1/tool-results/r1.txt", "s2.jsonl", "s2/subagents/agent-b.jsonl"} {
		src := f.proj(rel)
		dst := f.newClaude("projects", projectKey, rel)
		if exists(src) {
			t.Errorf("%s is still in ~/.claude", rel)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Errorf("%s did not arrive: %v", rel, err)
		} else if string(got) != before[src] {
			t.Errorf("%s changed in the move", rel)
		}
	}
	for _, rel := range []string{
		"sessions/2026/10/01/rollout-2026-10-01T00-00-00-x1.jsonl",
		"sessions/2026/10/01/rollout-2026-10-01T00-00-01-x2.jsonl",
		"archived_sessions/rollout-2026-09-01T00-00-00-x3.jsonl",
	} {
		if exists(filepath.Join(f.codex, rel)) || !exists(f.newCodex(rel)) {
			t.Errorf("codex %s did not move to the same place under the new CODEX_HOME", rel)
		}
	}

	// The terminal's own, and everything that is not a conversation, are
	// exactly as they were.
	after := tree(t, f.claud, f.codex)
	for path, content := range before {
		if strings.Contains(path, "/s1") || strings.Contains(path, "/s2") || strings.Contains(path, "-x") {
			continue
		}
		if after[path] != content {
			t.Errorf("%s changed or went away", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s appeared in a machine home", path)
		}
	}
	if exists(f.newClaude("projects", projectKey, "c1.jsonl")) || exists(f.newClaude("projects", projectKey, "c1")) {
		t.Error("a terminal conversation moved")
	}

	// The zero8 account was already on its own home and is untouched.
	if !exists(filepath.Join(accounthome.InstanceHome(f.home, "zero8"), "projects", projectKey, "z1.jsonl")) {
		t.Error("zero8's conversation moved")
	}
}

func TestCredentialsAreNeverMovedOrCopied(t *testing.T) {
	f := newFixture(t)
	creds := map[string]string{}
	for _, p := range []string{filepath.Join(f.claud, ".credentials.json"), filepath.Join(f.home, ".claude.json"), filepath.Join(f.codex, "auth.json")} {
		b, _ := os.ReadFile(p)
		creds[p] = string(b)
	}
	f.run()
	for p, content := range creds {
		if b, err := os.ReadFile(p); err != nil || string(b) != content {
			t.Errorf("%s changed: %v", p, err)
		}
	}
	for path := range tree(t, filepath.Join(f.home, ".omniplex", "instances")) {
		if credentialNames[filepath.Base(path)] {
			t.Errorf("credential file in an account home: %s", path)
		}
	}
}

func TestConfigEntryGetsItsHomeAndKeepsItsIDAndName(t *testing.T) {
	f := newFixture(t)
	f.run()
	got := providers(t)

	claude := got["claude"]
	if claude.DisplayName != "Aaron" || !claude.Enabled {
		t.Errorf("claude entry lost its name or enabled flag: %+v", claude)
	}
	if v := envValue(claude, "CLAUDE_CONFIG_DIR"); v != f.newClaude() {
		t.Errorf("claude CLAUDE_CONFIG_DIR = %q, want %q", v, f.newClaude())
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(claude.Raw, &raw)
	if string(compact(raw["extra"])) != `{"kept":true}` {
		t.Errorf("unknown key lost: %s", claude.Raw)
	}

	codex, ok := got["codex"]
	if !ok {
		t.Fatal("the implicit codex account got no config entry")
	}
	if codex.Driver != "codex" || codex.DisplayName != "Codex" || !codex.Enabled || envValue(codex, "CODEX_HOME") != f.newCodex() {
		t.Errorf("codex entry = %+v", codex)
	}
	if v := envValue(got["zero8"], "CLAUDE_CONFIG_DIR"); v != accounthome.InstanceHome(f.home, "zero8") {
		t.Errorf("zero8 changed: %q", v)
	}
	cfg, _ := userconfig.Load()
	if cfg.DefaultInstance != "claude" {
		t.Errorf("defaultInstance = %q", cfg.DefaultInstance)
	}

	// The new home has the shared configuration linked in.
	if target, err := os.Readlink(f.newClaude("settings.json")); err != nil || target != filepath.Join(f.claud, "settings.json") {
		t.Errorf("settings.json link = %q, %v", target, err)
	}
}

func TestManifestHashesMatchTheMovedFiles(t *testing.T) {
	f := newFixture(t)
	res := f.run()
	if res.Manifest == "" {
		t.Fatal("no manifest")
	}
	b, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, c := range m.Conversations {
		if c.Status != statusMoved {
			continue
		}
		for _, file := range c.Files {
			files++
			sum, size, err := hashFile(file.Dst)
			if err != nil || sum != file.SHA256 || size != file.Size {
				t.Errorf("%s: sha256 %s size %d err %v; manifest says %s %d", file.Dst, sum, size, err, file.SHA256, file.Size)
			}
		}
	}
	// s1 (3 files), s2 (2), x1, x2, x3.
	if files != 8 {
		t.Errorf("manifest has %d moved files, want 8", files)
	}
	for _, a := range m.Accounts {
		if a.ID == "codex" && (a.Before != nil && string(a.Before) != "null" || a.After == nil) {
			t.Errorf("codex before/after = %s / %s", a.Before, a.After)
		}
	}
}

func TestExistingDestinationIsSkippedNotOverwritten(t *testing.T) {
	f := newFixture(t)
	taken := f.newClaude("projects", projectKey, "s2.jsonl")
	f.write(taken, "someone else's\n")
	res := f.run()

	if b, _ := os.ReadFile(taken); string(b) != "someone else's\n" {
		t.Error("existing destination overwritten")
	}
	if !exists(f.proj("s2.jsonl")) || !exists(f.proj("s2", "subagents", "agent-b.jsonl")) {
		t.Error("the skipped conversation, or its sidecar, left the machine home")
	}
	if exists(f.newClaude("projects", projectKey, "s2")) {
		t.Error("the sidecar of a skipped conversation moved without it")
	}
	if !exists(f.newClaude("projects", projectKey, "s1.jsonl")) {
		t.Error("the rest of the account did not move")
	}
	if !skipped(res.Plan, "claude", "s2") {
		t.Error("the skip was not reported")
	}
}

func TestRecentlyModifiedConversationIsSkipped(t *testing.T) {
	f := newFixture(t)
	// A subagent file still being written holds back the whole conversation.
	if err := os.Chtimes(f.proj("s2", "subagents", "agent-b.jsonl"), f.now, f.now); err != nil {
		t.Fatal(err)
	}
	res := f.run()
	if !exists(f.proj("s2.jsonl")) || !exists(f.proj("s2", "subagents", "agent-b.jsonl")) {
		t.Error("a recently modified conversation moved")
	}
	if !skipped(res.Plan, "claude", "s2") {
		t.Error("the skip was not reported")
	}

	// Once it has settled, the next run picks it up.
	later := f.opts(f.st)
	later.Now = f.now.Add(time.Hour)
	if _, err := Run(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	if exists(f.proj("s2.jsonl")) || !exists(f.newClaude("projects", projectKey, "s2", "subagents", "agent-b.jsonl")) {
		t.Error("the later run did not move the settled conversation")
	}
}

func TestRecentConversationAThreadResumesStillMoves(t *testing.T) {
	f := newFixture(t)
	if err := os.Chtimes(f.proj("s1.jsonl"), f.now, f.now); err != nil {
		t.Fatal(err)
	}
	f.run()
	if exists(f.proj("s1.jsonl")) || !exists(f.newClaude("projects", projectKey, "s1.jsonl")) {
		t.Error("the conversation thread t1 resumes did not move")
	}
	if v := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); v != f.newClaude() {
		t.Errorf("claude was not migrated: %q", v)
	}
}

func skipped(p *Plan, account, id string) bool {
	for _, a := range p.Accounts {
		if a.ID != account {
			continue
		}
		for _, c := range append(append([]*Conversation(nil), a.Skipped...), a.Moves...) {
			if c.ID == id && c.Status == statusSkipped {
				return true
			}
		}
	}
	return false
}

func TestReverseRestoresTheOriginalTreeExactly(t *testing.T) {
	f := newFixture(t)
	before := tree(t, f.claud, f.codex)
	cfgBefore := providers(t)
	res := f.run()

	rev, err := Reverse(res.Manifest, f.now, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Skipped) > 0 || len(rev.Kept) > 0 {
		t.Fatalf("reverse left things: %+v", rev)
	}
	after := tree(t, f.claud, f.codex)
	if len(after) != len(before) {
		t.Errorf("%d entries before, %d after", len(before), len(after))
	}
	for path, content := range before {
		if got, ok := after[path]; !ok || got != content {
			t.Errorf("%s not restored", path)
		}
	}
	cfgAfter := providers(t)
	if len(cfgAfter) != len(cfgBefore) {
		t.Errorf("config has %d providers after reverse, %d before", len(cfgAfter), len(cfgBefore))
	}
	for id, inst := range cfgBefore {
		if string(compact(cfgAfter[id].Raw)) != string(compact(inst.Raw)) {
			t.Errorf("%s: %s, want %s", id, cfgAfter[id].Raw, inst.Raw)
		}
	}

	// A second reverse finds nothing to do.
	again, err := Reverse(res.Manifest, f.now, t.Logf)
	if err != nil || again.Restored != 0 || len(again.Accounts) != 0 {
		t.Errorf("second reverse: %+v, %v", again, err)
	}
}

func TestReverseNeverOverwritesAnOriginalPlace(t *testing.T) {
	f := newFixture(t)
	res := f.run()
	f.write(f.proj("s2.jsonl"), "new terminal file\n")
	rev, err := Reverse(res.Manifest, f.now, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f.proj("s2.jsonl")); string(b) != "new terminal file\n" {
		t.Error("reverse overwrote a file at the original place")
	}
	if !exists(f.newClaude("projects", projectKey, "s2.jsonl")) {
		t.Error("the moved copy went away")
	}
	if len(rev.Kept) != 1 || rev.Kept[0] != "claude" {
		t.Errorf("claude's config should stay migrated while its conversation cannot go back: %+v", rev)
	}
	if v := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); v != f.newClaude() {
		t.Errorf("claude config reverted with a conversation still in its home: %q", v)
	}
}

func TestSecondRunIsANoOp(t *testing.T) {
	f := newFixture(t)
	f.run()
	cfgPath, _ := userconfig.Path()
	cfg, _ := os.ReadFile(cfgPath)
	homes := tree(t, f.home)
	manifests, _ := os.ReadDir(filepath.Join(f.home, ".omniplex", "migrations"))

	res := f.run()
	if res.Manifest != "" {
		t.Errorf("second run wrote manifest %s", res.Manifest)
	}
	if now, _ := os.ReadFile(cfgPath); string(now) != string(cfg) {
		t.Error("second run changed the config")
	}
	again := tree(t, f.home)
	for path, content := range homes {
		if strings.HasPrefix(filepath.Base(path), "omniplex.db") {
			continue
		}
		if again[path] != content {
			t.Errorf("second run changed %s", path)
		}
	}
	if m, _ := os.ReadDir(filepath.Join(f.home, ".omniplex", "migrations")); len(m) != len(manifests) {
		t.Error("second run wrote a manifest")
	}
}

func TestLaterRunPicksUpConversationsLeftBehind(t *testing.T) {
	f := newFixture(t)
	f.run()
	// A dev server or probe wrote another Omniplex conversation into
	// ~/.claude after the migration.
	f.write(f.proj("s9.jsonl"), claudeTranscript("sdk-ts", "s9"))
	old := f.now.Add(-time.Hour)
	_ = os.Chtimes(f.proj("s9.jsonl"), old, old)
	res := f.run()
	if exists(f.proj("s9.jsonl")) || !exists(f.newClaude("projects", projectKey, "s9.jsonl")) {
		t.Error("the catch-all account did not take the new conversation")
	}
	if res.Manifest == "" {
		t.Error("a run that moved something wrote no manifest")
	}
}

func TestThreadWhoseConversationDoesNotMoveAbortsItsAccount(t *testing.T) {
	f := newFixture(t)
	// t5 on claude resumes a terminal conversation, which is not Omniplex's to
	// move.
	f.thread("t5", "claude", "", "c1")
	cfgPath, _ := userconfig.Path()
	before := tree(t, f.claud)
	res := f.run()

	after := tree(t, f.claud)
	for path, content := range before {
		if after[path] != content {
			t.Errorf("%s moved or changed though its account was aborted", path)
		}
	}
	if exists(f.newClaude("projects")) {
		t.Error("something of the aborted account moved")
	}
	if v := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); v != "" {
		t.Errorf("aborted account's config changed: %q", v)
	}
	var failed bool
	for _, c := range res.Plan.Threads {
		if c.Thread == "t5" && !c.OK {
			failed = true
		}
	}
	if !failed {
		t.Error("the thread check did not fail for t5")
	}
	// The other account still migrates.
	if envValue(providers(t)["codex"], "CODEX_HOME") != f.newCodex() {
		t.Error("codex did not migrate alongside the aborted account")
	}
	_ = cfgPath
}

func TestThreadWhoseConversationExistsNowhereDoesNotHoldBackItsAccount(t *testing.T) {
	f := newFixture(t)
	// Claude's own cleanup deleted the transcript t6 resumed.
	f.thread("t6", "claude", "", "deleted")
	res := f.run()
	if exists(f.proj("s1.jsonl")) || envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR") != f.newClaude() {
		t.Error("claude did not migrate")
	}
	var gone bool
	for _, c := range res.Plan.Threads {
		if c.Thread == "t6" {
			gone = c.OK && c.Gone
		}
	}
	if !gone {
		t.Error("t6 not reported as gone")
	}
	b, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Accounts {
		if a.ID == "claude" && (len(a.Gone) != 1 || a.Gone[0] != (GoneThread{Thread: "t6", Session: "deleted"})) {
			t.Errorf("manifest gone = %+v", a.Gone)
		}
	}
}

func TestThreadWhoseConversationIsInAnotherHomeAbortsItsAccount(t *testing.T) {
	cases := map[string]func(f *fixture){
		// A home left by an account no longer in the config.
		"another instance home": func(f *fixture) {
			f.write(filepath.Join(accounthome.InstanceHome(f.home, "removed"), "projects", projectKey, "o1.jsonl"), claudeTranscript("sdk-ts", "o1"))
		},
		// The transcript went but its subagents are still there.
		"only a sidecar": func(f *fixture) {
			f.write(f.proj("o1", "subagents", "agent-z.jsonl"), "{}\n")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			setup(f)
			f.thread("t7", "claude", "", "o1")
			res := f.run()
			if !exists(f.proj("s1.jsonl")) || envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR") != "" {
				t.Error("claude migrated with a thread whose conversation is somewhere else")
			}
			for _, c := range res.Plan.Threads {
				if c.Thread == "t7" && (c.OK || c.Gone) {
					t.Errorf("t7 check = %+v", c)
				}
			}
		})
	}
}

func TestFailedMoveUndoesTheAccount(t *testing.T) {
	f := newFixture(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes through folder permissions")
	}
	// A second project whose destination folder cannot be written: the
	// plan cannot see it, and its move fails after s1 and s2 have moved.
	other := filepath.Join(f.claud, "projects", "-zz")
	f.write(filepath.Join(other, "s3.jsonl"), claudeTranscript("sdk-ts", "s3"))
	old := f.now.Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(other, "s3.jsonl"), old, old)
	locked := f.newClaude("projects", "-zz")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	before := tree(t, f.claud)

	res := f.run()
	after := tree(t, f.claud)
	for path, content := range before {
		if after[path] != content {
			t.Errorf("%s not back after the failed move", path)
		}
	}
	if v := envValue(providers(t)["claude"], "CLAUDE_CONFIG_DIR"); v != "" {
		t.Errorf("config changed after a failed move: %q", v)
	}
	for _, a := range res.Plan.Accounts {
		if a.ID == "claude" && a.Status != statusFailed {
			t.Errorf("claude status %s", a.Status)
		}
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	cfgPath, _ := userconfig.Path()
	cfg, _ := os.ReadFile(cfgPath)
	before := tree(t, f.home)
	dirs := folders(t, f.home)

	// Beside the live store, as a dry run beside a running server is.
	ro, err := OpenReadOnly(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	p, err := PlanMigration(context.Background(), f.opts(ro))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	p.Print(&sb)

	if !p.HasWork() {
		t.Error("the plan found nothing to do")
	}
	moves := 0
	for _, a := range p.Accounts {
		moves += len(a.Moves)
	}
	if moves != 5 {
		t.Errorf("plan moves %d conversations, want 5", moves)
	}
	if now, _ := os.ReadFile(cfgPath); string(now) != string(cfg) {
		t.Error("dry run changed the config")
	}
	after := tree(t, f.home)
	for path, content := range before {
		if strings.HasPrefix(filepath.Base(path), "omniplex.db") {
			continue
		}
		if after[path] != content {
			t.Errorf("dry run changed %s", path)
		}
	}
	if len(after) != len(before) {
		t.Errorf("dry run made files: %d before, %d after", len(before), len(after))
	}
	if now := folders(t, f.home); len(now) != len(dirs) {
		t.Errorf("dry run made folders: %d before, %d after", len(dirs), len(now))
	}
}

func folders(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			out = append(out, path)
		}
		return err
	})
	return out
}

func TestReadOnlyStoreRefusesWrites(t *testing.T) {
	f := newFixture(t)
	ro, err := OpenReadOnly(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.db.Exec(`DELETE FROM commands`); err == nil {
		t.Error("a write through the read-only store succeeded")
	}
	threads, err := ro.ListThreads(context.Background())
	if err != nil || len(threads) != 4 {
		t.Errorf("read %d threads, %v", len(threads), err)
	}
}

func TestPiAccountGetsAHomeButAbortsWhenItsThreadsHaveSessions(t *testing.T) {
	f := newFixture(t)
	f.config(
		`{"id":"pi","driver":"pi","displayName":"Pi","enabled":true}`,
		`{"id":"pi-work","driver":"pi","displayName":"Work","enabled":true}`,
	)
	f.thread("tp", "pi", "pi-work", "pi-session")
	f.run()
	got := providers(t)
	if envValue(got["pi"], "PI_CODING_AGENT_DIR") != accounthome.InstanceHome(f.home, "pi") {
		t.Errorf("unreferenced pi account not given its home: %s", got["pi"].Raw)
	}
	if envValue(got["pi-work"], "PI_CODING_AGENT_DIR") != "" {
		t.Errorf("pi account with a thread session was migrated: %s", got["pi-work"].Raw)
	}
}
