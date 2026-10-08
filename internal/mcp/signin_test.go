package mcp

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// signInURL starts a sign-in for name and returns the authorize URL it
// sends the person to, then abandons the flow.
func signInURL(t *testing.T, c *Connections, name string) *url.URL {
	t.Helper()
	run, err := c.SignIn(name, "", "http://localhost:4321")
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
	return u
}

// A second server on the same host is a second login there, so signing
// in to either asks for a fresh login rather than taking the browser's
// session for the other one.
func TestSignInAsksForAFreshLoginWhenAnotherServerSharesTheHost(t *testing.T) {
	f := newOAFake(t)
	c := newConns(t, nil)
	u := f.server().URL
	save(t, c, Draft{Name: "cf-work", URL: u})
	if got := signInURL(t, c, "cf-work").Query().Get("prompt"); got != "" {
		t.Errorf("alone: prompt=%q", got)
	}
	save(t, c, Draft{Name: "cf-home", URL: u})
	save(t, c, Draft{Name: "local", Command: "x"})
	for _, name := range []string{"cf-work", "cf-home"} {
		if got := signInURL(t, c, name).Query().Get("prompt"); got != "login" {
			t.Errorf("%s: prompt=%q", name, got)
		}
	}
}

// A sign-in that finishes after its server was removed, renamed or moved
// writes nothing back; one that finishes on an unchanged server is kept.
func TestSignInFinishingAfterAChangeWritesNothing(t *testing.T) {
	cases := map[string]struct {
		change func(c *Connections, url string) error
		kept   bool
	}{
		"unchanged": {change: func(*Connections, string) error { return nil }, kept: true},
		"removed":   {change: func(c *Connections, _ string) error { return c.store.RemoveServer("cf", "") }},
		"renamed": {change: func(c *Connections, url string) error {
			_, err := c.store.SaveServer(Draft{Name: "cf2", URL: url}, "cf")
			return err
		}},
		"moved": {change: func(c *Connections, url string) error {
			_, err := c.store.SaveServer(Draft{Name: "cf", URL: url + "/v2"}, "cf")
			return err
		}},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			f := newOAFake(t)
			c := newConns(t, nil)
			u := f.server().URL
			save(t, c, Draft{Name: "cf", URL: u})

			run, err := c.SignIn("cf", "", "http://localhost:4321")
			if err != nil {
				t.Fatal(err)
			}
			ia := newOAIA()
			done := make(chan error, 1)
			go func() { done <- run(context.Background(), ia) }()
			landed := f.authorize(ia.authURL(t))
			if err := tc.change(c, u); err != nil {
				t.Fatal(err)
			}
			ia.prompt(t).answer <- landed
			err = waitErr(t, done)

			stored := false
			for _, n := range []string{"cf", "cf2"} {
				if _, ok := c.store.Secrets().Get(n, OAuthKey); ok {
					stored = true
				}
			}
			if tc.kept && (err != nil || !stored) {
				t.Errorf("err=%v stored=%v", err, stored)
			}
			if !tc.kept && (!errors.Is(err, ErrServerChanged) || stored) {
				t.Errorf("err=%v stored=%v", err, stored)
			}
		})
	}
}
