package homemigrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

const logPrefix = "homes migration: "

// Print writes the plan for a person: per harness what is in the machine's
// home, per account what would move and what would not, every thread's
// check, and what the config would become.
func (p *Plan) Print(w io.Writer) {
	pf := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }
	if len(p.Harnesses) == 0 && len(p.Accounts) == 0 {
		pf("No account uses a machine home; nothing to migrate.")
	}
	for _, h := range p.Harnesses {
		pf("%s: %s", h.Driver, h.Ambient)
		pf("  conversations: %d started by Omniplex (%s), %d by something else, %d with no origin", h.Owned, size(h.Bytes), h.Others, h.Unknown)
		if h.SameFilesystem {
			pf("  same filesystem as %s: every move is a rename", p.Instances)
		} else {
			pf("  NOT the same filesystem: %s", h.FilesystemNote)
		}
	}
	for _, a := range p.Accounts {
		pf("")
		title := fmt.Sprintf("%s account %s (%s)", a.Driver, a.Instance, a.DisplayName)
		if a.Leftover {
			title += ", leftovers of an earlier run"
		}
		pf("%s: %s", title, a.Why)
		if a.Abort != "" {
			pf("  NOT MIGRATED: %s", a.Abort)
		}
		if !a.Leftover {
			pf("  home: %s -> %s", a.Ambient, a.Target)
		}
		n, files := a.Bytes()
		pf("  moves %d conversations (%d files, %s)", len(a.Moves), files, size(n))
		for _, c := range a.Moves {
			pf("    %s%s", c.Units[0].Rel, sidecar(c))
		}
		for _, l := range []struct {
			name  string
			convs []*Conversation
		}{{"left for a later run (recently modified)", a.Recent}, {"not moved (conflict)", a.Conflicts}, {"left with another account", a.Left}} {
			if len(l.convs) == 0 {
				continue
			}
			pf("  %s: %d", l.name, len(l.convs))
			for _, c := range l.convs {
				pf("    %s%s: %s", c.Units[0].Rel, sidecar(c), c.Reason)
			}
		}
		if len(a.Threads) > 0 {
			pf("  threads:")
			for _, t := range a.Threads {
				line := fmt.Sprintf("    %s %s %s", t.Status, t.Thread, t.SessionID)
				if t.Title != "" {
					line += fmt.Sprintf(" %q", t.Title)
				}
				if t.Detail != "" {
					line += ": " + t.Detail
				}
				pf("%s", line)
			}
		}
		if a.Abort == "" && !a.Leftover {
			if a.Entry == nil {
				pf("  config: adds provider %s (%s) with %s=%s", a.Instance, a.DisplayName, a.HomeEnv, a.Target)
			} else {
				pf("  config: sets %s=%s on provider %s, keeping its id and name", a.HomeEnv, a.Target, a.Instance)
			}
			pf("  after: the account needs signing in again (credentials stay where they are)")
		}
	}
	if len(p.Notes) > 0 {
		pf("")
		for _, n := range p.Notes {
			pf("note: %s", n)
		}
	}
}

func sidecar(c *Conversation) string {
	if len(c.Units) > 1 {
		return " (+ " + c.Units[1].Rel + "/)"
	}
	return ""
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Lines is the run for a log: one line per account, then a warning per
// thread whose conversation is nowhere, then the notes.
func (r *Report) Lines() []string {
	var out []string
	for _, a := range r.Accounts {
		who := fmt.Sprintf("%s account %s (%s)", a.Driver, a.Instance, a.DisplayName)
		switch {
		case a.Aborted != "":
			out = append(out, fmt.Sprintf("%sNOT migrating %s: %s; it stays on %s, config unchanged", logPrefix, who, a.Aborted, a.Ambient))
		case a.Stopped != "":
			out = append(out, fmt.Sprintf("%sSTOPPED on %s: %s; it stays on %s, config unchanged; manifest %s", logPrefix, who, a.Stopped, a.Ambient, r.Manifest))
		case a.Leftover && a.Moved == 0:
			out = append(out, fmt.Sprintf("%s%s: %d conversations still in use, left in %s for a later run", logPrefix, who, a.Recent, a.Ambient))
		case a.Leftover:
			out = append(out, fmt.Sprintf("%s%s: moved %d conversations an earlier run left, %d still in use, %d skipped as conflicts; manifest %s", logPrefix, who, a.Moved, a.Recent, a.Conflicts, r.Manifest))
		default:
			line := fmt.Sprintf("%s%s now uses %s: %d conversations moved, %d left for later (in use), %d skipped as conflicts; manifest %s; it needs signing in", logPrefix, who, a.Target, a.Moved, a.Recent, a.Conflicts, r.Manifest)
			if a.Sharing != "" {
				line += "; sharing the user's configuration failed: " + a.Sharing
			}
			out = append(out, line)
		}
		for _, t := range a.Missing {
			out = append(out, fmt.Sprintf("%s%s: thread %s resumes %s, which is %s", logPrefix, a.Instance, t.Thread, t.SessionID, t.Detail))
		}
	}
	if len(r.Accounts) == 0 {
		out = append(out, logPrefix+"nothing to migrate")
	}
	for _, n := range r.Notes {
		out = append(out, logPrefix+n)
	}
	return out
}

// Print writes what Reverse did.
func (r *ReverseReport) Print(w io.Writer) {
	fmt.Fprintf(w, "Reversed %s: %d conversations back where they were", r.Manifest, r.Restored)
	if r.Changed > 0 {
		fmt.Fprintf(w, " (%d were written to after they moved and went back as they are now)", r.Changed)
	}
	fmt.Fprintln(w)
	for _, l := range []struct {
		name  string
		items []string
	}{{"not moved back", r.Conflicts}, {"missing", r.Missing}, {"cross-filesystem asides left in place", r.Asides}, {"config", r.Config}, {"note", r.Notes}} {
		for _, item := range l.items {
			fmt.Fprintf(w, "%s: %s\n", l.name, item)
		}
	}
	fmt.Fprintln(w, "The server will not migrate again until you run omniplex migrate-homes.")
}

// Startup runs the migration as the server starts, before any thread or
// harness: conversations its threads resume are not in use yet, so they move
// even if recently modified. It only logs; whatever happens, the server
// starts.
func Startup(ctx context.Context, opts Options, logf func(format string, args ...any)) {
	defer func() {
		if r := recover(); r != nil {
			logf("%spanicked: %v; the server starts anyway, see the newest manifest in ~/.omniplex/migrations", logPrefix, r)
		}
	}()
	opts.ExemptReferenced = true
	opts.Retry = false
	if opts.ServerEnv == nil {
		opts.ServerEnv = os.Getenv
	}
	p, err := NewPlan(ctx, opts)
	if err != nil {
		logf("%scould not plan: %v; nothing moved", logPrefix, err)
		return
	}
	rep, err := Execute(ctx, p)
	if rep != nil {
		for _, line := range rep.Lines() {
			logf("%s", line)
		}
	}
	if err != nil {
		logf("%s%v", logPrefix, strings.TrimSpace(err.Error()))
	}
}
