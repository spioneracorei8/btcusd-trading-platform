# Phase 06 — Parameter flags and two missing exit mechanisms

> Two items. The first makes every strategy parameter reachable without recompiling; the second adds two exit mechanisms the system has never had.
> `CLAUDE.md` rules apply as always.

---

## Context

Fifty-seven evaluation runs have used **documented defaults for every strategy parameter**. Timeframe, cost model, equity and risk have been varied; `fast`, `slow`, `stop_atr_mult`, `target_atr_mult` and the rest never have. That is a real gap, and it exists because Phase 05 and 06 forbade tuning — correctly at the time, since tuning before a measuring instrument exists produces numbers that describe the past.

The instrument exists now. Opening the parameters is the right next step.

**What the numbers say about what to expect.** Gross return before costs — the raw edge, before a single fee — is `+7.22%` over two years on 4h and `+1.84%` on 1h. Parameter changes work on that figure. Moving 7% to 12% is plausible; moving it to 50% is not. Tuning can make a thin edge slightly less thin. It does not manufacture one.

**The specific risk this change introduces.** Every additional parameter combination is another chance for a result to look good by accident. Fifty-seven runs already sit in the log. If the next fifty are a grid search and the best one is chosen, that result has roughly a hundred chances to be luck and no way to tell from its own report. This is why Fix 14 exists.

---

## Fix 13 — Expose all parameters

### 13.1 Mechanism

Add `--param key=value`, repeatable, applied to whichever strategy `--strategy` selected:

```bash
backtest -strategy=ema_crossover -param fast=12 -param slow=26 -param stop_atr_mult=2.5
```

- Unknown keys are a **hard error** naming the valid keys for that strategy. A silently ignored typo means running the default while believing otherwise, and there is no way to detect that afterwards from the report.
- Values are validated by the same rules the constructor already applies.
- `--list-strategies` gains each strategy's parameter names, types, and defaults.
- Every parameter that differs from its default appears in the run header and in the experiment log entry's **Parameters** field. A run whose parameters are not recorded is not reproducible, and the log's whole purpose is that it can be trusted later.

### 13.2 Trend filter parameters

Same treatment: `--filter-param weight_4h=0.7`, `--filter-param deadzone=0.25`.

### 13.3 Tests

- Unknown key errors with the valid list
- Invalid value is rejected by the constructor's own rules
- Every exposed parameter actually reaches the strategy — assert behaviour changes, not just that the value was stored
- Header and log record non-default values
- A run with no `--param` is byte-identical to before this change

---

## Fix 14 — Neighbourhood check

A parameter set that works only at its exact value is a coincidence. If EMA(9,21) is profitable and (8,21) and (10,21) are not, nothing has been discovered — the value has been fitted to the noise in this particular history.

Add `--neighbourhood`, which runs the given configuration plus one step either side of each varied parameter, and prints them together:

```
              fast    slow    net      PF
  base          9      21    +7.22%   1.13
  fast-1        8      21    +6.80%   1.11
  fast+1       10      21    +7.51%   1.14
  slow-1        9      20    +4.10%   1.06
  slow+1        9      22    +7.05%   1.12
```

Judgement rule, stated in the output: a result whose neighbours are broadly similar is a plateau and may be real. A result that collapses one step away is a spike and should be discarded regardless of how good it looks.

Print that interpretation with the table. It is the reading that matters, and it is the one that gets forgotten when the base row looks good.

The neighbourhood run appends **one** log entry containing all rows — it is one experiment, not five.

---

## Fix 15 — Trailing stop

The system has only fixed stops and fixed targets. Every trade exits at a level set on entry, which caps the winners at exactly the point where a trend trade would otherwise start paying.

This matters more than it usually would here. Average win on the 4h run is `31.62` against average loss `16.89` — close to the 2:1 the target enforces, because the target *is* what closes the winners. The strategy is never allowed to find out how far a good trade would have gone. With costs consuming a third of gross profit, letting winners run is one of the few levers that addresses the actual problem rather than trimming around it.

### 15.1 Behaviour

- `trailing_atr_mult` — distance from the running extreme, in ATR. Zero disables it, which is the default, so existing results do not move.
- `trailing_activate_atr` — profit required before the trail arms. Before that the fixed stop applies unchanged.
- Once armed, the stop moves only in the favourable direction. **Never** away from the entry — a stop that can loosen is not a stop.
- ATR is read from the entry bar, not recomputed each bar, so the trail distance is fixed at entry and cannot drift with volatility mid-trade.
- Trailing exits are stop exits: market fill, taker fee, slippage applied. The rule from round three stands — a stop that can only fill at a limit price is not a stop.

### 15.2 Intrabar ambiguity

Same class of problem as stop-before-target, and it must be handled with the same pessimism.

When a bar's range would both extend the trail and trigger it, assume **the stop triggers first, at the pre-extension level**. The alternative assumes price went favourable before it went against, which is unknowable from OHLC and flatters the result.

Count these bars and report them alongside the existing `stop-before-target` line. If the count is high, the result rests on the assumption rather than on the data.

### 15.3 Reporting

- Exit reason gains `trailing_stop`, distinct from `stop` and `target`
- Report the breakdown of exits by reason — this is how you tell whether the trail is doing anything or just adding a code path
- Report average win by exit reason: if trailing exits are not larger on average than target exits, the mechanism is not earning its complexity

---

## Fix 16 — Time-based exit

A position that has gone nowhere for many bars is paying the spread to hold an opinion the market has not confirmed. On 1m the average holding time was under eight minutes and the strategy still bled to costs; on 4h it is 32 hours, which is a long time to be wrong in one direction.

- `max_holding_bars` — force exit after N bars. Zero disables, which is the default.
- `timeout_exit_atr` — optional: only force the exit if the position is within this distance of entry. A trade that is running should not be closed by a clock.
- Exit reason `timeout`, market fill, full costs.

Report timeout exits separately with their average P&L. If timeout exits are on average profitable, the targets are set too far. If they are heavily negative, the timeout is cutting trades that would have recovered. Either reading is useful; the aggregate number hides both.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] A run with no new flags is byte-identical to before this change
- [ ] `--param` reaches every documented parameter of every strategy; unknown keys error
- [ ] `--list-strategies` shows parameters with types and defaults
- [ ] Non-default parameters appear in the header and the log entry
- [ ] `--neighbourhood` runs and prints the comparison with its interpretation, logging one entry
- [ ] Trailing stop never moves against the position; intrabar ambiguity resolves pessimistically and is counted
- [ ] Exit-reason breakdown and average win by reason are reported
- [ ] Time-based exit works and reports separately
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Automated grid search, walk-forward, genetic optimisation. These industrialise overfitting and there is no defence against them at this stage — `--neighbourhood` exists precisely so that tuning stays deliberate and each run is a decision rather than a sweep
- New strategies
- Anything in Phase 07

---

## A note on how to use this

Trailing stop and time exit are new mechanisms, not new parameters — they change what the strategy can do, and are the more promising of the two halves. Parameter tuning adjusts a rule that already exists; a trailing stop lets it do something it previously could not.

Test them separately. If a run changes three things at once and improves, nothing has been learned about which one mattered.

---

## How to start

Fix 13 first — nothing else can be exercised without it. Summarise the plan and wait for approval.
