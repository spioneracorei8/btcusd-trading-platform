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

The development sandbox has no Docker daemon and no TimescaleDB build, only
stock PostgreSQL 16, so `CREATE EXTENSION timescaledb` cannot run there.

The `DO` block itself **has** been executed. Stub functions matching both real
TimescaleDB signatures were defined in a local PostgreSQL 16 and the block was
run against them twice:

| `by_range` present | branch taken |
|---|---|
| yes | `create_hypertable(regclass, by_range(...), if_not_exists, migrate_data)` |
| no  | `create_hypertable(table, column, chunk_time_interval, if_not_exists, migrate_data)` |

That confirms the PL/pgSQL parses, goose passes it through intact, the branch
detection works, and both argument lists match the signatures they target.
What remains unverified is only `CREATE EXTENSION` itself and the real
extension's behaviour, which `TestCandlesIsHypertable` and
`make verify-hypertable` check on first deployment.

The `by_range` lookup is deliberately **not** restricted to the `public`
schema. Pinning it there would silently select the deprecated branch on an
installation that puts TimescaleDB in another schema.
