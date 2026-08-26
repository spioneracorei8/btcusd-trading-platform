# ADR 0025 — Resolution is one-way at the database, not by convention

Status: accepted
Date: 2026-08-26
Phase: 08 (audit finding 8)

## Context

`UpdateSignalOutcome` matched on `signal_id` alone. Every caller reaches it
through `FetchOpen`, which filters to `status = 'open'`, so with one collector
the statement can only ever touch an open row — the invariant held, but it held
because of where the callers happened to come from.

Two collectors running at once breaks that, and two collectors will run at once
eventually. Not as a supported mode: as a deploy that did not stop the old
container, a systemd unit started twice, a manual `docker compose up` beside a
running stack. The audit called it out for that reason rather than because
anything today does it.

Both followers fetch the same open rows. One resolves a signal; the other then
writes a row it read before that resolution existed, and the second write wins.

The case that decided this is `invalidated`. That status means the window this
signal was followed over had missing data, so what happened is not knowable —
it exists precisely so those signals can be left out of statistics rather than
averaged in. Overwriting it with a computed outcome puts a number derived from
data known to be incomplete into the table, indistinguishable from a sound one,
counted in every win rate afterwards. Nothing in the row records that it was
ever invalidated, so there is no query that finds it later. That is not a bug
that produces a wrong answer once; it is contamination of the measurement this
whole system exists to produce.

## Decision

`AND status = 'open'` on the update. Resolution becomes one-way at the
database.

Three things follow from it:

**A miss now has two causes and they are reported separately.** The statement
matching nothing means either the row is gone — a real inconsistency somebody
has to look at — or something else finished it first, which is the guard
working. `ErrOutcomeNotOpen` says the second, and one read-back on the miss
path decides which. Reporting them as one error would either send somebody
hunting for a row that is sitting there resolved, or hide a genuine
inconsistency behind an expected one. A single collector never takes that path,
so the extra query costs nothing in the normal case.

**A lost race is not a failure to follow the signal.** Nothing was lost — the
first resolution stands, which is the point — so it is not logged at ERROR. An
error per contested row on every pass is how a real fault gets lost in noise.

**But it is not silent either.** `FollowReport.Contended` counts it, `Quiet()`
accounts for it so a pass that lost every race still says so, and the pass log
carries the count. The only way to reach this state is two followers, which
means two collectors, whose other symptoms are subtle: duplicated work, two
exchange connections, both racing on every row, and a `reconnect_count` that
looks ordinary. A non-zero count names the misdeploy directly.

## Consequences

- An outcome cannot be corrected after the fact by any code path, including a
  deliberate one. That is audit finding 10, and it is now enforced rather than
  merely true. It is the right trade: an outcome a racing process can silently
  rewrite is worse than one that needs an explicit correction path. If a
  correction is ever wanted — a backfill filling the hole that caused an
  `invalidated`, say — it has to be built as one: deliberate, audited, and
  distinguishable in the row from the original answer, not a second `UPDATE`
  that leaves no trace.
- Nothing today writes to a resolved outcome on purpose. The accounting is
  written by the same statement that resolves; the reconciliation only reads.
  A future feature that needs to would have to change this statement, which is
  the point — it becomes a decision rather than a side effect.
- Contention is visible in the collector's log and its pass report, but **not**
  on `GET /api/v1/status`, which reads the database and cannot see another
  process's in-memory counters. Surfacing it there would need a column. Left
  undone deliberately: the condition is a misdeploy, which is noticed by
  looking at the host, and a column written by two racing processes has its own
  problems.

## Alternatives considered

**Leave it to the callers.** What was already true, and it was true by
coincidence of where the callers came from rather than by anything stated. The
next caller written straight against the repository re-opens it, and nothing
fails at the time.

**A transaction with `SELECT ... FOR UPDATE`.** Correct and heavier than the
problem: it serialises the followers against each other rather than letting the
second one discover it lost. The guard achieves the same outcome in the
statement that was already being executed.

**An advisory lock, or a `collector_status` leader election, so only one
follower runs.** Solves a different and larger problem — two collectors are
wasteful and duplicate the exchange connection whether or not they race on
rows. Worth doing on its own terms if this ever happens twice; it is not a
reason to leave the write unguarded in the meantime, because the guard is what
makes the data safe while the wasteful state exists.

## References

- `docs/audit-phase-07.md` finding 8 (and finding 10, which this forecloses)
- `server/database/queries/signal_outcomes.sql` — the statement
- `TestResolutionIsOneWayAtTheDatabase`, `TestAMissingRowAndALostRaceAreDifferentErrors`,
  `TestASignalResolvedByAnotherProcessIsContentionRatherThanFailure`
