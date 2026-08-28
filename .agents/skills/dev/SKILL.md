---
name: dev
description: Start this worktree's Omniplex dev server and hand the user a working link — the tailnet URL plus a pairing link, because they are usually on a phone and 127.0.0.1 is not an address they have. Use whenever asked to run the app, "give me a link", "let me see it", pair a device, or verify a change in a real browser.
---

# Running the dev server

## Never in the main checkout

The main checkout is probably running the live server that hosts the session
you are in. Starting or restarting a server there takes the user's session down
with it.

A worktree is identified by `.omniplex/worktree.env` — its own ports and its own
database. If that file is missing, you are in the main checkout: stop and say so.
`scripts/dev-link` refuses on its own, so prefer running it over `npm run dev`.

## Start it and share it

```sh
scripts/dev-link
```

Starts the server if it is not already up (idempotent — safe to run again to
re-read the link), waits for it to answer, and prints:

```
  open    http://omni.tailb9bafe.ts.net:8800
  pair    http://omni.tailb9bafe.ts.net:8800/pair#c=NLNKWRWAJEZV43WE
  code    NLNK-WRWA-JEZV-43WE
  log     .omniplex/dev.log
```

Give the user all three lines. Reasoning:

- **open** is the tailnet address off the startup banner, which is the only one
  that works from the phone. Never hand over `127.0.0.1` or a `10.x`/`172.x`
  address: the first is not their machine, the second is unencrypted.
- **pair** carries the code in the URL fragment, so one tap gets them in.
- **code** is the fallback for typing it in by hand.

Pairing is per origin, and the port is part of the origin — so a device paired
against production, or against another worktree, is not paired against this one.
It only has to be done once per worktree; the device token survives restarts in
`.omniplex/dev.db`.

The code is single-use and expires in 10 minutes. The server only mints one at
startup, so an expired code means a restart:

```sh
scripts/dev-link --restart   # mints a new pairing code
```

## What is in the database

A worktree gets its own `.omniplex/dev.db`, seeded at provision time with the
**projects and labels** of the database the provisioning server was on — enough
to start a session and to work on the list UI. Sessions are never copied: a
copied session's harness process belongs to the other server, and resuming it
from here would put two harnesses on one transcript. So the session list starts
empty, and anything you start here is genuinely yours.

Worktrees provisioned before seeding existed start empty; re-provisioning, or a
new worktree, picks it up.

## While you work

Leave it running. `air` rebuilds and restarts the Go binary on save, Vite hot-
reloads the web app. Neither needs help from you, and a restart costs the user
their pairing code. Check `.omniplex/dev.log` when something looks wrong —
compile errors land there.

Anything touching the UI gets looked at in a mobile viewport before it is called
done (see AGENTS.md). Say which widths you checked.

## Stopping

```sh
scripts/dev-link --stop
```

Only ever signals the process group in `.omniplex/dev.pid` — the one this script
started. Never `pkill omniplex`, never kill a process you found by matching a
name or a path: the match will include the server running the user's session.

Ask before stopping if the user might still be looking at it.
