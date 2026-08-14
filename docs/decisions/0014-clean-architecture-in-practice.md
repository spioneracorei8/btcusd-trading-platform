# 0014 — Clean architecture in practice, after four phases

**Status:** accepted · **Date:** 2026-08-08 · **Extends:** [0005](0005-clean-architecture-layout.md)

## Context

ADR 0005 chose the layout: interfaces at a service's root, implementations in
`handler/`, `usecase/`, `repository/` subpackages, wired in one place. That was
decided with one complete vertical slice to look at (`health`).

Four phases in there are eleven services, an ingestion worker, a CLI and an
engine. The layout held, but three questions came up that 0005 did not answer,
and each was settled the same way more than once. This records the answers so
the next phase does not re-derive them.

The question this pattern exists to answer is never "where do I put this file".
It is: **when this is wrong, how much of the system do I have to understand
before I can tell?** Every rule below is chosen to keep that number small.

## The layering, as actually built

```
main / collector / backtest        entry points: build config, wire, run
        │
        ▼
routes ──► handler          HTTP in, HTTP out. No rules live here.
                │
                ▼
             usecase        the rules. Knows no SQL, no HTTP, no vendor.
                │
                ▼
            repository      the outward edge: PostgreSQL, and Binance.
                │
                ▼
             database       pgx pool, sqlc output, wire-type conversion
```

Dependencies point one way. A usecase names `candle.CandleRepository`, never
the SQL behind it; nothing below reaches back up.

### The rule that pays for itself: an outbound client is a repository

`services/market/repository/binance/` speaks WebSocket and REST. It is not a
new layer and not a "client" package, because it is the same kind of thing as
PostgreSQL: an edge the system reads from. Its DTOs stop at the package
boundary and become `models.*`.

That paid for itself twice already:

- Binance's kline JSON uses `l`/`L`, `v`/`V`, `q`/`Q` for unrelated fields, and
  Go's `encoding/json` matches case-insensitively. The bug was contained to the
  one package that had ever seen those field names.
- The backtest engine consumes `candle.CandleUsecase`, which is fed by
  PostgreSQL. It never learns that Binance exists. Live and replay differ only
  in where candles come from, which is precisely what CLAUDE.md §3.2 requires.

### The rule everything else rests on: the usecase owns the rules

"An unclosed candle is never stored" lives in `candle.CandleUsecase`, not in
the repository. It is a statement about what the system may reason over, not
about how rows are written, and putting it there means it holds on every path
into storage rather than on the paths that happened to go through one function.
It is also testable without a database.

The backtest engine takes `candle.CandleUsecase` rather than
`candle.CandleRepository` for exactly this reason: the engine must not be able
to reach around the rule by taking the faster route.

## What four phases changed

### 1. A usecase may depend on another service's usecase

`market` drives `candle` and `datagap`. `backtest` drives `candle` and
`datagap` too. That is sideways, not backwards, and it is allowed.

The alternative — one service reaching into another's repository — is what is
forbidden, because it bypasses the rules that live in between. `market` calling
`candleRepo.UpsertCandles` directly would have let a forming candle into
storage no matter what `CandleUsecase` said about it.

### 2. `services/strategy` is a contract package, not a service

`services/backtest/backtest.go` imports `services/strategy`, which CLAUDE.md §5
reads as forbidden: a service's interface file may import `models` and
`constants` only.

The exception is deliberate and narrow. The *root* of `services/strategy` is a
pure contract: it imports `models` and `constants` and nothing else, which puts
it beside them in the dependency graph rather than above them.

The rule exists to stop an interface file depending on a *layer*. Depending on
a leaf contract with nothing beneath it cannot create the cycle or the hidden
coupling the rule is written against.

> **Amended 2026-08-14 (phase 06).** As first written, this section said
> `strategy` held no implementations and that "the moment `strategy` grows a
> subpackage, this reasoning expires". Phase 06 added
> `services/strategy/usecase/` with three strategies, so the first half is now
> false — and the second half turned out to be the wrong test.
>
> What matters is not whether a subpackage *exists* but whether the root
> package *imports* it. `strategy/usecase` imports `strategy`, which points
> down the graph like every other implementation package; `strategy` still
> reaches only `models` and `constants`, so `backtest.RunParams` naming a
> `strategy.Strategy` still drags in no layer.
>
> The original wording confused a proxy for the thing it stood for. "No
> subpackages" was an easy signal that the contract was a leaf; it was never
> the reason. The reason is the transitive closure, and that is what
> `server/architecture_test.go` checks — which is why the test kept passing
> through a change the prose had predicted would break it. A rule worth
> keeping is checked by the test, not by the sentence describing it.
>
> The day `strategy.go` imports `strategy/usecase`, the exception really does
> expire, and the test fails without anyone having to remember this paragraph.

This is why `indicator.Snapshot` was moved to `models.IndicatorSnapshot`
instead: `services/indicator` **does** have a `usecase/`, so it is a service,
and `strategy.BarContext` carrying its type would have been the forbidden
thing. The alias keeps phase 03 reading unchanged.

### 3. A CLI renderer is not a handler

`services/backtest/report/` turns a result into text and JSON. It sits where a
`handler/` would, and does the same job — render a usecase's output for a
consumer — but `handler` means HTTP everywhere else in this repository, and
reusing the name for something that never sees a request would make the
convention useless as a signal.

It is kept separate from the engine so that changing how a statistic is
presented cannot change what was simulated.

## Where the pattern earned its keep

| Phase | What went wrong | What the layering did |
|---|---|---|
| 02 | Binance JSON case collision | contained to `repository/binance/` |
| 02 | errgroup cancelled the context and 1000 buffered candles were dropped | the fix was in one usecase; no repository or handler changed |
| 04 | a real gap was crossed as if absent | one method in `usecase/`; the report, CLI and storage were untouched |
| 04 | levels attached to an entry were dropped | same |

## Where it costs

Honest accounting, because a pattern defended only by its benefits stops being
a decision:

- **A method costs four edits.** Interface, implementation, every test fake,
  and the wiring. `FetchCandlePage` in phase 04 touched five files before it
  did anything. Worth it for a boundary that is crossed by two processes;
  visibly not worth it for a helper used once.
- **Test fakes drift.** Adding `StreamCandles` broke three unrelated test files
  that implement `CandleUsecase`. The compiler catches it, so the cost is
  keystrokes rather than risk — but it is a real tax on widening an interface,
  and it is the reason to think before widening one.
- **`New<Thing>Impl` returning an interface** means a caller cannot reach a
  method the interface does not expose. That is the point, and it is also
  occasionally annoying. It has not yet been worth an exception.

## Consequences

- `server/server.go` remains the only place that knows which implementation
  satisfies which interface. Reading it is how someone learns what this system
  is made of.
- Adding a phase means adding a service, not editing an existing one. Phases
  05–09 (trend, strategy, notify, api, mobile) each have a home already.
- The dependency direction is checkable mechanically. `constants` imports
  nothing from the project, `models` imports only `constants`, and no
  `usecase/` package imports pgx — all three currently hold and are worth
  re-checking when they seem to stop mattering.
