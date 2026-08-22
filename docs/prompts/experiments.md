# Experiment log

Appended after every evaluation run. Not automated: writing the line is the
point, because the act of recording a result you dislike is the discipline
this file exists to impose.

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

One entry per run, newest at the bottom.

```
### <date> — <strategy> <version>

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
