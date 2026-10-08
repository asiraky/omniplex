package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func values(t *testing.T, st *Store, name string) (env, headers map[string]string) {
	t.Helper()
	srv, ok, err := st.Server(name, "")
	if err != nil || !ok {
		t.Fatalf("server %s: ok=%v err=%v", name, ok, err)
	}
	return st.Values(srv)
}

func TestStoreKeepsValuesOutOfTheFile(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{
		Name: "sentry", URL: "https://mcp.sentry.dev/mcp",
		Headers: map[string]string{"X-Api-Key": "hunter2", "Authorization": "Bearer s3cret"},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{
		Name: "gh", Command: "npx", Args: []string{"-y", "server-github"},
		Env: map[string]string{"GITHUB_TOKEN": "ghp_abc"},
	}, ""); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", "s3cret", "ghp_abc"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("connections.json holds %q", secret)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dir, "connections.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("connections.json mode %v", fi.Mode().Perm())
	}

	// A fresh store over the same folder sees the same thing.
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := again.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Servers) != 2 || f.Servers[0].Name != "sentry" || f.Servers[1].Name != "gh" {
		t.Fatalf("servers %+v", f.Servers)
	}
	if got := f.Servers[0].HeaderNames; !reflect.DeepEqual(got, []string{"Authorization", "X-Api-Key"}) {
		t.Errorf("header names %v", got)
	}
	_, headers := again.Values(f.Servers[0])
	if headers["X-Api-Key"] != "hunter2" || headers["Authorization"] != "Bearer s3cret" {
		t.Errorf("headers %v", headers)
	}
	env, _ := again.Values(f.Servers[1])
	if env["GITHUB_TOKEN"] != "ghp_abc" {
		t.Errorf("env %v", env)
	}
}

func TestStoreReadsMissingFileAsEmpty(t *testing.T) {
	f, err := newStore(t).Read()
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Servers) != 0 || len(f.CLIs) != 0 {
		t.Fatalf("%+v", f)
	}
}

func TestStoreRefusesAnotherVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Read(); err == nil {
		t.Fatal("read a version 2 file")
	}
}

func TestSaveServerEmptyValueKeepsStoredOne(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "s", Command: "run", Env: map[string]string{"A": "1", "B": "2", "C": "3"}}, ""); err != nil {
		t.Fatal(err)
	}
	// A kept, B changed, C dropped, D new and empty.
	if _, err := st.SaveServer(Draft{Name: "s", Command: "run", Env: map[string]string{"A": "", "B": "two", "D": ""}}, "s"); err != nil {
		t.Fatal(err)
	}
	env, _ := values(t, st, "s")
	if !reflect.DeepEqual(env, map[string]string{"A": "1", "B": "two", "D": ""}) {
		t.Fatalf("env %v", env)
	}
	if _, ok := st.Secrets().Get("s", envKey+"C"); ok {
		t.Error("dropped variable C is still stored")
	}
}

func TestSaveServerRenameMovesSecrets(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "old", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "k1"}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Secrets().Put("old", OAuthKey, `{"accessToken":"t"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{Name: "new", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": ""}}, "old"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Server("old", ""); ok {
		t.Error("old name still listed")
	}
	_, headers := values(t, st, "new")
	if headers["X-Key"] != "k1" {
		t.Errorf("kept header did not move: %v", headers)
	}
	if v, ok := st.Secrets().Get("new", OAuthKey); !ok || v == "" {
		t.Error("sign-in did not move with an unchanged URL")
	}
	for _, key := range []string{headerKey + "X-Key", OAuthKey} {
		if _, ok := st.Secrets().Get("old", key); ok {
			t.Errorf("old %s left behind", key)
		}
	}
}

func TestSaveServerNewURLDropsSignIn(t *testing.T) {
	for _, rename := range []bool{false, true} {
		st := newStore(t)
		if _, err := st.SaveServer(Draft{Name: "a", URL: "https://a.example.com/mcp"}, ""); err != nil {
			t.Fatal(err)
		}
		if err := st.Secrets().Put("a", OAuthKey, "tok"); err != nil {
			t.Fatal(err)
		}
		name := "a"
		if rename {
			name = "b"
		}
		if _, err := st.SaveServer(Draft{Name: name, URL: "https://b.example.com/mcp"}, "a"); err != nil {
			t.Fatal(err)
		}
		if _, ok := st.Secrets().Get(name, OAuthKey); ok {
			t.Errorf("rename=%v: sign-in for the old URL kept", rename)
		}
	}
}

func TestSaveServerSameURLKeepsSignIn(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "a", URL: "https://a.example.com/mcp"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Secrets().Put("a", OAuthKey, "tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{Name: "a", URL: "https://a.example.com/mcp", Off: []string{"x"}}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Secrets().Get("a", OAuthKey); !ok {
		t.Error("sign-in dropped by an edit that kept the URL")
	}
}

func TestSaveServerNewNameStartsClean(t *testing.T) {
	st := newStore(t)
	// Left over from something that never made it into the file.
	if err := st.Secrets().Put("a", envKey+"A", "stale"); err != nil {
		t.Fatal(err)
	}
	if err := st.Secrets().Put("a", OAuthKey, "stale"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{Name: "a", Command: "run", Env: map[string]string{"A": ""}}, ""); err != nil {
		t.Fatal(err)
	}
	env, _ := values(t, st, "a")
	if env["A"] != "" {
		t.Errorf("new server inherited %q", env["A"])
	}
	if _, ok := st.Secrets().Get("a", OAuthKey); ok {
		t.Error("new server inherited a sign-in")
	}
}

func TestSaveServerRefusals(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "taken", Command: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		d        Draft
		previous string
	}{
		"bad name":            {Draft{Name: "Has Space", Command: "x"}, ""},
		"reserved name":       {Draft{Name: ReservedName, Command: "x"}, ""},
		"both kinds":          {Draft{Name: "a", Command: "x", URL: "https://a.example.com"}, ""},
		"neither kind":        {Draft{Name: "a"}, ""},
		"not http":            {Draft{Name: "a", URL: "ftp://a.example.com"}, ""},
		"env on a URL":        {Draft{Name: "a", URL: "https://a.example.com", Env: map[string]string{"A": "1"}}, ""},
		"args on a URL":       {Draft{Name: "a", URL: "https://a.example.com", Args: []string{"x"}}, ""},
		"headers on command":  {Draft{Name: "a", Command: "x", Headers: map[string]string{"H": "1"}}, ""},
		"bad env name":        {Draft{Name: "a", Command: "x", Env: map[string]string{"1A": "1"}}, ""},
		"bad header name":     {Draft{Name: "a", URL: "https://a.example.com", Headers: map[string]string{"X Key": "1"}}, ""},
		"header twice":        {Draft{Name: "a", URL: "https://a.example.com", Headers: map[string]string{"X-Key": "1", "x-key": "2"}}, ""},
		"duplicate name":      {Draft{Name: "taken", Command: "x"}, ""},
		"rename onto another": {Draft{Name: "taken", Command: "x"}, "missing"},
		"unknown previous":    {Draft{Name: "fresh", Command: "x"}, "missing"},
	}
	for name, tc := range cases {
		if _, err := st.SaveServer(tc.d, tc.previous); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	f, _ := st.Read()
	if len(f.Servers) != 1 {
		t.Errorf("refused saves changed the file: %+v", f.Servers)
	}
}

func TestSetOffChangesOnlyOff(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "a", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "k"}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Secrets().Put("a", OAuthKey, "tok"); err != nil {
		t.Fatal(err)
	}
	srv, err := st.SetOff("a", "", []string{"b", " a ", "b", ""})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(srv.Off, []string{"a", "b"}) {
		t.Errorf("off %v", srv.Off)
	}
	_, headers := values(t, st, "a")
	if headers["X-Key"] != "k" {
		t.Errorf("headers %v", headers)
	}
	if _, ok := st.Secrets().Get("a", OAuthKey); !ok {
		t.Error("sign-in dropped")
	}
	if _, err := st.SetOff("missing", "", nil); err == nil {
		t.Error("set off on a missing server")
	}
}

func TestRemoveServerPurgesSecrets(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveServer(Draft{Name: "a", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "k"}}, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Secrets().Put("a", OAuthKey, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveServer("a", ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{headerKey + "X-Key", OAuthKey} {
		if _, ok := st.Secrets().Get("a", key); ok {
			t.Errorf("%s survived", key)
		}
	}
	if err := st.RemoveServer("a", ""); err == nil {
		t.Error("removed a missing server")
	}
}

// mustSave saves d, failing the test on a refusal.
func mustSave(t *testing.T, st *Store, d Draft, previous string) Server {
	t.Helper()
	srv, err := st.SaveServer(d, previous)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func scopedValues(t *testing.T, st *Store, name, project string) map[string]string {
	t.Helper()
	srv, ok, err := st.Server(name, project)
	if err != nil || !ok {
		t.Fatalf("server %s in %q: ok=%v err=%v", name, project, ok, err)
	}
	_, headers := st.Values(srv)
	return headers
}

func TestServerNamesAreUniquePerScope(t *testing.T) {
	st := newStore(t)
	for _, p := range []string{"", "p1", "p2"} {
		mustSave(t, st, Draft{Name: "linear", Project: p, URL: "https://l.example.com/mcp", Headers: map[string]string{"X-Key": "k-" + p}}, "")
	}
	if _, err := st.SaveServer(Draft{Name: "linear", Project: "p1", Command: "x"}, ""); err == nil {
		t.Error("saved a second linear in p1")
	}
	if _, err := st.SaveServer(Draft{Name: "linear", Command: "x"}, ""); err == nil {
		t.Error("saved a second linear everywhere")
	}
	for _, p := range []string{"", "p1", "p2"} {
		if got := scopedValues(t, st, "linear", p)["X-Key"]; got != "k-"+p {
			t.Errorf("linear in %q has X-Key %q", p, got)
		}
	}

	// previous is looked up in the draft's scope only.
	if _, err := st.SaveServer(Draft{Name: "only-p1", Project: "p1", Command: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveServer(Draft{Name: "only-p1", Project: "p2", Command: "y"}, "only-p1"); err == nil {
		t.Error("edited p1's server as p2's")
	}
	if _, err := st.SaveServer(Draft{Name: "only-p1", Command: "y"}, "only-p1"); err == nil {
		t.Error("edited p1's server as one that goes everywhere")
	}

	// Renaming p1's linear moves its secrets within p1 and leaves the other
	// two alone.
	if err := st.Secrets().Put("p1.linear", OAuthKey, "tok-p1"); err != nil {
		t.Fatal(err)
	}
	mustSave(t, st, Draft{Name: "client-linear", Project: "p1", URL: "https://l.example.com/mcp", Headers: map[string]string{"X-Key": ""}}, "linear")
	if got := scopedValues(t, st, "client-linear", "p1")["X-Key"]; got != "k-p1" {
		t.Errorf("renamed server has X-Key %q", got)
	}
	if v, ok := st.Secrets().Get("p1.client-linear", OAuthKey); !ok || v != "tok-p1" {
		t.Errorf("sign-in after rename = %q, %v", v, ok)
	}
	if _, ok := st.Secrets().Get("p1.linear", headerKey+"X-Key"); ok {
		t.Error("the old key's secrets survived the rename")
	}
	for _, p := range []string{"", "p2"} {
		if got := scopedValues(t, st, "linear", p)["X-Key"]; got != "k-"+p {
			t.Errorf("after p1's rename, linear in %q has X-Key %q", p, got)
		}
	}

	for _, bad := range []string{"a/b", "a.b", "-a", " "} {
		if _, err := st.SaveServer(Draft{Name: "x", Project: bad, Command: "x"}, ""); err == nil {
			t.Errorf("saved into project %q", bad)
		}
	}
}

func TestSetProjectOff(t *testing.T) {
	st := newStore(t)
	mustSave(t, st, Draft{Name: "gmail", Command: "gmail-mcp"}, "")
	mustSave(t, st, Draft{Name: "sentry", Project: "p1", Command: "sentry-mcp"}, "")

	for _, step := range []struct {
		project string
		off     bool
		want    []string
	}{
		{"p2", true, []string{"p2"}},
		{"p1", true, []string{"p1", "p2"}},
		{"p1", true, []string{"p1", "p2"}},
		{"p2", false, []string{"p1"}},
		{"p3", false, []string{"p1"}},
	} {
		srv, err := st.SetProjectOff("gmail", step.project, step.off)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(srv.OffIn, step.want) {
			t.Errorf("%s off=%v: offIn %v, want %v", step.project, step.off, srv.OffIn, step.want)
		}
	}

	// An edit keeps it.
	srv := mustSave(t, st, Draft{Name: "gmail", Command: "gmail-mcp", Args: []string{"-v"}}, "gmail")
	if !reflect.DeepEqual(srv.OffIn, []string{"p1"}) {
		t.Errorf("after an edit offIn %v", srv.OffIn)
	}

	// Only servers that go everywhere have it.
	if _, err := st.SetProjectOff("sentry", "p1", true); err == nil {
		t.Error("set a project server off in a project")
	}
	if _, err := st.SetProjectOff("gmail", "", true); err == nil {
		t.Error("set a server off in no project")
	}
}

func TestRemoveProjectDeletesItsServersAndSecrets(t *testing.T) {
	st := newStore(t)
	mustSave(t, st, Draft{Name: "a", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "global"}}, "")
	mustSave(t, st, Draft{Name: "a", Project: "p1", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "p1"}}, "")
	mustSave(t, st, Draft{Name: "b", Project: "p1", Command: "b", Env: map[string]string{"TOKEN": "p1-env"}}, "")
	mustSave(t, st, Draft{Name: "a", Project: "p2", URL: "https://a.example.com/mcp", Headers: map[string]string{"X-Key": "p2"}}, "")
	for _, id := range []string{"a", "p1.a", "p2.a"} {
		if err := st.Secrets().Put(id, OAuthKey, "tok"); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"p1", "p2"} {
		if _, err := st.SetProjectOff("a", p, true); err != nil {
			t.Fatal(err)
		}
	}

	gone, err := st.RemoveProject("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 2 {
		t.Errorf("removed %+v", gone)
	}
	f, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, s := range f.Servers {
		left = append(left, s.Key())
		if s.Project == "" && !reflect.DeepEqual(s.OffIn, []string{"p2"}) {
			t.Errorf("%s offIn %v", s.Key(), s.OffIn)
		}
	}
	if !reflect.DeepEqual(left, []string{"a", "p2/a"}) {
		t.Errorf("left %v", left)
	}
	for _, sec := range []struct{ id, name string }{{"p1.a", OAuthKey}, {"p1.a", headerKey + "X-Key"}, {"p1.b", envKey + "TOKEN"}} {
		if _, ok := st.Secrets().Get(sec.id, sec.name); ok {
			t.Errorf("%s %s survived", sec.id, sec.name)
		}
	}
	for _, id := range []string{"a", "p2.a"} {
		if _, ok := st.Secrets().Get(id, OAuthKey); !ok {
			t.Errorf("%s lost its sign-in", id)
		}
	}
	if got := scopedValues(t, st, "a", "p2")["X-Key"]; got != "p2" {
		t.Errorf("p2's a has X-Key %q", got)
	}

	if _, err := st.RemoveProject(""); err == nil {
		t.Error("removed the servers of no project")
	}
}

func testCLI() CLI {
	return CLI{
		ID: "gws", StatusCommand: "gws auth status", SignInCommand: "gws auth login",
		AccountEnv: map[string]string{"GWS_CONFIG_DIR": "/cfg/gws-{account}"},
	}
}

func TestCLIAccounts(t *testing.T) {
	st := newStore(t)
	c, err := st.SaveCLI(testCLI(), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "gws" {
		t.Errorf("name defaults to the id, got %q", c.Name)
	}
	c, err = st.AddAccount("gws", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Accounts[0].Env["GWS_CONFIG_DIR"]; got != "/cfg/gws-work" {
		t.Errorf("account env %q", got)
	}
	if _, err := st.AddAccount("gws", "work"); err == nil {
		t.Error("added the same account twice")
	}
	if _, err := st.AddAccount("gws", "Bad Name"); err == nil {
		t.Error("added a badly named account")
	}

	// Editing (and renaming) keeps the accounts, whatever the edit says.
	edit := testCLI()
	edit.ID, edit.Name, edit.Accounts = "google", "Google", nil
	c, err = st.SaveCLI(edit, "gws")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Accounts) != 1 || c.Accounts[0].Name != "work" {
		t.Errorf("accounts after edit %+v", c.Accounts)
	}
	f, _ := st.Read()
	if len(f.CLIs) != 1 || f.CLIs[0].ID != "google" {
		t.Fatalf("clis %+v", f.CLIs)
	}

	c, err = st.RemoveAccount("google", "work")
	if err != nil {
		t.Fatal(err)
	}
	if c.Accounts == nil || len(c.Accounts) != 0 {
		t.Errorf("accounts %#v", c.Accounts)
	}
	if _, err := st.RemoveAccount("google", "work"); err == nil {
		t.Error("removed a missing account")
	}
	if err := st.RemoveCLI("google"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveCLI("google"); err == nil {
		t.Error("removed a missing CLI")
	}
}

func TestSaveCLIRefusals(t *testing.T) {
	st := newStore(t)
	if _, err := st.SaveCLI(testCLI(), ""); err != nil {
		t.Fatal(err)
	}
	mod := func(f func(*CLI)) CLI { c := testCLI(); c.ID = "other"; f(&c); return c }
	cases := map[string]struct {
		c        CLI
		previous string
	}{
		"bad id":          {mod(func(c *CLI) { c.ID = "A B" }), ""},
		"no status":       {mod(func(c *CLI) { c.StatusCommand = " " }), ""},
		"no sign-in":      {mod(func(c *CLI) { c.SignInCommand = "" }), ""},
		"bad pattern":     {mod(func(c *CLI) { c.SignedInPattern = "(" }), ""},
		"bad env name":    {mod(func(c *CLI) { c.AccountEnv = map[string]string{"A-B": "x"} }), ""},
		"bad account":     {mod(func(c *CLI) { c.Accounts = []Account{{Name: "No"}} }), ""},
		"duplicate id":    {testCLI(), ""},
		"unknown edit":    {mod(func(*CLI) {}), "missing"},
		"rename onto one": {testCLI(), "missing"},
	}
	for name, tc := range cases {
		if _, err := st.SaveCLI(tc.c, tc.previous); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
}
