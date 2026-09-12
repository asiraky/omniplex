// Package usagelimit recognises the one failure that fixes itself: a provider
// saying the account's usage or spend window is exhausted.
//
// Every harness reports it as an ordinary failed turn — a result with an error
// message, or a bridge that exited — so from the outside it is indistinguishable
// from a bug, and the only thing on offer is a Continue button that is
// guaranteed to fail until the window reopens. It is also, in practice, the
// failure that happens while nobody is watching: the window resets at 3am, and
// the work sits there until morning.
//
// The messages are English prose written for a human, and they change between
// releases, so this reads them defensively: the keyword gate decides whether a
// message is about a usage limit at all, and the time parsing is allowed to
// fail without changing that answer. "It is a limit, and I do not know when it
// lifts" is a useful answer — the caller backs off instead of waiting for a
// moment it was never told.
package usagelimit

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// Hit is a recognised limit. ResetAt is zero when the harness named a limit
// but no time.
type Hit struct {
	ResetAt time.Time
}

// needles are the phrases that make a message about a usage or spend window.
// They are matched against lowercased text, and any one of them is enough.
var needles = []string{
	"usage limit",
	"session limit",
	"spending limit",
	"spend limit",
	"rate limit",
	"rate_limit",
	"weekly limit",
	"hourly limit",
	"quota",
	"out of credits",
	"insufficient credit",
	"credit balance",
	"purchase more credits",
	"upgrade to pro",
	"limit reached",
	"429",
}

// notLimits are the other things that run out. A context window filling up is
// also "a limit reached", and resuming it in five hours fixes nothing, so a
// message about one is never treated as a usage limit however it is phrased.
var notLimits = []string{
	"context limit",
	"context window",
	"token limit",
	"maximum context",
	"too many tokens",
}

// epochPattern is the machine-readable form Claude Code has used alongside the
// prose: "Claude AI usage limit reached|1746000000".
var epochPattern = regexp.MustCompile(`limit reached\s*\|\s*(\d{9,13})`)

// clockPattern reads the wall time a message points at: "resets 11:10am",
// "try again at 3:49 PM", "your limit will reset at 3pm". The zone, if any,
// arrives separately — see zonePattern — because it is written after it and
// only sometimes.
var clockPattern = regexp.MustCompile(`(?:reset[s]?(?:\s+at)?|try again at|available again at|again at)\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?`)

// zonePattern finds an IANA zone in parentheses, which is how both harnesses
// disambiguate the clock time they just printed: "(Australia/Brisbane)".
var zonePattern = regexp.MustCompile(`\(([A-Za-z][A-Za-z_+-]*(?:/[A-Za-z][A-Za-z0-9_+-]*)+)\)`)

// relativePattern reads a wait expressed as a duration: "try again in 4h 12m",
// "retry in 45 minutes".
var relativePattern = regexp.MustCompile(`(?:try again|retry|again)\s+in\s+(?:(\d+)\s*(?:h|hours?|hrs?)\s*)?(?:(\d+)\s*(?:m|minutes?|mins?))?`)

// Detect reports whether some text a harness produced is a usage limit, and
// when the window reopens if it said. now anchors the wall-clock forms: a
// message pointing at 11:10am means the next 11:10am, which may be tomorrow.
func Detect(text string, now time.Time) (Hit, bool) {
	low := strings.ToLower(text)
	if strings.TrimSpace(low) == "" {
		return Hit{}, false
	}
	for _, no := range notLimits {
		if strings.Contains(low, no) {
			return Hit{}, false
		}
	}
	hit := false
	for _, needle := range needles {
		if strings.Contains(low, needle) {
			hit = true
			break
		}
	}
	if !hit {
		return Hit{}, false
	}
	return Hit{ResetAt: resetAt(low, now)}, true
}

// resetAt is the moment named in the message, or the zero time when it named
// none. It never guesses: an unparsed time is reported as unknown so the
// caller can back off rather than waiting on a number this file invented.
func resetAt(low string, now time.Time) time.Time {
	if m := epochPattern.FindStringSubmatch(low); m != nil {
		if secs, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			// Providers have sent both seconds and milliseconds here.
			if secs > 1e12 {
				return time.UnixMilli(secs)
			}
			return time.Unix(secs, 0)
		}
	}
	if m := relativePattern.FindStringSubmatch(low); m != nil && (m[1] != "" || m[2] != "") {
		hours, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		if d := time.Duration(hours)*time.Hour + time.Duration(mins)*time.Minute; d > 0 {
			return now.Add(d)
		}
	}
	if m := clockPattern.FindStringSubmatch(low); m != nil {
		return nextWallClock(m, zoneIn(low, now), now)
	}
	return time.Time{}
}

// zoneIn is the zone the message printed, falling back to the server's own.
// A named zone this build cannot load — an abbreviation, a typo, a machine
// without tzdata — falls back the same way rather than failing the parse: the
// wrong hour is still worth having, because the resumed turn either works or
// re-arms itself.
func zoneIn(low string, now time.Time) *time.Location {
	if m := zonePattern.FindStringSubmatch(low); m != nil {
		if loc, err := time.LoadLocation(m[1]); err == nil {
			return loc
		}
	}
	return now.Location()
}

// nextWallClock turns "11:10am" into the next instant that reads 11:10am in
// the given zone. A message without am/pm — "reset at 15:00" — is read as a
// 24-hour clock.
func nextWallClock(m []string, loc *time.Location, now time.Time) time.Time {
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour > 23 {
		return time.Time{}
	}
	minute := 0
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	if minute > 59 {
		return time.Time{}
	}
	switch meridiem := strings.ReplaceAll(m[3], ".", ""); meridiem {
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 12 {
			hour += 12
		}
	}
	local := now.In(loc)
	at := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}
