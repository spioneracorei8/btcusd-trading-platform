# Audit of Phase 07

Phase 07 was six commits: live evaluation, delivery queueing, FCM delivery,
outcome resolution, reconciliation, and the engine's gap-past-stop counter.
Mutation testing during construction caught a great deal. This audit looks for
what it could not — the things that need a second component, a real database,
a shutdown, or a comparison between two implementations to become visible.

Each finding says what breaks, how it would be noticed, and whether it is a bug
or a design decision.

**Status.** The report below was written before any fix, and is left as it was
written. Four findings were triaged for repair and are now fixed — see
"After triage" at the end. The rest stand as recorded.

---

## Summary

| # | Area | Finding | Class | Severity | Status |
|---|---|---|---|---|---|
| 1 | A2 | Live is position-blind; the engine is not. The two compare different populations | design | **high** | **fixed** |
| 2 | A2 | Cost model dropped in three places; live and reconciliation silently price as percentage | bug | **high** | **fixed** |
| 3 | A2 | The follower's accounting hardcodes `taker × 2` regardless of cost model | bug | **high** | **fixed** |
| 4 | A1 | Shutdown drain stores candles without evaluating them; signals lost silently | bug | medium | **fixed** |
| 5 | A2/A4 | A missing bar immediately after a signal is not detected; the outcome is fabricated | bug | medium | **fixed** |
| 6 | A3 | Nothing reports the health of the signal pipeline | gap | medium | **fixed** (phase 08 B2) |
| 7 | A1 | A signal can exist with no notification queued, forever | design | low | open |
| 8 | A4 | `SaveOutcome` has no status guard; two collectors could overwrite a resolution | bug | low | **fixed** |
| 9 | A1 | Observer can run on a cancelled context; the signal is lost but logged | bug | low | open |
| 10 | A2 | Resolution is one-way: a backfill that arrives later cannot correct an outcome | design | low | open, accepted |

7, 9 and 10 are left open deliberately, recorded rather than fixed. Note that
8 and 10 pull in opposite directions and 8 won: making resolution one-way at
the database is exactly what stops a later correction, and a correction path
would have to be built as one — deliberate, audited, and not something a
racing second process can do by accident.

Clean, with nothing found: delivery/resolution contention, connection handling
across the Firebase call, restart-safety of outcome resolution, `entry_price`
write-once, UTC handling, signals without outcome rows.

---

## A1 — Concurrency and ordering

### Finding 1 (bug, medium): the shutdown drain stores candles without evaluating them

`writeLoop` calls `observeClosed` after each `SaveCandle`. When the context is
cancelled it hands the remaining buffer to `drainRemaining`, which saves every
candle on a detached context — **and never calls `observeClosed`.** It is the
only other place candles are written, and `observeClosed` has exactly one call
site.

So every closed candle still in the buffer at SIGTERM is stored but never shown
to the strategy.

**Why it does not recover.** On restart, warm-up replays stored history and
deliberately emits nothing. Those bars are now stored, so they are replayed as
warm-up, and their signals are never recorded. The loss is permanent.

**How it would be noticed.** It would not. The candle series is complete, no
error is logged, and a strategy producing 0.1 signals a day gives nobody a
baseline to miss one against.

This is the same shape as the Phase 02 writer defect the spec asked me to look
for — confirmed work dropped on cancellation. Phase 02 fixed it for candles;
the observer added in Phase 07 reintroduced it for signals.

### Finding 9 (bug, low): the observer can run on a cancelled context

`writeLoop` checks `ctx.Err()` and then calls `SaveCandle` and `observeClosed`
with that same context. Cancellation between the check and the observer leaves
the signal write failing with `context.Canceled`.

Unlike finding 4 this is logged — "could not record a signal; the candle is
stored" — so it is visible in the log, though the signal is equally lost.

**Left open, knowingly.** It needs a shutdown during the moment between storing
a candle and evaluating it, and when it happens the log says so and the next
start-up replays the bar as warm-up. Recorded rather than fixed.

### Finding 7 (design, low): a signal can exist with no notification queued

The signal insert and the queue insert are two statements. A process dying
between them leaves a recorded signal that will never be delivered. On restart
the bar is replayed as warm-up, so nothing re-offers it.

This was a deliberate call in commit 2 — the unique constraint on
`(signal_id, channel)` exists precisely so a recovery sweep would be safe, and
no sweep was written. It is recorded here because "safe to build" and "built"
are different states and the commit message reads like the former.

**Not a defect in the priority ordering**, which is right: the signal is the
artefact and it survives.

**Left open, knowingly.** The signal — the thing worth keeping — is recorded;
what is lost is one alert about it. The recovery sweep the unique constraint
was built for is still unwritten, and this entry is the record that it is
missing rather than unnecessary.

### No issues found

- **Delivery and resolution contention.** They write different tables
  (`notifications`, `signal_outcomes`) and the only shared table is `signals`,
  where delivery reads and resolution writes `entry_price` — a column delivery
  does not depend on. No lock ordering to get wrong.
- **A database connection held across the Firebase call.** `deliver()` reads
  the signal, releases, sends, then acquires again to mark the row. pgx's pool
  releases on `Scan`, so no connection spans the network call.
- **`entry_price` backfill under close arrivals or a mid-backfill restart.**
  The follower is a single goroutine on a ticker, and the write is `WHERE
  entry_price IS NULL` — enforced by the database, not by the follower.
  Verified against a real database.
- **Outcome resolution across a restart.** `follow` re-reads the signal and
  re-walks from stored candles; it never uses previously stored progress as
  input. Interrupting it loses nothing but a pass.

---

## A2 — The comparison's integrity

### Finding 1 (design, HIGH): live is position-blind and the engine is not

**This is the most consequential finding in the audit.**

The live evaluator always shows the strategy a flat position:

```go
Position: strategy.Position{Direction: constants.DirectionFlat},
```

That is deliberate and documented — the live path holds nothing, and a
strategy that suppressed entries while "holding" would go silent forever
waiting for an exit no order was placed to need.

But **all four shipped strategies branch on it**: `ema_crossover`,
`rsi_reversion`, `trend_pullback` and `mtf_alignment` each suppress an entry
while a position is open. The engine shows them the real position; the live
path never does.

**Measured.** Feeding the same 2,191 stored 4h bars through both, with
`ema_crossover` at defaults:

```
bars fed = 2191   engine entries = 141   live decisions = 143
engine-only = 0   live-only = 2
```

Every engine entry has a live counterpart. Live has a surplus — decisions the
engine's strategy never made because it was already in a trade.

**Why it matters more than 1.4% suggests.** The surplus is a function of
holding time. `ema_crossover` on this fixture exits within about two bars; a
strategy holding twenty would suppress an order of magnitude more, and the
whole point of the reconciliation is to be used on strategies nobody has
characterised yet.

**What it does to the report.** The reconciliation puts live signal count and
engine trade count side by side and fires *"live signals fewer than expected —
warm-up, gaps, or the filter behaving differently live"* below 80%. The two are
not the same population by construction, and the bias runs the other way. A
genuine warm-up bug producing 85% of expected signals would be masked by a
structural surplus pushing it back over the threshold.

**This is a design decision, not a bug**, and it is yours: the alternatives are
to give the live path a shadow position so it mirrors the engine, to compare
only signals that have an engine counterpart, or to state the difference in the
report and leave the populations as they are. I have not chosen.

### Finding 2 (bug, HIGH): the cost model is dropped in three places

`backtest.Costs` has eleven fields. The backtest CLI populates all eleven. The
collector, the API and the reconcile CLI each populate **three**:

| Built by | Fields set |
|---|---|
| `backtest/main.go` | all 11 |
| `collector/main.go` `liveCosts` | `FeeTakerPct`, `TickSize`, `SlippageTicks` |
| `server/server.go` `apiCosts` | same 3 |
| `reconcile/main.go` `reconcileCosts` | same 3 |

Dropped: `Model`, `SpreadPoints`, `PointValue`, `ContractSize`, `MinLot`,
`LotStep`, `CommissionPerLot`, `FeeMakerPct`.

`Costs.CostModel()` returns `percentage` when `Model` is empty. So with
`COST_MODEL=spread` configured:

- the outcome follower prices every live signal as percentage-with-taker
- the reconciliation's **backtest side** replays the engine as percentage too
- `make backtest` on the same configuration uses the spread

The reconciliation then compares two percentage-priced sides while the
operator's own evaluations used a different model — and reports the result as a
verdict on the strategy. `MinLot` and `LotStep` being zero also changes sizing
in that replay.

**How it would be noticed.** Only by someone comparing a reconciliation against
a hand-run backtest and asking why the cost row disagrees. Everything renders
normally.

Three copies of the same partial constructor is itself the duplication ADR 0022
warned about, one level up: the fix is one shared constructor, not three edits.

### Finding 3 (bug, HIGH): the follower's accounting hardcodes the taker fee

Independently of finding 2, `accountFor` computes:

```go
cost := u.cfg.Costs.FeeTakerPct.Mul(decimal.NewFromInt(2))
```

It never consults `CostModel()`. Passing the full `Costs` through would not fix
it. The engine charges via `feeOn`/`spreadCostOn`, which branch on the model and
account for maker fills and per-lot commission.

Every `net_return_pct` in `signal_outcomes.backtest_would_have` is computed this
way, and the reconciliation's live win rate is defined as *positive net return
after modelled cost*. Under a spread venue those wins and losses are classified
against the wrong cost.

### Finding 5 (bug, medium): a missing bar right after a signal is not detected

`windowIsHoled` compares consecutive fetched bars starting at `i = 1`. It never
compares `bars[0].OpenTime` against `signalRow.SignalTime`. If the bar the entry
should have filled on is absent, `bars[0]` is a later bar and the entry is taken
from its open.

The recorded-gap query does cover `[SignalTime, last]`, so a gap the collector
recorded is caught. An unrecorded break is not — and the follower's own comment
says the two checks exist to catch exactly those two different failures.

**Reproduced.** A signal decided at 100 with its next bar missing:

```
entry_price = 140.01   (from a bar an hour after the decision)
status      = target
```

With a large jump the gap-past-level note fires, which at least marks the row.
With a small one it does not:

```
entry_price = 101.01
status      = target
divergence  = ""
```

A fabricated win enters the statistics with nothing flagging it.

### Finding 10 (design, low): resolution is one-way

Once resolved, an outcome is never revisited — `FetchOpen` returns only
`status = 'open'`. A signal marked `expired` or `invalidated` before a backfill
filled the hole keeps the answer it got from incomplete data.

Reasonable as built. Worth stating because a backfill reaching back years
(added in Phase 06) can now change what the right answer would have been.

**Left open, knowingly, and now enforced.** Finding 8's guard makes one-way
resolution a property of the database rather than a convention, which is the
opposite direction from this finding and the right trade: an outcome that a
racing process can silently rewrite is worse than one that needs a deliberate
correction path. If a correction is ever wanted it has to be built as one —
explicit, audited, and distinguishable in the row from the original answer.

### No issues found

- **Fill conventions, level rules, gap-past-level.** Shared through
  `backtest.FillPrice`, `Levels.HitBy` and `Levels.EntryBeyond` per ADR 0022.
  Verified: mutating any of the three breaks the engine's golden tests and the
  follower's tests together.
- **Timestamps and timezone.** `signal_time` is the bar's close, which equals
  the next bar's open, and the follower's window query starts there. Every
  `time.Now()` in Phase 07 code is `.UTC()`; the repository normalises on both
  directions.
- **Rounding.** The follower reports exact decimal strings; the rounded value
  appears only in the notification body. Pinned by a mutation test.
- **A parameter change mid-flight.** Grouping uses the parameter set recorded on
  each signal, so signals from before and after land in different groups.
  Verified against a real database with three (version, parameter set)
  combinations.
- **Look-ahead in resolution.** The follower reads only stored candles, and only
  closed candles are ever stored. Nothing lets it see past a bar's close.

---

## A3 — Silent failure

### Finding 6 (gap, medium): nothing reports the signal pipeline's health

`/internal/market/status` answers "is ingestion alive" for the collector.
There is no equivalent for anything Phase 07 added. Asking the spec's question —
*if this stopped working entirely, how long before anyone noticed?* —

| If this stopped | Reported by | Time to notice |
|---|---|---|
| Signals stop being generated | nothing queryable | indefinite |
| The evaluator never becomes warm | one log line at start-up | indefinite |
| Outcomes stop resolving | nothing | indefinite |
| Every delivery fails permanently | an ERROR per give-up, in the log | until someone reads logs |
| `signal_outcomes` falls behind | nothing | indefinite |

Silence is genuinely ambiguous here. A strategy at 0.1 signals a day is
indistinguishable from a strategy that has stopped, and `Ready()` reporting
"not warm" is only visible if someone calls it — nothing does outside the
evaluator.

The parts needed already exist: `Ready()` returns a reason with counts, the
oldest open outcome is one query, and `notifications` carries `status` and
`attempts`. **This gap is real and Part B's `/api/status` is the right place
for it.**

### No issues found

- **A half-applied migration.** goose runs each file in a transaction and
  records the version; a failure leaves the version unchanged and the next start
  retries.
- **`signal_outcomes` falling behind its batch size.** `EnsureOutcomes` selects
  the oldest signals *without* an outcome row, so a backlog larger than the
  batch drains across passes instead of starving. Pinned by a test with a batch
  smaller than the backlog.

---

## A4 — Data integrity

### Finding 8 (bug, low): `SaveOutcome` has no status guard — FIXED

The update matches on `signal_id` alone. A follower holding a row fetched
before another process resolved it would overwrite that resolution — including
overwriting `invalidated` with a computed outcome.

Single-collector deployments cannot hit this: `FetchOpen` filters to `open` and
one goroutine iterates the batch. It needs two collectors, which the spec notes
will happen by accident during a deploy.

The cheap fix is `AND status = 'open'` in the WHERE clause, which makes the
transition one-way at the database rather than by convention.

**Fixed.** The guard is on `UpdateSignalOutcome`. Overwriting `invalidated`
with a computed result was the case that decided it: a number derived from data
known to be incomplete, sitting in the table looking exactly like a sound one,
counted in every win rate afterwards, and not findable later.

Two things came with it, because the guard alone would have been quieter than
it should be:

- **A miss now has two causes and they are told apart.** `ErrOutcomeNotOpen`
  when the row is there and finished, the existing "no outcome for signal"
  when it is genuinely absent. One read-back on the miss path — which a single
  collector never takes — decides which. Reporting them as one would either
  send somebody hunting for a row that is sitting there, or hide a real
  inconsistency behind an expected one.
- **`FollowReport.Contended` counts lost races,** and `Quiet()` accounts for
  it so a pass where every row was taken still says so. The guard means no
  data is lost, so this is not a failure to follow the signal and is not
  logged as one; but the only way to reach it is two collectors, whose other
  symptoms are subtle — duplicated work, two exchange connections, both racing
  on every row. A non-zero count names the misdeploy directly.

Recorded as ADR 0025. Pinned by `TestResolutionIsOneWayAtTheDatabase` (three cases, against the real
database, each asserting the stored row did not move),
`TestAMissingRowAndALostRaceAreDifferentErrors`, and
`TestASignalResolvedByAnotherProcessIsContentionRatherThanFailure`. The usecase
fake now refuses exactly as the statement does, so a test cannot pass on
behaviour the database does not have. Fourteen mutations run against the guard,
the classification and the counting; all killed.

### No issues found

- **A signal existing without an outcome row indefinitely.** `EnsureOutcomes`
  runs at the start of every pass and is idempotent. It cannot starve.
- **Resolution twice.** `FetchOpen` returns only open rows, so within a process
  a resolved outcome is never re-fetched. The walk is also idempotent — same
  bars, same answer — so a duplicate resolution would write the same values.
- **MAE and MFE when the position gaps, or when the resolving bar is the entry
  bar.** Both extremes of every bar are counted, including the resolving one.
  Pinned by two tests, both mutation-verified.
- **UTC end to end.** No `time.Now()` in Phase 07 code escapes `.UTC()`.
- **The unique constraint under two collectors.** Both the evaluator's
  last-bar guard and `signals_unique_per_bar` were tested, the second with the
  guard deliberately given nothing to work with.

---

## A5 — What a test cannot see

Ran the pipeline against the real database and read the rows rather than
checking for a pass.

**Already reported.** The gap-past-stop entry (ADR 0023) came out of exactly
this: a planted signal deciding at 28,200 with a stop at 28,000 filled at
27,600 and was recorded as a stop with a net return of **+1.22%**. The engine
now counts it and the follower marks the row.

**Found by re-running here.** Findings 1 and 5 both came from running rather
than reading — finding 1 from feeding 2,191 real stored bars through both
implementations and counting, finding 5 from deleting one bar and looking at
what the follower produced.

**Rows that make sense.** Thirty planted signals across two parameter sets
resolved to 4 targets and 26 stops, with MAE and MFE hand-checkable against the
bars, `bars_held` consistent, `entry_price` one bar after `signal_price` in
every row, and the reconciliation splitting them into the two parameter groups
with no total across them. Nothing anomalous beyond the findings above.

---

## Before building an API on this

Three things, in the order they would hurt.

**Findings 2 and 3 make `/api/performance` wrong under a non-default cost
model**, in the same way they make the reconciliation wrong — and an endpoint
is read more often and questioned less than a CLI report.

**Finding 1 decides what `/api/performance` is allowed to claim.** If live and
backtest count different populations, the endpoint should either say so beside
the numbers or compare only what is comparable. This needs your decision before
the endpoint is written, not after.

**Finding 6 is the endpoint** — `/api/status` in B2 is the answer to A3, and
building it means deciding what "healthy" means for a pipeline whose normal
output is silence. Suggested: evaluator readiness and its reason, last signal
time, oldest unresolved outcome age, counts of pending and failed
notifications, and the collector state already available.

---

## Method

- Every path in A1–A4 read against the code, not inferred from commit messages
- Findings 1 and 5 reproduced with throwaway harnesses against the real
  database, then removed
- Shared-rule claims re-verified by mutation: breaking one rule must break both
  sides
- Nothing fixed

---

# After triage

Four findings were repaired. Everything else stands as recorded above.

## Finding 2 and 3 — the cost model

There is now **one** place a `Costs` is built: `Config.BacktestCosts()`. The
backtest CLI, the collector, the API and the reconcile CLI all call it, and
`grep 'backtest.Costs{'` outside config and tests returns nothing.

That alone would only fix today's omission, so the guarantee is structural:
`TestEveryCostFieldIsWiredFromConfiguration` loads a config with every cost
variable set to a distinctive non-zero value, reflects over the returned
struct, and fails on any field left at its zero value. A field added to `Costs`
and not wired now fails immediately instead of taking a default. Verified by
removing `Model`, `MinLot` and `FeeMakerPct` in turn — each is caught.

The follower's accounting no longer hardcodes the fee. `Costs.RoundTripPctOf`
is shared with the engine's own `Costs` and branches on `CostModel()`, so a
spread venue is priced as a spread. Where a cost genuinely cannot be expressed
— a per-lot commission has no meaning without a size, and the live path sizes
nothing — the row says so in `cost_excludes` rather than reporting a figure
that is quietly short. Each outcome row also records the model it was priced
under.

## Finding 4 — the shutdown drain

`writeLoop` and `drainRemaining` now both go through one `store`, which saves
and then observes. A third path added later inherits the observer rather than
depending on whoever writes it remembering.

The test pins the invariant rather than the call: *a candle that was stored was
seen*. Reintroducing the exact defect — the drain saving directly — fails it,
and so does removing the observer from the normal path.

## Finding 5 — the missing first bar

`windowIsHoled` now checks that `bars[0]` opens exactly at `signal_time`,
which is where the entry must fill. Both variants from the report are covered:
the one the gapped-past-level note happened to catch, and the silent one.

## Finding 1 — the population split

Chosen: **compare only signals that have a counterpart.** No shadow position,
which would have simulated state the live path does not have and opened a new
way for the two sides to drift.

The engine comparer now returns the instants it entered at alongside its
statistics. Each group's live signals are split into:

- **matched** — the engine also entered on that bar. This is the only column
  compared against the backtest, and the only one the divergence readings and
  the sample banner are computed from.
- **surplus** — the engine did not. Reported on its own, with the reason: it is
  structural, and an entry the engine asked for and then refused for lot size
  or leverage lands here too, because it produced no trade to compare against.

The reconciliation is now one aggregation (`outcome.SideOf`) called three
times, over rows the repository projects, rather than three SQL aggregates
that would have to agree.

`TestASurplusCannotMaskAShortfall` is the point of the change: 140 matched
against 200 the engine entered on is a 30% shortfall, and 60 surplus signals
bring the live total to exactly 200 — so comparing totals finds nothing wrong.
The split reports it.

Verified end to end: fifty planted signals, none of which the engine entered
on, all land in the surplus with the matched column empty and the comparison
correctly declining to say anything.

## Also corrected

ADR 0023's command used `--json`, a flag the backtest CLI does not have, and a
`jq` path that does not exist. The whole value of that ADR is that somebody
runs the command later. It now reads `--out /tmp/run.json` with the right
paths, and has been run:

```
{ "beyond_stop": 0, "beyond_target": 0, "trades": 141 }
```
