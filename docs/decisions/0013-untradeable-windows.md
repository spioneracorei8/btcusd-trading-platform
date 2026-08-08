# 0013 — An exchange outage is not a data gap

**Status:** accepted · **Date:** 2026-08-08 · **Phase:** 04

## Context

Two different things produce the same symptom — a stretch of time with no
candles — and they demand opposite treatment:

| | what it means | can it be fixed? |
|---|---|---|
| **gap** | we do not know what the price did | yes, by backfill |
| **outage** | nothing could have happened at all | no, there is nothing to fetch |

Phase 02's `data_gaps` records the first. It has no way to express the second,
and the difference is not cosmetic. A gap is a question about our data, so
whether it stops a run is a policy question the operator can answer with
`--allow-gaps`. An outage is a fact about the market: during Binance's
2023-03-24 spot matching-engine halt no order could have been filled at any
price. A backtest that trades through one is not working with uncertain data,
it is reporting fills that were impossible.

## Decision

`backtest.KnownOutages` is a list in code — not a table.

These are facts about exchange history, fixed once they happen. Shipping them
with the code means they are reviewed with the code and a run over an old range
produces the same answer next year as today. Operational state the collector
discovers stays in `data_gaps`; this does not.

During an outage window the engine refuses entries and force-closes any open
position at the last close before the halt, with `ForcedByGap` set and exit
reason `gap_forced`. **This applies under every gap policy, including
`--allow-gaps=ignore`.** `ignore` is permission to run over data we are unsure
of; it is not permission to invent fills that could not have occurred.

Bounds are half-open, `[Start, End)`: the first bar of the halt is excluded and
the bar at `End` is tradeable again. `TestUntradeableWindowBoundsAreHalfOpen`
pins both edges, because one minute too wide silently deletes a tradeable bar
and one minute too narrow lets an impossible fill through.

## The 2023-03-24 entry, and what is not verified

The seeded window is 2023-03-24 12:40–14:00 UTC, taken from
`docs/prompts/phase-04.md`, which states it as "roughly 12:40 to 14:00 UTC".

**It has not been independently verified.** The environment this was built in
has no route to Binance, so the incident's true extent could not be checked
against the exchange's own record. The value is treated as the spec's claim,
not as established fact.

This is survivable because the window lives in exactly one place. Correcting it
is a one-line change to `KnownOutages`; the engine, the tests and the reports
all follow from there. Anyone adding a future entry should verify it first and
put the source in the `Reason` string.

## Consequences

- `ListUnfilledInRange` was added to the gap repository because
  `ListUnfilled` filters out gaps whose retry budget is spent. That filter is
  right for the backfill worker, which has nothing left to try, and exactly
  wrong for a backtest: a gap nobody can fill is the strongest reason to refuse
  to report a number over it.
- The report lists untradeable windows separately from unfilled gaps, so a
  reader can tell "we are missing this" from "this could not have been traded".
- Adding an outage retroactively changes past results. That is correct — the
  earlier numbers included fills that never could have happened — but it does
  mean a stored report is only comparable with another produced at the same
  code version, which is why the strategy name and version are in every header.
