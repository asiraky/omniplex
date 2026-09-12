package usagelimit

import (
	"testing"
	"time"
)

func brisbane(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return loc
}

// The two messages that started this, verbatim, are the cases that matter
// most: a change in either harness's wording is what this file exists to
// catch.
func TestDetectHarnessMessages(t *testing.T) {
	loc := brisbane(t)
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, loc)

	claude := "You've hit your session limit · resets 11:10am (Australia/Brisbane)"
	hit, ok := Detect(claude, now)
	if !ok {
		t.Fatalf("claude message was not read as a limit")
	}
	if want := time.Date(2026, 9, 8, 11, 10, 0, 0, loc); !hit.ResetAt.Equal(want) {
		t.Fatalf("claude reset = %s, want %s", hit.ResetAt, want)
	}

	codex := "This turn ended with an error before it finished.\n\nYou've hit your usage limit. " +
		"Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage " +
		"to purchase more credits or try again at 3:49 PM."
	hit, ok = Detect(codex, now)
	if !ok {
		t.Fatalf("codex message was not read as a limit")
	}
	// No zone in the message, so it is read in the server's own — which the
	// test pins by anchoring now there.
	if want := time.Date(2026, 9, 8, 15, 49, 0, 0, loc); !hit.ResetAt.Equal(want) {
		t.Fatalf("codex reset = %s, want %s", hit.ResetAt, want)
	}
}

// A time already past today is tomorrow's: a window that resets at 3am, read
// at 11pm, must not schedule a resume twenty hours in the past.
func TestDetectRollsOverMidnight(t *testing.T) {
	loc := brisbane(t)
	now := time.Date(2026, 9, 8, 23, 0, 0, 0, loc)
	hit, ok := Detect("You've hit your usage limit · resets 3am (Australia/Brisbane)", now)
	if !ok {
		t.Fatal("not read as a limit")
	}
	if want := time.Date(2026, 9, 9, 3, 0, 0, 0, loc); !hit.ResetAt.Equal(want) {
		t.Fatalf("reset = %s, want %s", hit.ResetAt, want)
	}
}

func TestDetectForms(t *testing.T) {
	loc := brisbane(t)
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, loc)
	cases := []struct {
		name string
		text string
		want time.Time
	}{
		{"epoch", "Claude AI usage limit reached|1789000000", time.Unix(1789000000, 0)},
		{"epoch millis", "Claude AI usage limit reached|1789000000000", time.UnixMilli(1789000000000)},
		{"relative", "You've hit your usage limit. Try again in 4h 12m.", now.Add(4*time.Hour + 12*time.Minute)},
		{"relative minutes", "rate limit exceeded; retry in 45 minutes", now.Add(45 * time.Minute)},
		{"24 hour clock", "usage limit reached. Your limit will reset at 15:30 (Australia/Brisbane)", time.Date(2026, 9, 8, 15, 30, 0, 0, loc)},
		{"midnight", "session limit · resets 12:00am (Australia/Brisbane)", time.Date(2026, 9, 9, 0, 0, 0, 0, loc)},
		{"noon", "session limit · resets 12:30pm (Australia/Brisbane)", time.Date(2026, 9, 8, 12, 30, 0, 0, loc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, ok := Detect(c.text, now)
			if !ok {
				t.Fatalf("not read as a limit")
			}
			if !hit.ResetAt.Equal(c.want) {
				t.Fatalf("reset = %s, want %s", hit.ResetAt, c.want)
			}
		})
	}
}

// A limit with no time is still a limit. Reporting it as "not a limit" is what
// would leave the work sitting there; the caller backs off instead.
func TestDetectWithoutATime(t *testing.T) {
	now := time.Now()
	for _, text := range []string{
		"You've hit your usage limit.",
		"429 too many requests",
		"Your credit balance is too low to run this request.",
	} {
		hit, ok := Detect(text, now)
		if !ok {
			t.Fatalf("%q was not read as a limit", text)
		}
		if !hit.ResetAt.IsZero() {
			t.Fatalf("%q invented a reset time: %s", text, hit.ResetAt)
		}
	}
}

func TestDetectIgnoresOtherFailures(t *testing.T) {
	now := time.Now()
	for _, text := range []string{
		"",
		"claude exited: connect ECONNREFUSED",
		"Please run /login to authenticate",
		"prompt is too long: the context limit was reached",
		"the conversation exceeded the model's context window",
		"the turn failed and the harness did not say why",
	} {
		if _, ok := Detect(text, now); ok {
			t.Fatalf("%q was read as a usage limit", text)
		}
	}
}
