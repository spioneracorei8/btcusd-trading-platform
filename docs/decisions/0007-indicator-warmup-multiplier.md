# 0007 — Indicator warm-up is 5x the period

**Status:** accepted · **Date:** 2026-08-07 · **Phase:** 03

## Context

An EMA has infinite memory. Its value never fully forgets the seed it started
from, and the same is true of RSI and ATR under Wilder's smoothing, which is
the same recurrence with a different coefficient.

The arithmetic minimum is tempting and wrong. An EMA(200) can produce a number
after 200 candles — it just seeded an SMA and has applied the recurrence zero
times. That number is essentially the arbitrary starting mean. Emitting it
lets a backtest score its earliest bars against unconverged values and report
the result as history.

The decay is geometric: after *n* bars past the seed, the seed's remaining
weight is `(1 - k)^n` where `k = 2/(period+1)`.

| bars past seed | residual seed weight, EMA(200) |
|---|---|
| 200 | 0.135 |
| 400 | 0.018 |
| 800 | 3.3e-4 |
| 1000 (5x) | 4.8e-5 |

## Decision

`WarmupPeriod()` returns `5 x period` for EMA, RSI and ATR. `Ready()` is false
for that entire window and there is no partial-credit state.

Five is chosen because it puts the residual seed weight around 5e-5, which is
two orders of magnitude below the `1e-6`-per-value agreement the fixtures
require of everything else, while still costing only about 17 hours of 1m
candles for the longest indicator in use.

VWAP is exempt and returns 1. It has no memory to converge — it is exact for
the bars it has seen. Its first bars of a UTC session do average a small
sample and are correspondingly jumpy, but that is a property of the
definition, not an unconverged state. See ADR 0008.

## Consequences

- An EMA(200) needs 1000 candles before it says anything. A backtest must
  therefore load history before the range it intends to score, and phase 04
  refuses any bar where a required indicator is not ready.
- `MaxWarmupPeriod` reports the longest warm-up across a set, so callers do not
  have to work this out per indicator.
- The tests demonstrate the hazard rather than assert a magic number:
  `TestEMAConvergenceDependsOnHistoryLength` shows two EMA(200)s ending on the
  same candle differing by 3.9e-10 when one has seen 1200 bars and the other
  4000, and `TestEMAWithOnlyPeriodCandlesEmitsNothing` shows that exactly
  `period` candles produce no output at all.
- Values are compared against TA-Lib only after our warm-up. TA-Lib emits from
  `period-1`, so our window is a strict superset of its own and the comparison
  is never made against its unconverged bars either.
