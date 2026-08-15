# 0020 — Maker fees, and the missed fills that pay for them

**Status:** accepted · **Date:** 2026-08-15 · **Extends:** [0012](0012-execution-model.md)

## Context

Every evaluation so far charged 0.05% taker on both sides — 0.1% a round trip.
That assumption decided the results, not the entry logic: `ema_crossover` at 1h
returns +25.60% unfiltered and +7.68% filtered, and the filtered edge goes to
−1.10% at 1.5× cost.

Binance charges roughly 0.02% for maker fills. A round trip at maker rates is
0.04% rather than 0.1% — a 60% reduction in the dominant term, larger than any
parameter change tried.

## Decision

**Maker fees are modelled together with the fills they cost.**

A limit order pays less *and* only fills if price comes to it. Modelling the
cheaper fee alone would produce a report that is straightforwardly false —
cheaper trades that always happen — which would be a strictly better strategy
than the one under test. The two halves are one change.

- `FEE_MAKER_PCT`, `ENTRY_ORDER_TYPE`, `EXIT_ORDER_TYPE` and
  `LIMIT_ORDER_TIMEOUT_BARS` are configuration, defaulting to today's behaviour
- a limit entry rests at the **close of the signal bar**, fills when a later
  bar's low reaches it (or high, for a sell), at the limit price with no
  slippage, and is cancelled unfilled after the timeout
- cancelled orders are counted and reported as a share of signals

### Why `Execution` is not part of `Costs`

`Costs` answers "what does a fill pay". `Execution` answers "does the fill
happen at all". Folding them together would also make the cost sweep
ambiguous: the sweep scales what things cost and must not quietly change how
orders are placed.

### Why the zero value is market, when `Sizing` refuses one

`Sizing` has no natural default, so it demands one be stated. Market is both
what every completed evaluation used and the conservative side of this choice:
it always fills and pays the higher fee. An unstated execution model therefore
cannot flatter a result, and defaulting to limit — the direction that could —
is the one this refuses. `Costs.MakerFeePct()` falls back to the taker rate for
the same reason: a zero-value `Costs` must not make trading cheaper.

### Stops are market orders under every configuration

`EXIT_ORDER_TYPE=limit` applies to targets only, and this is structural: the
maker branch is selected by the exit *reason*, so a stop cannot reach it.

The reason is not modelling convenience. A stop that only fills at its limit
price is a stop that does not fill when the market gaps through it — precisely
the situation stops exist for. Modelling one as a resting order would delete
the worst losses from the record and produce a strategy that looks robust
because its tail was quietly removed.

Mark-to-market follows the same rule: an open position is always priced as if
it would exit at market, because its exit is not known to be a target fill.
Drawdown is the statistic that most needs to be pessimistic.

## Where this model is optimistic

Both of these are stated in the report's ASSUMPTIONS block, beside the
stop-before-target assumption, because they are the same class of thing — a
simplification the result rests on, which the reader has to be able to weigh.

- **Touch is treated as fill.** Queue position is real; being at the front of
  the book is not automatic. The true fill rate is at best the modelled one.
- **Intrabar path is unknown.** A bar whose low reached the limit may have done
  so before or after everything else in that bar.

## What to read first

Not the headline. **The cancelled-order rate, alongside the cost sweep.**

A strategy that survives 1.5× maker costs with few cancellations is a finding.
One that turns positive only because half its intended trades never happened
has not been improved — it has been sampled, and its statistics describe the
subset that filled rather than the strategy as written.

The first real run made the point without being asked to: at a 15m base with a
one-bar timeout, **100% of 5,458 signals were cancelled** and the run produced
no trades at all. On a rising series a buy limit below the market simply never
gets hit. With an eight-bar timeout the same run filled 3,386 maker entries at
**6.65 bps per round trip against 9.52 bps** for the market version — the
saving is real, and so is what it costs to obtain.

Because a run can now pay maker on one side and taker on the other, neither
configured rate answers "what did this cost". `CostPerTripBps` is measured from
the trades instead, and the cost sweep names which model it scaled.

## Consequences

- `docs/experiments.md` entries produced before this change remain valid and
  comparable: they were market-order runs, and market-order arithmetic is
  unchanged. `testdata/golden/market-orders-baseline.json` is what proves it.
- That golden was regenerated once here, when the JSON document gained fields.
  Verified field by field: **68 keys added, zero existing values changed.**
- A future run must state its order types when reporting a result. Two runs of
  the same strategy at different order types are different measurements, and
  the header says which one produced the number.
