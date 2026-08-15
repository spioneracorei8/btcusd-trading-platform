# Phase 06 — Fixes, round two

> Three items. The first unblocks the evaluation that is currently stuck; the second is a reporting bug; the third automates a piece of record-keeping that is currently manual and therefore already behind.
> `CLAUDE.md` rules apply as always.

---

## Fix 4 — Base-aware default contributors

**Decision on the conflict you raised**

You were right that the previous spec contradicted itself, and right to stop. Of the two options you offered, take the **second**: a base-aware default. Do not add 4h and 1d to a single shared default.

**Reasoning, for the ADR**

Adding 4h and 1d to one shared default would change the filter for a 1m base as well, and the three completed 1m runs would stop being comparable to anything run afterwards. Seven evaluation runs exist in total; discarding the comparability of three of them to save a few lines of code is the wrong trade at this stage.

The deeper reason is that there is no reason the right contributor set for a 1m base should also be right for a 1h base. One shared default forces two different situations to use the same value because it is convenient, not because it is correct. Base-aware is the honest shape.

**Required change**

```
base 1m   → 5m, 15m, 1h      (unchanged — existing results stay comparable)
base 5m   → 15m, 1h, 4h
base 15m  → 1h, 4h, 1d
base 1h   → 4h, 1d
base 4h   → 1d
base 1d   → error: nothing above it
```

- Weights keep the existing proportions, heaviest on the highest contributor. State the resolved set and weights in the run header, as `ForBase` already does.
- The Phase 05 §1 look-ahead rule is unchanged and stays enforced for every surviving contributor.
- `ForBase` and its partitioning stay exactly as built — this changes what is passed in, not how it is filtered.
- A base with no contributor above it is still a hard error. That case is real for 1d.

**Note on data:** 4h and 1d candles need collecting before a 1h base can build a filter. That is being handled separately via `MARKET_TIMEFRAMES`; the code should fail with a clear message naming the missing timeframe if the candles are absent, not with an empty result.

**Tests**

- Each base in the table resolves to the documented set, with weights summing as configured
- base 1m resolves exactly as it does today — pin this, it protects the completed results
- base 1d errors clearly
- A base whose contributors are configured but not present in the database fails with a message naming the missing timeframe

---

## Fix 5 — `earliest_open_time` is null on a restored database

**What happened**

On the VPS, `/internal/market/status` reports `earliest_open_time: 2023-01-01T00:00:00Z`. On a developer machine holding the same data — restored from that VPS's own dump — every timeframe reports `null`:

```json
{"timeframe": "1m", "earliest_open_time": null, "latest_open_time": "2026-08-15T04:16:00Z"}
```

The data is definitely present. A direct query returns `min(open_time) = 2023-01-01` for all four timeframes, 1.9M rows for 1m, and backtests over 2023–2024 run fine against it.

**Diagnosis**

The value is almost certainly being read from collector-owned state — something recorded during the backfill this process performed — rather than from `candles` itself. A machine that received its history by restore never ran that backfill, so the state is empty while the data is complete.

That is the wrong source. `earliest_open_time` is a fact about the table, and the table is the only thing that knows it. Restoring from a dump is a first-class way for this system to acquire data — it is exactly how the developer machine is meant to be seeded — so any field that goes blank under restore is reporting on the wrong thing.

**Required change**

- Derive `earliest_open_time` from `MIN(open_time)` on `candles`, per symbol/market_type/timeframe, the same way `latest_open_time` is derived.
- Audit every other field on that endpoint for the same defect: anything describing stored data must come from the tables, and only things describing this process's own lifecycle (`uptime_seconds`, `reconnect_count`, `state`) may come from collector state.
- Keep the query cheap. With the primary key on `(symbol, market_type, timeframe, open_time)` this is an index scan, but confirm rather than assume — this endpoint is polled.

**Tests**

- A database seeded by insert alone, with no collector run, reports the correct `earliest_open_time`
- The value matches `MIN(open_time)` for every timeframe
- A timeframe with no rows reports `null` — genuinely absent, not merely unrecorded

---

## Fix 6 — Append to the experiment log automatically

**Why**

`docs/experiments.md` exists because the number of runs is itself a finding: a strategy chosen out of fifty has fifty chances to look good by accident, and only the log records the denominator. Its value depends entirely on being complete.

It is already incomplete. Seven runs happened; all seven were written up afterwards, from scrollback, in one batch. The runs that get skipped are never the interesting ones — they are the ones abandoned halfway, or the ones whose result was disappointing enough to move on from quickly. Those are exactly the entries that make the denominator honest.

**Required change**

Every completed run appends an entry to `docs/experiments.md` — not only sweep runs, all of them.

- Append on success. A run that errored before producing a report writes nothing.
- Path configurable via `--experiment-log`, defaulting to `docs/experiments.md`. `--no-experiment-log` exists for genuine one-offs and its use is noted **in the entry format itself**, so a suppressed run is at least visible as a gap in the numbering.
- Number the entries. A count that has to be derived by scrolling is a count nobody checks.
- Match the existing hand-written format exactly — the seven entries already there are the reference. Read them before writing the formatter.
- Fill everything the report already knows: strategy, version, dataset, resolved range, timeframe, filter and resolved contributors, sizing, net return, profit factor, max drawdown, trades, cost share of gross profit, concentration, and the verdict computed against `docs/acceptance-criteria.md`.
- Leave `**Note:**` as a placeholder line the human fills in — that line is a judgement and the tool should not invent one.

**For `--cost-sweep` specifically**, the entry records all three passes:

```
- **Cost sweep:** 1.0x +25.60% (PF 1.13) | 1.5x <net> (PF <pf>) | 2.0x <net> (PF <pf>)
```

This line matters more than most for this system. Costs are the dominant variable — the same rule went from −99.97% to +25.60% purely on trade frequency — so how fast an edge decays as costs rise is closer to the real question than the headline number is. An edge that disappears at 1.5× was never robust enough to trade, and that fact belongs in the permanent record rather than in a terminal that gets closed.

**Verdict computation:** read the criteria from `docs/acceptance-criteria.md` rather than hardcoding thresholds. If the file is missing or unparseable, write `verdict: not evaluated (criteria file unreadable)` instead of guessing — a wrong pass in the log is worse than no verdict.

**Tests**

- A successful run appends exactly one entry
- A failed run appends nothing
- The appended format parses as the same structure as the seven existing entries
- A cost-sweep run records all three passes on one line
- Entry numbers increment and do not collide when the file already has entries
- Verdict matches a hand-computed result for a fixture whose numbers are known
- `--no-experiment-log` suppresses the entry and the suppression is recoverable from the numbering

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] `-timeframe=1h -compare` runs with the filter, header showing 4h and 1d resolved
- [ ] `-timeframe=1h -cost-sweep` completes and prints all three passes
- [ ] `-timeframe=1m` filter resolution is byte-identical to before this change
- [ ] `earliest_open_time` is correct on a restore-seeded database
- [ ] Every successful run appends a numbered entry to `docs/experiments.md`
- [ ] Cost-sweep entries record all three passes
- [ ] ADR written for the base-aware decision, including the comparability reasoning
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- New strategies or parameter changes
- Anything in Phase 07
- Re-running evaluations
- Backfilling 4h and 1d candles — handled separately via configuration

---

## How to start

Fix 4 unblocks the stuck evaluation and comes first. Summarise the plan, then commit each fix separately. Stop and explain if any of them needs a change larger than described.
