package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/adapter/claudecode"
	"github.com/asiraky/omniplex/internal/adapter/codexapp"
	"github.com/asiraky/omniplex/internal/adapter/piapp"
	"github.com/asiraky/omniplex/internal/datalock"
	"github.com/asiraky/omniplex/internal/homemigrate"
	"github.com/asiraky/omniplex/internal/userconfig"
)

const migrateHomesUsage = "usage: omniplex migrate-homes [-db path] [--dry-run] [--reverse <manifest>]"

// runMigrateHomesCommand moves accounts on the machine's harness homes onto
// homes of their own (issue #234), or with --reverse undoes a run. The
// recency rule applies to every conversation, and accounts an earlier run
// stopped on are taken up again. A dry run reads and prints; anything else
// needs the server stopped, so its view of the config cannot go stale.
func runMigrateHomesCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate-homes", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", envStr("OMNIPLEX_DB", defaultDB()), "path to the event log database")
	dryRun := flags.Bool("dry-run", false, "print what would happen and change nothing")
	reverse := flags.String("reverse", "", "undo the run recorded in this manifest")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", migrateHomesUsage, err)
	}
	if flags.NArg() != 0 || (*dryRun && *reverse != "") {
		return errors.New(migrateHomesUsage)
	}
	opts := homemigrate.Options{Adapters: []adapter.Adapter{claudecode.New(""), codexapp.New("codex"), piapp.New("pi")}, Retry: true}

	if !*dryRun {
		if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
			return err
		}
		lock, err := datalock.Acquire(*dbPath)
		if errors.Is(err, datalock.ErrHeld) {
			return fmt.Errorf("an Omniplex server is running on %s; stop it first (--dry-run works while it runs)", *dbPath)
		}
		if err != nil {
			return err
		}
		defer lock.Release()
	}
	if *reverse != "" {
		rep, err := homemigrate.Reverse(ctx, *reverse, opts)
		if rep != nil {
			rep.Print(out)
		}
		return err
	}

	threads, closeThreads, err := homemigrate.OpenThreads(*dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", *dbPath, err)
	}
	defer closeThreads()
	opts.Threads = threads
	plan, err := homemigrate.NewPlan(ctx, opts)
	if err != nil {
		return err
	}
	if *dryRun {
		plan.Print(out)
		fmt.Fprintln(out, "\nDry run: nothing changed.")
		return nil
	}
	rep, err := homemigrate.Execute(ctx, plan)
	if rep != nil {
		for _, line := range rep.Lines() {
			fmt.Fprintln(out, line)
		}
	}
	return err
}

// migrateHomesAtStartup runs the migration before any thread or harness
// starts. Only the server on the machine's own data does: a worktree's dev
// server shares the real home folder and must not move its transcripts.
// OMNIPLEX_MIGRATE_HOMES=off turns it off. It never stops the server.
func migrateHomesAtStartup(ctx context.Context, threads homemigrate.ThreadSource, dbPath string, logf func(string, ...any), ads ...adapter.Adapter) {
	if os.Getenv("OMNIPLEX_MIGRATE_HOMES") == "off" {
		logf("homes migration: off (OMNIPLEX_MIGRATE_HOMES=off)")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logf("homes migration: skipped: %v", err)
		return
	}
	cfgPath, err := userconfig.Path()
	if err != nil {
		logf("homes migration: skipped: %v", err)
		return
	}
	if filepath.Clean(dbPath) != filepath.Join(home, ".omniplex", "omniplex.db") || filepath.Clean(cfgPath) != filepath.Join(home, ".omniplex", "config.json") {
		logf("homes migration: skipped: this server is not on the machine's own data (db %s, config %s); run omniplex migrate-homes against that", dbPath, cfgPath)
		return
	}
	homemigrate.Startup(ctx, homemigrate.Options{UserHome: home, Adapters: ads, Threads: threads}, logf)
}
