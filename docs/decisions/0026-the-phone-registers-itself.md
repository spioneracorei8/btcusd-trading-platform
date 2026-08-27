# ADR 0026 — The phone registers itself; the device token is not configuration

Status: accepted
Date: 2026-08-27
Phase: 09 (part C1)

## Context

Phase 07 read the delivery destination from `FCM_DEVICE_TOKEN`, and refused to
start in notify mode without it — the same rule applied to `FCM_PROJECT_ID` and
`FCM_CREDENTIALS_FILE`, on the stated principle that a mode which claims to
deliver and cannot is worse than one that says it will not.

That principle is right. Applying it to the device token was not, for two
reasons that only became visible once there was an app.

**A token is not a value anyone can set.** FCM issues it to the app on the
phone. Nobody knows it before the app is installed and opened, and there is no
way to obtain it other than from the device. A configuration file is the wrong
place for a value that is produced by the thing being configured.

**It rotates.** Firebase replaces a registration token on its own schedule, on
reinstall, on restore to a new device, and sometimes for no reason the app is
told. A `.env` holding last month's token is not stale in a way anything
notices: the process starts, the credentials validate, signals are recorded and
queued, and every send is rejected as `UNREGISTERED`. The delivery worker
correctly treats that as permanent and gives up. The symptom is alerts stopping,
and the file everyone looks at still contains a token that looks fine.

There is also a chicken-and-egg. Requiring the token at start-up means the
process cannot run until a phone has registered, and a phone cannot register
until the process is running to receive the registration.

## Decision

The token lives in a `devices` row, written by the app through
`POST /api/v1/device`. `FCM_DEVICE_TOKEN` is **rejected** at start-up rather
than ignored — the treatment `NOTIFY_ENABLED` already gets, for the same
reason: the variable that would mislead somebody is the one that has to fail.

**Notify mode starts without a registration.** What is required at start-up is
the ability to look a device up, not the presence of one. Every deployment
passes through "notify mode, nothing registered" between switching the mode on
and opening the app; that is a normal state, not a misconfiguration.

**The state is reported instead of enforced.** `GET /api/v1/status` carries
`delivery.devices_registered`, and when it is zero in notify mode the concerns
list says so in words:

> no device is registered, so signals will be recorded and queued but not
> delivered; open the app to register this phone

A bare `0` beside `mode: notify` would leave the reader to join those two facts
themselves, and the person reading that page is usually reading it because
something is already confusing.

**A queued alert with nowhere to go waits; it does not fail.** The retry budget
is five attempts over about eight minutes. If a missing registration counted as
a failed attempt, every alert produced before the app was first installed would
be marked failed long before anyone could install it — and nothing retries a
failed row, so they would be gone, under a recorded reason that reads like a
network problem. Such rows come out of a pass untouched: same attempt count,
still pending, still due. `DeliveryReport.Waiting` counts them and `Quiet()`
accounts for them, so a pass that delivered nothing because there is nowhere to
deliver still says so.

**One device, enforced by the table.** `devices` holds a single row —
`id integer PRIMARY KEY DEFAULT 1 CHECK (id = 1)` — because the delivery queue
already says the same thing in its own shape: `notifications` is unique on
`(signal_id, channel)`, so it can record that a signal was delivered over FCM
and not which of several devices received it. A second device would mean either
that it silently receives nothing, or that one signal needs two queue rows —
a design change, not a configuration. Making it a constraint means that change
fails loudly at the table, next to the comment explaining why, rather than
half-working in the queue.

**Registering the same token again is a success.** The app calls this on every
launch and every refresh; that call is the mechanism that keeps a rotated token
from silently ending delivery. `registered_at` survives a refresh of the same
token and resets when the token changes, so the row can say both "this phone
has been the registered one since March" and "the app checked in an hour ago".

## Consequences

- One `.env` variable fewer, and one that could be silently wrong.
- A deployment can be fully configured and still deliver nothing. That is why
  the status endpoint states it rather than implying it.
- The token is never returned by the API, only a six-character prefix. There is
  no authentication in front of these endpoints (ADR 0024), and the
  registration token is the one credential here that lets anything push to the
  owner's phone. It is masked in logs and errors for the same reason.
- The api process gained a write. Every other `/api/v1` endpoint is read-only,
  and this one is not — it is the single exception, it writes one row in one
  table, and it cannot touch signals, outcomes or candles. Worth stating
  because "the API is read-only" was true until now and is the kind of
  assumption that gets built on.
- A token that has genuinely died — the app uninstalled and never reopened —
  stays in the table. `delivery.failed` is what surfaces that. Deleting the row
  on a permanent rejection was considered and left out: a transient failure
  misclassified as permanent would unregister the phone, and the recovery from
  that is worse than the tidiness is worth.

## Alternatives considered

**Keep the env var as a fallback when no device is registered.** Two sources
for one value, which is exactly the `NOTIFY_ENABLED` mistake: somebody sets the
variable, the app registers something else, and the alerts go to whichever the
code happened to prefer.

**Keep the env var authoritative and have the endpoint only record for
observability.** Smallest change, and it leaves the actual problem in place —
the token in the file is still the one being used, and it is still the one that
goes stale.

**Allow several devices and deliver to each.** The delivery queue cannot
express it without a schema change, and this is a single-owner system with one
phone. If it is ever wanted, the constraint is where the work starts.

## References

- ADR 0024 — why there is no authentication in front of this endpoint
- `server/migrations/00014_create_devices.sql` — the table and the constraint
- `docs/api.md` — the endpoint
- `docs/prompts/phase-07.md` — the Definition of Done item this unblocks
