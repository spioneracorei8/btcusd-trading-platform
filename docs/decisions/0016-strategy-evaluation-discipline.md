# 0016 — What a strategy is allowed to decide, and what the tooling decides for it

**Status:** accepted · **Date:** 2026-08-14 · **Phase:** 06

## Context

Phases 01–05 could be checked against a known answer. An EMA is right or wrong;
a gap is present or absent; a hypertable exists or does not.

Phase 06 is the first phase where that stops being true. A strategy can be
implemented perfectly and still have no edge, and no test can tell the
difference. Every decision below is about the same problem: the code is no
longer the thing most likely to be wrong, and the person reading the results is
now the component most likely to fail.

The round trip is **0.1%** before slippage — 0.05% taker each way. A rule
averaging 0.15% gross per trade keeps a third of it. That number is the floor
everything here is measured against.

## Decisions

### 1. Levels are carried on the entry intent, not issued beside it

`Intent` gained `Stop` and `Target`, and `EnterLong`/`EnterShort` require both.

Phase 04 had a defect where a stop issued as a separate intent alongside an
entry was silently dropped: the position opened, the protection vanished, and
the report said nothing. That bug is fixed. This makes its *shape*
unrepresentable — there is no way to express an entry without levels, so a
future strategy cannot reintroduce it by accident.

`IntentSetStop` and `IntentSetTarget` still exist, for adjusting an open
position. A strategy that uses one *at entry* fails its test with an
explanation, because that is the ordering the defect lived in.

### 2. Stops are ATR-scaled, and a configuration that cannot pay for itself
fails at construction

Percentage stops assume BTC's volatility is constant across regimes, which it
is not: the same 0.5% stop is untouchable in one month and hit hourly in
another. Levels are `entry ± mult × ATR`, both multipliers parameters.

`Levels.Validate(roundTripCostPct)` refuses a configuration whose reward cannot
clear the round trip. It fails at construction rather than in the results,
because a strategy that cannot win is not a disappointing result — it is a
typo, and a typo that produces plausible-looking losses is worse than one that
produces none.

The check needs an ATR-to-price ratio it cannot know at construction time, so
it uses `ReferenceATRPct = 0.10` and says so. It catches the configuration that
is arithmetically hopeless, not the one that is merely optimistic.

### 3. Sizing is a stated mode, never a default

`Sizing` has no usable zero value: a `RunParams` that does not state a mode is
rejected. Fixed-fractional at 1% is the documented default for anything with a
stop; the stop-less fixtures from phase 04 say `AllInSizing()` explicitly.

Sizing changes every reported number, so an unstated default is a silent
assumption inside every result. Making the field mandatory cost an edit to
every existing fixture, which is the correct price: each one now says which
model produced its figures.

The sizing logic lives in the engine, on the shared path. There is no live
sizing and backtest sizing to diverge.

### 4. The reason column captures the whole snapshot, not a sentence

Indicators are never persisted. Six weeks after a surprising alert, the values
behind it are gone unless they were stored with it — recomputing them would
need the exact warm-up state the live process had, and nothing stores that.

So `signals.reason` holds the bar, the four indicator values, the trend verdict
with **each contributing timeframe's close_time**, and the advisory levels. The
close_time is the evidence there was no cross-timeframe look-ahead; it is
checkable later only because it was written down at the time.

NaN is refused rather than coerced. Substituting zero would store a plausible
number that never existed, and the stored signal is the only record there will
be.

### 5. The holdout is a mirror, not a lock

`--dataset=dev` iterates freely over 2023–2024. `--dataset=holdout` runs over
2025 onward and appends to `docs/holdout-log.md` **before** the numbers are
printed, so a run whose result was disliked is still on the record.

It cannot be enforced. Anyone can pass explicit dates, or run against a copy,
and a lock would create the illusion of a guarantee it could not give. What the
log does is make a second use visible — including to the person making it — and
state plainly that the set was already spent. That is the whole mechanism, and
claiming more for it would be the same category of error the phase is about.

### 6. The neighbourhood is reported and never selected from

Cost sensitivity, per-year and per-volatility splits, concentration against
gross profit, and neighbouring parameter values are all reported alongside the
headline.

None of them feed a choice. Selecting the best neighbour is precisely the
overfitting the report exists to detect, and a tool that both measures and
optimises will eventually be used to optimise. There is no grid search here and
none is planned; the phase spec rules it out, and the reason is that automated
search industrialises the failure this whole apparatus is built to catch.

### 7. Concentration is measured against gross profit, not net

Net can be near zero or negative, at which point "the best 5 trades are 400% of
profit" is arithmetically true and tells nobody anything. Gross profit — the
sum of the winners — keeps the ratio interpretable in exactly the cases where
the question matters most.

### 8. The two absolute rules are checked by a test, not by a paragraph

`server/architecture_test.go` gained two rules:

- **No file references an order, account or withdrawal endpoint.** CLAUDE.md §1
  has no exception, and the danger is not that someone decides to add trading —
  it is that the code arrives one defensible piece at a time. The test fails on
  the first piece, which is the only point at which refusing is easy. Phase 06
  is where the pressure begins: the system now has an opinion, and the distance
  between having one and acting on it is one HTTP call.
- **No code path branches on being a backtest.** CLAUDE.md §3.2. The check is
  textual and approximate; a determined author evades it and someone adding the
  branch for convenience at 2am does not. That is who the rule is for.

The second is deliberately narrow enough to let `if backtest.Dataset…` through:
naming the package you call into is not branching on the mode.

## A bug this tooling found in itself

`--cost-sweep` reported 1590 trades where the headline run reported 1589.

`scaled := params` copies the struct but shares the `Strategy` pointer, which
is stateful. The second run began with the first run's warm-up already
consumed, so it was replaying a different series than it claimed to be. A one
trade discrepancy is exactly the size of error that gets rationalised.

Runs now build a fresh instance, and `strategyGuard` refuses one that has
already been used. The first version of the guard keyed on pointer address
alone and produced a false positive, because Go reuses the address of a
collected object; it now holds the strategy itself, which keeps it reachable.

This is the phase-04 measuring instrument doing its job on the phase-06 code,
and it is the argument for having built it first.

## Consequences

- Adding a strategy means adding a file to `services/strategy/usecase/` and a
  registry entry. Nothing else changes.
- `services/strategy` now has a `usecase/` subpackage, which
  [ADR 0014](0014-clean-architecture-in-practice.md) predicted would end its
  contract-package exception. It did not, and that ADR carries the correction:
  the test was always the transitive closure, not the absence of subpackages.
- Every reported figure now depends on a stated sizing mode, so results from
  before this phase are not comparable with results after it unless they say
  `all_in`.
- The likely outcome of evaluation is that nothing clears
  `docs/acceptance-criteria.md`. That is the normal result, it is written down
  in advance, and the apparatus exists to make it believable when it arrives.
