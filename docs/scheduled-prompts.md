# Scheduled prompts

Use the clock beside Send in an existing thread. Choose a duration or an
absolute date/time in the next 24 hours. The sheet previews the resolved date,
time and UTC offset; its IANA timezone defaults to the browser's timezone and
can be changed. Daylight-saving gaps are refused and repeated times require
choosing an occurrence.

The server saves the prompt, attachment references and current model, permission
mode and effort. Scheduled cards support editing, sending immediately and
cancelling. Editing preserves the original settings. When sent, those settings
become the thread's active settings. Normal Stop does not cancel schedules.
Deleting the thread removes them.

No browser needs to remain open. The host must be awake. A schedule picked up
within one hour of its due time waits for a busy thread to become idle; a host
that returns more than one hour late marks it missed. Restarting also rechecks
the grace window for messages previously waiting on a busy thread. Failed and
missed messages stay available for explicit retry or rescheduling. Provider
limits and requests for human input can still prevent completion.

## Usage-limit continuations

When a turn stops at a usage limit with a known reset, the server arms a
schedule of kind `limit` for one minute after the reset. It asks the agent to
check the real state of the work, then continue. It is not listed with the
other schedules; the limit card shows it as a chip with Cancel. Stop, sending
by hand and switching the thread's account also cancel it. It is sent with the
thread's settings as they are then, not the ones saved when it was armed. With
no 24-hour cap, a weekly reset can be days out; a host that returns more than a
day after the reset marks it missed.

While the latest turn stands at a limit, the server holds queued prompts,
since each would only hit the limit again. The next turn to start releases
them. That is the continuation, a manual send, or a human's schedule. A human's
schedule goes past the held queue only while no continuation is armed, so it
stays a way round an unknown reset without calling an armed continuation off.
The hold covers only prompts queued before the limit: one sent by hand while a
settings change is still on its way waits for that change, then goes. Moving
the thread to another account lifts the hold, since the limit was the old
account's.

Codex can fail a turn before it knows the reset. The adapter then reads the
account's limits for up to 10 seconds. If that still says nothing, the first
later rate-limit update that names the reset arms the continuation.

A restart continuation that hits a limit is handled the same way: the queue is
held and the limit's continuation is armed. A prompt the limit refused never
reached the agent, so its note of background work the restart killed is still
owed. The next turn past the held queue carries it, whether that is the
continuation or a manual send. The queued prompts get none of it.

## Persistence

Startup creates the SQLite `scheduled_prompts` table and `scheduled_due` index.
This is an additive database migration. Scheduling events in the existing event
log remain authoritative; the due-time lookup is updated in the same SQLite
transaction. Dispatch records the turn and marks the schedule sent atomically,
before contacting the provider. A crash during handoff cannot automatically
send the original scheduled prompt twice; existing interrupted-turn recovery
asks the harness to inspect actual work before continuing.

## Verification

`npm test` runs the Go race tests and web tests, including scheduling branches,
restart restoration, the one-hour boundary and timezone/DST conversion.

For the browser end-to-end test:

```sh
npx playwright install chromium
npm run test:e2e:schedules
```

To use an installed Chrome instead:

```sh
CHROME_BIN=/usr/bin/google-chrome npm run test:e2e:schedules
```

The test runs the built UI and real server against a temporary database. Only
the harness is a deterministic test adapter. It exercises desktop and 390px
mobile controls, edit/cancel/send-now, provider failure, timezone conversion,
page reload, then a real one-minute timer with every browser closed and the
server restarted. Screenshots go to `.omniplex/schedule-screenshots/` (override
with `OMNIPLEX_SCREENSHOTS`). No production threads or provider tokens are used.
