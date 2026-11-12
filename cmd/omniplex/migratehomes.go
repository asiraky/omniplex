package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/adapter/claudecode"
	"github.com/asiraky/omniplex/internal/adapter/codexapp"
	"github.com/asiraky/omniplex/internal/adapter/piapp"
	"github.com/asiraky/omniplex/internal/datalock"
	"github.com/asiraky/omniplex/internal/homemigrate"
	"github.com/asiraky/omniplex/internal/store"
)

const migrateHomesUsage = "usage: omniplex migrate-homes [-db path] [--dry-run] [--reverse <manifest>]"

// runMigrateHomesCommand gives every account still on a machine harness home
// its own, moving the conversations Omniplex started with it. --dry-run
// prints the plan and works beside a running server; a real run or a reverse
// needs the server stopped.
func runMigrateHomesCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate-homes", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", envStr("OMNIPLEX_DB", defaultDB()), "path to the event log database")
	dryRun := flags.Bool("dry-run", false, "print what would move and change nothing")
	reverse := flags.String("reverse", "", "undo the run recorded in this manifest")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", migrateHomesUsage, err)
	}
	if flags.NArg() != 0 || (*dryRun && *reverse != "") {
		return errors.New(migrateHomesUsage)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	logf := func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) }

	if *dryRun {
		ro, err := homemigrate.OpenReadOnly(*dbPath)
		if err != nil {
			return err
		}
		defer ro.Close()
		p, err := homemigrate.PlanMigration(ctx, migrateHomesOptions(home, ro, logf))
		if err != nil {
			return err
		}
		p.Print(out)
		return nil
	}

	lock, err := datalock.Acquire(*dbPath)
	if errors.Is(err, datalock.ErrHeld) {
		return fmt.Errorf("%s is in use by a running Omniplex server; stop the server first (--dry-run works beside it)", *dbPath)
	}
	if err != nil {
		return fmt.Errorf("lock data: %w", err)
	}
	defer lock.Release()

	if *reverse != "" {
		res, err := homemigrate.Reverse(*reverse, time.Now(), logf)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Put back %d entries; restored the config of %d accounts.\n", res.Restored, len(res.Accounts))
		for _, s := range res.Skipped {
			fmt.Fprintf(out, "  left: %s\n", s)
		}
		for _, id := range res.Kept {
			fmt.Fprintf(out, "  account %s keeps its own home in its config\n", id)
		}
		return nil
	}

	if _, err := os.Stat(*dbPath); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer st.Close()
	res, err := homemigrate.Run(ctx, migrateHomesOptions(home, st, logf))
	if err != nil {
		return err
	}
	if res.Manifest == "" {
		fmt.Fprintln(out, "Nothing to migrate.")
	} else {
		fmt.Fprintf(out, "Manifest: %s (undo with omniplex migrate-homes --reverse %s)\n", res.Manifest, res.Manifest)
	}
	return nil
}

// migrateHomesAtStartup runs the migration before any session starts. It
// runs only on the default database: a worktree's dev server shares the
// machine's home and config, and must not migrate the live server's
// accounts from its own log. A failure is logged and the server starts.
func migrateHomesAtStartup(ctx context.Context, st *store.Store, dbPath string, logf func(string, ...any)) {
	if filepath.Clean(dbPath) != filepath.Clean(defaultDB()) {
		logf("home migration skipped: the database %s (-db or OMNIPLEX_DB) is not the default %s", dbPath, defaultDB())
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logf("HOME MIGRATION FAILED: find home directory: %v; accounts stay on the machine's harness homes", err)
		return
	}
	if _, err := homemigrate.Run(ctx, migrateHomesOptions(home, st, logf)); err != nil {
		logf("HOME MIGRATION FAILED: %v; accounts not yet migrated stay on the machine's harness homes", err)
	}
}

func migrateHomesOptions(home string, st homemigrate.Store, logf func(string, ...any)) homemigrate.Options {
	return homemigrate.Options{
		Home:    home,
		Store:   st,
		Drivers: homeDrivers(home, &claudecode.Adapter{}, &codexapp.Adapter{}, &piapp.Adapter{}),
		Now:     time.Now(),
		MinAge:  homemigrate.DefaultMinAge,
		Logf:    logf,
	}
}

// homeDrivers describes each adapter whose accounts have a home of their
// own: the isolating variable, the machine's home, and what is shared from
// it. The zero adapters are enough; none of this starts a harness.
func homeDrivers(userHome string, adapters ...adapter.Adapter) []homemigrate.Driver {
	var out []homemigrate.Driver
	for _, a := range adapters {
		sh, ok := a.(adapter.SharedHome)
		cf, ok2 := a.(adapter.Configurer)
		if !ok || !ok2 {
			continue
		}
		for _, f := range cf.ConfigFields() {
			if !f.Isolates {
				continue
			}
			out = append(out, homemigrate.Driver{
				ID:            a.ID(),
				Name:          a.Meta().Name,
				HomeEnv:       f.Env,
				DefaultHome:   sh.DefaultHome(userHome),
				SharedEntries: sh.SharedEntries(),
			})
			break
		}
	}
	return out
}
