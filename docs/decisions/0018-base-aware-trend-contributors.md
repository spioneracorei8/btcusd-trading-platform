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
| 15m | 1h, 4h, 1d |
| 1h | 4h, 1d |
| 4h | 1d |
| 1d | error — nothing above it |

Weights keep the shipped proportions: the three-contributor sets are
0.2/0.3/0.5 shortest-first, and shorter sets are the heaviest end of that
renormalised to 1. So 1d:4h at a 1h base is the same 5:3 that 1h:15m is at a 1m
base, and the slowest contributor is always the strongest voice.

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

## Consequence: the slowest contributor sets the warm-up, and it is long

The filter says nothing until every contributor is warm. Warm-up is
`WarmupMultiplier × EMAPeriod` = 5 × 200 = **1000 closes of each contributor**.
In wall-clock time that is:

| contributor | 1000 closes |
|---|---|
| 1h | ~42 days |
| 4h | ~167 days |
| 1d | ~2.7 years |

So a **1h base cannot be filtered over the development set as things stand**.
Running from 2023-01-01 needs 4h candles from 2022-07-19 and 1d candles from
**2020-04-06**; `MARKET_BACKFILL_FROM` is 2023-01-01, so the 1d series is far
too short, the filter never becomes ready, and every entry is blocked. This was
observed, not predicted: a filtered 1h run reports 100% of bars not-ready and
zero trades.

This is a data problem rather than a code one, and the code now says so — the
CLI refuses to build a filter whose contributors have no stored candles, naming
the timeframe and pointing at `MARKET_TIMEFRAMES`, instead of running to
completion and reporting a filtered result that filtered nothing.

Three ways forward, none of them taken here because they are evaluation
decisions rather than code ones:

- backfill 1d (and 4h) far enough — Binance has BTCUSDT daily candles from 2017,
  so this is collection, not availability
- accept a shorter warm-up for slow contributors, which means deciding what
  `WarmupMultiplier` means for a 1d EMA(200) — a parameter change, and those
  are made deliberately and separately
- use a 15m or 1h base with only 4h as a contributor, which warms in 167 days

## Consequences

- Adding a timeframe to the system means adding a row to `defaultContributors`,
  not editing a shared constant.
- The 1m results in `docs/experiments.md` stay comparable with future 1m runs.
- A base with nothing above it is still a hard error. That case is now real
  rather than theoretical: 1d is the slowest timeframe this system collects.
