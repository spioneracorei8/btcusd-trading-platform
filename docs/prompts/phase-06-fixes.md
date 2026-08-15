# Phase 06 — Post-Evaluation Fixes

> Three defects found during the first real evaluation runs against BTCUSDT 2023–2024.
> None of them changes a completed result. All three block further evaluation, which is why they come before anything else.
> `CLAUDE.md` rules apply as always.

---

## Context

The first evaluation runs produced a clear finding: at 1m the strategies lose almost the entire account to costs, and the same rules on 1h turn positive. Following that thread requires running higher timeframes and comparing filtered against unfiltered — and two of the three defects below make exactly that impossible.

The third does not block anything. It is worse: it produces a plausible-looking number that means nothing.

---

## Fix 1 — Trend filter rejects any base timeframe above 1m

**What happened**

```
./backtest -strategy=ema_crossover -timeframe=15m
ERROR could not build the trend filter
  error="trend: 5m is not higher than the base timeframe 15m; a contributor
  that closes no less often than the base adds nothing but a chance to misalign"
```

The rejection is correct in principle — Phase 05 §1 exists precisely because a contributor that does not close strictly less often than the base is a look-ahead hazard. The defect is the response. The filter treats a configured contributor that happens to sit at or below the base as a fatal configuration error, when the sensible reading is that this contributor has nothing to say at this base and the others still do.

The practical effect is that `--compare` and `--trend-filter` are unusable at every timeframe except 1m, and 1m is the one the evidence says to move away from.

**Required change**

At construction, partition the configured contributors:

- Contributors strictly higher than the base are used
- Contributors at or below the base are dropped, and the drop is **reported in the run header** — silently ignoring configuration is its own defect
- If no contributor remains above the base, that is a genuine error: a filter with nothing to contribute must fail rather than pretend to be a filter

Re-normalise the weights across the surviving contributors so that dropping 5m does not silently reduce the filter's total influence. State both the surviving set and the re-normalised weights in the header.

The look-ahead rule from Phase 05 §1 is unchanged and must remain enforced for every surviving contributor.

**Tests**

- base 15m with contributors 5m/15m/1h → 1h survives, 5m and 15m dropped, weights re-normalised, drop reported
- base 1h with contributors 5m/15m/1h → construction fails with a clear message
- base 1m → all three survive, behaviour identical to today
- A dropped contributor cannot influence the resulting `TrendState` in any way

---

## Fix 2 — Cost sweep does not reset the trend filter between passes

**What happened**

```
ERROR could not run the cost sweep
  error="cost sweep at 1.0x: advance the trend filter: trend: Advance called
  with 2023-01-01T16:39:59Z after 2024-12-31T23:59:59Z; the aligner only
  moves forward"
```

The aligner's forward-only invariant is correct and should stay. The sweep reuses one filter instance across passes: the first pass leaves it at the end of the range, the second starts again at the beginning, and the invariant fires.

The base run completed and printed before the sweep failed, which made it look like a partial success. It is not — the sweep is the part that answers whether an edge survives higher costs, and for a strategy whose viability turns entirely on costs, that is the more important number.

**Required change**

Each sweep pass gets fresh state: a newly constructed strategy, filter, and indicator set, exactly as the base run does. Do not add a `Reset()` call as the fix unless every component's reset is already proven equivalent to reconstruction — Phase 03 has that test for indicators, but the filter does not. Construction is the safer default.

Audit the same pattern elsewhere. `--compare` runs twice as well; check whether it happens to work by accident of ordering rather than by design.

**Tests**

- `--cost-sweep` completes and prints 1.0×, 1.5×, and 2.0× results
- The 1.0× sweep pass reproduces the base run exactly — same trades, same net return. If it does not, state is leaking between passes
- `--compare` filtered and unfiltered passes are independent of the order they run in
- `--cost-sweep` and `--compare` together complete without error

---

## Fix 3 — Volatility split uses an outcome, not a condition

**What happened**

Every 1m run reported this shape:

```
by volatility (median split on entry-to-exit move)
  low volatility     4332 trades   -6803.01 USDT   win rate   0.00%
  high volatility    4330 trades   -3194.31 USDT   win rate  49.61%
```

A 0.00% win rate across 4,332 trades, reproduced across three structurally different strategies, is not a finding about markets. It is arithmetic.

The split is made on entry-to-exit move — the size of the move the trade actually captured. That is the trade's own outcome. Every trade whose price barely moved lost to costs, and every such trade lands in the low bucket by construction. The bucket does not describe a market condition the strategy encountered; it describes how the trade turned out. Sorting losses into a bucket and then reporting that the bucket lost is circular.

Phase 06 §C4 asked for regime dependence — whether the strategy behaves differently in different market conditions. That question needs a variable knowable **at entry**, before the outcome exists.

**Required change**

Split on **ATR at the entry bar**, normalised as a percentage of entry price so the buckets are comparable across a range where BTC moved between roughly \$16k and \$100k. An absolute ATR split would mostly be sorting by calendar date.

- Compute the median normalised ATR across entries in the run, and split there
- Label the buckets with the actual ATR percentage range each covers, not just "low" and "high" — the reader needs to know whether "high" means 0.3% or 3%
- Consider reporting terciles rather than a median split; two buckets hide a lot, and the interesting behaviour is often at the extremes

While in this code, check the same failure mode elsewhere: any breakdown that partitions trades using a value that is only knowable after the exit is measuring itself. The by-year split is fine — the year is known at entry.

**Tests**

- The split value for each trade is taken from the entry bar's indicator snapshot, not from the trade result
- A fixture where trades are deliberately spread across known ATR levels lands in the expected buckets
- Bucket boundaries are reported as ATR percentages
- Neither bucket can return a 0.00% win rate purely as an artefact of the split

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `-timeframe=15m` and `-timeframe=1h` run with the trend filter, reporting which contributors were dropped and the re-normalised weights
- [ ] `--compare` runs at 15m and 1h
- [ ] `--cost-sweep` completes, and its 1.0× pass reproduces the base run exactly
- [ ] `--cost-sweep --compare` together complete
- [ ] Volatility buckets are formed from entry-bar ATR and labelled with their ranges
- [ ] No breakdown anywhere partitions trades by a post-exit value
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- New strategies
- Parameter changes to existing strategies
- Anything in Phase 07
- Re-running evaluations — that is mine to do once these land

---

## A note on what the evaluation found

For context, since it explains the priority order. The same rules across timeframes:

| timeframe | trades | profit factor | net after costs |
|---|---|---|---|
| 1m | 8,662 | 0.31 | −99.97% |
| 15m | 1,386 | 0.78 | −60.40% |
| 1h | 324 | 1.13 | +25.60% |

The rule did not change; the trade frequency did. Costs are the dominant variable, which is why Fix 2 — the sweep that measures cost sensitivity — is not a nice-to-have, and why Fix 1 blocking higher timeframes is the most urgent of the three.

---

## How to start

These are independent. Summarise briefly, then commit each separately. If any of them turns out to require a change larger than described here, stop and explain before writing code.
