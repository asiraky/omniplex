package homemigrate

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
)

// Print writes the plan for a person to check before a run: per account and
// per harness what moves, stays and is skipped, with bytes; the per-thread
// assertion; every skip and conflict; and whether moves will be renames.
func (p *Plan) Print(w io.Writer) {
	if len(p.Accounts) == 0 {
		fmt.Fprintln(w, "No account runs on a machine harness home; nothing to migrate.")
		for _, n := range p.Notes {
			fmt.Fprintf(w, "note: %s\n", n)
		}
		return
	}
	if !p.HasWork() {
		fmt.Fprintln(w, "Nothing would change.")
	}

	fmt.Fprintln(w, "Accounts:")
	for _, a := range p.Accounts {
		kind := "config entry kept, home set"
		switch {
		case a.Implicit:
			kind = "implicit default, config entry created"
		case a.CatchAll:
			kind = "already on its own home; picks up what earlier runs skipped"
		}
		fmt.Fprintf(w, "  %s (%s, %s): %s -> %s [%s]\n", a.ID, a.Name, a.Driver, a.Shared, a.Home, kind)
		if a.Status == statusAborted {
			fmt.Fprintf(w, "    NOT MIGRATED: %s\n", a.Reason)
			continue
		}
		files, size := 0, int64(0)
		for _, c := range a.Moves {
			files += len(c.Files)
			size += c.Bytes
		}
		fmt.Fprintf(w, "    move %d conversations (%d files, %s); skip %d", len(a.Moves), files, humanBytes(size), len(a.Skipped))
		if a.After != nil {
			fmt.Fprint(w, "; config entry written; needs signing in")
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "Per harness:")
	drivers := map[string]bool{}
	for d := range p.Kept {
		drivers[d] = true
	}
	var names []string
	for d := range drivers {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		move, skip, stay := 0, 0, 0
		var size int64
		for _, a := range p.Accounts {
			if a.Driver != d {
				continue
			}
			move += len(a.Moves)
			skip += len(a.Skipped)
			for _, c := range a.Moves {
				size += c.Bytes
			}
		}
		for _, c := range p.Stay {
			if c.Driver == d {
				stay++
			}
		}
		fmt.Fprintf(w, "  %s: move %d (%s), skip %d, Omniplex-started staying %d, not started by Omniplex staying %d\n",
			d, move, humanBytes(size), skip, stay, p.Kept[d])
	}

	if len(p.Threads) > 0 {
		fmt.Fprintln(w, "Threads:")
		for _, t := range p.Threads {
			mark := "ok  "
			if !t.OK {
				mark = "FAIL"
			}
			fmt.Fprintf(w, "  %s %s account %s session %s: %s", mark, t.Thread, t.Account, t.Session, t.Status)
			if t.Where != "" {
				fmt.Fprintf(w, " (%s)", t.Where)
			}
			if t.Conflicts != "" {
				fmt.Fprintf(w, "; conflict: %s", t.Conflicts)
			}
			fmt.Fprintln(w)
		}
	}

	var skipped bool
	for _, a := range p.Accounts {
		for _, c := range a.Skipped {
			if !skipped {
				fmt.Fprintln(w, "Skipped (a later run picks these up):")
				skipped = true
			}
			fmt.Fprintf(w, "  %s %s: %s\n", a.ID, c.Entries[len(c.Entries)-1].Src, c.Reason)
		}
	}
	if len(p.Stay) > 0 {
		fmt.Fprintln(w, "Omniplex-started, staying:")
		for _, c := range p.Stay {
			fmt.Fprintf(w, "  %s: %s\n", c.Entries[len(c.Entries)-1].Src, c.Reason)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}

	fmt.Fprintln(w, "Same filesystem:")
	instances := filepath.Join(p.Home, ".omniplex", "instances")
	for _, s := range p.SameFS {
		switch {
		case s.Err != "":
			fmt.Fprintf(w, "  %s and %s: cannot tell (%s)\n", s.Ambient, instances, s.Err)
		case s.Same:
			fmt.Fprintf(w, "  %s and %s: same filesystem; every move is a rename\n", s.Ambient, instances)
		default:
			fmt.Fprintf(w, "  %s and %s: DIFFERENT filesystems; conversations are copied and each original is left aside\n", s.Ambient, instances)
		}
	}
}
