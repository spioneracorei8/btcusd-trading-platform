# Phase 04 — Backtest Engine

> Read `CLAUDE.md` fully before starting.
> Phases 01–03 are merged. Candles cover 2023-01-01 onward across 1m/5m/15m/1h. Indicators are verified.
> **No strategy logic in this phase.** The engine must be able to run a strategy without any real strategy existing yet.

This phase builds the measuring instrument. Everything after it will be judged by numbers this code produces, so a flattering bug here is worse than a crash — a crash you find in an hour, an optimistic backtest you act on for months.

The engine is deliberately built before there is anything to measure. Resist the pull to sketch "just a simple strategy" to test it with. Use the fixtures described in §7 instead.

---

## Goal

A CLI that replays stored candles through a strategy interface, simulates fills with realistic costs, refuses to produce results over untrustworthy data, and emits a report of risk-adjusted performance.

---

## 1. The strategy interface

Create `internal/backtest` and `internal/strategy` (interface only — no implementations).

```go
// Strategy sees one bar at a time and may return an intent.
// It has no access to future bars, no access to the database, and no clock.
type Strategy interface {
    // OnBar is called once per closed candle, in chronological order.
    OnBar(ctx BarContext) []Intent

    // WarmupPeriod is the number of bars required before OnBar is called.
    WarmupPeriod() int

    Name() string
    Version() string
}
```

`BarContext` carries the current candle, the indicator `Snapshot` from Phase 03, and the current position state. It must **not** carry a slice of all candles, an index into a series, or anything else that would let a strategy peek forward. Make look-ahead structurally impossible rather than forbidden by convention — a rule can be broken by accident, a missing field cannot.

`Intent` expresses what the strategy wants (`EnterLong`, `EnterShort`, `Exit`, `SetStop`, `SetTarget`), never a fill. The engine decides what actually happens. A strategy that could set its own fill price could report any result it liked.

`Name()` and `Version()` are recorded in every report. When a result looks surprising six weeks from now, you will need to know exactly which code produced it.

---

## 2. Live and backtest share one path

Phase 06 will run strategies live. If live and backtest use different code, the backtest stops describing reality and you will not be told.

- `OnBar` is the only entry point in both modes. The difference is the source of candles: a database cursor versus the live stream.
- Indicators are driven by the same `indicator.Set` from Phase 03, fed candle-by-candle, exactly as live ingestion does.
- No `if backtesting {}` branch anywhere in strategy or indicator code. If you find yourself needing one, stop and explain why before writing it.

---

## 3. Data trust gate

Phase 02 records gaps. The engine must respect them, and this is not optional.

Before any run:

- Query `data_gaps` for unfilled rows overlapping the requested range and timeframe.
- **Halt by default** if any exist. Print the gap ranges and exit non-zero. Do not silently produce a number.
- `--allow-gaps=skip` continues but excludes affected regions: no bar inside a gap is evaluated, and any position open when a gap begins is force-closed at the last known close with a flag in the trade record.
- `--allow-gaps=ignore` runs straight through. Every report from such a run is stamped `DATA_INCOMPLETE` in its header and in the JSON output. There is no flag that produces a clean-looking report over dirty data.

**The March 2023 case.** BTCUSDT has an unfillable gap on 2023-03-24 from roughly 12:40 to 14:00 UTC — Binance halted spot trading during a matching-engine incident. This is not missing data; it is a period during which no order could have been placed at all. Treat known-permanent gaps as **untradeable windows**: force-close any open position at the last close before the halt, and do not allow entries until the market reopens. A backtest that trades through an exchange outage is reporting fills that could not have happened.

Add a test using this exact range.

---

## 4. Execution model

Realism matters more than sophistication here. Every simplification must be visible in the report rather than buried in the code.

**Fill timing.** A signal generated on the close of bar `t` fills at the **open of bar `t+1`**. Never at the close of `t` — that price is only knowable after the decision moment. This single rule is the most common source of backtests that cannot be reproduced live.

**Costs.** Read `FEE_TAKER_PCT` and `SLIPPAGE_TICKS` from config (defaults 0.05% and 1 tick, per Phase 01). Apply on entry and exit, both directions. Costs are never optional and there is no flag to disable them.

At 1m–5m scalping frequency these dominate: a round trip costs roughly 0.1% before slippage. A strategy targeting 0.3% per trade gives up a third of its edge to fees. The report must make this impossible to overlook — see §6.

**Stops and targets.** Within a bar, when both the stop and the target lie inside the bar's range, assume the **stop fills first**. This is pessimistic and it is deliberate: 1m bars do not tell you the path price took inside them, and the optimistic assumption is how backtests quietly inflate their own results. Record how often this ambiguity occurred; if it is frequent, the strategy's results depend on an assumption rather than on evidence.

**Position model.** One position at a time, long or short, no pyramiding, no partial fills. Fixed position size for now — position sizing arrives in Phase 06. Keep it simple and keep it honest.

**Spot versus futures.** `MARKET_TYPE` is spot for now. Do not implement funding rates, leverage, or liquidation. Do not allow shorts when `market_type` is spot — a spot backtest that shorts is fiction. Make this a hard error, not a warning.

---

## 5. Engine loop

```
load candles (chronological cursor, streamed — never load 1.5M bars into memory)
  → feed indicator.Set
  → skip bar if !Set.Ready()
  → skip bar if inside an excluded gap region
  → strategy.OnBar
  → queue intents for next bar's open
  → on next bar: apply fills, check stops/targets, update equity
  → record trade on close
```

Requirements:

- Stream with a cursor or keyset pagination. Loading three years of 1m candles into a slice is not acceptable and will not fit comfortably on a 4 GB VPS.
- Equity is tracked per bar, not just per trade — the equity curve is what drawdown is computed from.
- Prices and PnL use `decimal.Decimal` throughout (`CLAUDE.md` §4). Convert to float only when computing statistics for the report.
- The run must be deterministic: same inputs, same output, every time. If randomness is ever introduced, it takes an explicit seed recorded in the report.

---

## 6. Report

`cmd/backtest` writes both a human-readable summary and a JSON file (path via `--out`).

**Header — always shown, never suppressible:**
- strategy name and version, symbol, market type, timeframe
- date range, bars evaluated, bars skipped (warm-up versus gaps, counted separately)
- fee and slippage settings actually applied
- `DATA_INCOMPLETE` stamp when applicable

**Performance:**
- total return, and **total costs paid as a separate line** — a strategy where costs exceed gross profit must be visibly obvious, not something you compute yourself
- max drawdown (percent and absolute), and its date range
- Sharpe ratio, annualised, with the risk-free rate stated
- profit factor, win rate, trade count
- average win, average loss, largest win, largest loss
- average holding time
- longest losing streak
- count of bars where stop and target were both reachable (§4)

**Per-trade records** in the JSON: entry and exit time and price, direction, PnL gross and net, costs, exit reason (`target`, `stop`, `strategy_exit`, `gap_forced`), and any flags.

Lead the summary with **net return after costs**. Gross return may appear, but never first and never alone.

---

## 7. Testing without a strategy

Verify the engine with fixtures whose correct answers are known in advance:

- **Always-flat strategy** → zero trades, zero costs, flat equity. Catches phantom fills.
- **Buy-and-hold** → net return equals close-to-close change minus exactly one round trip of costs. Compute the expected number by hand and assert on it.
- **Alternating entry/exit every N bars** → trade count is exactly predictable; total costs equal trades × round-trip cost. Catches cost accounting errors.
- **Guaranteed-loss strategy** (enters and exits immediately, every bar) → net return must be negative and equal to accumulated costs precisely. This is the strongest single test of the cost model.
- **Look-ahead probe** — a strategy that attempts to read future data through `BarContext`. This must not compile. If it does, §1 is not satisfied and the interface needs to change.

Additional required tests:

- Fill occurs at next bar's open, never current close
- Stop-before-target resolution when both are inside one bar
- Gap handling in all three modes over the 2023-03-24 range
- Short entry rejected under spot market type
- Determinism: same inputs run twice produce byte-identical JSON
- Equity curve length equals evaluated bar count

No test may hit the network. Database-backed tests use a real Postgres with a seeded fixture range.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `cmd/backtest` runs over a full year of 1m data without exceeding 500 MB RSS
- [ ] Buy-and-hold over a known range matches a hand-computed figure exactly
- [ ] Guaranteed-loss strategy returns precisely the accumulated cost total
- [ ] Run halts by default when unfilled gaps are present in range
- [ ] 2023-03-24 outage window is handled as untradeable in `skip` mode
- [ ] Two identical runs produce byte-identical JSON
- [ ] Report leads with net return and shows total costs as its own line
- [ ] No `if backtesting` branch exists in strategy or indicator code
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Any real trading strategy
- Parameter optimisation, walk-forward analysis, Monte Carlo
- Multi-symbol or portfolio backtesting
- Position sizing and risk management (Phase 06)
- Futures mechanics of any kind
- Charts, plots, web UI
- Performance optimisation beyond the memory constraint above

---

## How to start

Summarise your plan as a numbered list and wait for approval. If any requirement here conflicts with what Phases 01–03 actually built, say so before writing code rather than working around it.
