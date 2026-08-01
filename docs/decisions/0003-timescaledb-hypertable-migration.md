# 0003 — Creating the candles hypertable across TimescaleDB versions

**Status:** accepted · **Date:** 2026-08-01 · **Phase:** 01

## Context

`candles` must be a TimescaleDB hypertable partitioned on `open_time` with 7
day chunks. The compose file pins the image to `timescale/timescaledb:latest-pg16`,
a moving tag.

TimescaleDB changed the signature of `create_hypertable` in 2.13. The old
positional form (`create_hypertable('candles', 'open_time', chunk_time_interval => ...)`)
is deprecated in favour of the dimension form
(`create_hypertable('candles'::regclass, by_range('open_time', INTERVAL '7 days'))`).
Because the tag floats, the migration cannot assume which one exists.

## Decision

Migration `00001_create_candles.sql` wraps the conversion in a `DO $$ ... $$`
block that checks `pg_proc` for `by_range` and calls the matching form. PL/pgSQL
parses a statement only when it is reached, so the unused branch never fails.

The block also raises an explicit error when the `timescaledb` extension is
absent, instead of silently leaving a plain table behind. A plain table would
work until the data grew, which is the worst time to discover the problem.

## Consequences

- The migration works on TimescaleDB before and after 2.13.
- `TestCandlesIsHypertable` in the storage integration tests queries
  `timescaledb_information.hypertables` and fails if the conversion did not
  happen, so this is verified rather than assumed.
- `make verify-hypertable` prints the same check from the command line, which
  is the definition-of-done item for this phase.

## Note on verification

The hypertable path could not be executed while this phase was written: the
development sandbox had no Docker daemon and no TimescaleDB build, only stock
PostgreSQL 16. Everything else in the migrations was applied to a real
PostgreSQL 16 and the storage tests were run against it. The two TimescaleDB
statements (`CREATE EXTENSION` and the `DO` block) remain unexecuted and must be
confirmed on first real `make migrate-up`.
