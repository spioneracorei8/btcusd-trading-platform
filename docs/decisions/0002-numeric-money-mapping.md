# 0002 — Mapping numeric columns to decimal.Decimal

**Status:** accepted · **Date:** 2026-08-01 · **Phase:** 01

## Context

`CLAUDE.md` section 4 forbids `float64` for prices and balances and requires
`shopspring/decimal`. Prices are stored as `numeric(20,8)` and volumes as
`numeric(30,8)`.

pgx v5 has no built-in codec for `shopspring/decimal`: it reads a `numeric`
column into `pgtype.Numeric`. There are three ways to bridge the gap.

1. Register a third-party codec (`github.com/jackc/pgx-shopspring-decimal`) and
   have sqlc emit `decimal.Decimal` fields directly.
2. Let sqlc emit `pgtype.Numeric` and convert in the storage layer.
3. Use `float64`. Not an option — it is exactly what the coding standard bans.

Option 1 was tried first. The module has no tagged release (only a 2022
pseudo-version) and pulls `github.com/jackc/pgx v3` into the build.

## Decision

Option 2. sqlc emits `pgtype.Numeric`, and
`backend/internal/storage/convert.go` converts to and from `decimal.Decimal`.

The conversion is exact in both directions and needs no rounding rule, because
both types represent a value the same way:

| type | representation |
|---|---|
| `pgtype.Numeric` | `Int * 10^Exp` |
| `decimal.Decimal` | `Coefficient() * 10^Exponent()` |

NaN, infinity and unexpected NULL are rejected with an error rather than
coerced, since none of them can legitimately appear in a price column.

## Consequences

- No untagged dependency, and the application module keeps four direct
  dependencies: chi, pgx, decimal, uuid.
- The conversion is covered by unit tests that run without a database
  (`convert_test.go`), including the 8-decimal-place and negative cases.
- Every new numeric column needs a line in the mapping function. That is
  deliberate: it is one place to audit, and a forgotten field fails to compile.
- The same approach is used for `uuid` columns: `pgtype.UUID` in the generated
  code, `google/uuid.UUID` in the domain.
