# 0011 — Look-ahead is prevented structurally, not by rule

**Status:** accepted · **Date:** 2026-08-08 · **Phase:** 04

## Context

`CLAUDE.md` §3.2 forbids look-ahead bias: at time *t* the engine may see data
up to *t* and no further. Stated as a rule, that survives exactly as long as
nobody refactors carelessly. A strategy handed a `[]models.Candle` and an index
is one `+1` away from reading the future, and the resulting backtest does not
fail — it produces a better number, which is the worst possible failure mode
for a measuring instrument.

The distinction that matters: a rule can be broken by accident, a field that
does not exist cannot be dereferenced.

## Decision

`strategy.BarContext` carries three things and nothing else:

| field | why it is safe |
|---|---|
| `Candle` | the bar that just closed; its close is the last knowable price |
| `Indicators` | values at that same close, computed only from bars up to it |
| `Position` | a **copy** of what is held, so the engine keeps ownership |

Deliberately absent: any slice of the series, any index into one, any reader or
repository, and any clock. A clock is excluded for the same reason as the rest
— a strategy that could read wall time would behave differently in a replay
than it did live, and that surfaces as an unreproducible backtest rather than
as an error.

`Intent` carries no fill price and no timestamp. The engine prices every fill;
a strategy that could name its own would make the cost model advisory and could
report any result it liked. `Price` exists only on `SetStop` and `SetTarget`,
where it is a threshold rather than a fill.

Two tests keep this true:

- **`TestLookAheadDoesNotCompile`** writes a probe strategy that reaches for
  future data five different ways — `bar.Candles`, `bar.Index`,
  `bar.Candles.Next()`, `bar.Now`, `bar.NextCandle` — and shells out to `go
  build`. The test fails if the probe *compiles*, and also fails if the
  compiler's complaint does not mention every field, which would mean it was
  passing for the wrong reason. The probe is stored as `.go.txt` so `go vet
  ./...` never parses it.
- **`TestBarContextExposesNothingBeyondTheCurrentBar`** reflects over the
  struct against an allow-list, catching any shape the probe did not anticipate.

## Consequences

- Adding a field to `BarContext` breaks a test on purpose. That is the review
  prompt: is this knowable at the moment the bar closes?
- A strategy needing history must accumulate it itself, bar by bar, exactly as
  it would live. This is the point, not a limitation — it is what makes one
  code path serve both modes.
- `models.IndicatorSnapshot` had to move out of `services/indicator`, because
  `BarContext` carries it and CLAUDE.md §5 allows a service interface file to
  import `models` and `constants` only. `indicator.Snapshot` is now an alias,
  so phase 03 reads unchanged.
- The probe test costs about 70ms to compile. It is skipped under `-short`.
