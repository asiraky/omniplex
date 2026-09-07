# Scheduled prompts

Use the clock beside Send in an existing session. Choose a duration or an
absolute date/time in the next 24 hours. The sheet previews the resolved date,
time and UTC offset; its IANA timezone defaults to the browser's timezone and
can be changed. Daylight-saving gaps are refused and repeated times require
choosing an occurrence.

The server saves the prompt, attachment references and current model, permission
mode and effort. Scheduled cards support editing, sending immediately and
cancelling. Editing preserves the original settings. When sent, those settings
become the session's active settings. Normal Stop does not cancel schedules.
Deleting the session removes them.

No browser needs to remain open. The host must be awake. A schedule picked up
within one hour of its due time waits for a busy session to become idle; a host
that returns more than one hour late marks it missed. Restarting also rechecks
the grace window for messages previously waiting on a busy session. Failed and
missed messages stay available for explicit retry or rescheduling. Provider
limits and requests for human input can still prevent completion.

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
with `OMNIPLEX_SCREENSHOTS`). No production sessions or provider tokens are used.
