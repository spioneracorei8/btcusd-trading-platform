# Phase 08 — API, and an audit of Phase 07

> Read `CLAUDE.md` fully before starting.
> Phases 01–07 are merged. Signals are evaluated live, queued, delivered, resolved, and reconciled.
> **This phase still places no orders.** `CLAUDE.md` §1 stands.

Two parts, in this order. Part A is a review of what was just built; Part B is the API the mobile app will consume. Part A comes first because building on top of a defect is more expensive than finding it.

---

# Part A — Audit Phase 07

Phase 07 was six commits touching live evaluation, delivery, outcome resolution, and reconciliation. Mutation testing caught a great deal during construction. This part looks for what it could not.

**Ground rule.** Report what you find, including "found nothing" for any section. A clean audit is a result. An audit that manufactures findings to look thorough is worse than none, because it spends attention that the real defects need. Do not fix anything you find without describing it first — some findings will be design decisions rather than bugs, and that call is mine.

## A1. Concurrency and ordering

The collector now runs ingestion, signal evaluation, delivery, and outcome resolution together. Phase 02's writer defect — confirmed candles silently dropped when a shared context was cancelled — is the shape to look for again.

- What happens if the process is killed between writing a signal and queueing its notification? Between resolving an outcome and marking it resolved?
- Can the delivery worker and resolution worker contend on the same rows?
- Is `entry_price` backfill safe if two candles arrive close together, or if a restart lands mid-backfill?
- Does anything hold a database connection across a network call to Firebase?
- On shutdown, does in-flight work finish or get dropped? Phase 02 established that dropping confirmed work silently is the worst available failure.

## A2. The comparison's integrity

The entire justification for Phase 07 is that live outcomes can be compared to backtest predictions. Anything that quietly breaks that comparison is the most serious class of defect available here, because it produces numbers that look valid.

- Can live and backtest diverge for any reason other than the market? Fill conventions, rounding, ATR source, timestamp handling, timezone.
- ADR 0022 shared `Levels` and `FillPrice`. Is anything else duplicated between engine and follower that could drift?
- If a parameter changes mid-flight, can signals from before and after end up in one group?
- Can a signal be resolved against candles that were not available at the time — the look-ahead rule from Phase 04 §1, in a new place?
- Does `signal_price` versus `entry_price` hold up when the next bar is missing, or lands inside a gap?

## A3. Silent failure

The recurring failure mode in this project is not a crash — it is a system that appears to work while doing nothing. `Ready()` latching in commit 1 was one; `EnsureOutcomes` reopening rows in commit 4 was another. Both would have looked healthy from outside.

Ask specifically: **if this component stopped working entirely, how long before anyone noticed?**

- Signals stop being generated — strategy not warm, filter refusing, timeframe mismatch. Is silence distinguishable from "no setup found"?
- Outcomes stop resolving. Does anything report the age of the oldest unresolved signal?
- Delivery fails permanently for every signal. Is that visible without reading the table?
- A migration half-applies, or `signal_outcomes` falls behind by more than its batch size.

`/internal/market/status` answers this for ingestion. Nothing answers it for the signal pipeline. If that gap is real, say so — Part B has an endpoint for it.

## A4. Data integrity

- Can a signal exist without an outcome row indefinitely? What reports that?
- Can an outcome be resolved twice, or resolved after being invalidated?
- Do MAE and MFE hold when the position gaps, or when the resolving bar is the entry bar?
- Are all timestamps UTC end to end (`CLAUDE.md` §4)?
- Does the unique constraint hold under two collectors running at once — not because that is supported, but because it will eventually happen by accident during a deploy.

## A5. What a test cannot see

Some of the strongest findings so far came from running against real candles rather than fixtures: the gap-past-stop entry, the `Ready()` latch, the fixture whose rounded price equalled its exact price.

Run the pipeline against the real database. Not to check it passes — to look at what it actually produced and ask whether each row makes sense.

## A6. Deliverable

`docs/audit-phase-07.md`:

- Every area examined, with the finding, including "no issues found"
- For each finding: what breaks, how it would be noticed, severity, and whether it is a bug or a design decision
- Anything you would want to know before building an API on top of this

Then stop and wait. I will decide what gets fixed before Part B starts.

---

# Part B — REST and WebSocket API

Only after Part A is reported and its findings triaged.

## B1. Scope and access

The API serves one person, over Tailscale, on a VPS with no public ports. That shapes it:

- Bind to loopback and the tailnet interface only, never `0.0.0.0` — as `deploy/docker-compose.prod.yml` already does
- **No authentication for now.** The network boundary is the boundary. Document that decision explicitly in an ADR, along with what would have to change before this could ever be exposed publicly, because the temptation to expose it later is exactly when nobody rereads the assumptions.
- No user accounts, no multi-tenancy, no rate limiting. One user, one device.

If any of that stops being true, this design is wrong and should be revisited rather than patched.

## B2. REST endpoints

Read-only. Nothing here mutates trading state.

```
GET /api/candles?timeframe=&from=&to=&limit=
GET /api/indicators?timeframe=&from=&to=
GET /api/signals?status=&limit=&offset=
GET /api/signals/{id}
GET /api/outcomes?status=&from=&to=
GET /api/performance?from=&to=
GET /api/status
```

- `/api/candles` caps `limit` and paginates. A phone asking for three years of 1m candles must be refused clearly, not served slowly.
- `/api/indicators` recomputes from candles — Phase 03 §7 decided not to store them, and that decision stands. If it is too slow, say so with measurements rather than adding a table.
- `/api/signals/{id}` includes the full `reason` payload: indicators, trend state, resolved parameters. This is what makes a signal reviewable months later.
- `/api/performance` reports live outcomes: win rate, average win and loss, expectancy, all **after modelled costs**, with the same insufficient-sample banner as reconciliation.
- `/api/status` is the pipeline health from A3 — collector state, oldest unresolved signal, delivery failures, last signal time.

Every response that reports a computed figure carries the sample size beside it. A win rate over nine trades and one over nine hundred must not be able to look alike.

## B3. WebSocket

```
WS /api/stream
```

Subscribable topics: `candles` (including the forming bar), `signals`, `outcomes`, `status`.

- The forming candle may be streamed for display — it is the one place open candles are legitimate. **It must be flagged as unclosed in the payload**, and nothing downstream may compute on it. `CLAUDE.md` §3.1 has held everywhere so far; this is where it is most likely to be broken by accident.
- Ping/pong keepalive; assume the phone drops connection constantly on mobile networks
- On reconnect the client says what it last saw and the server sends the delta. Do not replay from the beginning.
- Backpressure: a slow client must not stall the server. Drop and mark the gap rather than buffering without limit.

## B4. Shape and errors

- JSON, snake_case, ISO 8601 UTC timestamps, `null` for absent, never `0`
- Prices as strings, not floats — `CLAUDE.md` §4. A phone parsing `0.1 + 0.2` is the same hazard as a server doing it
- Errors: HTTP status plus `{"error": {"code": "...", "message": "..."}}` with codes stable enough for the app to branch on
- Version the path (`/api/v1/...`) so Phase 09 can change without breaking a deployed app

## B5. Documentation

`docs/api.md`: every endpoint, parameters, response shape, error codes, and a working `curl` for each. Phase 09 is written against this document; if it is wrong, the app is wrong.

---

## Definition of Done

**Part A**
- [ ] `docs/audit-phase-07.md` covering A1–A5, including areas with no findings
- [ ] Each finding classified as bug or design decision, with severity
- [ ] No fixes applied without describing them first

**Part B**
- [x] `go build ./... && go vet ./... && go test ./...` passes
- [x] Every endpoint in B2 works, with pagination and limits enforced
- [x] WebSocket streams all four topics, forming candles flagged unclosed
- [x] Reconnect-with-delta works; a slow client cannot stall the server
- [x] Sample size accompanies every computed figure
- [x] Bound to loopback and tailnet only; unreachable from the public IP
- [x] `docs/api.md` complete, every example verified against a running server
- [x] ADR for the no-authentication decision
- [x] No code touches any order, trade, account, or withdrawal endpoint

One deviation from B2, deliberate and documented in `docs/api.md`:
`/api/v1/signals` takes `direction` rather than `status`. A signal has no
status of its own — what became of it lives in `signal_outcomes` — and
`/api/v1/outcomes?status=` already answers that while carrying the signal's
fields alongside. A second way to ask it would be a second thing to keep
consistent.

---

## Out of scope

- The mobile app (Phase 09)
- Authentication, accounts, multi-tenancy
- Public exposure, TLS, reverse proxy
- Storing computed indicators
- Order placement, in any form
- Resuming the strategy search

---

## How to start

Part A first. Summarise the audit plan, run it, write the report, and stop. Part B waits for triage.
