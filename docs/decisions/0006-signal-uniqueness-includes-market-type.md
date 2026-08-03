# 0006 — Signal uniqueness includes market_type

**Status:** accepted · **Date:** 2026-08-03 · **Deviates from:** `docs/prompts/phase-01.md`

## Context

`phase-01.md` specifies the signal de-duplication key as:

```
UNIQUE (strategy_name, strategy_version, symbol, timeframe, signal_time)
```

Migration `00003` implemented exactly that. A review of the phase found it is
wrong, and a test proved it:

```
--- FAIL: TestSignalSpotAndFuturesSameBar
    a futures signal collides with the spot signal for the same bar
```

The key has no `market_type`, so `BTCUSDT` spot and `BTCUSDT` futures are
treated as one instrument. The second market's signal comes back as
`ErrDuplicateSignal` and is silently dropped.

This contradicts two things the same specs insist on:

- `CLAUDE.md` section 3.5 — market type is a config/enum dimension from day
  one so the spot/futures choice stays open.
- The `candles` primary key, which already includes `market_type`. Candles for
  both markets coexist correctly; signals derived from them cannot.

## Decision

Migration `00005` drops and recreates the constraint as:

```
UNIQUE (strategy_name, strategy_version, symbol, market_type, timeframe, signal_time)
```

The guarantee the spec wanted is unchanged — one strategy version emits at
most one signal per bar, so the owner is never notified twice — it is simply
scoped per market instead of across markets.

A new migration rather than an edit to `00003`, so a database that has already
been migrated moves forward correctly instead of silently keeping the old key.

## Consequences

- Running spot and futures against one database is possible, which is what the
  `market_type` column was added for.
- `TestInsertSignalSeparatesMarketTypes` guards the behaviour, alongside
  `TestInsertSignalRejectsDuplicates` which still proves the duplicate case.
- This is a deliberate deviation from the written phase-01 spec. It was
  reported rather than applied silently; if the narrower key was intentional,
  revert `00005` and the tests will fail loudly rather than the signals
  disappearing quietly.
