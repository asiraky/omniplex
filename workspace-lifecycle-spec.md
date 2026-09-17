# Workspace lifecycle: provision before the agent, clean up after the session

## The solution

Each project has a `.omniplex/project.json` file and can define two executable hooks:

```json
{
  "version": 1,
  "name": "Zero8",
  "defaults": {
    "harness": "codex",
    "model": "gpt-5.6-sol",
    "effort": "high",
    "workspace": "managed",
    "baseBranch": "staging"
  },
  "workspace": {
    "provision": "./scripts/omniplex-provision",
    "deprovision": "./scripts/omniplex-deprovision"
  }
}
```

Omniplex runs `provision` when the session is created and **waits for it to exit**.
The script can create a worktree wherever it wants, clone databases, allocate
Redis, copy env files, install dependencies, and rewrite `AGENTS.md`. It returns
the directory in which the agent should run. Only after that does Omniplex start
Claude or Codex.

When the session is closed, Omniplex runs `deprovision` and waits for it to exit. That
script receives the same session context plus the saved result of `provision`,
so it can drop databases, release Redis/ports, and remove the exact worktree it
created.

The hooks own project-specific behavior. Omniplex only owns when to call them,
waiting for completion, streaming their output, persisting their result, and
retrying/reporting failure.

## The project artifact

The current application treats `cwd` as a value typed into each new session.
That should be replaced by a first-class project:

```text
Project
  id                  stable ID in Omniplex's database
  root                main checkout / working-directory root
  name                display name
  config              parsed .omniplex/project.json
  defaults            harness, model, effort, mode, base branch
  lifecycle hooks     provision and deprovision paths

Session
  projectId           owning project
  cwd                 final cwd returned by provision
  branch              this session's branch
  provisionResult     saved output needed by deprovision
```

Omniplex keeps a local project registry in its existing database so the user adds a
directory only once. `.omniplex/project.json` is the portable, project-owned
configuration. The UI edits that file; the database indexes it and holds
machine-local state such as last-used time. Sessions reference `projectId`
instead of treating an arbitrary cwd as the whole identity.

Script paths stored in the JSON file are relative to the project root. The
picker may browse only that project by default and writes the selected relative
path, so moving or cloning the project does not break its configuration.

## User experience

### Add a project once

The first time, the user chooses **Add project** and picks a directory. Omniplex finds
the main Git checkout, registers it, and reads `.omniplex/project.json` if present.
Thereafter the project appears in the new-session picker; the user never browses
to that directory again.

For migration, the server's current default cwd can be offered as the first
project automatically.

### Configure it in Project Settings

Every project has a settings action beside its name and in the session header.
The settings screen contains:

```text
General
  Project name                 Zero8
  Project directory            /Users/aaron/code/zero8

Agent defaults
  Harness                      Codex
  Model                        gpt-5.6-sol
  Effort                       High
  Permission/runtime mode      Full access

Workspace
  New sessions use             Managed worktree
  Base branch                  staging
  Suggested worktree folder    .worktrees
  Provision script             scripts/omniplex-provision       [Choose...]
  Deprovision script           scripts/omniplex-deprovision     [Choose...]
```

**Choose...** opens a server-side file picker rooted at the project. Selecting a
file fills the field. **Save** writes `.omniplex/project.json` immediately and updates
the in-memory project. There is no import step, no first dummy chat, and no
separate command to teach Omniplex that the hook should run next time.

The app validates that each configured script exists and is executable. It does
not run provision as a “test,” because testing it would create real resources.

### Create a session

**New session** shows projects rather than a cwd browser:

```text
Project      Zero8
Harness      Codex             (from project default)
Model        gpt-5.6-sol       (from project default)
Effort       High              (from project default)
Workspace    New worktree      (from project default)
Branch       feature/...
Base         staging           (from project default, overridable per session)
```

The workspace row is a list rather than a text field that means different
things depending on what is typed into it. Its options are: the main checkout,
a new worktree from an issue or a branch name, a new scratch worktree Omniplex names
itself, and attaching to a worktree that already exists. **Base** applies to
the two that create a branch, which is what makes a session stackable on
another worktree's unmerged work.

The defaults are already selected but can be overridden for this session. The
advanced fields can stay collapsed in the common case, making creation roughly
“pick project, name the work, start.”

When the user starts:

1. Omniplex navigates straight into the normal session/chat screen. The header,
   transcript, and composer are all in their usual places.
2. The transcript contains a system-style **Preparing workspace** message. It
   is clearly from Omniplex's workspace provisioner, not fabricated model output.
3. The composer remains visible but disabled, with `Preparing workspace...` in
   place of its normal placeholder. No prompt can be submitted or queued yet.
4. Omniplex automatically runs the project's provision hook. Its stdout and stderr
   stream live into an expandable console area inside the system message.
5. When the hook exits successfully, Omniplex starts the selected harness in the cwd
   returned by the hook, enables and focuses the composer, and collapses the
   message to a compact **Workspace ready** row.
6. If provision fails, the message remains expanded with the command, exit
   code, output, **Retry**, and **Clean up** actions. The session becomes
   **Setup needs attention** in the sidebar.

The user can leave the session while it prepares. Completion or failure is
visible from the sidebar/inbox.

The successful row can be expanded again to inspect the full output or
dismissed with an X. Dismissal is presentation state only: the provisioning
events and output remain durable and available from session activity/details.
This preserves auditability without leaving setup noise in every completed
conversation.

### Close a session

**Close and clean up** stops the harness and automatically runs the project's
deprovision hook. The session shows **Cleaning up** and can be left in the
background. Success closes it; failure keeps it in the inbox with **Retry
cleanup**. The user never needs to remember which script to run.

## The problem

A session is not always ready merely because `git worktree add` returned. In
the projects this application is intended to drive, creating an isolated
workspace can also mean:

- copying and rewriting env files;
- allocating ports and a Redis database;
- cloning a Postgres database;
- installing dependencies;
- writing worktree-specific instructions into `AGENTS.md` or `CLAUDE.md`; and
- recording enough metadata to undo all of that later.

Those operations are part of the session's correctness boundary. The harness
must not be created until provisioning has exited successfully: both Claude Code and
Codex load project instructions when their process/session starts, so merely
disabling the composer while an already-running harness waits is too late.

Cleanup is equally important. Removing only the Git worktree leaks databases,
Redis allocations, ports, tunnels, and registry entries. Cleanup must be a
durable, retryable lifecycle operation, not a best-effort UI callback.

## Decision: a session owns a workspace lease

Keep **conversation** and **workspace** as separate concepts, joined by an
explicit lease:

```text
project (main checkout)
  -> workspace lease (local checkout, borrowed worktree, or owned worktree)
      -> session (durable event log)
          -> harness process (Claude, Codex, ...)
```

A session can use one of three workspace modes:

| Mode | Created by Omniplex | Setup hook | Teardown hook | Git removal |
|---|---:|---:|---:|---:|
| `local` | no | never | never | never |
| `borrowed` | no | never | never | never |
| `managed` | provision hook | yes | yes | deprovision hook |

Ownership is recorded when the lease is created and never inferred later from
the path, and `local` never reaches teardown however it is asked to: the
project directory is not Omniplex's to remove. For the other two, ownership decides
the *default* answer and not the answer — removing a worktree is the user's
explicit choice in the delete dialog, ticked by default for `managed` and
unticked for `borrowed`. Both non-managed modes still have their hook paths
cleared at creation rather than being skipped at teardown, so a later change to
the teardown branch cannot resurrect them.

Hooks are a property of provisioning, not of running. A `local` session is the
project's own working directory and a `borrowed` one is a checkout somebody
else made; in both cases there is nothing to prepare, and a provision hook
written to populate a fresh worktree — installing dependencies, seeding
configuration, copying secrets — would be at best redundant and at worst
destructive when run against a checkout that is already in use. Neither mode
runs them.

Sharing a checkout is allowed. Two `local` sessions may run in the project
directory, and any number of sessions may attach to one worktree: nothing in
Git prevents it, and the operator is often the best judge of when it is safe.
A checkout a live session already holds is reported busy and the new-session
dialog says so inline before the second session starts — advice at the moment
of the decision rather than a lock. The one thing sharing does forbid is
removal: a worktree that another session still references cannot be deleted
with the session that is leaving it.

The branch is preserved when a managed worktree is released. Discarding a
branch is a separate, explicit Git action.

## Lifecycle state machine

The sidebar's current `idle | turn | closed` phase is not expressive enough.
The durable session phase should be:

```text
creating
  -> provisioning
      -> ready <-> running
      -> provision_failed

ready | provision_failed
  -> cleaning
      -> closed
      -> cleanup_failed -> cleaning (retry)
```

`waiting_for_user` can later refine `running` for permissions and elicitation,
but it is orthogonal to workspace readiness.

Important rules:

1. `create_session` persists the session and returns its ID immediately. The
   UI can attach and render provision progress while it continues.
2. No harness subprocess or harness conversation is created in `creating` or
   `provisioning`.
3. The composer is absent/disabled until `workspace.ready` is durable.
4. Setup exit code zero is the readiness barrier. Starting a terminal or
   successfully writing a command to it is not readiness.
5. Provision failure leaves an inspectable session with its output and a
   **Retry provision** / **Clean up** action. It does not silently delete the evidence.
6. Closing a managed session closes the harness first, then runs deprovision.
   A failed deprovision retains the session so cleanup can be retried.
7. Server shutdown only disposes harness process trees. It does not release
   workspace leases or run deprovision; sessions are meant to survive a restart.
8. Deleting a transcript is two-phase: release the workspace successfully,
   then purge the event log. The log must remain if cleanup fails.

## Durable events

Lifecycle activity belongs in the same per-session log as tool calls. Add:

```text
workspace.requested
workspace.worktree_created
workspace.hook_started
workspace.hook_output
workspace.hook_finished
workspace.ready
workspace.failed
workspace.cleanup_started
workspace.cleanup_finished
workspace.cleanup_failed
workspace.released
```

Representative payloads:

```json
{
  "type": "workspace.requested",
  "payload": {
    "mode": "managed",
    "projectRoot": "/code/zero8",
    "path": "/code/zero8/.worktrees/feature-session-inbox",
    "branch": "feature/session-inbox",
    "baseRef": "staging",
    "owned": true
  }
}
```

```json
{
  "type": "workspace.hook_output",
  "payload": {
    "runId": "...",
    "hook": "provision",
    "stream": "stdout",
    "chunk": "worktree-setup: cloning zero8 -> zero8_feature_session_inbox ...\n"
  }
}
```

```json
{
  "type": "workspace.ready",
  "payload": {
    "resources": {
      "appUrl": "http://app--feature-session-inbox.zero8.test:8204",
      "apiUrl": "http://api--feature-session-inbox.zero8.test:8104",
      "database": "zero8_feature_session_inbox",
      "redisDb": 4
    }
  }
}
```

Hook output should be streamed in bounded chunks, treated as potentially
sensitive, passed through a shared redaction policy before persistence, and
capped per run. Store a truncation marker rather than allowing an install log
to grow the event database without limit.

The `sessions` table remains a query projection for the sidebar. Add indexed
metadata such as `project_root`, `workspace_path`, `branch`, `base_ref`,
`workspace_mode`, and the lifecycle phase, but keep the event log authoritative.

## Project configuration and hook contract

Use one checked-in file at the main checkout, `.omniplex/project.json`:

```json
{
  "version": 1,
  "name": "Zero8",
  "defaults": {
    "harness": "codex",
    "model": "gpt-5.6-sol",
    "effort": "high",
    "workspace": "managed",
    "baseBranch": "staging"
  },
  "workspace": {
    "suggestedRoot": ".worktrees",
    "provision": "./scripts/omniplex-provision",
    "deprovision": "./scripts/omniplex-deprovision",
    "provisionTimeoutSeconds": 1800,
    "deprovisionTimeoutSeconds": 600
  }
}
```

Hooks can be executable files using any language via their shebang. Omniplex also
recognises `.ts`/`.mts`, `.js`/`.mjs`/`.cjs`, and `.sh` files and launches them
with Bun, Node, or `sh` respectively, so an existing project script does not
have to be made executable. A shell script can simply delegate to an existing
TypeScript implementation:

```sh
#!/bin/sh
exec bun run scripts/worktree-setup.ts --Omniplex-context "$OMNIPLEX_CONTEXT_FILE"
```

Hooks run with the main checkout as their cwd. Omniplex writes an input JSON file and
provides its location as `OMNIPLEX_CONTEXT_FILE`. The input contains:

```json
{
  "version": 1,
  "sessionId": "...",
  "projectRoot": "/code/zero8",
  "requestedBranch": "feature/session-inbox",
  "baseRef": "staging",
  "suggestedWorktreePath": "/code/zero8/.worktrees/feature-session-inbox"
}
```

The suggested path is only a default. The provision hook may create the
worktree somewhere else. It writes its result atomically to `OMNIPLEX_RESULT_FILE`:

```json
{
  "cwd": "/custom/worktrees/zero8/session-inbox",
  "branch": "feature/session-inbox",
  "resources": {
    "appUrl": "http://app--session-inbox.zero8.test:8204",
    "apiUrl": "http://api--session-inbox.zero8.test:8104",
    "database": "zero8_feature_session_inbox",
    "redisDb": 4
  }
}
```

`cwd` is the only required result. It is the directory Omniplex gives to the harness.
Omniplex verifies that it exists, saves the complete result, and starts the agent
there only after the provision process exits zero.

Deprovision receives the original context and provision result in its context
file. It owns the complete inverse operation, including Git worktree removal if
the provision hook created one. A successful exit means the workspace has been
released; a non-zero exit leaves the session in `cleanup_failed` with a retry
button.

The process environment is deliberately small:

```text
OMNIPLEX_LIFECYCLE_VERSION=2
OMNIPLEX_HOOK=provision | deprovision
OMNIPLEX_SESSION_ID=<stable UUID>
OMNIPLEX_PROJECT_ROOT=<absolute main checkout>
OMNIPLEX_CONTEXT_FILE=<absolute JSON input path>
OMNIPLEX_STATE_DIR=<durable per-session directory outside the worktree>
OMNIPLEX_RESULT_FILE=<path for an atomic JSON result>
```

The durable `OMNIPLEX_STATE_DIR` is available to both hooks and survives removal of
the worktree.

Version 2 is the clean-break Omniplex contract. Every lifecycle variable now
uses the `OMNIPLEX_` prefix; hooks written for version 1 must be updated rather
than expecting compatibility variables.

Hooks must be idempotent:

- provision rerun for the same session must converge on the same resource
  allocation;
- deprovision must succeed when some or all resources are already absent; and
- deprovision must use the saved provision result and durable state to clean up
  even after a partially completed provision.

Omniplex should also provide a zero-configuration compatibility resolver for the
existing `clawd` conventions:

```text
.claude/worktree/setup and teardown
scripts/worktree-setup.ts and worktree-teardown.ts
scripts/worktree-setup.mjs and worktree-teardown.mjs
scripts/worktree-setup.sh and worktree-teardown.sh
```

The compatibility adapter supplies the arguments those scripts already expect.
Their teardown scripts already own full removal, which matches the hook contract.
New projects should use the context/result files above.

### Trust boundary

Lifecycle hooks are trusted host code. They can intentionally reach Docker,
databases, env files, and other resources that the harness itself may not be
allowed to manage. Omniplex should show the resolved hook commands and require trust
once per project/config hash before executing them.

Resolve configuration and hook paths from the persisted main checkout, never
from the agent-editable managed worktree. Teardown must not execute a script
that the agent changed during its turn. Persist the resolved configuration and
its hash with `workspace.requested` so reconciliation can explain configuration
drift and require renewed trust when appropriate.

## Process and crash semantics

The session actor remains the only writer to a session log. Provisioning runs
in a child goroutine/process, but progress and completion come back through the
actor inbox before being appended. This preserves the existing gapless event
ordering invariant.

Session creation becomes:

```text
persist session in `creating`
start actor without a harness
return session ID
actor runs and awaits the provision hook
actor validates the returned cwd
actor creates the harness in that cwd
actor appends workspace.ready
actor changes phase to ready
```

On provision failure, Omniplex keeps the output and invokes deprovision only when the
user chooses **Clean up**. The user can instead inspect the failure and retry
the idempotent provision hook.

On restart, reconcile by durable phase:

| Stored phase | Reconciliation |
|---|---|
| `provisioning` | mark interrupted; offer/perform idempotent provision retry |
| `ready` / `running` | verify the recorded worktree; resume harness lazily |
| `cleaning` / `cleanup_failed` | retry deprovision with the same context |
| `closed` | no process or workspace action |

Hook children must belong to a process group owned by Omniplex. Normal shutdown
terminates and reaps them, then leaves the durable phase interrupted for the
next reconciliation pass. A startup reaper also checks stale managed leases so
a hard kill cannot leak resources forever.

A harness process, and a terminal shell, is the root of a tree: the agent forks
shells, the shells start dev servers and headless browsers, and those detach
until nothing but the kernel links them to the session. Each such root runs in
a cgroup of its own beneath the server's (`internal/procgroup`), placed there
at clone time so nothing escapes, and closing the session kills the cgroup —
one write that ends every process in it however it detached. Memory stays
charged to the server's cgroup, so the service's limits cover the whole tree.
The startup sweep kills trees a previous server left behind. Without a
writable cgroup v2 tree the root gets a process group instead, which catches
ordinary children but not a `setsid`.

## Safe default when a project has no hooks

Hooks are optional. Without them, Omniplex can provide a basic built-in worktree
implementation using the suggested `.worktrees/<branch>` location. That default
only creates and removes Git worktrees; it does not guess how to clone env
files, databases, Redis state, or other project resources.

Before the built-in implementation removes a path, Omniplex must verify:

1. the lease says `owned: true`;
2. the resolved target equals the persisted path (no fresh template expansion);
3. `git worktree list --porcelain` lists that exact path;
4. its Git common directory matches the persisted project root; and
5. the target is neither the project root nor an ancestor of it.

Then it runs `git worktree remove --force <exact-path>` without a shell and
`git worktree prune`. Omniplex does not perform Git removal after a custom
deprovision hook—the hook owns the complete operation.

## User-facing behaviour

Creation should navigate to the normal chat screen immediately and show a
system-style lifecycle message in the transcript:

```text
Workspace provisioner
Preparing workspace...

  Creating worktree                     done
  Running scripts/omniplex-provision
    Allocated API 8104 / app 8204
    Installed dependencies
    Cloning zero8 -> zero8_feature_...   running
  Starting Codex                        waiting
```

The output area follows the script as it runs and can distinguish stdout from
stderr without making ordinary output look like an error. The composer is
present but disabled. On success the provisioner message automatically becomes
a compact row such as `Workspace ready in 1m 42s`; the composer enables and
receives focus. The row may be expanded or dismissed. On failure it stays open
with **Retry provision** and **Clean up** actions.

Closing a managed session shows deprovision in the same feed. A cleanup failure
keeps the session near the top of the sidebar because it needs attention.
Expose three distinct actions so lifecycle intent is never hidden behind an X:

- **Stop for now**: dispose the harness; retain conversation and workspace.
- **Close and clean up**: close the conversation, run deprovision, remove the
  managed worktree, preserve the branch and transcript.
- **Delete**: close and clean up first; purge the transcript only after success.
  Whether the checkout on disk goes too is a checkbox in the confirmation, not
  an inference from the workspace mode.

## Compatibility with the existing projects

The current `zero8`, `worksauce`, and `eden` setup scripts already do the hard
parts correctly: they accept an externally-created worktree path, allocate
stable resources, clone env/DB state, write agent instructions, and are mostly
idempotent. The immediate migration is:

1. add tiny `omniplex-provision` and `omniplex-deprovision` wrappers around the existing
   setup/teardown scripts;
2. make provision write its selected cwd and optional display metadata to
   `OMNIPLEX_RESULT_FILE`; and
3. add `.omniplex/project.json`, or rely initially on the compatibility resolver.

This removes the current T3-specific dispatcher from the correctness path.
The same repository hooks then work from `clawd`, Omniplex, CI, or another launcher
because the project-specific logic remains in the project and the lifecycle
contract belongs to the orchestrator.

## Implementation slices

1. **Projects:** add the project registry/table, `.omniplex/project.json` loading and
   saving, Project Settings, and a project picker in place of the cwd field.
2. **Provision barrier:** add lifecycle events/phases, create an actor without a
   harness, stream the project hook, persist its result, and start the harness
   in its returned cwd only after success.
3. **Cleanup:** deprovision hook execution, retryable
   `cleanup_failed`, and two-phase transcript deletion.
4. **Recovery:** restart reconciliation, process-group cleanup, and a stale
   lease reaper.
5. **Resources UI:** parse hook result metadata and surface URLs/services in
   session details.

The first two slices produce the seamless creation flow and fix the
instruction-file race. The first three reproduce the essential `clawd`
lifecycle without tying the core to any one project's Bun, Docker, Postgres,
Redis, or env-file conventions.
