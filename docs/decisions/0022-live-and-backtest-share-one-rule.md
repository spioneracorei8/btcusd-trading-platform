# ADR 0022 — Live and backtest share one rule, not two implementations

**Status:** accepted (phase 07)

## Context

Phase 07 records what became of each live signal so live outcomes can be
compared against backtest predictions. The comparison is the point of the
phase: it is the only thing that separates "the pipeline is broken" from "the
edge is thin", and those demand opposite responses.

Resolving a live signal needs the same decisions the backtest engine makes:

- what price an entry filled at
- whether a bar reached the stop or the target
- what happens when one bar reached both

The engine already had all three, as unexported methods on its internal
`openPosition` type. The obvious path was to write the same logic again inside
the outcome follower.

## Decision

The rules moved to the backtest service root as `backtest.FillPrice` and
`backtest.Levels`, and both the engine and the outcome follower call them.

The engine's internal helpers now delegate rather than implement.

## Why

Two implementations of "did this bar hit the stop" would drift. Not
immediately, and not visibly — the drift shows up as a divergence between
prediction and outcome, which is exactly the signal this phase exists to
detect. A reconciliation would then be measuring the difference between two
pieces of code and reporting it as a difference between the model and reality.

That failure is worse than an ordinary bug because it is self-concealing: the
report keeps working, the numbers stay plausible, and the conclusion drawn
from them is wrong in the direction of "something is wrong with the strategy"
— sending the search away from the actual fault.

Sharing the rule makes the property checkable rather than asserted. Mutating
either shared function now breaks the engine's golden-file tests and the
follower's tests together. A copy would break only one.

## Consequences

- `services/outcome` imports `services/backtest`'s root package. That is
  within the import rules: the root reaches only `models` and `constants`.
- The follower reproduces the engine's optimistic fills as well as its
  pessimistic ones. An entry that gaps past its own stop is closed at the stop
  price, which the market did not trade at — recording, in one observed case,
  a long that gapped down as a stop that made 1.22%.

  This is deliberate. Correcting it in the follower alone would make the two
  sides disagree for a reason unrelated to the strategy. Instead the outcome
  row is marked, and the reconciliation counts those resolutions separately as
  resting on an assumption.
- Changing either rule now changes both live resolution and every historical
  backtest number. That is the intended coupling, and the golden files are
  what make such a change impossible to land silently.

## Alternatives rejected

**Copy the logic into the follower.** Rejected for the reason above: the drift
is invisible until it corrupts the one measurement the phase exists to make.

**Have the follower call the engine per signal.** The engine replays a range
and manages equity, sizing and position state. Driving it one signal at a time
would mean constructing a fake run per signal, and the parts that would differ
are exactly the parts that matter.

**Record what the engine computed at signal time.** The engine does not run
live, so there would be nothing to record. It is the follower that computes
the accounting, using the engine's rules.
