# 0009 — The collector publishes a lifecycle state

**Status:** accepted · **Date:** 2026-08-07 · **Phase:** 02 (fix)

## Context

The first real deployment exposed a question the status endpoint could not
answer. `latest_open_time` was three years old. That single fact has two
readings and they demand opposite responses:

| reading | what it means | what to do |
|---|---|---|
| backfilling | the collector is working through history, exactly as asked | wait |
| live | ingestion has silently stopped | investigate now |

Nothing in the payload told them apart. Worse, the staleness check fired on
every backfill, because a mid-backfill series is by definition far behind. A
check that cries wolf for the entire first run of the system teaches the reader
to ignore it, which costs precisely the one alert it exists to deliver.

The missing information is not another measurement. It is what phase the
collector is in, which only the collector knows and never wrote down.

## Decision

The collector tracks a lifecycle state and publishes every transition:

```
starting → backfilling → live ⇄ reconnecting
```

- **In memory** it is the authority for the running process, guarded by a mutex
  and read by the staleness check.
- **In `collector_status.state`** it is how the api container — a different
  process, with no access to that memory — learns the same thing.
- `never_started` is deliberately **not** a stored value. It is the absence of a
  row, and a `CHECK` constraint refuses to store it as a lie.

The staleness check runs **only in the live state**. Outside it the result is
`null`, not `false`: "the check did not run" is a third answer, and collapsing
it into `false` would be indistinguishable from a genuine all-clear.

Persisting a transition is best effort. A failed write logs a warning and
ingestion continues — losing a status row is not worth stopping data collection
for. The write uses a context detached from the caller's, because a transition
into a terminal state happens while that context is already cancelled, which is
exactly the moment the row most needs to stop claiming the collector is live.

## Consequences

- `state_changed_at` moves only on a real change, so "how long has it been
  backfilling" is measurable rather than reset by every write.
- The transition log line carries `spent_in_previous`, which makes a reconnect
  storm visible in the logs without reading timestamps by hand.
- `RegisterCollectorStart` resets the state to `starting`, so a restarted
  process cannot inherit the previous run's `live`.
- Adding a state means touching three places: the enum, the migration's `CHECK`
  constraint, and `ParseCollectorState`. The constraint is the point — the
  column is `text`, and without it a typo would be stored happily and only
  surface much later when the api failed to parse the row back.
- The state is a report, never a control. Nothing branches on it except the
  staleness check; the reconnect loop is driven by errors as before.
