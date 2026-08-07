# 0008 — VWAP is daily, resets at 00:00 UTC, weighted by base volume

**Status:** accepted · **Date:** 2026-08-07 · **Phase:** 03

## Context

VWAP has no single definition. Three separate choices have to be pinned down
or the numbers become meaningless the moment anybody compares them to a chart:
when it resets, what price it averages, and what it weights by.

Left implicit, each of these silently changes the value, and a strategy tuned
against one reading would misbehave against another.

## Decision

Confirmed with the owner before implementation.

**Reset: daily at 00:00 UTC.** Crypto has no trading session, and UTC midnight
is the boundary charting tools use for it, so a value here matches what the
owner sees on a chart. The alternative — a rolling window of N candles — is
smoother and has no discontinuity at the day boundary, but it does not match
any standard chart and would need N chosen arbitrarily.

**Price: the typical price, `(high + low + close) / 3`.** Not the close. A
single close ignores where the bar actually traded.

**Weight: base volume**, so the value is
`sum(typical x volume) / sum(volume)`.

`quote_volume` was considered and rejected. It is not wrong in isolation —
`sum(quote_volume) / sum(volume)` is arguably the truer volume weighted price,
since it uses the prices trades actually happened at. But it cannot be
combined with typical-price weighting: quote volume already embeds real trade
prices, so mixing the two averages two different notions of price in one
number. Typical price with base volume is the conventional formulation and is
internally consistent.

**The reset is driven by the candle's `open_time`, never by wall-clock time.**
A backtest replaying 2023 has to produce the resets that 2023 had. Reading the
clock would make the same input produce different output on a different day,
which is the class of bug that makes a backtest unreproducible.

## Consequences

- The first bars of each UTC day average a very small sample and are jumpy.
  This is inherent and is why VWAP's `WarmupPeriod()` is 1 rather than
  something larger: the value is exact for what it has seen, it is simply
  computed over less.
- A session of entirely zero-volume bars would divide by zero. The typical
  price is used as the stand-in, which keeps the series continuous and
  finite; `TestVWAPHandlesZeroVolumeSession` covers it.
- TA-Lib has no VWAP, so the reference fixture computes this rule
  independently in `generate_reference.py` rather than offering a second
  opinion on the definition. The Go implementation is checked against that
  expression of the same rule, which catches transcription errors but not a
  wrong choice of rule — the rule itself is this document.
- `TestVWAPResetIsDrivenByCandleTimeNotWallClock` replays bars from 2023 and
  asserts the session breaks where that history says, which a wall-clock
  implementation would fail.
