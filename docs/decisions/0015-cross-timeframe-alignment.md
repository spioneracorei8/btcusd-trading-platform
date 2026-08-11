# 0015 — Cross-timeframe alignment, and the warm-up it really costs

**Status:** accepted · **Date:** 2026-08-08 · **Phase:** 05

## Context

At 14:23 the 1m candle for 14:22 has closed. The 1h candle for 14:00 has not —
it will not finish forming until 15:00. Joining on timestamp hands a strategy
the completed 14:00–15:00 hourly bar, which contains the next 37 minutes of
price action.

This does not fail. The backtest runs, the numbers come out better, and nothing
in the system can tell the difference — live simply never reproduces them. It
is the same class of mistake as filling at the close of bar *t* (ADR 0012),
one timeframe up, and it is harder to see because both series are legitimately
present in the database.

## Decision

**A higher-timeframe candle contributes only once `close_time <= t`**, where
*t* is the base bar's `close_time` — the instant the decision is being made at.
At 14:23 the newest usable hourly bar is the one covering 13:00–14:00.

Equality is included. At exactly 14:00:00 the 13:00–14:00 bar has closed and is
usable; excluding it would leave every contribution one whole period stale for
its entire life.

**Three things enforce it, at different costs:**

| | what it catches |
|---|---|
| `trend.TimeframeView` carries `CloseTime` | makes the claim checkable after the fact |
| `assertClosedBy`, under `-tags trenddebug` | panics in tests the instant it is violated |
| `TestNoContributionEverClosesAfterTheDecisionInstant` | sweeps 1440 instants across three timeframes |

The assertion is behind a build tag because the spec asks for a loud panic and
`CLAUDE.md` §4 forbids `panic()` in business logic — and both are right. Tests
compile the panicking version (`make test` passes `-tags trenddebug`); shipped
binaries compile an empty function the compiler removes, so no collector can
die of an assertion on a VPS at 3am.

**The naive implementation is kept.** `naiveAligner` does the obvious wrong
thing — reaches for the bar *containing* `t` — and a test asserts that the
alignment check rejects it. Watching the check fail once during development
proves nothing later; keeping the wrong implementation makes it a standing
guarantee that the check can still tell right from wrong.

## The warm-up nobody expects

This is the part that surprises. `WarmupBaseBars` converts each contributor's
warm-up into base bars:

| contributor | closes needed (EMA 200, 5×) | base bars at 1m |
|---|---|---|
| 5m | 1000 | 5,000 |
| 15m | 1000 | 15,000 |
| 1h | 1000 | **60,000** |

Sixty thousand 1m bars is **about 42 days** of continuous data before the
filter says anything at all. A one-month backtest with the filter on is vetoed
end to end, and the reason will look like a bug.

The full-year run measured this directly: 526,041 bars evaluated, **59,000
reported not-ready** — the computed figure, observed. That is why
`bars_filter_not_ready` is counted and reported apart from `bars_vetoed`:
"blocked on purpose" and "could not say" are different findings, and a run that
is mostly the second was simply started too early.

## Gaps reset a timeframe rather than being absorbed

A hole in a contributor's own series makes its indicators stale by however long
the hole ran. Carrying a pre-gap EMA forward would have it describe post-gap
price — across the March 2023 outage, a $1,000 move.

So the stream detects the sequence break itself, from the data, and resets:
`Set.Reset()`, warm-up counter to zero, not-ready until the **full** warm-up is
re-earned. Not a shorter recovery — a partially re-warmed EMA is still mostly
the seed it restarted from, which is the unconverged value ADR 0007 exists to
withhold.

## Consequences

- `PerTF map[Timeframe]TFState` in the phase-05 sketch is a slice here. Go
  randomises map iteration and ADR 0012 requires byte-identical reports; a map
  in this position would have broken that quietly.
- `trend.BarContext` is its own type rather than `strategy.BarContext`. A
  filter needs a reading per timeframe where a strategy needs one, and widening
  the strategy type would defeat the allow-list test that exists to stop
  exactly that (ADR 0011). The discipline is unchanged: no series, no index, no
  reader, no clock.
- `candle.CandleUsecase` gained `OpenCursor`. Lockstep merging cannot be
  expressed with the push-callback `StreamCandles`: with push, each series
  drives its own loop and nothing can hold one back to wait for another.
- The filter costs about 11 MB. A year of 1m with three extra contributors
  peaked at 91 MB against the 500 MB budget, because the higher timeframes page
  rather than load — 148,920 extra candles held resident would have been most
  of the allowance.
- Weights and the dead zone are documented defaults and are **not tuned**.
  Fitting them against the same data used to evaluate the result is how a
  filter is made to look good on the past and nowhere else. `TestDefaultsAreThe
  DocumentedOnes` pins them so a change has to be deliberate and arrives with a
  version bump.
