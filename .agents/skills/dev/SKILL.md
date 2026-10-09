---
name: dev
description: Starts this worktree's Omniplex dev server and hands the user a link that opens on their phone. Use when asked to run the app, share a link, pair a device, or check a change in a real browser.
---

# Dev server

The user is usually on their phone, somewhere else on the tailnet. The job is
done when they have a link that opens there with one tap.

## 1. Start it

```sh
scripts/dev-link
```

Safe to repeat: it starts the server if it is down, waits for it to answer,
and prints `open`, `pair` and `code` from the startup banner. Run it again to
re-read them. It refuses in the main checkout (no `.omniplex/worktree.env`);
when it does, stop and tell the user.

## 2. Hand over the link

Reply with all three as plain text, each URL bare on its own line so the chat
autolinks it:

open: http://omni.tailb9bafe.ts.net:8800
pair: http://omni.tailb9bafe.ts.net:8800/pair#c=NLNKWRWAJEZV43WE
code: NLNK-WRWA-JEZV-43WE

A URL inside a code fence, backticks or `[label](url)` reaches the user as dead
text. Hand over the tailnet address the script prints: `127.0.0.1` is not
their machine, and `10.x`/`172.x` is unencrypted.

- **pair** carries the code in the URL fragment, so one tap pairs the device.
  Pairing is per origin, port included: a device paired with production or
  another worktree still pairs here, once. The token survives restarts in
  `.omniplex/dev.db`.
- **code** is for typing by hand. Single-use, expires in 10 minutes, and the
  server mints one only at startup: `scripts/dev-link --restart` gets a fresh
  one.

## While it runs

Leave it running. `air` rebuilds the Go binary on save and Vite hot-reloads
the web app; a restart costs the user their pairing code. Compile errors land
in `.omniplex/dev.log`.

Check UI changes at mobile width, and say which widths you checked.

## The database

`.omniplex/dev.db` belongs to this worktree, seeded at provision time with the
provisioning server's projects and labels: enough to start a thread and work
on the list UI. Threads are not copied, because a copied thread's harness
belongs to the other server and resuming it here would put two harnesses on
one transcript. The thread list starts empty.

## Stopping

Ask first if the user might still be looking at it, then:

```sh
scripts/dev-link --stop
```

It signals only the process group it started, recorded in `.omniplex/dev.pid`.
