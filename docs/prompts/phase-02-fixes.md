# Phase 02 — Post-Deployment Fixes

> Three defects found during the first real run of the stack (local, podman).
> Small scope. Do all three, then stop. Do not start Phase 04.
> `CLAUDE.md` rules apply as always.

---

## Context

The stack came up for the first time on a developer machine. Postgres and api started; the collector crashed immediately, and the status endpoint returned a bare 500. Once migrations were applied by hand, the collector started and began backfilling correctly.

Each of the three problems below is small on its own. Together they mean the system cannot report its own health at the exact moment its health is worst — which is when reporting matters most.

---

## Fix 1 — Migrations must run automatically

**What happened**

```
level=ERROR msg="collector stopped with error"
error="run ingestion: register collector start: register collector start:
ERROR: relation \"collector_status\" does not exist (SQLSTATE 42P01)"
```

`make migrate-up` had not been run. Nothing in the compose stack applies migrations, so a fresh `docker compose up` produces a crashed collector every time. This will happen again on the VPS, and there it will be less obvious.

**Required change**

Add a `migrate` service to `deploy/docker-compose.yml`:

- Runs `goose up` against `DATABASE_URL`, then exits
- `depends_on: postgres: condition: service_healthy`
- Both `api` and `collector` get `depends_on: migrate: condition: service_completed_successfully`
- Build from the same Go image; do not pull a separate migration tool image
- Migration SQL files must be present in the image

goose is already idempotent, so re-running on every startup is safe and intended.

**Also note** the error message repeats `register collector start` twice. That is double-wrapping — one layer adds no information. Fix the wrapping at whichever layer is redundant. Check for the same pattern elsewhere while you are in there; it makes logs harder to read at exactly the wrong moment.

---

## Fix 2 — Status endpoint must not fail when the collector is down

**What happened**

With no rows in `collector_status`, `GET /internal/market/status` returned:

```json
{"error":"internal server error"}
```

This endpoint exists to answer "is my data healthy right now". A dead collector is the single most important thing it should report. Instead it returned nothing usable, and diagnosis required reading container logs — which is precisely the workflow the endpoint was meant to replace.

**Required change**

An absent `collector_status` row is a valid state, not an error.

- Return HTTP 200 with `collector.running: false` and a `collector.state` of `never_started`
- All other fields (`ws_connected`, `uptime_seconds`, `reconnect_count`) become `null`, not zero — zero implies a measurement was taken
- Per-timeframe data still renders from the `candles` table, which is independent of collector liveness
- Reserve 500 for genuine failures such as the database being unreachable

Add a test that asserts 200 and `state: never_started` against a database with an empty `collector_status` table.

---

## Fix 3 — Distinguish backfilling from live

**What happened**

Thirty-six seconds after startup, mid-backfill:

```json
"latest_open_time":"2023-02-11T15:59:00Z",
"latest_age_seconds":109978588,
"stale":false
```

The newest candle was three and a half years old and `stale` was `false`. The suppression is probably deliberate — warning about staleness during a backfill would be noise — but the output cannot distinguish "data is current" from "catching up on history" from "ingestion has silently stopped". Those are three different situations and only one of them is fine.

Phase 02 §7 specified a staleness check for the case where the WS reports connected but data has stopped arriving. That check is meaningless without knowing which phase the collector is in.

**Required change**

Add an explicit lifecycle state to the collector, exposed in the status payload:

| State | Meaning |
|---|---|
| `starting` | Process up, has not begun backfill |
| `backfilling` | Historical backfill in progress |
| `live` | Backfill complete, consuming the WS stream |
| `reconnecting` | WS dropped, backoff or reconnect backfill in progress |

Then:

- `stale` is computed **only** in `live` state. In every other state it is `null`, not `false` — the check did not run, so it has no result
- While `backfilling`, expose progress: oldest and newest stored `open_time` per timeframe, and the target range. A user watching a three-year backfill needs to know it is advancing
- Log every state transition at info level with the previous state and the duration spent in it

**Also:** the timeframes array currently omits `latest_open_time` entirely for `5m`, `15m`, and `1h` because no rows exist yet. Emit the field as `null` rather than dropping it, so consumers can rely on a stable shape.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `podman compose down -v && podman compose up` on a completely empty volume produces a running collector with **no manual migration step**
- [ ] `/internal/market/status` returns 200 with `state: never_started` when `collector_status` is empty
- [ ] Status payload reports `starting` → `backfilling` → `live` across a real run, and `stale` is `null` outside `live`
- [ ] `latest_open_time` is present as `null` for timeframes with no data
- [ ] No duplicated wrapping text in error strings
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Anything in Phase 04
- Indicator or strategy changes
- Restructuring the status endpoint beyond the fields named above
- Authentication on the internal endpoint
- Metrics, Prometheus, dashboards

---

## How to start

These are small and independent. Summarise the plan briefly, then commit each fix separately. No approval gate needed unless you find that one of them requires a change larger than described — in that case, stop and explain before writing code.
