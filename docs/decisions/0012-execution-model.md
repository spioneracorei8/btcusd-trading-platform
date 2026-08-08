# 0012 — Execution model: fills, costs and the ambiguous bar

**Status:** accepted · **Date:** 2026-08-08 · **Phase:** 04

## Context

Every simulated fill is an assumption. The question is not how to avoid making
them — that is impossible — but which way to lean when the data cannot settle
the matter, and whether the reader is told.

At 1m–5m frequency the answer decides everything. A round trip costs roughly
0.1% before slippage; a strategy targeting 0.3% a trade gives up a third of its
edge to costs. Small optimism in the fill model is not a rounding error there,
it is the entire result.

## Decision

**Fill at the next bar's open.** A signal produced on the close of bar *t*
fills at the open of *t+1*. Never at the close of *t*: that price is only
knowable once *t* is over, so a decision made on it cannot also fill on it.
This is the single rule most responsible for backtests that cannot be
reproduced live.

**Costs are mandatory and split.** Fee and slippage apply to both sides of
every round trip and no flag disables them. Slippage is charged as a *cost*
rather than folded into the fill price, and gross PnL is measured between the
unslipped reference prices:

```
gross    = (refExit - refEntry) × size          what the market did
fees     = feeOn(fillEntry) + feeOn(fillExit)
slippage = tick × slippageTicks × size × 2
net      = gross - fees - slippage
```

The account follows the actual fills, and `realised = gross - slippage`
reconciles the two by construction. Charging slippage inside the price and
still calling the result "gross" would hide half the cost of trading inside the
number that is supposed to be free of it. `Trade` reports fees and slippage
separately because a strategy killed by one is fixed differently from a
strategy killed by the other.

**The stop wins an ambiguous bar.** When a bar's range reaches the stop and the
target both, the stop is taken. A 1m bar records four prices and says nothing
about the path between them, so the bar genuinely does not say which came
first. The optimistic reading would inflate results precisely on the bars where
the data cannot contradict it. Every such bar is counted into the report: a
result resting on many of them is being scored on an assumption rather than on
evidence.

**All-in position sizing.** Each entry commits the whole account, fee included:

```
size = equity / (fillPrice × (1 + feeRate))
```

Dividing by `(1 + feeRate)` is what makes the entry affordable rather than one
fee over budget. Returns therefore compound and the equity curve is directly
comparable with buy-and-hold. Position sizing proper arrives in phase 06.

**End of run liquidates at the last close.** A position still open when the
range ends is closed at the final bar's close, reason `end_of_run`. There is no
following open to fill at, and no decision is being made on that price — the
engine is liquidating so the reported return is realised rather than half on
paper.

**Equity is marked net of the exit fee.** A curve that ignored the cost of
getting out would overstate every point and understate every drawdown, which is
the statistic that most needs to be pessimistic.

## Consequences

- The phase-04 spec §7 describes buy-and-hold's expected result as the
  close-to-close change less one round trip. That cannot hold alongside §4's
  fill-at-next-open rule, since the entry price is an open. §4 is the stronger
  rule, so `TestBuyAndHoldMatchesAHandComputedFigure` derives its expected
  figure from the fills the stated rules actually produce, and says so.
- `ExitEndOfRun` is a fifth exit reason beyond the four §6 lists. A run whose
  strategy holds to the end would otherwise have no way to report a realised
  return.
- `decimal.Div` rounds at 16 decimal places, so `size` carries a rounding.
  It is deterministic, which is what determinism requires; the tests reproduce
  the documented formula in the documented order rather than an algebraic
  rearrangement of it.
- A break-even trade counts as a loss in the statistics. It paid costs and
  returned nothing; counting it as a win would make a strategy that never
  profits look half successful.
