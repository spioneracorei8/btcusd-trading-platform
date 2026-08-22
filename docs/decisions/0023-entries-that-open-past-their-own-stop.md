# ADR 0023 — Entries that open past their own stop are counted, not corrected

**Status:** accepted (phase 07). **The correction itself is outstanding work.**

## Context

A decision is taken on one bar's close. The fill happens at the next bar's
open. The stop was computed from the close, using the ATR at the close.
Nothing constrains the distance between that close and that open.

When the market moves far enough in between, the position opens **already
beyond the level that was meant to bound it**. The engine takes the position
anyway, and `checkLevels` then closes it at the stop — because the stop's
price is what the engine prices a triggered stop from.

On such a bar the market never traded at that price. So the loss is
understated, and in the worst case the sign flips: a long that gapped *down*
is recorded as a stop that **made money**.

Found in phase 07 while running the outcome follower against stored candles,
not by a unit test. A planted signal on 4h data decided at 28,200 with a stop
at 28,000; the next bar opened at 27,600. The follower — reproducing the
engine faithfully — recorded status `stop` with a **net return of +1.22%**.

The engine's regression test now demonstrates the same thing directly: a long
entered at 80.01 with a stop at 95 exits at 95 and books a gross gain.

## Decision

**Count it. Do not correct it yet.**

`backtest.Levels.EntryBeyond` reports that a fill landed at or past one of its
levels. The engine counts `EntriesBeyondStop` and `EntriesBeyondTarget` into
every `Result`, the JSON report carries both, and the rendered report prints
them with their share of trades whenever either is non-zero.

The outcome follower calls the same function — per ADR 0022, both sides read
one rule — and marks the affected `signal_outcomes` row with a
`divergence_note`. The reconciliation counts those resolutions separately as
*rested on assumption*.

## Why not correct it now

Correcting the fill — pricing the exit at the open rather than at the level —
changes the P&L of every affected trade, and therefore **every number in
`docs/experiments.md`**. Sixty-four evaluations were judged against acceptance
criteria using the current arithmetic. Changing it silently would leave a log
whose entries were produced by two different engines with no way to tell which.

The first thing needed is the size of the problem. A correction that moves
nothing is not worth invalidating the log for; one that moves a lot is worth
doing carefully and re-running the set behind it. Counting first, correcting
second, is the order that keeps the log honest either way.

## How much does it affect the 64 evaluations?

**Not measured. This is the outstanding work, and it is recorded rather than
left to be rediscovered.**

It could not be measured here. The build environment cannot reach Binance
(`api.binance.com` is refused by policy) and holds no real BTCUSDT history —
even the indicator fixtures are synthetic, for the same reason. The
experiment log records no such field for past runs, so it cannot be mined
either. The number requires re-running against the stored history on the VPS.

### What is known about the bound

The condition, for a long, is:

```
close(N) − open(N+1)  ≥  k · ATR(N) + slippage
```

with `k` the strategy's `stop_atr_mult`: 1.5 for `ema_crossover`, 1.2 for
`trend_pullback` and `mtf_alignment`, 1.0 for `rsi_reversion`. So the required
jump is at least one full ATR of the deciding bar, in a single instant.

Two things follow.

**On contiguous spot data it should be near zero.** Binance spot trades
continuously and `close_time(N) == open_time(N+1)`; the close is the last
trade before that instant and the open the first trade after it. On a liquid
pair those differ by a tick or two, against an ATR that is the mean *range*
over fourteen bars.

**The condition is self-limiting.** It needs a jump large relative to
prevailing volatility — but a series full of such jumps has an ATR inflated by
them. Running `ema_crossover` on the sandbox's synthetic sawtooth, whose 4h
bars jump 600 points between close and open, produced **141 trades and zero
entries past their level**, because those same jumps set the ATR.

**Where the real exposure is: gap boundaries.** When bar N and bar N+1 are not
adjacent in time — a collector outage, or Binance downtime — the price can have
moved arbitrarily. The engine already discards a pending entry across a
*recorded* gap or outage (`onCandle` step 1, via `excluded.crossedBetween`).
It does **not** do so across a break that was never recorded as a gap, because
that check consults the recorded set. Unrecorded breaks are therefore the
plausible source, and the dev-set evaluations ran with `--allow-gaps=skip`.

### How to get the number

On the VPS, with the stored history, using a binary built from this commit or
later:

```sh
make backtest ARGS="--strategy ema_crossover --timeframe 4h \
  --from 2023-01-01T00:00:00Z --to 2024-12-31T00:00:00Z --json" \
  | jq '.bars.entries_beyond_stop, .bars.entries_beyond_target, .performance.trades'
```

Repeat for the configurations behind the evaluations that mattered —
`ema_crossover` on 4h and 1h, `trend_pullback`, `rsi_reversion` — and record
the counts as a share of trades.

**If the share is negligible**, say so in `docs/experiments.md` and close this
out: the sixty-four evaluations stand.

**If it is not**, the correction is to price the exit at the fill-bar open
when the entry opened past the level, regenerate the golden file, and re-run
the affected evaluations under a new engine version — with the log stating
plainly which entries came from which.

## Consequences

- Every run from this commit reports the count, so the question is answerable
  by running rather than by reasoning.
- The counter is additive to the golden file: regenerating it against the
  `1e57bba` baseline changed **zero existing values and removed zero keys**.
- Live and backtest still agree, which is the property phase 07 depends on.
  The follower reproduces the flaw deliberately; correcting one side alone
  would make the two disagree for a reason unrelated to the strategy.
- Until the measurement is done, any evaluation resting on trades that hit
  this case is resting on a fill the market did not offer. The count is what
  makes that visible per run.

## Alternatives rejected

**Refuse the entry when it would open past its stop.** Defensible — a real
order would have been stopped out immediately — but it deletes trades from the
record rather than pricing them, and deleting the worst fills is exactly the
shape of an optimistic backtest.

**Correct the fill now, quietly.** Rejected: it rewrites the meaning of
sixty-four logged evaluations with nothing in the log to mark the change.

**Correct only the live follower.** Rejected: it breaks the one property the
comparison depends on, and the divergence it created would be attributed to
the market.
