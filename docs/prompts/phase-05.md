# Phase 05 — Multi-Timeframe Trend Filter

> Read `CLAUDE.md` fully before starting.
> Phases 01–04 are merged. The backtest engine exists and is verified against known-answer fixtures.
> **No trading strategy in this phase.** A filter decides what is *allowed*, never what is *taken*.

A trend filter is a veto, not a signal. Phase 06 will decide when to enter; this phase decides when entering is permitted at all. Keeping those separate is what makes it possible to measure whether the filter helps — run the same strategy with and without it and compare.

---

## Goal

A component that consumes indicator snapshots from several timeframes and reports a directional bias plus a confidence value, usable identically in live and backtest, with **no look-ahead across timeframes**.

---

## 1. The timeframe alignment problem

This is the hazard that makes this phase harder than it looks, and it has ruined more backtests than any other single mistake in multi-timeframe work.

At 14:23:00, the 1m candle for 14:22 has closed. The 1h candle for 14:00 has **not**. A naive implementation joins on timestamp and hands the strategy the completed 14:00–15:00 hourly bar — which contains the next 37 minutes of price action. The backtest then looks excellent and cannot be reproduced live, because live has no such data.

**Required rule:** when evaluating a bar at time `t` on the base timeframe, a higher timeframe may only contribute candles whose `close_time <= t`. The most recent usable 1h candle at 14:23 is the one covering 13:00–14:00.

Implementation requirements:

- Higher-timeframe state advances only when a higher-timeframe candle actually closes. Between closes, the filter uses the last closed value — it does not interpolate, and it does not peek at the forming candle.
- `TrendState` carries, for each timeframe, the `close_time` of the candle its values came from. Every value must be traceable to a bar that had definitively closed.
- Add an assertion in debug builds: any higher-timeframe contribution whose `close_time > t` is a programming error and must panic loudly in tests rather than degrade quietly.

**Required test.** Construct a case where the 1h candle containing `t` moves sharply in one direction while the previously-closed 1h candle pointed the other way. Assert the filter reports the *previous* candle's direction. This test fails on the naive implementation and passes only on a correct one — write it before the implementation.

---

## 2. Interface

Create `internal/trend`.

```go
type Filter interface {
    // OnBar is called once per closed base-timeframe candle.
    OnBar(ctx BarContext) TrendState

    WarmupPeriod() int
    Name() string
    Version() string
}

type TrendState struct {
    Bias       Direction // Bullish, Bearish, Neutral
    Confidence float64   // 0.0–1.0
    PerTF      map[Timeframe]TFState
    Ready      bool
}
```

Rules:

- The filter receives the same `BarContext` shape as `Strategy` (Phase 04 §1). It cannot reach the database, cannot see future bars, and has no clock. Look-ahead must remain structurally impossible.
- `Ready` is false until every contributing timeframe has completed its own indicator warm-up. Compute the requirement in base-timeframe bars: a 1h EMA(200) with a 5× warm-up needs 1000 hourly closes, which is roughly 60,000 1m bars. Do not underestimate this — it is easy to think the filter is ready long before it is.
- While `Ready` is false, `Bias` is `Neutral` and `Confidence` is 0. Phase 06 must treat that as "no entries permitted", not as "no opinion, proceed freely".
- `Version()` changes whenever the scoring logic changes. Backtest reports record it.

---

## 3. Timeframe roles

Configuration, not hardcoded values. Defaults for the scalping setup:

| Timeframe | Role |
|---|---|
| 1m | Execution — the base timeframe the engine iterates |
| 5m | Short-term structure |
| 15m | Intermediate confirmation |
| 1h | Dominant trend — the strongest veto |

The base timeframe is where `OnBar` fires. Higher timeframes only contribute bias. A filter must never emit entries on its own.

---

## 4. Scoring

Keep this simple and legible. A complicated score that nobody can reason about is worse than a crude one whose failures are obvious.

Per contributing timeframe, derive a directional reading from the Phase 03 indicators — for example price relative to EMA, EMA slope sign, RSI above or below 50, and ATR as a volatility context. Combine into a per-timeframe score in `[-1, +1]`.

Aggregate with configurable weights, defaulting to 1h weighted most heavily. Map the weighted sum to `Bias` with a **neutral dead zone** — a small band around zero that reports `Neutral` rather than flipping on noise. Without a dead zone the filter oscillates every few bars in chop and becomes worse than no filter at all.

`Confidence` is the absolute value of the weighted score, normalised to `[0, 1]`. Phase 06 may use it to size or to gate; this phase does not decide that.

**Do not tune the weights or thresholds in this phase.** Ship documented defaults. Tuning against the same data used to evaluate is how a filter is fitted to the past — that discipline belongs in Phase 06 with a proper split, and going near it here contaminates the only clean data you have.

---

## 5. Efficient higher-timeframe access

The engine streams base-timeframe candles. Higher-timeframe candles must be available without loading them all into memory or issuing a query per bar.

- Maintain one cursor per higher timeframe, advanced in lockstep with the base cursor. Both are already ordered by `open_time`, so this is a merge, not a lookup.
- A higher-timeframe candle becomes visible only once its `close_time <= t`, per §1.
- Do not aggregate 1m into higher timeframes locally. Phase 02 stores each timeframe from Binance directly; use those rows. Local aggregation reintroduces boundary and gap-handling bugs that were deliberately avoided.
- Memory ceiling for a full-year run stays within the Phase 04 budget of 500 MB RSS.

---

## 6. Gap interaction

Phase 04 §3 established gap handling. Extend it:

- A gap in **any** contributing timeframe makes the filter unreliable, not just a gap in the base timeframe. Higher-timeframe gaps matter more, since one missing 1h candle affects sixty base bars.
- During an excluded region, `TrendState.Ready` is false and `Bias` is `Neutral`.
- After a gap ends, higher-timeframe indicators are stale by however long the gap ran. Do not resume as if nothing happened: mark the filter not-ready until each affected timeframe has closed enough fresh candles to re-establish its own warm-up.
- The 2023-03-24 outage is the test case. Assert the filter reports not-ready across it and for the appropriate recovery window afterward.

---

## 7. Measuring whether it helps

The point of a filter is to improve results. That claim must be testable, so build the mechanism now — before there is a strategy whose numbers you are attached to.

- The backtest CLI gains `--trend-filter=<name>` and `--no-trend-filter`.
- Reports record the filter name, version, and configuration in the header alongside the strategy.
- Add a comparison mode that runs the same strategy twice, filtered and unfiltered, over the same range, and prints both result sets side by side with the deltas.
- Report `bars_vetoed` — how many entry opportunities the filter blocked. A filter that vetoes almost nothing is not doing anything; one that vetoes almost everything has too few surviving trades for its statistics to mean much. Both extremes need to be visible.

---

## 8. Tests

- Alignment: the §1 case, written before the implementation
- `Ready` is false until every timeframe has warmed up, computed in base-timeframe bars
- Dead zone: a score oscillating narrowly around zero yields stable `Neutral`, not alternating bias
- Gap: filter is not-ready during and after the 2023-03-24 outage for the correct recovery window
- Determinism: identical inputs produce identical `TrendState` sequences
- Weighting: a synthetic case where 1h and 5m disagree resolves in favour of 1h under default weights
- Every higher-timeframe value in `TrendState` traces to a candle with `close_time <= t`
- Filtered and unfiltered runs over a no-veto fixture produce identical trades — proving the filter is inert when it should be

No test may hit the network.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] The §1 alignment test exists and passes
- [ ] No higher-timeframe candle with `close_time > t` can reach the filter
- [ ] Warm-up is computed correctly in base-timeframe bars and documented
- [ ] Comparison mode runs and prints filtered versus unfiltered side by side
- [ ] `bars_vetoed` appears in the report
- [ ] A full-year 1m run stays within 500 MB RSS
- [ ] Weights and thresholds are documented defaults, not tuned against the data
- [ ] Filter emits no entries and takes no positions
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Any trading strategy or entry logic
- Position sizing, stops, targets (Phase 06)
- Parameter tuning or optimisation of any kind
- Market-structure detection (swing highs and lows, break of structure) — worth revisiting later, but it needs its own definitions and its own tests
- Additional indicators beyond the four from Phase 03
- Charts or visualisation

---

## How to start

Write the §1 alignment test first, watch it fail against a deliberately naive implementation, then build the real one. That ordering is the point of this phase.

Summarise your plan as a numbered list and wait for approval. If anything here conflicts with what Phase 04 actually built, say so before writing code.
