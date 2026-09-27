package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/relocate"
	"github.com/asiraky/omniplex/internal/userconfig"
)

func runRelocateCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("relocate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", envStr("OMNIPLEX_DB", defaultDB()), "path to the event log database")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("usage: omniplex relocate [-db path] <old-root> <new-root>: %w", err)
	}
	if flags.NArg() != 2 {
		return fmt.Errorf("usage: omniplex relocate [-db path] <old-root> <new-root>")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	configDirs, err := claudeConfigDirs()
	if err != nil {
		return err
	}
	report, err := relocate.Run(ctx, flags.Arg(0), flags.Arg(1), relocate.Options{
		DBPath:           *dbPath,
		HomeDir:          home,
		ClaudeConfigDir:  os.Getenv("CLAUDE_CONFIG_DIR"),
		ClaudeConfigDirs: configDirs,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Relocated %d threads; rewrote %d events, %d snapshots, %d command results, %d workspace files, and moved %d Claude transcript directories.\n",
		report.Database.Threads, report.Database.Events, report.Database.Snapshots,
		report.Database.Commands, report.WorkspaceFiles, report.ClaudeTranscripts)
	if report.GitRepaired {
		fmt.Fprintln(out, "Repaired Git worktree links.")
	}
	return nil
}

func claudeConfigDirs() (map[string]string, error) {
	cfg, err := userconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("load user config: %w", err)
	}
	secrets, err := provider.OpenSecretStore()
	if err != nil {
		return nil, fmt.Errorf("open provider secrets: %w", err)
	}
	return claudeConfigDirsFrom(cfg, secrets)
}

func claudeConfigDirsFrom(cfg userconfig.Config, secrets *provider.SecretStore) (map[string]string, error) {
	dirs := map[string]string{}
	for _, raw := range cfg.Providers {
		instance, err := provider.Parse(raw)
		if err != nil || instance.Driver != "claude" {
			continue
		}
		for _, variable := range instance.Env {
			if variable.Name != "CLAUDE_CONFIG_DIR" {
				continue
			}
			if !variable.Sensitive {
				dirs[instance.ID] = variable.Value
				continue
			}
			configured, ok := secrets.Get(instance.ID, variable.Name)
			if !ok {
				return nil, fmt.Errorf("resolve provider instance %s: no stored secret for %s", instance.ID, variable.Name)
			}
			dirs[instance.ID] = configured
		}
	}
	return dirs, nil
}
