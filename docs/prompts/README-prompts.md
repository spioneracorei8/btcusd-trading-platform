# Prompt files — index

Every prompt written for this project, in the order they were used.

Suggested layout:

```
CLAUDE.md                        ← repo root
docs/experiments.md              ← the evaluation log
docs/prompts/                    ← everything else below
```

`deployment.md` and `phase-07.md` onward go in `docs/prompts/` too.

---

## Status

| File | Phase | Status |
|---|---|---|
| `CLAUDE.md` | — | Live. Read before every task. Root of the repo, not `docs/prompts/` |
| `phase-01.md` | 1 | Done — skeleton, config, Docker, schema |
| `phase-02.md` | 2 | Done — market data, backfill, gap detection |
| `phase-02-fixes.md` | 2 | Done — migrate service, status endpoint, lifecycle state |
| `phase-03.md` | 3 | Done — EMA, RSI, ATR, VWAP |
| `phase-04.md` | 4 | Done — backtest engine |
| `phase-05.md` | 5 | Done — multi-timeframe trend filter |
| `phase-06.md` | 6 | Done — strategy engine + three starter strategies |
| `phase-06-fixes.md` | 6 | Done — filter timeframe selection, cost sweep reset, volatility split |
| `phase-06-fixes-2.md` | 6 | Done — base-aware contributors, earliest_open_time, auto experiment log |
| `phase-06-fixes-3.md` | 6 | Done — maker fees, limit fills, sweep runner |
| `phase-06-fixes-4.md` | 6 | Done — 4h base, duplicate log entries |
| `phase-06-fixes-5.md` | 6 | Done — spread-based costs, mtf_alignment strategy |
| `phase-06-fixes-6.md` | 6 | Done — parameter flags, neighbourhood, trailing stop, time exit |
| `deployment.md` | — | Done — VPS setup, backups, disk checks, 48-hour test |
| `phase-07.md` | 7 | **Next** — notification + outcome tracking |
| `experiments.md` | — | Live. Goes in `docs/`, appended automatically by every run |

Phase 08 (REST/WS API) and Phase 09 (React Native app) are not written yet.

---

## What each phase produced

**1–2 — data you can trust.** Candles stored, gaps detected and recorded, reconnects survived. The 48-hour test against real Binance passed on 2026-08-17: disconnect after 48h54m, 2.4 seconds down, no gap.

**3 — numbers that are correct.** Four indicators verified against TA-Lib, warm-up enforced at 5× period, incremental and batch identical.

**4 — an honest measuring instrument.** Fills at next bar's open, stop assumed before target, gaps halt the run by default, costs never optional. Deliberately built before any strategy existed.

**5 — a trend filter that cannot see the future.** The alignment test was written before the implementation and fails on a naive one.

**6 — strategies, and the answer.** Four strategies, sixty-four evaluations. No strategy has met `docs/acceptance-criteria.md`.

---

## Where the project stands

Every rule tried loses its edge at 1.5× the assumed cost. The best-looking result — `ema_crossover` on 4h with `target_atr_mult=2.5`, +6.35% and PF 1.19 — was shown by `--neighbourhood` to be a spike: both neighbours unprofitable. It should not be used.

Best defensible configuration: `ema_crossover`, 4h, defaults. +4.78% over the development set, PF 1.13, edge gone at 1.5×.

The holdout set (2025+) has never been run. `docs/holdout-log.md` is empty. That is correct — nothing has earned it.

**Three facts that shaped everything:**

- Trade frequency dominates. The same rule went from −99.97% at 1m to +25.60% at 1h purely on how often it traded.
- The 100 USD account cannot trade this instrument at 0.01 lot minimum without either refusing most entries or taking leverage that a normal losing streak would wipe out.
- Prices are Binance BTCUSDT; the intended venue is IUX BTCUSD CFD. The cost model matches. The price series does not, and cannot without collecting from the venue.

---

## Conventions that held throughout

Worth keeping if the project is extended.

- **No orders, ever.** Every prompt restates it; `architecture_test.go` now enforces it.
- **Live and backtest share one code path.** No `if backtesting` branch — also enforced by test.
- **Look-ahead is made structurally impossible**, not forbidden by convention. `BarContext` has no field that could expose the future.
- **Pessimistic assumptions where the data is silent.** Stop before target, trail triggers before it extends.
- **Zero and null are different.** Not-ready indicators return NaN, absent measurements return null.
- **Every run is logged**, including the disappointing ones. The count of runs is part of the result.
- **Neighbourhood before belief.** A value whose neighbours collapse is fitted to noise.

---

## If the strategy search resumes

The unexplored directions, in rough order of promise:

1. A venue with materially tighter spreads — the 1.5× cliff is a spread problem before it is a strategy problem
2. Price data from the actual trading venue, which would remove the standing caveat on every result
3. Strategies with a genuinely different edge source — order flow, volatility regime, funding rates — rather than more variations on moving averages
4. More capital, which reopens the higher timeframes where the cost ratio is survivable

What is not promising: further parameter tuning of the existing four rules. Gross return before costs is +7.22% over two years at best. Tuning adjusts a thin edge; it does not create one.
