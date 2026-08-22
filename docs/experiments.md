# Experiment log

Appended automatically after every completed run by `backtest`. Entries 1–7
were written by hand; everything from 8 onward is generated.

The automation is the point. Writing the line by hand was meant to be the
discipline, and it was not: the seven entries below were reconstructed
afterwards from scrollback, in one batch. The runs that go unrecorded are never
the interesting ones — they are the ones abandoned halfway and the ones whose
result was disappointing enough to move on from quickly, which are exactly the
ones the denominator needs. Recording them cannot be left to whoever has just
been disappointed.

The tool fills everything except **Note**, which is a judgement and stays
yours.

## 2026-08-15 — entries up to 48 contain duplicates

`--compare` and `--cost-sweep` each execute the base run, and the sweep script
ran both modes for every cell. The same configuration was therefore computed
twice and logged twice. **The number of distinct configurations tested is
roughly half the number of entries up to 48.**

The defect is fixed: a cell now runs once with both modes and produces one
entry carrying both the comparison and the sweep line.

The affected entries are **left exactly as they are**. This file exists to be
an honest record, and quietly rewriting it to look tidier would cost more than
the inflated count does — a reader who found the edit later would have no way
to know what else had been adjusted. An inflated denominator is the safer
direction of error in any case: it understates how much weight a result
deserves rather than overstating it.

When counting the entries above a result, count distinct configurations up to
48, and entries after that.

## 2026-08-22 — an unmeasured flaw affecting every entry below

Phase 07 found a case the engine prices optimistically. A position whose entry
gaps past its own stop — the decision taken on one bar's close, the fill at
the next bar's open, the market having moved further than the stop distance in
between — is still **closed at the stop's price**, which that bar never traded
at. The loss is understated, and in the worst case the sign flips: a long that
gapped down is recorded as a stop that made money.

**How many trades in the entries below are affected is not known.** It could
not be measured where the flaw was found: that environment cannot reach
Binance and holds no real BTCUSDT history, and the entries below record no
field that would answer it.

Every run from 2026-08-22 onward reports `entries_beyond_stop` and
`entries_beyond_target`, so the question is now answerable by running. The
measurement is **outstanding work**, and ADR 0023 carries the exact command,
what is already known about the bound, and what to do with either answer.

Nothing below has been edited. If the share turns out to be material, the
correction will come with a new engine version and the affected entries will
be re-run and marked as such — rather than these being quietly restated under
arithmetic they were not produced by.

## Why this file exists

Two reasons, and the second matters more.

**You will otherwise retry things you already rejected.** Six weeks from now
the memory of "I tried EMA(9/21) and it was flat" is gone, and the idea will
look fresh.

**If you run fifty variants and pick the best, the count is the finding.** The
winner of fifty coin-flip contests is not a skilled coin-flipper. A backtest
that was chosen out of fifty has roughly fifty chances to look good by
accident, and nothing in its own report can tell you that happened. This log is
the only record of the denominator.

When you reach a result worth acting on, count the entries above it first.

## Format

One entry per run, newest at the bottom, numbered. A run suppressed with
`--no-experiment-log` still spends its number, so the gap is visible: a run
that happened is a run the count has to include.

```
### <n>. <date> — <strategy> <version> (<timeframe>)

- **Dataset:** dev | holdout | custom (range)
- **Parameters:** what differed from the documented defaults, or "defaults"
- **Filter:** trend filter name and version, or none
- **Sizing:** mode and risk
- **Net return after costs:** x.xx%
- **Profit factor / max drawdown / trades:** x.xx / x.xx% / n
- **Costs as share of gross profit:** x.xx%
- **Concentration (best 5):** x.xx% of gross profit
- **Verdict against docs/acceptance-criteria.md:** pass | fail (which criterion)
- **Note:** one line — what you learned, not what you hoped
```

---

## Summary so far

Runs to date: **7** — all on the dev set (2023–2024). Holdout untouched.

| # | strategy | tf | filter | trades | PF | net | verdict |
|---|---|---|---|---|---|---|---|
| 1 | ema_crossover | 1m | ema_rsi_mtf | 8,662 | 0.31 | −99.97% | fail |
| 2 | rsi_reversion | 1m | ema_rsi_mtf | 3,716 | 0.13 | −97.23% | fail |
| 3 | trend_pullback | 1m | ema_rsi_mtf | 6,205 | 0.27 | −99.74% | fail |
| 4 | ema_crossover | 15m | none | 1,386 | 0.78 | −60.40% | fail |
| 5 | ema_crossover | 1h | none | 324 | 1.13 | **+25.60%** | fail (PF, cost share) |
| 6 | trend_pullback | 1h | none | 221 | 0.87 | −15.06% | fail |
| 7 | rsi_reversion | 1h | none | 215 | 0.65 | −32.91% | fail |

**The finding so far:** the rule barely matters; the trade frequency does. The
same ema_crossover code goes from −99.97% to +25.60% purely by moving from 1m
to 1h. Costs are the dominant variable in this system, not entry logic.

**Runs 4–7 have no trend filter** — not by choice. The filter refuses to build
at any base above 1m (see `docs/prompts/phase-06-fixes.md` Fix 1), so filtered
comparisons at 15m and 1h are still unmeasured.

**No cost sweep has completed.** Fix 2. For a system where costs decide the
outcome, this is the most important missing number.

**Volatility breakdowns in every run above are unusable.** Fix 3 — the split
is made on the trade's own outcome, so the 0.00% win-rate bucket is an artefact
of the arithmetic, not a market observation. Ignore that section in runs 1–7.

---

## Entries

### 2026-08-15 — ema_crossover v1

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults (fast=9 slow=21 stop=1.50ATR target=3.00ATR), timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50, deadzone 0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** −99.9732%
- **Profit factor / max drawdown / trades:** 0.31 / −99.97% / 8,662
- **Costs as share of gross profit:** 1,026% (costs 11,076 vs gross profit 1,079)
- **Concentration (best 5):** 8.15%
- **Verdict:** fail — every criterion except trade count
- **Note:** The rule has a small real edge (gross +10.79%) and costs are ten
  times larger than it. 8,662 trades over two years is 12 a day; at 0.1% a
  round trip that is unpayable regardless of how good the entries are.

### 2026-08-15 — rsi_reversion v1

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults (oversold=30 overbought=70 stop=1.00ATR target=1.50ATR), timeframe 1m
- **Filter:** ema_rsi_mtf v1 — vetoed 1.00%
- **Sizing:** risk 1%
- **Net return after costs:** −97.2257%
- **Profit factor / max drawdown / trades:** 0.13 / −97.23% / 3,716
- **Costs as share of gross profit:** 2,468%
- **Concentration (best 5):** 12.52%
- **Verdict:** fail — all criteria
- **Note:** Worst profit factor of the three. Reward-to-risk is only 1.5:1 by
  design, and at a 15.61% win rate that cannot work even before costs.

### 2026-08-15 — trend_pullback v1

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults (trend=50 pullback=0.50ATR resume=2 stop=1.20ATR target=3.00ATR), timeframe 1m
- **Filter:** ema_rsi_mtf v1 — vetoed 0.85%
- **Sizing:** risk 1%
- **Net return after costs:** −99.7438%
- **Profit factor / max drawdown / trades:** 0.27 / −99.74% / 6,205
- **Costs as share of gross profit:** 3,028%
- **Concentration (best 5):** 9.23%
- **Verdict:** fail — all criteria
- **Note:** Same shape as ema_crossover: a thin positive gross edge (+3.41%)
  buried under costs. Three structurally different rules failing the same way
  points at the cost model, not the entry logic.

### 2026-08-15 — ema_crossover v1 (15m)

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults, **timeframe 15m**
- **Filter:** none — the filter refuses to build above 1m (Fix 1). Not a choice.
- **Sizing:** risk 1%
- **Net return after costs:** −60.3974%
- **Profit factor / max drawdown / trades:** 0.78 / −64.63% / 1,386
- **Costs as share of gross profit:** 454%
- **Concentration (best 5):** 3.74%
- **Verdict:** fail — net return, profit factor, drawdown
- **Note:** First real signal about what matters. Identical rule, six times
  fewer trades, profit factor 0.31 → 0.78. Still losing, but the direction is
  unambiguous.

### 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults, **timeframe 1h**
- **Filter:** none (Fix 1)
- **Sizing:** risk 1%
- **Net return after costs:** **+25.5972%**
- **Profit factor / max drawdown / trades:** 1.13 / −11.86% / 324
- **Costs as share of gross profit:** 56% (costs 3,314 vs gross profit 5,873)
- **Concentration (best 5):** 5.29%
- **Verdict:** **fail** — profit factor 1.13 < 1.30, cost share 56% > 50%.
  Passes net return, drawdown, and trade count.
- **Note:** First positive result. Two things are genuinely encouraging: profit
  is spread across trades rather than concentrated (5.29%), and both years are
  positive independently (2023 +1,928, 2024 +632). Two things are not: Sharpe
  is only 0.82, and the drawdown runs five months from Dec 2023 to May 2024 —
  a long time to hold a losing position in practice, whatever the end number
  says. Missing the criteria on two of five is close, and close is where the
  temptation to adjust the criteria appears. The criteria were written first
  for exactly this moment.

### 2026-08-15 — trend_pullback v1 (1h)

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** none (Fix 1)
- **Sizing:** risk 1%
- **Net return after costs:** −15.0594%
- **Profit factor / max drawdown / trades:** 0.87 / −29.28% / 221
- **Costs as share of gross profit:** far above gross (costs 1,909 vs gross profit 403)
- **Concentration (best 5):** 12.11%
- **Verdict:** fail — net return, profit factor, drawdown
- **Note:** Improves with timeframe like ema_crossover but from a much weaker
  base: gross is only +4.03% even before costs. The edge is too thin to
  survive any realistic cost assumption.

### 2026-08-15 — rsi_reversion v1 (1h)

- **Dataset:** dev, `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** none (Fix 1)
- **Sizing:** risk 1%
- **Net return after costs:** −32.9062%
- **Profit factor / max drawdown / trades:** 0.65 / −34.40% / 215
- **Costs as share of gross profit:** n/a — gross is negative
- **Concentration (best 5):** 10.41%
- **Verdict:** fail — all criteria
- **Note:** The one unambiguous result in the set. **Gross return is −16.15%**
  — negative before a single fee is charged. This rule has no edge at all,
  rather than an edge that costs consume. Unlike the other two, more timeframe
  will not save it. Consider it closed unless the entry logic changes
  fundamentally.

### 8. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 9. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 10. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 11. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 12. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 13. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=0.37 1d=0.62 deadzone=0.15) — vetoed 2.30%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(bug from contributor warm-up)_

### 14. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +7.6832%
- **Profit factor / max drawdown / trades:** 1.07 / -11.57% / 185
- **Costs as share of gross profit:** 70% (costs 1814 vs gross profit 2582)
- **Concentration (best 5):** 9.01%
- **Verdict:** fail — Profit factor (1.07, needs > 1.30), Trades in the development period (185, needs >= 200), Total costs as a share of gross profit (70.25%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 15. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +7.6832%
- **Profit factor / max drawdown / trades:** 1.07 / -11.57% / 185
- **Costs as share of gross profit:** 70% (costs 1814 vs gross profit 2582)
- **Concentration (best 5):** 9.01%
- **Cost sweep:** 1.0x +7.68% (PF 1.07) | 1.5x -1.10% (PF 0.99) | 2.0x -9.16% (PF 0.92)
- **Verdict:** fail — Profit factor (1.07, needs > 1.30), Trades in the development period (185, needs >= 200), Total costs as a share of gross profit (70.25%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 16. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +7.6832%
- **Profit factor / max drawdown / trades:** 1.07 / -11.57% / 185
- **Costs as share of gross profit:** 70% (costs 1814 vs gross profit 2582)
- **Concentration (best 5):** 9.01%
- **Cost sweep:** 1.0x +7.68% (PF 1.07) | 1.5x -1.10% (PF 0.99) | 2.0x -9.16% (PF 0.92)
- **Verdict:** fail — Profit factor (1.07, needs > 1.30), Trades in the development period (185, needs >= 200), Total costs as a share of gross profit (70.25%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 17. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +7.6832%
- **Profit factor / max drawdown / trades:** 1.07 / -11.57% / 185
- **Costs as share of gross profit:** 70% (costs 1814 vs gross profit 2582)
- **Concentration (best 5):** 9.01%
- **Cost sweep:** 1.0x +7.68% (PF 1.07) | 1.5x -1.10% (PF 0.99) | 2.0x -9.16% (PF 0.92)
- **Verdict:** fail — Profit factor (1.07, needs > 1.30), Trades in the development period (185, needs >= 200), Total costs as a share of gross profit (70.25%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 18. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +13.3155%
- **Profit factor / max drawdown / trades:** 1.12 / -10.42% / 185
- **Costs as share of gross profit:** 49% (costs 1303 vs gross profit 2635)
- **Concentration (best 5):** 9.00%
- **Cost sweep:** 1.0x +13.32% (PF 1.12) | 1.5x +6.77% (PF 1.06) | 2.0x +0.60% (PF 1.01)
- **Verdict:** fail — Profit factor (1.12, needs > 1.30), Trades in the development period (185, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 19. 2026-08-15 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -99.6315%
- **Profit factor / max drawdown / trades:** 0.42 / -99.63% / 8,619
- **Costs as share of gross profit:** 828% (costs 11332 vs gross profit 1369)
- **Concentration (best 5):** 5.73%
- **Verdict:** fail — Net return after costs (-99.63%, needs > 0.00%), Profit factor (0.42, needs > 1.30), Max drawdown (99.63%, needs < 20.00%), Total costs as a share of gross profit (827.77%, needs < 50.00%), Longest losing streak (72, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 20. 2026-08-15 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -99.6315%
- **Profit factor / max drawdown / trades:** 0.42 / -99.63% / 8,619
- **Costs as share of gross profit:** 828% (costs 11332 vs gross profit 1369)
- **Concentration (best 5):** 5.73%
- **Cost sweep:** 1.0x -99.63% (PF 0.42) | 1.5x -99.98% (PF 0.30) | 2.0x -100.00% (PF 0.23)
- **Verdict:** fail — Net return after costs (-99.63%, needs > 0.00%), Profit factor (0.42, needs > 1.30), Max drawdown (99.63%, needs < 20.00%), Total costs as a share of gross profit (827.77%, needs < 50.00%), Longest losing streak (72, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 21. 2026-08-15 — ema_crossover v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -72.9283%
- **Profit factor / max drawdown / trades:** 0.61 / -73.85% / 1,794
- **Costs as share of gross profit:** n/a — gross is not positive (costs 6279)
- **Concentration (best 5):** 4.51%
- **Verdict:** fail — Net return after costs (-72.93%, needs > 0.00%), Profit factor (0.61, needs > 1.30), Max drawdown (73.85%, needs < 20.00%), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 22. 2026-08-15 — ema_crossover v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -72.9283%
- **Profit factor / max drawdown / trades:** 0.61 / -73.85% / 1,794
- **Costs as share of gross profit:** n/a — gross is not positive (costs 6279)
- **Concentration (best 5):** 4.51%
- **Cost sweep:** 1.0x -72.93% (PF 0.61) | 1.5x -85.55% (PF 0.49) | 2.0x -92.28% (PF 0.39)
- **Verdict:** fail — Net return after costs (-72.93%, needs > 0.00%), Profit factor (0.61, needs > 1.30), Max drawdown (73.85%, needs < 20.00%), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 23. 2026-08-15 — ema_crossover v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 1.44%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -6.6087%
- **Profit factor / max drawdown / trades:** 0.96 / -26.27% / 585
- **Costs as share of gross profit:** 122% (costs 3659 vs gross profit 2998)
- **Concentration (best 5):** 5.52%
- **Verdict:** fail — Net return after costs (-6.61%, needs > 0.00%), Profit factor (0.96, needs > 1.30), Max drawdown (26.27%, needs < 20.00%), Total costs as a share of gross profit (122.05%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 24. 2026-08-15 — ema_crossover v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 1.44%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -6.6087%
- **Profit factor / max drawdown / trades:** 0.96 / -26.27% / 585
- **Costs as share of gross profit:** 122% (costs 3659 vs gross profit 2998)
- **Concentration (best 5):** 5.52%
- **Cost sweep:** 1.0x -6.61% (PF 0.96) | 1.5x -23.80% (PF 0.86) | 2.0x -37.82% (PF 0.77)
- **Verdict:** fail — Net return after costs (-6.61%, needs > 0.00%), Profit factor (0.96, needs > 1.30), Max drawdown (26.27%, needs < 20.00%), Total costs as a share of gross profit (122.05%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 25. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +13.3155%
- **Profit factor / max drawdown / trades:** 1.12 / -10.42% / 185
- **Costs as share of gross profit:** 49% (costs 1303 vs gross profit 2635)
- **Concentration (best 5):** 9.00%
- **Verdict:** fail — Profit factor (1.12, needs > 1.30), Trades in the development period (185, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 26. 2026-08-15 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.97%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +13.3155%
- **Profit factor / max drawdown / trades:** 1.12 / -10.42% / 185
- **Costs as share of gross profit:** 49% (costs 1303 vs gross profit 2635)
- **Concentration (best 5):** 9.00%
- **Cost sweep:** 1.0x +13.32% (PF 1.12) | 1.5x +6.77% (PF 1.06) | 2.0x +0.60% (PF 1.01)
- **Verdict:** fail — Profit factor (1.12, needs > 1.30), Trades in the development period (185, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 27. 2026-08-15 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.89%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 28. 2026-08-15 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.89%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 29. 2026-08-15 — rsi_reversion v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.00%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -91.4371%
- **Profit factor / max drawdown / trades:** 0.23 / -91.44% / 3,692
- **Costs as share of gross profit:** 1771% (costs 9691 vs gross profit 547)
- **Concentration (best 5):** 8.10%
- **Verdict:** fail — Net return after costs (-91.44%, needs > 0.00%), Profit factor (0.23, needs > 1.30), Max drawdown (91.44%, needs < 20.00%), Total costs as a share of gross profit (1771.28%, needs < 50.00%), Longest losing streak (46, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 30. 2026-08-15 — rsi_reversion v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.00%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -91.4371%
- **Profit factor / max drawdown / trades:** 0.23 / -91.44% / 3,692
- **Costs as share of gross profit:** 1771% (costs 9691 vs gross profit 547)
- **Concentration (best 5):** 8.10%
- **Cost sweep:** 1.0x -91.44% (PF 0.23) | 1.5x -97.65% (PF 0.12) | 2.0x -99.35% (PF 0.07)
- **Verdict:** fail — Net return after costs (-91.44%, needs > 0.00%), Profit factor (0.23, needs > 1.30), Max drawdown (91.44%, needs < 20.00%), Total costs as a share of gross profit (1771.28%, needs < 50.00%), Longest losing streak (46, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 31. 2026-08-15 — rsi_reversion v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 0.85%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -31.1800%
- **Profit factor / max drawdown / trades:** 0.57 / -31.50% / 493
- **Costs as share of gross profit:** n/a — gross is not positive (costs 2824)
- **Concentration (best 5):** 10.31%
- **Verdict:** fail — Net return after costs (-31.18%, needs > 0.00%), Profit factor (0.57, needs > 1.30), Max drawdown (31.50%, needs < 20.00%), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 32. 2026-08-15 — rsi_reversion v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 0.85%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -31.1800%
- **Profit factor / max drawdown / trades:** 0.57 / -31.50% / 493
- **Costs as share of gross profit:** n/a — gross is not positive (costs 2824)
- **Concentration (best 5):** 10.31%
- **Cost sweep:** 1.0x -31.18% (PF 0.57) | 1.5x -42.09% (PF 0.44) | 2.0x -51.26% (PF 0.34)
- **Verdict:** fail — Net return after costs (-31.18%, needs > 0.00%), Profit factor (0.57, needs > 1.30), Max drawdown (31.50%, needs < 20.00%), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 33. 2026-08-15 — rsi_reversion v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 0.92%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -15.0519%
- **Profit factor / max drawdown / trades:** 0.55 / -17.26% / 107
- **Costs as share of gross profit:** n/a — gross is not positive (costs 698)
- **Concentration (best 5):** 32.42%
- **Verdict:** fail — Net return after costs (-15.05%, needs > 0.00%), Profit factor (0.55, needs > 1.30), Trades in the development period (107, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 34. 2026-08-15 — rsi_reversion v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 0.92%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -15.0519%
- **Profit factor / max drawdown / trades:** 0.55 / -17.26% / 107
- **Costs as share of gross profit:** n/a — gross is not positive (costs 698)
- **Concentration (best 5):** 32.42%
- **Cost sweep:** 1.0x -15.05% (PF 0.55) | 1.5x -18.16% (PF 0.48) | 2.0x -21.16% (PF 0.42)
- **Verdict:** fail — Net return after costs (-15.05%, needs > 0.00%), Profit factor (0.55, needs > 1.30), Trades in the development period (107, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 35. 2026-08-15 — rsi_reversion v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 1.32%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -0.9770%
- **Profit factor / max drawdown / trades:** 0.77 / -2.89% / 9
- **Costs as share of gross profit:** n/a — gross is not positive (costs 62)
- **Concentration (best 5):** 100.00%
- **Verdict:** fail — Net return after costs (-0.98%, needs > 0.00%), Profit factor (0.77, needs > 1.30), Trades in the development period (9, needs >= 200), Total costs as a share of gross profit (not measurable), Concentration: profit from the best 5 trades (100.00%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 36. 2026-08-15 — rsi_reversion v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 1.32%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -0.9770%
- **Profit factor / max drawdown / trades:** 0.77 / -2.89% / 9
- **Costs as share of gross profit:** n/a — gross is not positive (costs 62)
- **Concentration (best 5):** 100.00%
- **Cost sweep:** 1.0x -0.98% (PF 0.77) | 1.5x -1.29% (PF 0.70) | 2.0x -1.60% (PF 0.65)
- **Verdict:** fail — Net return after costs (-0.98%, needs > 0.00%), Profit factor (0.77, needs > 1.30), Trades in the development period (9, needs >= 200), Total costs as a share of gross profit (not measurable), Concentration: profit from the best 5 trades (100.00%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 37. 2026-08-15 — rsi_reversion v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.16%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 38. 2026-08-15 — rsi_reversion v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.16%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 39. 2026-08-15 — trend_pullback v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 0.85%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -98.3717%
- **Profit factor / max drawdown / trades:** 0.38 / -98.37% / 6,176
- **Costs as share of gross profit:** 1955% (costs 10367 vs gross profit 530)
- **Concentration (best 5):** 6.27%
- **Verdict:** fail — Net return after costs (-98.37%, needs > 0.00%), Profit factor (0.38, needs > 1.30), Max drawdown (98.37%, needs < 20.00%), Total costs as a share of gross profit (1955.45%, needs < 50.00%), Longest losing streak (32, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 40. 2026-08-15 — trend_pullback v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 0.85%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -98.3717%
- **Profit factor / max drawdown / trades:** 0.38 / -98.37% / 6,176
- **Costs as share of gross profit:** 1955% (costs 10367 vs gross profit 530)
- **Concentration (best 5):** 6.27%
- **Cost sweep:** 1.0x -98.37% (PF 0.38) | 1.5x -99.81% (PF 0.26) | 2.0x -99.98% (PF 0.19)
- **Verdict:** fail — Net return after costs (-98.37%, needs > 0.00%), Profit factor (0.38, needs > 1.30), Max drawdown (98.37%, needs < 20.00%), Total costs as a share of gross profit (1955.45%, needs < 50.00%), Longest losing streak (32, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 41. 2026-08-15 — trend_pullback v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 0.91%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -61.0428%
- **Profit factor / max drawdown / trades:** 0.66 / -62.23% / 1,609
- **Costs as share of gross profit:** 844% (costs 6925 vs gross profit 821)
- **Concentration (best 5):** 5.52%
- **Verdict:** fail — Net return after costs (-61.04%, needs > 0.00%), Profit factor (0.66, needs > 1.30), Max drawdown (62.23%, needs < 20.00%), Total costs as a share of gross profit (843.84%, needs < 50.00%), Longest losing streak (17, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 42. 2026-08-15 — trend_pullback v1 (5m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 5m
- **Filter:** ema_rsi_mtf v1 (15m=0.20 1h=0.30 4h=0.50 deadzone=0.15) — vetoed 0.91%, not-ready 10.98%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -61.0428%
- **Profit factor / max drawdown / trades:** 0.66 / -62.23% / 1,609
- **Costs as share of gross profit:** 844% (costs 6925 vs gross profit 821)
- **Concentration (best 5):** 5.52%
- **Cost sweep:** 1.0x -61.04% (PF 0.66) | 1.5x -77.82% (PF 0.53) | 2.0x -87.37% (PF 0.42)
- **Verdict:** fail — Net return after costs (-61.04%, needs > 0.00%), Profit factor (0.66, needs > 1.30), Max drawdown (62.23%, needs < 20.00%), Total costs as a share of gross profit (843.84%, needs < 50.00%), Longest losing streak (17, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 43. 2026-08-15 — trend_pullback v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 0.83%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -39.0255%
- **Profit factor / max drawdown / trades:** 0.71 / -44.30% / 592
- **Costs as share of gross profit:** n/a — gross is not positive (costs 3105)
- **Concentration (best 5):** 9.17%
- **Verdict:** fail — Net return after costs (-39.03%, needs > 0.00%), Profit factor (0.71, needs > 1.30), Max drawdown (44.30%, needs < 20.00%), Total costs as a share of gross profit (not measurable), Longest losing streak (21, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 44. 2026-08-15 — trend_pullback v1 (15m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 15m
- **Filter:** ema_rsi_mtf v1 (1h=0.37 4h=0.62 deadzone=0.15) — vetoed 0.83%, not-ready 10.11%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -39.0255%
- **Profit factor / max drawdown / trades:** 0.71 / -44.30% / 592
- **Costs as share of gross profit:** n/a — gross is not positive (costs 3105)
- **Concentration (best 5):** 9.17%
- **Cost sweep:** 1.0x -39.03% (PF 0.71) | 1.5x -50.42% (PF 0.63) | 2.0x -59.67% (PF 0.55)
- **Verdict:** fail — Net return after costs (-39.03%, needs > 0.00%), Profit factor (0.71, needs > 1.30), Max drawdown (44.30%, needs < 20.00%), Total costs as a share of gross profit (not measurable), Longest losing streak (21, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 45. 2026-08-15 — trend_pullback v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.51%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -15.2937%
- **Profit factor / max drawdown / trades:** 0.82 / -24.53% / 149
- **Costs as share of gross profit:** n/a — gross is not positive (costs 941)
- **Concentration (best 5):** 18.52%
- **Verdict:** fail — Net return after costs (-15.29%, needs > 0.00%), Profit factor (0.82, needs > 1.30), Max drawdown (24.53%, needs < 20.00%), Trades in the development period (149, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 46. 2026-08-15 — trend_pullback v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.51%, not-ready 0.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -15.2937%
- **Profit factor / max drawdown / trades:** 0.82 / -24.53% / 149
- **Costs as share of gross profit:** n/a — gross is not positive (costs 941)
- **Concentration (best 5):** 18.52%
- **Cost sweep:** 1.0x -15.29% (PF 0.82) | 1.5x -19.45% (PF 0.77) | 2.0x -23.40% (PF 0.73)
- **Verdict:** fail — Net return after costs (-15.29%, needs > 0.00%), Profit factor (0.82, needs > 1.30), Max drawdown (24.53%, needs < 20.00%), Trades in the development period (149, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 47. 2026-08-15 — trend_pullback v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.44%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 48. 2026-08-15 — trend_pullback v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** ema_rsi_mtf v1 (1d=1.00 deadzone=0.15) — vetoed 1.44%, not-ready 100.00%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** inf / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF inf) | 1.5x +0.00% (PF inf) | 2.0x +0.00% (PF inf)
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 49. 2026-08-15 — mtf_alignment v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity
- **Net return after costs:** -99.9995%
- **Profit factor / max drawdown / trades:** 0.33 / -100.00% / 12,468
- **Costs as share of gross profit:** n/a — gross is not positive (costs 100)
- **Concentration (best 5):** 9.38%
- **Cost sweep:** 1.0x -100.00% (PF 0.33) | 1.5x -100.00% (PF 0.23) | 2.0x -100.00% (PF 0.17)
- **Verdict:** fail — Net return after costs (-100.00%, needs > 0.00%), Profit factor (0.33, needs > 1.30), Max drawdown (100.00%, needs < 20.00%), Total costs as a share of gross profit (not measurable), Longest losing streak (82, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 50. 2026-08-15 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** -99.9732%
- **Profit factor / max drawdown / trades:** 0.31 / -99.97% / 8,662
- **Costs as share of gross profit:** 1027% (costs 111 vs gross profit 11)
- **Concentration (best 5):** 8.15%
- **Cost sweep:** 1.0x -99.97% (PF 0.31) | 1.5x -100.00% (PF 0.21) | 2.0x -100.00% (PF 0.14)
- **Verdict:** fail — Net return after costs (-99.97%, needs > 0.00%), Profit factor (0.31, needs > 1.30), Max drawdown (99.97%, needs < 20.00%), Total costs as a share of gross profit (1026.52%, needs < 50.00%), Longest losing streak (73, needs <= 15)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 51. 2026-08-15 — mtf_alignment v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** n/a (no trades) / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF n/a (no trades)) | 1.5x +0.00% (PF n/a (no trades)) | 2.0x +0.00% (PF n/a (no trades))
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 52. 2026-08-15 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** n/a (no trades) / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF n/a (no trades)) | 1.5x +0.00% (PF n/a (no trades)) | 2.0x +0.00% (PF n/a (no trades))
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 53. 2026-08-17 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 5% of equity
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** n/a (no trades) / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF n/a (no trades)) | 1.5x +0.00% (PF n/a (no trades)) | 2.0x +0.00% (PF n/a (no trades))
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 54. 2026-08-17 — ema_crossover v1 (1m)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1m
- **Filter:** ema_rsi_mtf v1 (5m=0.20 15m=0.30 1h=0.50 deadzone=0.15) — vetoed 1.47%, not-ready 11.32%
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** -87.2833%
- **Profit factor / max drawdown / trades:** 0.16 / -87.42% / 186
- **Costs as share of gross profit:** n/a — gross is not positive (costs 85)
- **Concentration (best 5):** 21.31%
- **Cost sweep:** 1.0x -87.28% (PF 0.16) | 1.5x -87.45% (PF 0.04) | 2.0x -87.57% (PF 0.01)
- **Verdict:** fail — Net return after costs (-87.28%, needs > 0.00%), Profit factor (0.16, needs > 1.30), Max drawdown (87.42%, needs < 20.00%), Trades in the development period (186, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 55. 2026-08-17 — ema_crossover v1 (1h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 1h
- **Filter:** ema_rsi_mtf v1 (4h=1.00 deadzone=0.15) — vetoed 0.95%, not-ready 0.00%
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +0.5891%
- **Profit factor / max drawdown / trades:** 1.25 / -1.36% / 4
- **Costs as share of gross profit:** 68% (costs 1 vs gross profit 2)
- **Concentration (best 5):** 100.00%
- **Cost sweep:** 1.0x +0.59% (PF 1.25) | 1.5x -0.04% (PF 0.99) | 2.0x -0.66% (PF 0.79)
- **Verdict:** fail — Profit factor (1.25, needs > 1.30), Trades in the development period (4, needs >= 200), Total costs as a share of gross profit (67.99%, needs < 50.00%), Concentration: profit from the best 5 trades (100.00%, needs < 50.00%)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 56. 2026-08-17 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +0.0000%
- **Profit factor / max drawdown / trades:** n/a (no trades) / 0.00% / 0
- **Costs as share of gross profit:** n/a — gross is not positive (costs 0)
- **Concentration (best 5):** 0.00%
- **Cost sweep:** 1.0x +0.00% (PF n/a (no trades)) | 1.5x +0.00% (PF n/a (no trades)) | 2.0x +0.00% (PF n/a (no trades))
- **Verdict:** fail — Net return after costs (0.00%, needs > 0.00%), Profit factor (not measurable), Trades in the development period (0, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 57. 2026-08-17 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +4.7849%
- **Profit factor / max drawdown / trades:** 1.13 / -10.38% / 69
- **Costs as share of gross profit:** 34% (costs 49 vs gross profit 144)
- **Concentration (best 5):** 23.27%
- **Cost sweep:** 1.0x +4.78% (PF 1.13) | 1.5x -0.46% (PF 0.99) | 2.0x -0.67% (PF 0.98)
- **Verdict:** fail — Profit factor (1.13, needs > 1.30), Trades in the development period (69, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 58. 2026-08-21 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** defaults, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +4.7849%
- **Profit factor / max drawdown / trades:** 1.13 / -10.38% / 69
- **Costs as share of gross profit:** 34% (costs 49 vs gross profit 144)
- **Concentration (best 5):** 23.27%
- **Filter comparison:** unfiltered +4.78% (69 trades) | filtered +4.78% (69 trades)
- **Cost sweep:** 1.0x +4.78% (PF 1.13) | 1.5x -0.46% (PF 0.99) | 2.0x -0.67% (PF 0.98)
- **Verdict:** fail — Profit factor (1.13, needs > 1.30), Trades in the development period (69, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 59. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** trailing_atr_mult=2 trailing_activate_atr=1, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** -2.6757%
- **Profit factor / max drawdown / trades:** 0.92 / -8.07% / 69
- **Costs as share of gross profit:** n/a — gross is not positive (costs 51)
- **Concentration (best 5):** 29.68%
- **Cost sweep:** 1.0x -2.68% (PF 0.92) | 1.5x -2.83% (PF 0.91) | 2.0x -3.76% (PF 0.89)
- **Verdict:** fail — Net return after costs (-2.68%, needs > 0.00%), Profit factor (0.92, needs > 1.30), Trades in the development period (69, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 60. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** trailing_atr_mult=4 trailing_activate_atr=2, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +5.1403%
- **Profit factor / max drawdown / trades:** 1.14 / -10.25% / 69
- **Costs as share of gross profit:** 32% (costs 49 vs gross profit 152)
- **Concentration (best 5):** 23.27%
- **Cost sweep:** 1.0x +5.14% (PF 1.14) | 1.5x -0.10% (PF 1.00) | 2.0x -0.31% (PF 0.99)
- **Verdict:** fail — Profit factor (1.14, needs > 1.30), Trades in the development period (69, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 61. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** max_holding_bars=12, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** -4.8279%
- **Profit factor / max drawdown / trades:** 0.86 / -10.31% / 70
- **Costs as share of gross profit:** n/a — gross is not positive (costs 50)
- **Concentration (best 5):** 29.86%
- **Cost sweep:** 1.0x -4.83% (PF 0.86) | 1.5x -4.74% (PF 0.86) | 2.0x -5.87% (PF 0.83)
- **Verdict:** fail — Net return after costs (-4.83%, needs > 0.00%), Profit factor (0.86, needs > 1.30), Trades in the development period (70, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 62. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** target_atr_mult=2, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** -3.9432%
- **Profit factor / max drawdown / trades:** 0.88 / -11.21% / 66
- **Costs as share of gross profit:** n/a — gross is not positive (costs 48)
- **Concentration (best 5):** 21.40%
- **Cost sweep:** 1.0x -3.94% (PF 0.88) | 1.5x -4.57% (PF 0.86) | 2.0x -5.64% (PF 0.83)
- **Verdict:** fail — Net return after costs (-3.94%, needs > 0.00%), Profit factor (0.88, needs > 1.30), Trades in the development period (66, needs >= 200), Total costs as a share of gross profit (not measurable)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 63. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** target_atr_mult=2.5, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Net return after costs:** +6.3450%
- **Profit factor / max drawdown / trades:** 1.19 / -9.49% / 70
- **Costs as share of gross profit:** 28% (costs 50 vs gross profit 177)
- **Concentration (best 5):** 20.19%
- **Cost sweep:** 1.0x +6.34% (PF 1.19) | 1.5x -0.23% (PF 0.99) | 2.0x -1.45% (PF 0.96)
- **Verdict:** fail — Profit factor (1.19, needs > 1.30), Trades in the development period (70, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_

### 64. 2026-08-22 — ema_crossover v1 (4h)

- **Dataset:** dev (2023-01-01 .. 2024-12-31), `--allow-gaps=skip`
- **Parameters:** target_atr_mult=2.5, timeframe 4h
- **Filter:** none (unfiltered)
- **Sizing:** risk 1% of equity, 20x notional limit
- **Neighbourhood:**
  |  | target_atr_mult | net return | trades | PF |
  |---|---|---|---|---|
  | base | 2.5 | +6.34% | 70 | 1.1857 |
  | target_atr_mult-1 | 2.25 | -1.42% | 66 | 0.9570 |
  | target_atr_mult+1 | 2.75 | -0.32% | 67 | 0.9910 |
- **Net return after costs:** +6.3450%
- **Profit factor / max drawdown / trades:** 1.19 / -9.49% / 70
- **Costs as share of gross profit:** 28% (costs 50 vs gross profit 177)
- **Concentration (best 5):** 20.19%
- **Verdict:** fail — Profit factor (1.19, needs > 1.30), Trades in the development period (70, needs >= 200)
- **Note:** _(to fill in — what you learned, not what you hoped)_
