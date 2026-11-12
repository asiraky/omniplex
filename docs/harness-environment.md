# Harness environment

A harness process receives Omniplex's own environment, less what its adapter
strips (below), plus the account's overlay (its config home, API keys and the
like) and nothing else. It never receives the project's dotenv files. Projects load their
own, and that matters: Vite's `loadEnv`, dotenv and Node's `--env-file` all keep
a variable that is already set, so a thread carrying the main checkout's
`.env.local` would override the files of any worktree it starts a dev server or
a worker in.

Per adapter:

- **Claude.** The bridge runs under Bun where present, with the project as its
  cwd, and Bun loads that directory's `.env*` files into `process.env` — as a
  script runtime and inside the compiled bundle. Two things stop it. Bun is
  started with `--env-file=/dev/null` (`--no-env-file` is silently ignored by
  Bun 1.2). And the host lists the names of the variables it set in the bridge
  config (`envKeys`); the bridge passes only those to Claude Code through the
  SDK's `env` option. The second covers the bundle, which takes no runtime
  flags. Names rather than values, because argv is world-readable. Bun never
  overwrites a variable that was already set, so the listed values are the
  host's. A Claude Code installed through npm is a script, which the SDK runs
  under the bridge's own runtime: under Bun that is a second Bun in the project
  cwd, so the bridge gives it the same empty env file through `executableArgs`.
  Every Claude process Omniplex starts, sign-in included, gets Omniplex's own
  environment without `CLAUDECODE` and `CLAUDE_*`: a server started from inside
  a Claude Code session would otherwise hand every thread that session's id,
  messaging socket and token, effort, and context window. That includes
  `CLAUDE_CONFIG_DIR` and credentials like `CLAUDE_CODE_OAUTH_TOKEN`: an
  inherited one is some terminal's login, not the account's. Only the account's
  overlay sets them.
- **Codex.** A native binary. `adapter.HarnessEnv` drops an inherited
  `CODEX_HOME` and the overlay sets the account's. Codex reads `.env` from that
  home and no project file.
- **Pi.** A Node script, and its auth bridge is too. Node loads no dotenv file
  unless asked. `adapter.HarnessEnv` drops an inherited `PI_CODING_AGENT_DIR`
  and the overlay sets the account's.

## Config homes

Every account, the first included, has its own home at
`~/.omniplex/instances/<id>`, and its overlay names it (`CLAUDE_CONFIG_DIR`,
`CODEX_HOME`, `PI_CODING_AGENT_DIR`). Its login lives there and nowhere else, so
signing in or out from a terminal on the box changes nothing in Omniplex.
Omniplex refuses an account whose home is the machine's own `~/.claude`,
`~/.codex` or `~/.pi/agent`.

The user's configuration is shared. Skills, `CLAUDE.md`, settings, agents and
the rest of the adapter's `SharedEntries` are symlinks from the account's home
to the same entry in the machine's home, e.g. `~/.claude/skills`. That source
is always under the user's home directory, never a config dir in the server's
environment. Omniplex checks the links on startup and before each use: it
restores a missing one, re-points one whose target isn't the shared entry, and
leaves an account's own real copy alone, flagging it in Providers settings.

An adapter that adds a runtime which loads dotenv files from its cwd needs the
same treatment; `envleak_test.go` in the Claude adapter is the shape of the test.

Threads that were already running when this shipped keep the variables they
leaked until they are restarted.
