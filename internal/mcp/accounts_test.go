package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

func addAccount(t *testing.T, st *Store, server string, d AccountDraft, previous string) Server {
	t.Helper()
	s, err := st.SaveServerAccount(server, d, previous)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memberValues(t *testing.T, st *Store, name string) (env, headers map[string]string) {
	t.Helper()
	m, _, ok, err := st.Member(name)
	if err != nil || !ok {
		t.Fatalf("member %s: ok=%v err=%v", name, ok, err)
	}
	return st.Values(m)
}

// Each account goes to the agent as a server of its own, so its tools are
// told apart by name, with its own token and its own values over the
// server's.
func TestAccountsReachSessionsUnderTheirOwnNames(t *testing.T) {
	c := newConns(t, nil)
	save(t, c, Draft{Name: "cf", URL: "https://cf.example.com/mcp", Headers: map[string]string{"X-Team": "base"}, Off: []string{"b"}})
	addAccount(t, c.store, "cf", AccountDraft{Label: "work"}, "")
	addAccount(t, c.store, "cf", AccountDraft{Label: "home", Headers: map[string]string{"X-Team": "mine"}}, "")
	signIn(t, c, "cf", "https://cf.example.com/mcp", map[string]any{"accessToken": "tok-cf"})
	signIn(t, c, "cf-work", "https://cf.example.com/mcp", map[string]any{"accessToken": "tok-work"})

	defs := c.Servers(context.Background(), "a", []string{"http"})
	if got := strings.Join(names(defs), ","); got != "cf,cf-work,cf-home" {
		t.Fatalf("names %s", got)
	}
	want := []map[string]string{
		{"X-Team": "base", "Authorization": "Bearer tok-cf"},
		{"X-Team": "base", "Authorization": "Bearer tok-work"},
		// Not signed in: no token, and never another account's.
		{"X-Team": "mine"},
	}
	for i, d := range defs {
		if d.URL != "https://cf.example.com/mcp" || len(d.Headers) != len(want[i]) {
			t.Errorf("%s: %+v", d.Name, d)
		}
		for k, v := range want[i] {
			if d.Headers[k] != v {
				t.Errorf("%s: %s = %q, want %q", d.Name, k, d.Headers[k], v)
			}
		}
	}
	// The server's switches are its accounts' too.
	if defs := c.Servers(context.Background(), "b", []string{"http"}); len(defs) != 0 {
		t.Errorf("switched off, yet got %v", names(defs))
	}
	// Reconnecting finds an account by the name the agent has it under.
	if d, err := c.Server(context.Background(), "a", []string{"http"}, "cf-home"); err != nil || d.Headers["X-Team"] != "mine" {
		t.Errorf("reconnect cf-home: %+v %v", d, err)
	}
}

func TestAccountNamesNeverCollide(t *testing.T) {
	st := newStore(t)
	for _, d := range []Draft{{Name: "cf", URL: "https://cf.example.com/mcp", Headers: map[string]string{"X-Team": "t"}}, {Name: "gh-work", Command: "gh"}, {Name: "gh", Command: "gh"}} {
		if _, err := st.SaveServer(d, ""); err != nil {
			t.Fatal(err)
		}
	}
	addAccount(t, st, "cf", AccountDraft{Label: "work"}, "")

	refusals := map[string]func() error{
		"a server under an account's name": func() error {
			_, err := st.SaveServer(Draft{Name: "cf-work", Command: "x"}, "")
			return err
		},
		"an account under a server's name": func() error {
			_, err := st.SaveServerAccount("gh", AccountDraft{Label: "work"}, "")
			return err
		},
		"a rename that lands an account on a server": func() error {
			_, err := st.SaveServer(Draft{Name: "gh", URL: "https://cf.example.com/mcp"}, "cf")
			return err
		},
		"the same label twice": func() error {
			_, err := st.SaveServerAccount("cf", AccountDraft{Label: "work"}, "")
			return err
		},
		"a label the server's name makes too long": func() error {
			_, err := st.SaveServerAccount("cf", AccountDraft{Label: strings.Repeat("x", 46)}, "")
			return err
		},
		"a header the server does not have": func() error {
			_, err := st.SaveServerAccount("cf", AccountDraft{Label: "home", Headers: map[string]string{"X-Other": "v"}}, "")
			return err
		},
		"a label outside the naming rule": func() error {
			_, err := st.SaveServerAccount("cf", AccountDraft{Label: "Work Account"}, "")
			return err
		},
	}
	for name, try := range refusals {
		if try() == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	// Relabelling an account to its own label is no collision.
	addAccount(t, st, "cf", AccountDraft{Label: "work"}, "work")
}

func TestAccountSecretsFollowTheirServer(t *testing.T) {
	st := newStore(t)
	const u = "https://cf.example.com/mcp"
	if _, err := st.SaveServer(Draft{Name: "a", URL: u, Headers: map[string]string{"X-Key": "base", "X-Team": "t"}}, ""); err != nil {
		t.Fatal(err)
	}
	addAccount(t, st, "a", AccountDraft{Label: "w", Headers: map[string]string{"X-Key": "own"}}, "")
	if err := st.Secrets().Put("a-w", OAuthKey, `{"accessToken":"t"}`); err != nil {
		t.Fatal(err)
	}
	signedIn := func(name string) bool { _, ok := st.Secrets().Get(name, OAuthKey); return ok }

	// Renaming the server, same address: the account moves with its sign-in.
	if _, err := st.SaveServer(Draft{Name: "b", URL: u, Headers: map[string]string{"X-Key": "", "X-Team": ""}}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, h := memberValues(t, st, "b-w"); h["X-Key"] != "own" || h["X-Team"] != "t" || !signedIn("b-w") {
		t.Errorf("after rename %v signedIn=%v", h, signedIn("b-w"))
	}
	if _, ok := st.Secrets().Get("a-w", headerKey+"X-Key"); ok || signedIn("a-w") {
		t.Error("left secrets under the old account name")
	}

	// Relabelling keeps the sign-in; an empty value keeps the account's own.
	addAccount(t, st, "b", AccountDraft{Label: "v", Headers: map[string]string{"X-Key": ""}}, "w")
	if _, h := memberValues(t, st, "b-v"); h["X-Key"] != "own" || !signedIn("b-v") || signedIn("b-w") {
		t.Errorf("after relabel %v", h)
	}

	// Leaving a name out falls back to the server's value.
	addAccount(t, st, "b", AccountDraft{Label: "v"}, "v")
	if _, h := memberValues(t, st, "b-v"); h["X-Key"] != "base" {
		t.Errorf("own value outlived being left out: %v", h)
	}
	addAccount(t, st, "b", AccountDraft{Label: "v", Headers: map[string]string{"X-Team": "own-team"}}, "v")

	// The server dropping a header drops the account's own value for it; a
	// new address drops the account's sign-in, which was for the old one.
	srv, err := st.SaveServer(Draft{Name: "b", URL: "https://other.example.com/mcp", Headers: map[string]string{"X-Key": ""}}, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(srv.Accounts) != 1 || len(srv.Accounts[0].HeaderNames) != 0 {
		t.Errorf("accounts after header removed: %+v", srv.Accounts)
	}
	if _, ok := st.Secrets().Get("b-v", headerKey+"X-Team"); ok || signedIn("b-v") {
		t.Error("kept a dropped header or a sign-in for the old address")
	}

	// Removing an account, then the server, leaves nothing behind.
	if err := st.Secrets().Put("b-v", OAuthKey, `{"accessToken":"t"}`); err != nil {
		t.Fatal(err)
	}
	addAccount(t, st, "b", AccountDraft{Label: "x", Headers: map[string]string{"X-Key": "x"}}, "")
	if srv, err := st.RemoveServerAccount("b", "v"); err != nil || len(srv.Accounts) != 1 || signedIn("b-v") {
		t.Errorf("remove account: %+v %v", srv, err)
	}
	if err := st.RemoveServer("b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Secrets().Get("b-x", headerKey+"X-Key"); ok {
		t.Error("removing the server left an account's secrets")
	}
}

// Checking a server checks each of its accounts with its own token;
// checking or signing out an account touches only that one. Both answer
// with the whole server, which is what the page shows.
func TestCheckAndSignOutByAccount(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	c := newConns(t, srv.Client())
	save(t, c, Draft{Name: "cf", URL: srv.URL})
	addAccount(t, c.store, "cf", AccountDraft{Label: "work"}, "")
	addAccount(t, c.store, "cf", AccountDraft{Label: "home"}, "")
	signIn(t, c, "cf", srv.URL, map[string]any{"accessToken": "tok-cf"})
	signIn(t, c, "cf-work", srv.URL, map[string]any{"accessToken": "tok-work"})
	took := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := slices.Clone(seen)
		seen = nil
		slices.Sort(out)
		return out
	}

	v, err := c.Check(context.Background(), "cf")
	if err != nil {
		t.Fatal(err)
	}
	if got := took(); !slices.Equal(got, []string{"", "Bearer tok-cf", "Bearer tok-work"}) {
		t.Errorf("checked with %q", got)
	}
	if v.Name != "cf" || v.Status != StatusConnected || len(v.Accounts) != 2 {
		t.Fatalf("view %+v", v)
	}
	work, home := v.Accounts[0], v.Accounts[1]
	if work.Name != "cf-work" || work.Status != StatusConnected || !work.OAuth || home.Status != StatusSignIn || home.OAuth {
		t.Errorf("accounts %+v", v.Accounts)
	}

	v, err = c.SignOut(context.Background(), "cf-work")
	if err != nil {
		t.Fatal(err)
	}
	if got := took(); !slices.Equal(got, []string{""}) {
		t.Errorf("sign-out checked with %q", got)
	}
	if v.Name != "cf" || !v.OAuth || v.Accounts[0].OAuth || v.Accounts[0].Status != StatusSignIn {
		t.Errorf("after signing out work: %+v", v)
	}
}

// Once a server has several accounts, signing in asks for a fresh login, so
// the browser's current session is not taken for the account being added.
func TestSignInAsksForAFreshLoginOnceThereAreAccounts(t *testing.T) {
	f := newOAFake(t)
	c := newConns(t, nil)
	save(t, c, Draft{Name: "cf", URL: f.server().URL})

	prompt := func(name string) string {
		t.Helper()
		run, err := c.SignIn(name, "http://localhost:4321")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ia := newOAIA()
		done := make(chan error, 1)
		go func() { done <- run(ctx, ia) }()
		u, _ := url.Parse(ia.authURL(t))
		cancel()
		<-done
		return u.Query().Get("prompt")
	}
	if got := prompt("cf"); got != "" {
		t.Errorf("one account: prompt=%q", got)
	}
	addAccount(t, c.store, "cf", AccountDraft{Label: "work"}, "")
	for _, name := range []string{"cf", "cf-work"} {
		if got := prompt(name); got != "login" {
			t.Errorf("%s: prompt=%q", name, got)
		}
	}
}
