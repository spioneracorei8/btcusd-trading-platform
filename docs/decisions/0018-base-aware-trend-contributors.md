# 0018 — The trend filter's contributors depend on the base timeframe

**Status:** accepted · **Date:** 2026-08-15 · **Extends:** [0015](0015-cross-timeframe-alignment.md)

## Context

Phase 05 shipped one contributor set — 5m, 15m, 1h — chosen for a 1m base.
Phase 06's first evaluation runs then found that 1m is the wrong place to
trade: the same `ema_crossover` rule goes from −99.97% at 1m to +25.60% at 1h,
purely on trade frequency. Costs are the dominant variable in this system, not
entry logic.

That made the filter unusable exactly where the evidence pointed. A 1h base has
nothing above it in a 5m/15m/1h set, so the filter either refused to build or
would have had nothing to contribute.

Two ways out were considered.

## Decision

**The contributor set is a function of the base timeframe.**

| base | contributors |
|---|---|
| 1m | 5m, 15m, 1h |
| 5m | 15m, 1h, 4h |
| 15m | 1h, 4h |
| 1h | 4h |
| 4h | 1d |
| 1d | error — nothing above it |

Weights keep the shipped proportions: the three-contributor sets are
0.2/0.3/0.5 shortest-first, and shorter sets are the heaviest end of that
renormalised to 1. So 4h:1h at a 15m base is the same 5:3 that 1h:15m is at a
1m base, and the slowest contributor is always the strongest voice. A set with
one contributor gives it the whole weight, so a 1h filter is not running at
half strength without saying so.

## Why not one shared list with 4h and 1d added

It was the smaller change, and it was rejected for a specific cost.

Adding 4h and 1d to a single default would change the filter for a **1m** base
too. Three of the seven completed evaluation runs used a 1m base with the
5m/15m/1h filter. Changing what that resolves to would leave those three
results incomparable with anything run afterwards, and `docs/experiments.md` —
whose entire value is being a complete, comparable record — would quietly
become misleading. Discarding the comparability of three of seven runs to save
a few lines of code is the wrong trade at this stage.

The deeper reason is that there is no reason the right contributor set for a 1m
base should also be right for a 1h base. One shared list forces two different
situations to use the same value because it is convenient, not because it is
correct. Base-aware is the honest shape; the comparability argument is what
made it urgent.

`TestTheOneMinuteBaseIsByteIdenticalToBefore` pins the 1m resolution against
the exact header string the logged runs recorded. That test is the guarantee,
not this paragraph.

## What did not change

`ForBase` and its partitioning are untouched. This decides what is passed in,
not how it is filtered. The per-base sets are already strictly above their
base, so `ForBase` drops nothing from a correct table — but it still runs, and
a future edit that got the table wrong fails there rather than silently
admitting a look-ahead hazard. A test asserts it drops nothing today.

The phase 05 §1 look-ahead rule is unchanged and stays enforced for every
surviving contributor by the aligner.

## Amended 2026-08-15: 1d removed from the 15m and 1h rows

As first written this ADR gave 15m the set 1h/4h/1d and 1h the set 4h/1d. Both
were wrong for a reason the next section had already worked out and the table
had not absorbed: **1d cannot warm up in time.**

The filter says nothing until every contributor has seen 1000 closes, and the
aligner starts its cursors that far before the range. For a daily contributor
that is 2.7 years *before* the range begins — earlier than any candle this
deployment has. The observed result was a filtered 1h run reporting
`bars not ready: 100.00%` and zero trades. A filter that blocks everything is
not a conservative filter; it is a broken one that produces a run with no
trades and no stated reason.

4h needs 1000 × 4h = 167 days, which the collected history covers.

**1d was removed because of the warm-up budget, not because a daily trend says
nothing.** It is the strongest trend signal available and it is the one this
system cannot currently afford. If the daily series is ever backfilled to
2020-04-06 or earlier, it belongs back in those rows.
`TestEveryContributorCanWarmUpBeforeTheDevelopmentSet` is what enforces the
budget: it fails if a contributor is added whose warm-up reaches further back
than the collected history, and it is the thing that will say the daily
contributor may return.

A 4h base keeps 1d, since it has nothing else above it and 4h is not a base
anything is evaluated on today. The same cost applies there and will announce
itself the same way.

## Consequence: the slowest contributor sets the warm-up, and it is long

The filter says nothing until every contributor is warm. Warm-up is
`WarmupMultiplier × EMAPeriod` = 5 × 200 = **1000 closes of each contributor**.
In wall-clock time that is:

| contributor | 1000 closes |
|---|---|
| 1h | ~42 days |
| 4h | ~167 days |
| 1d | ~2.7 years |

This is what drove the amendment above. Running from 2023-01-01 needs 4h
candles from 2022-07-19 — affordable — and 1d candles from **2020-04-06**,
which is not. With 1d in the set the filter never became ready and every entry
was blocked. This was observed, not predicted.

This is a data problem rather than a code one, and the code now says so — the
CLI refuses to build a filter whose contributors have no stored candles, naming
the timeframe and pointing at `MARKET_TIMEFRAMES`, instead of running to
completion and reporting a filtered result that filtered nothing.

The third option below was taken. The other two remain open and are evaluation
decisions rather than code ones:

- **taken:** use only contributors that warm up inside the collected history —
  4h at a 15m or 1h base, which needs 167 days
- backfill 1d far enough — Binance has BTCUSDT daily candles from 2017, so this
  is collection, not availability, and it is what would let 1d come back
- accept a shorter warm-up for slow contributors, which means deciding what
  `WarmupMultiplier` means for a 1d EMA(200) — a parameter change, and those
  are made deliberately and separately

## Consequences

- Adding a timeframe to the system means adding a row to `defaultContributors`,
  not editing a shared constant.
- The 1m results in `docs/experiments.md` stay comparable with future 1m runs.
- A base with nothing above it is still a hard error. That case is now real
  rather than theoretical: 1d is the slowest timeframe this system collects.
