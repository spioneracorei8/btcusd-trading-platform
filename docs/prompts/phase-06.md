# Phase 06 — Strategy Engine

> Read `CLAUDE.md` fully before starting.
> Phases 01–05 are merged. Backtest engine and trend filter exist and are verified.
> **This phase produces signals, not orders.** `CLAUDE.md` §1 stands: nothing here may place a trade.

This is the first phase where the system expresses an opinion about the market. Everything before it was infrastructure whose correctness could be checked against a known answer. From here on, correctness of the *code* is necessary but no longer sufficient — a strategy can be implemented perfectly and still have no edge, and the code cannot tell you which.

That is what the honest measuring instrument built in Phase 04 is for.

---

## Goal

A strategy interface with several implementations, a signal writer for live operation, and a disciplined evaluation procedure that does not lie to the person running it.

---

## Part A — Engine

### A1. Strategy implementations

Implement in `internal/strategy`, satisfying the Phase 04 interface. Each is a separate registered strategy, selectable by `--strategy=`.

Every strategy must:

- Declare all parameters as a config struct with documented defaults — no magic numbers in logic
- Return `Intent` values only; never compute a fill price
- Set stop and target on entry, in the same intent batch (Phase 04 had a defect where a separately-issued stop was silently dropped — the interface must make that impossible to repeat)
- Be deterministic: same bars in, same intents out

### A2. Stops and targets

Fixed percentage stops are the wrong default for BTC, which moves very differently across regimes. Use ATR-based sizing:

- Stop at `entry ± (stop_atr_mult × ATR)`
- Target at `entry ± (target_atr_mult × ATR)`
- Both multipliers are parameters
- Reject any configuration where the reward-to-risk ratio is below the round-trip cost — a strategy targeting less than it pays in fees cannot win and should fail loudly at construction, not quietly in the results

### A3. Live signal writing

When running live (Phase 07 consumes this), a signal is written to the `signals` table:

- `reason` (jsonb) captures the full indicator snapshot and trend state at entry — this is what makes later analysis possible, and it cannot be reconstructed after the fact
- The unique constraint prevents duplicate alerts for the same bar
- Signals are written for **closed bars only**, consistent with everything else

### A4. Position sizing

Fixed fractional risk: risk a configured percentage of notional equity per trade, with size derived from the stop distance. Default 1%.

This affects reported returns, so it must be identical in live and backtest. No sizing logic may live outside the shared path.

---

## Part B — Starter strategies

**Read this section carefully before implementing.**

Nobody knows which rules work. These are widely-used patterns, not recommendations — they are starting points for experiments, and most of them will fail at 1m–5m once costs are applied. Their value is that they fail in *legible* ways, so you learn something from each.

The single most important number in this phase is the round trip cost: **0.05% taker each way, so 0.1% per trade before slippage.** A strategy averaging 0.15% gross per trade keeps a third of it. Every strategy below must be judged against that floor.

| # | Strategy | Rule sketch | Known weakness |
|---|---|---|---|
| 1 | EMA crossover | Enter when fast EMA crosses slow EMA in the trend direction | Whipsaws badly in chop; fires often, and frequency is expensive here |
| 2 | RSI mean reversion | Enter long when RSI exits oversold, short when it exits overbought | Fights the trend; catastrophic in strong directional moves |
| 3 | Breakout | Enter when price breaks the N-bar high/low | Most breakouts at 1m are noise; false-break rate is high |
| 4 | VWAP reversion | Enter when price deviates from VWAP by k×ATR, target VWAP | Assumes mean reversion holds; fails when a real trend starts |
| 5 | Trend pullback | In an established trend, enter on a pullback to EMA | Fewer, better trades; requires defining "pullback" precisely |
| 6 | Volatility filter + any of the above | Only trade when ATR is within a band | Not a strategy alone; a modifier worth testing on top of others |

Implement 1, 2, and 5 first. They are structurally different — trend-following, counter-trend, and trend-continuation — so their results tell you something about the market rather than about parameter choices.

Add strategy 3 and 4 only after the first three have been evaluated. Resist implementing all six before evaluating any: a pile of untested strategies invites picking whichever happened to look best.

---

## Part C — Evaluation discipline

This section matters more than Part A. It is possible to build all of the above correctly and still reach a false conclusion.

### C1. Split the data before looking at it

- **Development set: 2023-01-01 to 2024-12-31.** Develop, tune, and iterate here freely.
- **Holdout set: 2025-01-01 onward.** Run **once**, at the end, on the single strategy you have chosen.

If the holdout fails, **do not return to development and retest on it.** That set is then spent. Its only value is having been untouched. Note the failure and treat it as the answer.

Enforce this in the tooling: `--dataset=dev|holdout`, with holdout runs writing a record of every use to `docs/holdout-log.md`. Not a lock, just a mirror — the log makes it visible when the rule is being bent.

### C2. Pre-register the acceptance criteria

Write these into `docs/acceptance-criteria.md` before running the first backtest, while nothing is at stake:

- Net return after costs is positive
- Profit factor above 1.3
- Max drawdown under 20%
- At least 200 trades in the development period
- Costs are less than 50% of gross profit
- Longest losing streak is survivable in practice — decide the number now

Under 200 trades, the statistics are not measuring a strategy; they are measuring a handful of lucky bars.

### C3. Log every experiment

`docs/experiments.md`, appended after each run: date, strategy, version, parameters, dataset, key metrics, and a one-line note.

Two reasons. You will otherwise retry things you already rejected. And more importantly, if you run fifty variants and pick the best, the count itself is the finding — the winner of fifty coin-flip contests is not a skilled coin-flipper. The log is what makes that visible instead of forgotten.

### C4. Watch for the specific failure modes

Report these alongside the headline numbers:

- **Concentration** — how much of total profit comes from the best 5 trades. If most of it, the result is a few lucky bars wearing a strategy costume
- **Regime dependence** — results broken down by year, and by high versus low volatility. A strategy that only worked in 2023 has told you about 2023
- **Cost sensitivity** — rerun at 1.5× and 2× the assumed cost. An edge that vanishes under modest slippage was never robust enough to trade
- **Parameter cliffs** — check neighbouring parameter values. If EMA(21) is profitable and EMA(20) and EMA(22) are not, that is a fitted artefact, not a discovery

---

## Tests

- Each strategy: deterministic output over a fixture
- Stop and target are always issued with entry, never separately
- Configurations with reward-to-risk below round-trip cost are rejected at construction
- Position sizing identical in live and backtest paths
- No entries when trend filter reports not-ready
- Signals written only for closed bars; duplicate constraint holds
- `--dataset=holdout` appends to the holdout log
- No `if backtesting` branch anywhere

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] Strategies 1, 2, and 5 implemented, registered, and runnable
- [ ] `--compare` now runs end-to-end (it could not in Phase 05 — no strategy existed)
- [ ] `docs/acceptance-criteria.md` written **before** the first evaluation run
- [ ] `docs/experiments.md` exists with a documented format
- [ ] `--dataset` flag enforced and holdout uses logged
- [ ] Concentration, regime, cost-sensitivity, and parameter-neighbourhood reporting implemented
- [ ] Live signal writing works against a real database
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Push notifications (Phase 07)
- API or mobile app
- Automated parameter optimisation, grid search, genetic algorithms — these industrialise overfitting and there is no defence against them at this stage
- Machine learning
- Multi-symbol or portfolio logic
- Any order placement

---

## A note on the likely outcome

The most probable result of this phase is that none of these strategies clears the acceptance criteria after costs. That is the normal outcome, it is what the majority of people attempting this find, and it is not a failure of the code.

The system was built so that finding out costs a few weeks of evaluation instead of months of losses. If the numbers say there is no edge, the correct response is to believe them — the entire apparatus exists to make that message trustworthy.

---

## How to start

Write `docs/acceptance-criteria.md` first, before any strategy code. Criteria written after seeing results are not criteria.

Then summarise the plan and wait for approval.
