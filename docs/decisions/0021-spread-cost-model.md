# 0021 — Costs in price points, because the venue changed

**Status:** accepted · **Date:** 2026-08-15 · **Extends:** [0012](0012-execution-model.md), [0020](0020-maker-fees-and-limit-fills.md)

## Context

Every evaluation up to this point assumed Binance spot: 0.05% taker per side,
charged as a share of notional. The venue this account will actually trade is
different in both structure and magnitude.

**IUX Standard, BTCUSD CFD on MT5:** floating spread around 25 USD, no
commission, 1 lot = 1 BTC, minimum 0.01 lot, 1 point = 0.01 of price.

At BTC 63,000 a 0.01 BTC round trip costs 0.63 USD on Binance and about
0.25 USD here — roughly 2.5× cheaper, in the term that has dominated every
result so far.

The second constraint is harder and points the other way. **The account is
100 USD.** The phase 06 sweep found monotonic improvement with timeframe across
all three strategies, but higher timeframes need wider stops: a 4h stop of
500–800 USD risks 5–8 USD at 0.01 lot, which is 5–8% of the account per trade.
Position size cannot absorb that — 0.01 lot is the floor, not a starting point.

## Decision

**The cost model is a choice between two shapes, not a parameter.**

`COST_MODEL=percentage` charges a share of notional. `COST_MODEL=spread`
charges the bid/ask distance in price points, plus optional commission per lot.

These are not two parameterisations of one formula. On an exchange the cost of
a round trip scales with the price level; on a spread venue it does not. At 1m,
where a bar's range and the quoted spread are the same order of magnitude,
using the wrong one misprices every trade — and in opposite directions
depending on where price happens to be sitting.

`percentage` remains the default and the zero value. Every completed evaluation
used it, and a run that mentions none of the new settings must be identical to
one from before they existed.

### Half a spread per side, not a full one per side

A quoted spread of 25 is the distance between bid and ask. Buying at the ask
and later selling at the bid gives that distance up **once across the round
trip**, not twice: 25 × 0.01 BTC = 0.25 USD at the minimum lot, which is the
venue's own arithmetic.

Modelled as half on each side of the mid, so entry and exit are symmetric and a
long and a short of the same size cost the same. Charging a full spread per
side would double every cost in the model — an error in the safe direction, and
still an error, because it would make a viable strategy look like half of one.

The report header says `half each side` rather than leaving it to be inferred.

### Spread and slippage are different things

`SLIPPAGE_TICKS` still applies on top, on market fills only. The spread is what
crossing costs; slippage is the book moving while you cross. A resting order
pays neither, which is the entire reason to use one.

### Sizes land on the venue's lot grid, downward

Fixed-fractional sizing solves for a continuous size. The venue cannot trade
one. So under the spread model a computed size is floored onto `LOT_STEP`, and
**a size below `MIN_LOT` is refused rather than taken at the minimum.**

Taking it anyway would be a different and larger bet than the strategy asked
for. On a 100 USD account the gap between "what 1% risk implies" and "the
smallest lot available" is often a factor of five or more, so quietly upsizing
is not a rounding — it is how an account dies while the report still describes
the strategy that was tested.

The refusals are counted and reported. With this balance the count may be most
of the signals, and that is a fact about **the account**, not about the
strategy. A run whose statistics describe a fraction of the strategy's intent
has to say so on its face.

### The grid is tied to the cost model, not to `MIN_LOT` being set

`LotConstrained()` requires `COST_MODEL=spread`. The lot constraint arrives with
the venue, so it is gated on the venue. Were it gated on `MIN_LOT` holding a
value, a stray line in an env file would silently change the sizing of a run
whose report says nothing about lots — and every earlier percentage result
would stop being comparable without anything visible having changed.

### `ACCOUNT_CURRENCY_BALANCE` was proposed and dropped

The round asked for a separate balance setting so percentage reporting could
stay readable while modelling a small real account. It was not added:
`--initial-equity=100` already models the account, and a second balance would
have created two numbers that must agree and no mechanism to keep them
agreeing. Absolute figures are reported beside every percentage instead, which
was the actual requirement.

## Reporting

The header states the model in force, and under `spread` it prints the venue
parameters and this, on every run:

```
  note: prices are Binance BTCUSDT; costs model IUX BTCUSD CFD.
```

Every candle in this system is Binance BTCUSDT. The intended venue is IUX
BTCUSD CFD. Those are different instruments — different liquidity, different
quotes, different spread behaviour, and a CFD tracks the underlying rather than
being it. **The cost model can be made to match; the price series cannot**,
without collecting from the venue itself.

This is not a reason to stop. It is a reason not to read any number here as a
prediction of what the account will do, and the report says so every time
rather than relying on it being remembered.

### Absolute figures beside every percentage

- risk per trade in currency and as a share of balance, average and worst
- worst drawdown in currency as well as percent
- average cost per round trip in currency
- total costs as a share of gross profit

"-45%" and "-45 USD" on a 100 USD account are the same number and read
completely differently, and only the second answers whether a drawdown could be
sat through. The 1m runs produced a **72-trade losing streak**; at 0.5–0.8 USD
a trade that is 36–58 USD, over half the balance. The arithmetic belongs in the
report rather than being discovered live.

## Consequences

- The cost sweep scales `SPREAD_POINTS` and `COMMISSION_PER_LOT` alongside the
  fee rates. On a floating-spread venue this is the realistic failure mode
  rather than a pessimistic one: 25 USD is a typical quote, not a guaranteed
  one, and it widens exactly when a strategy most wants to trade. A 2× pass is
  a normal Tuesday during a news release.
- `docs/experiments.md` entries produced before this change remain valid: they
  were percentage runs, and percentage arithmetic is untouched.
  `testdata/golden/market-orders-baseline.json` is what proves it, and it was
  regenerated here for the second time — **37 further keys added, zero existing
  values changed, none removed**, checked field by field against the file as
  first committed.
- Spread-model results and percentage-model results are **different
  measurements** and must not be compared. The header names which one produced
  the number, and the experiment log records it.
- Nothing here touches an order, trade, account, or withdrawal endpoint. The
  venue is modelled, never contacted.
