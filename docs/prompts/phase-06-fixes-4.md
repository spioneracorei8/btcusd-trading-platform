# Phase 06 — Fixes, round four

> Two items, both surfaced by the first full sweep (48 runs in the log).
> The first blocks the most promising remaining experiment; the second is corrupting the log's denominator.
> `CLAUDE.md` rules apply as always.

---

## Context

The sweep produced a pattern that repeats across all three strategies without exception:

| strategy | 1m | 5m | 15m | 1h | 4h |
|---|---|---|---|---|---|
| ema_crossover | −99.63% | −72.93% | −6.61% | **+13.32%** | — |
| rsi_reversion | −91.44% | −31.18% | −15.05% | −0.98% | — |
| trend_pullback | −98.37% | −61.04% | −39.03% | −15.29% | — |

Monotonic improvement with timeframe, in every column. That is a stronger finding than any single number in the table, because it holds across three structurally different entry rules — trend-following, counter-trend, and trend-continuation. Costs dominate; entry logic is secondary.

The 4h column is empty. Not because 4h was skipped — all six 4h runs executed and every one produced zero trades.

---

## Fix 9 — 4h base produces zero trades

**What happened**

Every 4h run, all three strategies:

```
trend filter:  ema_rsi_mtf v1 (1d=1.00 deadzone=0.15)
  bars not ready: 100.00%
trades: 0
profit factor: inf
```

This is the same defect fixed for the 1h base in round three, one shelf up. The base-aware table left `4h → 1d`, and 1d needs 1000 daily closes — roughly 2.7 years — before the development set begins. History starts 2022-07-01. The filter is never ready, so no bar is ever evaluated.

`TestEveryContributorCanWarmUpBeforeTheDevelopmentSet` exists and should have caught this. Check why it did not — most likely it only asserts over the bases it was written for. Whatever the cause, the test's value is that it covers every base in the table, so fix its coverage as part of this.

**Why it matters more than the other empty cells**

The trend is monotonic and 1h is the only positive result so far. 4h is the natural next step and the one cell that could tell us whether the pattern continues or turns over. Right now it is unmeasured, and an unmeasured cell in a monotonic series is exactly where you should not guess.

**Required change**

Resolve `4h → ` (no contributor). A base with no contributor above it that can warm up runs unfiltered rather than erroring.

That is a change to the rule set in round two, which said a base with nothing above it is a hard error. The rule was right when the alternative was a silently useless filter. It is wrong when the alternative is not running the experiment at all. Make the distinction explicit:

- Contributors configured but unusable (below the base, or unable to warm up) → drop them, report the drop, **run unfiltered**
- The header must say `trend filter: none (no contributor above 4h can warm up over this range)` — not simply `none`, which would read as a user choice

Apply the same reasoning to 1d as a base: unfiltered, not an error.

**Also report zero-trade runs as failures**

`profit factor: inf` on zero trades is arithmetic, not a result. A run producing no trades should say so prominently and should never report a metric computed from an empty set. The verdict logic already fails it on trade count, but `inf` in the log invites misreading later.

**Tests**

- `TestEveryContributorCanWarmUpBeforeTheDevelopmentSet` covers **every** base in the table, and fails if any base resolves to a contributor that cannot warm up
- base 4h resolves to no contributor and runs unfiltered
- The header distinguishes "no filter available" from "filter disabled by the user"
- A zero-trade run reports no profit factor rather than `inf`

---

## Fix 10 — The sweep records duplicate runs

**What happened**

The log contains identical entries. Runs 16 and 17 are the same configuration with byte-identical results. Run 18's numbers reappear as runs 25 and 26.

The cause is structural: `--compare` and `--cost-sweep` both execute the base run, and the sweep script runs both modes for every combination. The base result is therefore computed twice per cell and logged twice.

**Why this is not cosmetic**

`docs/experiments.md` exists to record the denominator. Its own header says a strategy chosen out of fifty has fifty chances to look good by accident. The log currently reads as 48 experiments; the number of distinct configurations tested is closer to 24. When a result eventually clears the criteria, the count above it is the thing that says how much weight to put on it — and right now that count is inflated by a factor of roughly two.

An inflated denominator is the safer direction of error, but it is still wrong, and a log nobody trusts the arithmetic of stops doing its job.

**Required change**

One entry per distinct configuration per invocation.

- When `--compare` and `--cost-sweep` are combined, produce a single entry carrying both the comparison and the sweep line
- The sweep script should run each cell once with both modes rather than twice with one each
- If an identical configuration is deliberately re-run, log it — a genuine repeat is data. Only the same run appearing twice from one invocation is the defect

**Do not retroactively edit the existing entries.** Add a dated note at the top of the log explaining that entries up to 48 contain duplicates from this defect and that the distinct-configuration count is lower. Rewriting history in a file whose purpose is to be an honest record would be the wrong fix, however tidy the result.

**Tests**

- `--compare --cost-sweep` together produce exactly one entry containing both sections
- A full sweep of N cells produces N entries, not 2N
- Two separate invocations of the same configuration produce two entries

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `-timeframe=4h` runs unfiltered and produces trades for all three strategies
- [ ] The warm-up test covers every base and fails on any unusable contributor
- [ ] Header distinguishes unavailable filter from disabled filter
- [ ] Zero-trade runs report no profit factor
- [ ] `--compare --cost-sweep` produces one entry
- [ ] A dated note about entries 1–48 is at the top of `docs/experiments.md`
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- New strategies or parameter changes
- Backfilling 1d to 2020 — worth doing later, not needed for this
- Anything in Phase 07
- Re-running evaluations

---

## How to start

Fix 9 first; it unblocks the experiment that matters. Summarise briefly and commit each separately.
