package thread

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
)

// accountsAdapter stands in for Claude: FAKE_HOME is its CLAUDE_CONFIG_DIR,
// ~/.fake its ~/.claude, and its probe says who each home is signed in as.
type accountsAdapter struct {
	cfgAdapter
	probeMu sync.Mutex
	probed  []map[string]string
	emails  map[string]string // FAKE_HOME -> signed-in email
}

func (a *accountsAdapter) DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".fake")
}

func (a *accountsAdapter) SharedEntries() []accounthome.Entry {
	return []accounthome.Entry{{Name: "settings.json"}, {Name: "CLAUDE.md"}, {Name: "skills", Dir: true}, {Name: "agents", Dir: true}}
}

func (a *accountsAdapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	a.probed = append(a.probed, env)
	facts := map[string]string{}
	if email := a.emails[env["FAKE_HOME"]]; email != "" {
		facts["account"] = email
	}
	return adapter.Ready(facts)
}

func (a *accountsAdapter) probes() []map[string]string {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	return slices.Clone(a.probed)
}

// accountsManager is a manager on a fresh HOME with no accounts yet.
func accountsManager(t *testing.T) (*Manager, *accountsAdapter, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ad := &accountsAdapter{emails: map[string]string{}}
	return adminTestManagerWith(t, ad), ad, home
}

func addAccount(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	if err := mgr.AddProviderInstance(provider.Spec{ID: id, Driver: "fake", DisplayName: id, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return accountHomeOf(t, mgr, id)
}

func accountHomeOf(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	reg, ok := mgr.lookup(id)
	if !ok {
		t.Fatalf("%s not registered", id)
	}
	env, err := mgr.envFor(reg.inst)
	if err != nil {
		t.Fatal(err)
	}
	return env["FAKE_HOME"]
}

// shape is a home's top level: each entry's link target, or "(real)".
func shape(t *testing.T, dir string) map[string]string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, de := range des {
		target, err := os.Readlink(filepath.Join(dir, de.Name()))
		if err != nil {
			target = "(real)"
		}
		out[de.Name()] = target
	}
	return out
}

func instanceMeta(t *testing.T, mgr *Manager, id string) InstanceMeta {
	t.Helper()
	for _, h := range mgr.Harnesses(context.Background()) {
		for _, im := range h.Instances {
			if im.ID == id {
				return im
			}
		}
	}
	t.Fatalf("%s not listed", id)
	return InstanceMeta{}
}

func TestTheFirstAccountOnACleanMachineGetsItsOwnHomeAndTheSharedConfig(t *testing.T) {
	mgr, _, home := accountsManager(t)
	machine := filepath.Join(home, ".fake")

	account := addAccount(t, mgr, "work")
	if want := filepath.Join(home, ".omniplex", "instances", "work"); account != want {
		t.Fatalf("home = %q, want %q", account, want)
	}
	for _, dir := range []string{"skills", "agents"} {
		info, err := os.Lstat(filepath.Join(machine, dir))
		if err != nil || !info.IsDir() {
			t.Fatalf("shared %s is not a real folder under ~/.fake: %v %v", dir, info, err)
		}
		target, err := os.Readlink(filepath.Join(account, dir))
		if err != nil || target != filepath.Join(machine, dir) {
			t.Fatalf("%s -> %q (%v), want the shared folder", dir, target, err)
		}
		if _, err := os.Stat(filepath.Join(account, dir)); err != nil {
			t.Fatalf("%s does not resolve: %v", dir, err)
		}
	}
	// A skill installed into the shared folder later shows in the account.
	if err := os.MkdirAll(filepath.Join(machine, "skills", "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(account, "skills", "infra")); err != nil {
		t.Fatalf("account does not see a later shared skill: %v", err)
	}
}

func TestTheSecondAccountIsShapedExactlyLikeTheFirst(t *testing.T) {
	mgr, _, home := accountsManager(t)
	machine := filepath.Join(home, ".fake")
	if err := os.MkdirAll(machine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machine, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := shape(t, addAccount(t, mgr, "work"))
	second := shape(t, addAccount(t, mgr, "personal"))
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("homes differ:\nfirst  %v\nsecond %v", first, second)
	}
}

func TestATerminalSignInOnTheMachineChangesNoAccount(t *testing.T) {
	mgr, ad, home := accountsManager(t)
	machine := filepath.Join(home, ".fake")
	ids := []string{"work", "personal"}
	envs, shapes := map[string]map[string]string{}, map[string]map[string]string{}
	for _, id := range ids {
		addAccount(t, mgr, id)
		reg, _ := mgr.lookup(id)
		env, err := mgr.envFor(reg.inst)
		if err != nil {
			t.Fatal(err)
		}
		envs[id], shapes[id] = env, shape(t, env["FAKE_HOME"])
	}
	mgr.Harnesses(context.Background())
	probedBefore := ad.probes()

	// Someone signs in from a terminal on the box.
	if err := os.WriteFile(filepath.Join(machine, ".credentials.json"), []byte(`{"token":"terminal"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machine, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"terminal@x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr.RecheckHarnesses()
	mgr.Harnesses(context.Background())

	for _, id := range ids {
		reg, _ := mgr.lookup(id)
		env, err := mgr.envFor(reg.inst)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(env, envs[id]) {
			t.Errorf("%s env changed: %v -> %v", id, envs[id], env)
		}
		if got := shape(t, env["FAKE_HOME"]); !reflect.DeepEqual(got, shapes[id]) {
			t.Errorf("%s home changed: %v -> %v", id, shapes[id], got)
		}
	}
	probedAfter := ad.probes()[len(probedBefore):]
	if len(probedAfter) == 0 {
		t.Fatal("recheck did not probe again")
	}
	for _, env := range probedAfter {
		if !slices.ContainsFunc(probedBefore, func(e map[string]string) bool { return reflect.DeepEqual(e, env) }) {
			t.Errorf("a probe ran with a new env: %v", env)
		}
	}
}

func TestTheServersOwnHomeVariableIsNeverAnAccountsHome(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("FAKE_HOME", inherited)
	mgr, _, home := accountsManager(t)

	account := addAccount(t, mgr, "work")
	if account == inherited {
		t.Fatal("the account took the server's inherited home")
	}
	for name, target := range shape(t, account) {
		if want := filepath.Join(home, ".fake", name); target != want {
			t.Errorf("%s -> %q, want the machine's %q", name, target, want)
		}
	}
	if des, _ := os.ReadDir(inherited); len(des) != 0 {
		t.Errorf("the inherited home was used as the shared one: %v", des)
	}
}

func TestAnAccountsOwnCopyIsReportedAndUseSharedBacksItUp(t *testing.T) {
	_, _, home := accountsManager(t)
	machine := filepath.Join(home, ".fake")
	account := filepath.Join(home, "accounts", "work")
	for dir, content := range map[string]string{machine: "shared", account: "mine"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A previous backup is never overwritten.
	if err := os.WriteFile(filepath.Join(account, "settings.json.account-backup"), []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := adminTestManagerWith(t, &accountsAdapter{})
	// Startup: the conflict is found before anything uses the account.
	mgr.ConfigureInstances([]provider.Instance{{ID: "work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: account}}}}, mgr.secretStore())

	conflicts := instanceMeta(t, mgr, "work").HomeConflicts
	want := []HomeConflict{{Entry: "settings.json", Path: filepath.Join(account, "settings.json"), Shared: filepath.Join(machine, "settings.json")}}
	if !reflect.DeepEqual(conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", conflicts, want)
	}
	if got := read(t, filepath.Join(account, "settings.json")); got != "mine" {
		t.Fatalf("the account's copy was touched: %q", got)
	}

	if err := mgr.UseSharedHomeEntry("work", "credentials.json"); err == nil {
		t.Error("an entry that is not shared configuration must be refused")
	}
	if err := mgr.UseSharedHomeEntry("work", "settings.json"); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(account, "settings.json")); target != filepath.Join(machine, "settings.json") {
		t.Fatalf("settings.json -> %q", target)
	}
	if got := read(t, filepath.Join(account, "settings.json.account-backup-2")); got != "mine" {
		t.Fatalf("backup = %q", got)
	}
	if got := read(t, filepath.Join(account, "settings.json.account-backup")); got != "older" {
		t.Fatalf("older backup = %q", got)
	}
	if c := instanceMeta(t, mgr, "work").HomeConflicts; len(c) != 0 {
		t.Fatalf("conflict still reported: %+v", c)
	}
}

func TestALinkStraightToTheDotfilesIsRepointedAtTheSharedEntry(t *testing.T) {
	_, _, home := accountsManager(t)
	machine := filepath.Join(home, ".fake")
	dotfiles := filepath.Join(home, "code", "dotfiles", "agents")
	account := filepath.Join(home, "accounts", "worksauce")
	for _, dir := range []string{filepath.Join(dotfiles, "skills"), machine, account} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dotfiles, "skills"), filepath.Join(machine, "skills")); err != nil {
		t.Fatal(err)
	}
	// Reaches the same folder, skipping ~/.fake.
	if err := os.Symlink(filepath.Join(dotfiles, "skills"), filepath.Join(account, "skills")); err != nil {
		t.Fatal(err)
	}
	mgr := adminTestManagerWith(t, &accountsAdapter{})
	mgr.ConfigureInstances([]provider.Instance{{ID: "worksauce", Driver: "fake", DisplayName: "Worksauce", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: account}}}}, mgr.secretStore())

	if target, _ := os.Readlink(filepath.Join(account, "skills")); target != filepath.Join(machine, "skills") {
		t.Fatalf("skills -> %q, want %q", target, filepath.Join(machine, "skills"))
	}
}

func TestTwoAccountsOnOneLoginAreFlaggedOnBoth(t *testing.T) {
	mgr, ad, home := accountsManager(t)
	instances := filepath.Join(home, ".omniplex", "instances")
	ad.emails[filepath.Join(instances, "aaron")] = "aaron@zero8.ai"
	ad.emails[filepath.Join(instances, "zero8")] = "Aaron@Zero8.ai"
	ad.emails[filepath.Join(instances, "worksauce")] = "tech@worksauce.com.au"
	for _, id := range []string{"aaron", "zero8", "worksauce"} {
		addAccount(t, mgr, id)
	}

	want := map[string][]string{"aaron": {"zero8"}, "zero8": {"aaron"}, "worksauce": nil}
	for id, same := range want {
		if got := instanceMeta(t, mgr, id).SameLoginAs; !reflect.DeepEqual(got, same) {
			t.Errorf("%s sameLoginAs = %v, want %v", id, got, same)
		}
	}
	for _, q := range mgr.Quotas() {
		if !reflect.DeepEqual(q.SameLoginAs, want[q.Instance]) {
			t.Errorf("quota %s sameLoginAs = %v, want %v", q.Instance, q.SameLoginAs, want[q.Instance])
		}
		if q.Account == "" {
			t.Errorf("quota %s does not say who is signed in", q.Instance)
		}
	}

	// With no email to go on, the provider's account id says the same.
	ad.emails = map[string]string{}
	mgr.RecheckHarnesses()
	mgr.Harnesses(context.Background())
	mgr.reportQuota("fake", "aaron", adapter.QuotaSnapshot{Full: true, CheckedAt: 1, AccountID: "d6711b45"}, true)
	mgr.reportQuota("fake", "worksauce", adapter.QuotaSnapshot{Full: true, CheckedAt: 1, AccountID: "d6711b45"}, true)
	for _, q := range mgr.Quotas() {
		switch q.Instance {
		case "aaron", "worksauce":
			if len(q.SameLoginAs) != 1 || q.Account != "d6711b45" {
				t.Errorf("quota %s = account %q sameLoginAs %v", q.Instance, q.Account, q.SameLoginAs)
			}
		default:
			if len(q.SameLoginAs) != 0 {
				t.Errorf("quota %s sameLoginAs = %v", q.Instance, q.SameLoginAs)
			}
		}
	}
	if got := instanceMeta(t, mgr, "aaron").SameLoginAs; !reflect.DeepEqual(got, []string{"worksauce"}) {
		t.Errorf("aaron sameLoginAs by account id = %v", got)
	}
}

func TestAnAccountCannotUseTheMachinesOwnHome(t *testing.T) {
	mgr, _, home := accountsManager(t)
	for _, value := range []string{"~/.fake", filepath.Join(home, ".fake"), filepath.Join(home, ".fake") + "/"} {
		err := mgr.AddProviderInstance(provider.Spec{ID: "work", Driver: "fake", DisplayName: "Work", Enabled: true,
			Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: value}}})
		if err == nil {
			t.Fatalf("Add accepted FAKE_HOME=%q", value)
		}
	}
	addAccount(t, mgr, "work")
	err := mgr.SaveProviderInstance(provider.Spec{ID: "work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "~/.fake"}}})
	if err == nil {
		t.Fatal("Save accepted the machine's home")
	}
	// A home blanked on save is filled in again, not left ambient.
	if err := mgr.SaveProviderInstance(provider.Spec{ID: "work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: ""}}}); err != nil {
		t.Fatal(err)
	}
	if got, want := accountHomeOf(t, mgr, "work"), filepath.Join(home, ".omniplex", "instances", "work"); got != want {
		t.Fatalf("blanked home = %q, want %q", got, want)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An account a migration left on the machine's home keeps it through a save
// that leaves the home blank: filling the default in would point it at an
// empty home and strand its conversations. An explicit home still wins.
func TestSavingAnAccountWithoutAHomeKeepsItWhereItIs(t *testing.T) {
	mgr, _, home := accountsManager(t)
	instances, err := provider.AddInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Aaron", Enabled: true}, mgr.secretStore(), mgr.logf)
	if err != nil {
		t.Fatal(err)
	}
	mgr.applyInstances(instances)

	for _, env := range [][]provider.EnvVar{nil, {{Name: "FAKE_HOME", Value: "  "}}} {
		if err := mgr.SaveProviderInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Aaron v2", Enabled: true, Env: env}); err != nil {
			t.Fatal(err)
		}
		reg, _ := mgr.lookup("fake")
		if reg.inst.DisplayName != "Aaron v2" {
			t.Fatalf("save not applied: %+v", reg.inst)
		}
		if v, _ := homeValue(reg.inst.Env, "FAKE_HOME"); v != "" {
			t.Fatalf("env %v: the account was repointed at %q", env, v)
		}
	}

	explicit := filepath.Join(home, "elsewhere")
	if err := mgr.SaveProviderInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Aaron", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: explicit}}}); err != nil {
		t.Fatal(err)
	}
	if got := accountHomeOf(t, mgr, "fake"); got != explicit {
		t.Fatalf("explicit home = %q, want %q", got, explicit)
	}
}

func TestTwoAccountsCannotShareAConfigHome(t *testing.T) {
	mgr, _, _ := accountsManager(t)
	taken := addAccount(t, mgr, "work")
	for _, value := range []string{taken, taken + "/", "~/.omniplex/instances/work"} {
		err := mgr.AddProviderInstance(provider.Spec{ID: "zero8", Driver: "fake", DisplayName: "zero8", Enabled: true,
			Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: value}}})
		if err == nil {
			t.Fatalf("Add accepted the home of another account (%q)", value)
		}
	}
	if _, ok := mgr.lookup("zero8"); ok {
		t.Fatal("a refused add was installed")
	}

	own := addAccount(t, mgr, "zero8")
	err := mgr.SaveProviderInstance(provider.Spec{ID: "zero8", Driver: "fake", DisplayName: "zero8", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: taken}}})
	if err == nil {
		t.Fatal("Save accepted the home of another account")
	}
	if got := accountHomeOf(t, mgr, "zero8"); got != own {
		t.Fatalf("a refused save changed the home to %q", got)
	}
	// Saving an account with its own home is not a clash with itself.
	if err := mgr.SaveProviderInstance(provider.Spec{ID: "work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: taken}}}); err != nil {
		t.Fatal(err)
	}
}
