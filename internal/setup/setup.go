// Package setup answers the first-run question: can this machine run a thread
// yet, and if not, what does the person in front of it have to install?
//
// The desktop app shows this on first launch to people who have never opened a
// terminal, so every missing piece comes with the fix in plain words. Git is
// required; of the harnesses, one working is enough.
package setup

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// Harnesses the setup screen asks about. Others (Pi) are for people who
// already know they want them, and listing them as missing would read as a
// problem to someone who does not.
var harnessIDs = []string{"claude", "codex"}

// Check is one thing the machine needs.
type Check struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"` // "tool" or "harness"
	Availability adapter.Availability `json:"availability"`
}

// Report is the whole answer.
type Report struct {
	Platform string  `json:"platform"`
	Ready    bool    `json:"ready"`
	Checks   []Check `json:"checks"`
}

// Harness is what the report needs to know about one harness.
type Harness struct {
	ID           string
	Name         string
	Availability adapter.Availability
}

// Build assembles the report: ready means git works and at least one of the
// harnesses it asks about can start a thread.
func Build(platform string, git adapter.Availability, harnesses []Harness) Report {
	r := Report{
		Platform: platform,
		Checks:   []Check{{ID: "git", Name: "Git", Kind: "tool", Availability: git}},
	}
	anyHarness := false
	for _, id := range harnessIDs {
		for _, h := range harnesses {
			if h.ID != id {
				continue
			}
			r.Checks = append(r.Checks, Check{ID: h.ID, Name: h.Name, Kind: "harness", Availability: h.Availability})
			anyHarness = anyHarness || h.Availability.OK()
		}
	}
	r.Ready = git.OK() && anyHarness
	return r
}

// Git reports whether git runs here. Found on the PATH is not enough: on a Mac
// without Apple's command line tools /usr/bin/git is a stub that fails (and
// offers to install them).
func Git(ctx context.Context) adapter.Availability {
	return gitAt(ctx, "git", runtime.GOOS)
}

func gitAt(ctx context.Context, bin, goos string) adapter.Availability {
	path, err := exec.LookPath(bin)
	if err != nil {
		return adapter.Unavailable("Git is not installed. Omniplex uses it to keep each thread's work separate.", gitRemedy(goos)...)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return adapter.Unavailable("Git was found at "+path+" but does not run.", gitRemedy(goos)...)
	}
	return adapter.Ready(map[string]string{"git": path, "gitVer": strings.TrimSpace(string(out))})
}

func gitRemedy(goos string) []adapter.Remedy {
	switch goos {
	case "darwin":
		return []adapter.Remedy{
			{Text: "Install Apple's command line tools, which include Git", Command: "xcode-select --install"},
			{Text: "Or download Git for Mac", URL: "https://git-scm.com/download/mac"},
		}
	case "windows":
		return []adapter.Remedy{
			{Text: "Install Git for Windows (Claude Code needs it too)", URL: "https://git-scm.com/download/win"},
		}
	default:
		return []adapter.Remedy{
			{Text: "Install git with your package manager", URL: "https://git-scm.com/download/linux"},
		}
	}
}
