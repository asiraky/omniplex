# Harness environment

A harness process receives Omniplex's own environment plus the instance's
overlay (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, API keys and the like) and
nothing else. It never receives the project's dotenv files. Projects load their
own, and that matters: Vite's `loadEnv`, dotenv and Node's `--env-file` all keep
a variable that is already set, so a session carrying the main checkout's
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
- **Codex.** A native binary. It reads `~/.codex/.env` and no project file.
- **Pi.** A Node script, and its auth bridge is too. Node loads no dotenv file
  unless asked.

An adapter that adds a runtime which loads dotenv files from its cwd needs the
same treatment; `envleak_test.go` in the Claude adapter is the shape of the test.

Sessions that were already running when this shipped keep the variables they
leaked until they are restarted.
