# Phase 07 — Notification and outcome tracking

> Read `CLAUDE.md` fully before starting.
> Phases 01–06 are merged. Sixty-four evaluations exist in `docs/experiments.md`; none has met the acceptance criteria.
> **This phase still places no orders.** `CLAUDE.md` §1 stands.

---

## Context, and why this phase is shaped the way it is

The strategy search is paused, not abandoned. Every rule tried so far loses its edge at 1.5× the assumed cost, and the single best-looking result — `target_atr_mult=2.5` at +6.35% — was shown by `--neighbourhood` to be a spike: both neighbours were unprofitable. Nothing has cleared the bar.

That is the situation this phase is built for. A notification system delivering signals from a strategy known not to clear its criteria is not useful on its own. What makes it worth building now is the second half: **the machinery that measures whether live signals behave like the backtest said they would.**

That measurement cannot be retrofitted. It has to be recording from the first signal, or the comparison has no history to draw on. So notification and outcome tracking are one phase, not two.

Concretely: the backtest reports a 42.86% win rate on 4h. If live signals come in at 25%, something in the system is wrong — look-ahead, fill assumptions, a cost model that flatters. If they come in at 41%, the pipeline is sound and the problem is genuinely that the edge is thin. **Those two conclusions demand opposite responses, and only tracked outcomes can tell them apart.**

---

## Part A — Signal delivery

### A1. Signal mode

`SIGNAL_MODE`, default `silent`:

- `silent` — evaluate, write to `signals`, send nothing
- `notify` — also deliver a push notification

`silent` is the default deliberately. Beginning to send alerts should be a decision someone made, not something that happened because a deploy went out.

Do not add environment names like UAT or TEST here. The system can do exactly two things — send or not send — and a third name would imply a behaviour that does not exist. Whether the person acts on an alert with demo money or real money is not something this system knows or should model.

### A2. Live strategy evaluation

The collector gains a signal path: as each candle closes, feed it to the configured strategy through the same `OnBar` contract the backtest uses.

- `STRATEGY_NAME`, `STRATEGY_TIMEFRAME`, and strategy parameters come from config, using the Phase 06 descriptor mechanism so live and backtest are configured identically
- Live and backtest **must** share one code path. This was the rule in Phase 04 §2 and it matters more here than anywhere: the moment they diverge, the comparison in Part B stops meaning anything
- Signals are written for closed bars only
- `reason` (jsonb) captures the full indicator snapshot, trend state, and the resolved parameter set. Reconstructing it later is impossible, and it is the only thing that will explain a surprising outcome six weeks on
- **The resolved parameter set must be recorded on every signal**, not just at startup. A parameter change between two signals must be visible in the data, or two incomparable groups will be silently averaged together

If the strategy is not ready — warm-up incomplete, gap in range, filter not ready — write nothing and log why. Silence must be explicable.

### A3. Firebase delivery

`internal/notify`, using FCM.

- `FCM_PROJECT_ID`, `FCM_CREDENTIALS_FILE`, device token in config
- The `notifications` table already tracks `status`, `attempts`, `last_error` — use it. Delivery is attempted from that table, not inline with signal generation, so a Firebase outage cannot cost a signal
- Retry with backoff; give up after 5 attempts and mark `failed`
- The unique constraint on `signals` prevents duplicate alerts for the same bar. Verify this holds across a collector restart mid-delivery

Payload: direction, entry, stop, target, timeframe, and a one-line reason. Enough to act on without opening the app.

**Delivery failure must never block signal recording.** The signal is the valuable artefact; the notification is a convenience. A system that drops signals because a phone was unreachable has the priority backwards.

---

## Part B — Outcome tracking

This is the part that makes the phase worth doing now.

### B1. What is tracked

New table `signal_outcomes`:

- `signal_id` (fk, unique), `status` (`open` | `target` | `stop` | `expired` | `invalidated`)
- `resolved_at`, `resolved_price`
- `mae` — maximum adverse excursion: worst price against the position before resolution
- `mfe` — maximum favourable excursion: best price in favour before resolution
- `bars_held`
- `backtest_would_have` — what the backtest engine computes for this same signal
- `divergence_note` — populated when live and backtest disagree

MAE and MFE are not decoration. If MAE is routinely close to the stop on trades that eventually win, the stop is barely surviving and a slightly worse fill would flip the result. That is invisible in win rate and decisive in practice.

### B2. How resolution works

A worker follows open signals against incoming candles:

- Target hit → `target`
- Stop hit → `stop`
- **Both inside one bar → `stop`**, matching the backtest's pessimistic rule (Phase 04 §4). It must be the same assumption in both places or the comparison compares assumptions rather than outcomes
- Neither, past `SIGNAL_EXPIRY_BARS` → `expired`
- Gap covering the window → `invalidated`, excluded from statistics

Resolution runs on closed candles from the database — the same data the backtest reads. Not on ticks, and not on a separate feed.

### B3. Comparing live to backtest

The point of the phase.

`GET /internal/signals/reconciliation` reports, over a configurable window:

- Live win rate against the backtest's, for the same strategy, parameters, and period
- Average win and loss, live against backtest
- Count of signals where the two disagree on outcome, with reasons
- Realised entry price against the price the backtest assumed
- Realised cost per round trip against the modelled cost

**Only compare like with like.** Group by strategy name, version, and resolved parameter set. Averaging across a parameter change produces a number describing nothing.

Add a CLI, `cmd/reconcile`, doing the same offline so results can be read without the API.

### B4. Reading a divergence

Document these in `docs/` and print the relevant one when a threshold is crossed:

| Symptom | Likely cause |
|---|---|
| Live win rate much lower, entries match | Strategy has no real edge; backtest was fitted |
| Live entry prices consistently worse | Slippage exceeds the model |
| Live wins smaller than backtest | Fill assumptions too optimistic |
| Live signals fewer than expected | Warm-up, gaps, or filter behaving differently live |
| Live outcomes match closely | Pipeline is sound; any disappointment is the edge itself |

The last row is as important as the others. A faithful pipeline delivering a thin edge is a different problem from a broken pipeline, and only this table's bottom row distinguishes them.

### B5. Sample size

The reconciliation endpoint must state its own reliability:

```
signals resolved: 23
NOT ENOUGH DATA — differences below are within normal variation.
A meaningful comparison needs at least 100 resolved signals.
```

At the 4h strategy's rate of 0.1 trades a day, 100 signals is nearly three years. **State the expected wait explicitly in the output.** It is better to know that up front than to draw conclusions from twenty trades. If that wait is unacceptable, the answer is a higher-frequency strategy, not a smaller sample.

---

## Tests

- `SIGNAL_MODE=silent` writes signals and sends nothing
- Duplicate signals for one bar are rejected by the constraint, including across restart
- Delivery failure does not prevent signal recording
- Retry backs off and gives up after 5 attempts
- Outcome resolution matches the backtest's stop-before-target rule on a fixture where both are hit
- MAE and MFE match hand-computed values
- A gap in the resolution window marks `invalidated`, excluded from statistics
- Reconciliation groups by parameter set and refuses to average across changes
- Fewer than 100 resolved signals produces the insufficient-data banner
- No `if live` branch in strategy or indicator code

---

## Definition of Done

- [x] `go build ./... && go vet ./... && go test ./...` passes
- [x] `SIGNAL_MODE=silent` is the default and writes signals without sending
- [ ] `SIGNAL_MODE=notify` delivers to a real device — **still open.** Phase 09
      built everything up to the last hop: the phone registers its own token
      through `POST /api/v1/device` (ADR 0026), the queue waits rather than
      failing while nothing is registered, and `GET /api/v1/status` reports
      whether a device is there. What has not happened is a signal arriving on
      a physical Android phone, because that needs hardware and a Firebase
      project that the development environment does not have. The procedure to
      close it is written down in `docs/mobile.md`; it is four checks and needs
      a phone, a real FCM project, and about twenty minutes.
- [x] Signals record the full resolved parameter set — on every signal, not
      once at start-up
- [x] Outcomes resolve to target, stop, expired, or invalidated with MAE and MFE
- [x] Reconciliation compares live to backtest, grouped by parameter set
- [x] Insufficient-sample banner appears below 100 resolved signals — and
      suppresses the divergence readings while it is showing
- [x] Divergence table documented and surfaced — `docs/reading-a-divergence.md`
- [x] No code touches any order, trade, account, or withdrawal endpoint —
      enforced by `TestNothingReachesATradingEndpoint`, and by
      `TestTheOnlyGoogleScopeIsSendingMessages` for the FCM credential

## What was built differently from this spec

Three deliberate departures, each agreed before the work:

**`internal/notify` and `cmd/reconcile` became `server/services/notify/` and
`server/reconcile/`.** This spec was written against an older layout; CLAUDE.md
§5 governs.

**Signals carry `signal_price` separately from `entry_price`.** A strategy
decides on a bar's close and nothing can fill there, so the entry is the next
bar's open plus slippage — filled in one bar later. Recording the close as the
entry would put that difference into every comparison as slippage nobody
introduced. The notification goes out immediately on `signal_price`, labelled
a reference, because the owner needs the news before the entry is knowable.

**The "live win rate much lower" row was split in two,** by whether the entries
match. Entries matching means the same trades did worse — the rule was fitted.
Entries differing means the live path took different trades and the win rate is
a symptom of that. Those point in opposite directions.

## Outstanding

**The engine prices an entry that gaps past its own stop optimistically**, and
how much that affects the sixty-four logged evaluations is not measured. It
could not be measured in the environment where it was found: no Binance access,
no real BTCUSDT history. Every run now reports the count. See ADR 0023 for the
command, the bound, and what to do with either answer.

**Live trend filtering is not built.** A configured `STRATEGY_TREND_FILTER` is
refused at start-up rather than run around, because unfiltered live signals
compared against a filtered backtest would diverge for a reason that is an
artefact of the configuration.

---

## Out of scope

- REST or WebSocket API for the mobile app (Phase 08)
- The app itself (Phase 09)
- Order placement, in any form
- Choosing which strategy to run live — a configuration decision, not a code one
- Resuming the strategy search

---

## A note on what to run live

No strategy has cleared the acceptance criteria, so whatever is configured is a placeholder for exercising the pipeline, not a recommendation.

Best available: `ema_crossover` on 4h with defaults — +4.78% over the development set, PF 1.13, edge gone at 1.5× cost. Do **not** use `target_atr_mult=2.5` despite its better headline; `--neighbourhood` showed it to be a spike, and running a known artefact live would corrupt the very comparison this phase exists to produce.

The value of running it is not the signals. It is that in three months there will be real outcomes to compare against backtest predictions — and that comparison is what will say whether the search should resume with better tools or stop for better reasons.
