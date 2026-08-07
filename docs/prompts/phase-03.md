# Phase 03 — Indicator Engine

> Read `CLAUDE.md` fully before starting.
> Phase 02 must be complete and merged: candles are stored, gaps are tracked, collector runs stably.
> **No strategy, no signals, no notifications in this phase.**

An indicator that is subtly wrong will not crash anything. It will quietly poison every backtest, every signal, and every decision built on top of it, and you will not find out for months. Correctness is the entire point of this phase. Performance is not a concern and must not be optimised for.

Scope: **EMA, RSI, ATR, VWAP only.** Do not add MACD, ADX, Bollinger, Stochastic, or anything else. Four indicators that are verified correct are worth more than twelve that nobody has checked.

---

## 0. Structure

Clean architecture (`CLAUDE.md` section 5, ADR 0005). Indicators are pure
computation: no I/O, so no repository, and section "Out of scope" forbids
exposing them over the API, so no handler either. They are business logic and
live in the usecase layer.

```
server/services/indicator/
├── indicator.go          # Indicator interface, Value, Snapshot — the contract
├── usecase.go            # IndicatorUsecase: build a Set, evaluate a series
└── usecase/
    ├── ema.go
    ├── rsi.go
    ├── atr.go
    ├── vwap.go
    ├── set.go            # Set + Snapshot assembly
    └── evaluate.go       # Evaluate = a loop over Update, nothing more
server/testdata/indicator/ # reference CSVs + the script that generated them
```

**Mapping from the original phase-03 draft**, written against the
pre-clean-architecture layout:

| Draft | Here |
|---|---|
| `internal/indicator` | `services/indicator/` |
| `domain.Candle` | `models.Candle` |
| `testdata/btcusdt_1m_reference.csv` | `server/testdata/indicator/` |

There is no `services/indicator/repository/`. Adding one would mean storing
computed values, which section 7 forbids outright.


---

## Goal

A stateful indicator package that produces values identical whether fed candle-by-candle (live) or over a historical series (backtest), that refuses to emit values before it has enough data, and whose output is verified against an external reference implementation.

---

## 1. Core interface

Create `services/indicator` (see section 0 for the layout).

Every indicator implements a single interface. The shape matters more than the names:

```go
type Indicator interface {
    // Update feeds exactly one closed candle and returns the current value.
    // Returns ok=false while the indicator is still warming up.
    Update(c models.Candle) (value float64, ok bool)

    // WarmupPeriod is the number of candles required before Update returns ok=true.
    WarmupPeriod() int

    // Ready reports whether enough candles have been consumed.
    Ready() bool

    // Reset clears all internal state.
    Reset()

    // Name returns a stable identifier including parameters, e.g. "ema_200".
    Name() string
}
```

Non-negotiable rules:

- **`Update` accepts closed candles only.** It has no way to verify this, so the contract must be documented on the interface and enforced by callers. Add a debug-build assertion if `is_closed` is ever false.
- **Never return a partially-computed value with `ok=true`.** Never return `0` as a stand-in for "not ready". A zero flowing into a strategy becomes a phantom signal.
- **Indicators are single-symbol, single-timeframe, and not goroutine-safe.** One instance per `(symbol, timeframe, params)`. Do not add mutexes; the pipeline is single-writer by design.
- `float64` is correct for indicator values (see `CLAUDE.md` §4). Prices arriving from `models.Candle` are `decimal.Decimal` — convert at the point of use and document the conversion.

---

## 2. Warm-up is a first-class concern

This is the most commonly botched part of indicator code and the reason this section exists.

EMA has infinite memory: its value never fully forgets its seed. An EMA(200) fed exactly 200 candles differs measurably from the same EMA fed 2000 candles. The same applies to RSI and ATR when using Wilder's smoothing. This means a backtest that starts evaluating at the first candle where the indicator "has a value" is evaluating against unconverged numbers, and its early results are fiction.

Requirements:

- `WarmupPeriod()` must return a value large enough for practical convergence, **not** the minimum arithmetically required. For EMA and Wilder-smoothed indicators use `5 × period` as the default warm-up. Document this choice in `docs/decisions/`.
- Seeding: EMA seeds with an SMA of the first `period` candles. RSI and ATR seed with Wilder's standard initial average over the first `period` candles. Do not seed with the first close price alone.
- `Ready()` must be false for the entire warm-up window. There is no partial-credit state.
- Downstream code (Phase 4 onward) will refuse to evaluate any bar where any required indicator reports `Ready() == false`. Make that easy by exposing a helper that reports the maximum warm-up across a set of indicators.

Add a test that constructs the same EMA(200) twice — once fed 200 candles, once fed 2000 ending at the same candle — and asserts the values differ by more than a trivial epsilon. The test documents the hazard rather than guarding against a regression.

---

## 3. Incremental and batch must agree exactly

Live ingestion feeds one candle at a time. Backtesting iterates a stored series. If those become two code paths, they will drift, and your backtest will stop describing reality.

There is exactly one implementation, and it is the incremental one. Batch evaluation is a loop over `Update`.

Provide a helper for series evaluation, implemented only as that loop:

```go
func Evaluate(ind Indicator, candles []models.Candle) []Value
```

Add a test that feeds a 5000-candle fixture through `Evaluate`, then through a manual candle-by-candle loop with a fresh instance, and asserts every emitted value is bit-identical. Not approximately equal — identical. Any divergence means state is leaking somewhere.

---

## 4. The four indicators

### EMA
- Standard: `multiplier = 2 / (period + 1)`
- Seeded with SMA over the first `period` candles, as described in §2
- Uses close price
- Constructor: `NewEMA(period int) (*EMA, error)` — reject `period < 2`

### RSI
- **Wilder's smoothing**, not the SMA-based variant. These give different numbers and the difference is not small. Document the choice explicitly; the fixtures must come from a source using the same variant.
- Standard 14 default but parameterised
- Handle the degenerate case where average loss is zero: RSI is 100, not a division by zero
- Handle the case where both average gain and loss are zero (a perfectly flat series): return 50 by convention, and document it

### ATR
- True Range: `max(high-low, |high-prevClose|, |low-prevClose|)`
- Wilder's smoothing, consistent with RSI
- The first candle has no previous close — it contributes to seeding only, and `Ready()` accounts for it

### VWAP
VWAP has no single definition. It must be pinned down or the numbers become meaningless later.

- **Decision required from you before implementing.** Present the options and wait: daily reset at 00:00 UTC, versus a rolling window of N candles.
- My recommendation, unless told otherwise: **daily reset at 00:00 UTC**, because crypto has no trading session and UTC midnight is the convention most charting tools use for crypto.
- Typical price is `(high + low + close) / 3`, not close
- Uses `quote_volume` if reliable, otherwise `close × volume` — pick one, document why
- Resets must be driven by the candle's `open_time` in UTC, never by wall-clock time. A backtest replaying 2023 must produce the same resets as it did in 2023.
- Record the decision in `docs/decisions/`

---

## 5. Verification against an external reference

An indicator cannot be verified against itself. Fixtures must come from outside this codebase.

- Take a real slice of stored BTCUSDT 1m candles (at least 5000 bars, covering a period with both trend and chop) and export it to `server/testdata/indicator/btcusdt_1m_reference.csv`.
- Compute expected values using an independent implementation — `pandas-ta` or `TA-Lib` in a throwaway Python script — and commit both the expected values and the generating script under `testdata/`. The script is documentation of provenance; it is not part of the build.
- Assert equality within a tolerance of `1e-6` relative error, after the warm-up window. Values inside warm-up are not compared.
- **Beware of variant mismatch.** If the reference library uses SMA-based RSI and ours uses Wilder, the test will fail for a correct implementation. Verify which variant the reference uses and state it in a comment beside the fixture.

Also include hand-computed fixtures for small cases — a 20-bar series where EMA and RSI can be checked by hand or in a spreadsheet. These catch errors the large fixtures mask.

---

## 6. Indicator set management

Create a small type that holds the indicators required for a given timeframe and drives them together:

```go
type Set struct { /* ... */ }

func (s *Set) Update(c models.Candle) (Snapshot, bool)
func (s *Set) WarmupPeriod() int   // max across members
func (s *Set) Ready() bool          // all members ready
```

`Snapshot` is a plain value type holding one value per indicator plus the candle's `open_time`. It is what Phase 4 and Phase 6 will consume. Keep it free of pointers and free of behaviour.

`Set.Update` returns `ok=false` unless every member is ready. Partial snapshots must not escape.

---

## 7. Persistence — deliberately omitted

Do **not** create an `indicators` table. Do not store computed values.

Indicators are cheap to recompute and expensive to keep correct across schema and parameter changes. A stored EMA(200) whose warm-up rules later change becomes silently stale data with no way to detect it. Recompute from candles every time.

If profiling in Phase 4 later shows this is a real bottleneck, we will revisit it with evidence. Do not pre-empt that.

---

## 8. Tests

- Unit: each indicator against large external fixtures (§5), post-warm-up, `1e-6` relative tolerance
- Unit: each indicator against small hand-computed fixtures
- Unit: `Ready()` is false for the entire warm-up window and true immediately after
- Unit: `Update` never returns `ok=true` with a zero value during warm-up
- Unit: `Reset()` returns an instance to exactly the state of a freshly constructed one — feed 100 candles, reset, feed the same 100, compare against a fresh instance
- Unit: incremental versus batch identity over 5000 bars (§3)
- Unit: EMA convergence demonstration (§2)
- Unit: RSI degenerate cases — all-gains, all-losses, perfectly flat
- Unit: VWAP resets at the correct UTC boundary, including a series that spans a day boundary
- Unit: constructors reject invalid parameters

No test may hit the network or the database. All fixtures live in `testdata/`.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] All four indicators match the external reference within `1e-6` after warm-up
- [ ] Incremental and batch evaluation produce identical values over 5000 bars
- [ ] No indicator emits a value while `Ready()` is false
- [ ] VWAP reset behaviour is decided, implemented, and recorded in `docs/decisions/`
- [ ] Warm-up multiplier choice recorded in `docs/decisions/`
- [ ] RSI smoothing variant documented beside the fixture
- [ ] No `indicators` table exists
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- MACD, ADX, Bollinger Bands, Stochastic, or any additional indicator
- Strategy or signal logic
- Multi-timeframe combination (that is Phase 5)
- Any performance optimisation, caching, or SIMD
- Exposing indicators over the API
- Storing computed values

---

## Decisions taken (2026-08-04)

Both open questions are settled; record them in `docs/decisions/` when
implementing.

1. **VWAP resets daily at 00:00 UTC**, driven by the candle's `open_time`, never
   wall-clock. Crypto has no session and UTC midnight is the convention charting
   tools use. The known cost is that the first bars of each UTC day are computed
   over a small sample and are correspondingly noisy.
2. **Reference implementation: TA-Lib 0.7.1**, verified empirically rather than
   assumed:
   - RSI matches a hand-rolled **Wilder** implementation to 7e-15, while the
     SMA-based variant differs by 2.77 on the same series. TA-Lib is Wilder, which
     is what §4 requires.
   - EMA seeds with the **SMA of the first `period` values**, exactly as §4
     specifies, matching to 0.00e+00.
   - TA-Lib has **no VWAP**, so the reference script computes it directly from the
     agreed daily-reset rule.
3. **Fixture series is synthetic and deterministic** (seeded), covering trend,
   chop, flat and gap regimes. Binance is unreachable from the build environment
   (`api.binance.com` and `data.binance.vision` both fail), and phase 02 has not
   yet stored real candles. What the verification actually needs is that the same
   series passes through TA-Lib and through this implementation; the origin of the
   bars does not affect whether the maths agrees. Regenerate against real candles
   once the collector has run — the indicator code does not change.

## How to start

Summarise your implementation plan as a numbered list and wait for approval.
Commit in small conventional-commit increments.
