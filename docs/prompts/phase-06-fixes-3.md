# Phase 06 — Maker fees and a sweep runner

> Two items. The first is the largest single lever left on this system's economics; the second removes the friction of exercising it.
> `CLAUDE.md` rules apply as always.

---

## Context

Every evaluation so far charges 0.05% taker on both sides — 0.1% a round trip. That assumption has decided every result:

| run | net at 1.0× | net at 1.5× |
|---|---|---|
| ema_crossover 1h, unfiltered | +25.60% | untested |
| ema_crossover 1h, filtered | +7.68% | **−1.10%** |

The filtered edge disappears at 1.5× cost. Nothing about the entry logic caused that; the cost model did.

Binance charges roughly 0.02% for maker fills against 0.05% for taker. A round trip at maker rates is 0.04% rather than 0.1% — a 60% reduction in the dominant term. No parameter change tried so far comes close to that.

**But maker fills are not free.** A limit order rests on the book and only fills if price comes to it. Modelling maker fees without modelling non-fills would produce a report that is straightforwardly false: cheaper trades that always happen. The point of this change is to model both halves honestly, and the second half is the harder one.

---

## Fix 7 — Order type and maker fee modelling

### 7.1 Configuration

Add to config, with the existing fee settings:

- `FEE_MAKER_PCT` — default `0.02`
- `ENTRY_ORDER_TYPE` — `market` | `limit`, default `market`
- `EXIT_ORDER_TYPE` — `market` | `limit`, default `market`
- `LIMIT_ORDER_TIMEOUT_BARS` — how many bars an unfilled limit order rests before it is cancelled, default `1`

Defaults reproduce today's behaviour exactly. A run with no new settings must produce byte-identical results to the current output — pin this with a test, because it is what makes every completed run still comparable.

Entry and exit are separate on purpose. Stop-losses in particular are hard to fill as limit orders (see 7.3), so the realistic configuration is likely a limit entry with a market exit, and the model must be able to express that.

### 7.2 Fill model for limit orders

A limit entry is placed at the close of the signal bar and rests at that price.

On each subsequent bar, up to `LIMIT_ORDER_TIMEOUT_BARS`:

- **Buy limit fills** if the bar's `low <= limit price`
- **Sell limit fills** if the bar's `high >= limit price`
- Fill price is the limit price — no slippage. That is the whole point of a resting order.
- Not filled within the timeout → order cancelled, **no trade**. Record it.

The cancelled orders matter as much as the filled ones. A strategy whose limit entries fill only when price moves against it has adverse selection: it gets filled on the trades it should have skipped and misses the ones it wanted. That is invisible in the headline number and must be reported (7.4).

**Where this model is optimistic, and say so in the report:**

- A bar touching the limit price does not guarantee a fill. Queue position is real; being at the front of the book is not automatic. This model assumes touch means fill.
- Intrabar path is unknown. A bar whose low reaches the limit may have done so before or after other things happened.

Both are the same class of assumption as the existing stop-before-target rule, and they belong beside it in the ASSUMPTIONS block rather than buried in a decision record.

### 7.3 Stops must remain market orders

When `EXIT_ORDER_TYPE=limit`, this applies to **targets only**. Stop-losses always execute as market orders and always pay taker fees plus slippage.

The reason is not modelling convenience. A stop that only fills at its limit price is a stop that does not fill when the market gaps through it — which is precisely the situation stops exist for. Modelling stops as maker fills would remove the worst losses from the record and produce a strategy that looks robust because its tail was quietly deleted.

Make this structural rather than a documented convention: the exit path for a stop must not be able to take the limit branch.

### 7.4 Reporting

The header must state the actual cost model rather than a single fee line:

```
  entry order type:          limit (maker 0.02%)
  exit order type:           market (taker 0.05%)
  stop exits:                market (taker 0.05%) — always
  slippage applied:          1 tick(s), market fills only
  limit order timeout:       1 bar(s)
```

Add to the trade statistics:

- Fills by type — maker versus taker counts, each side
- **Limit orders cancelled unfilled**, as a count and as a percentage of signals generated
- Effective average cost per round trip, in basis points

The cancelled-order line is the one to watch. If a large share of signals never became trades, the surviving sample is a filtered subset of the strategy's intent, and its statistics describe something other than the strategy as written.

Cost sweep multiplies both maker and taker rates together. An edge that survives 1.5× at maker rates is a materially stronger result than one that survives 1.5× at taker rates, and the sweep line should make which one is being read unambiguous.

### 7.5 Tests

- Defaults reproduce current results exactly — byte-identical JSON on a fixture run
- Buy limit fills when `low <= limit`, does not when `low > limit`
- Sell limit fills when `high >= limit`, does not when `high < limit`
- Limit fills pay maker and take no slippage
- An unfilled limit order past its timeout produces no trade and is counted
- A stop exit pays taker and slippage even when `EXIT_ORDER_TYPE=limit`
- Maker and taker fill counts sum to the total fill count
- Cost sweep scales both rates

---

## Fix 8 — Sweep runner script

### Purpose

Running one strategy at one timeframe at a time is slow and invites skipping the runs whose results look unpromising — which are exactly the runs the experiment log's denominator depends on.

Add `scripts/sweep.sh`.

### Behaviour

```bash
scripts/sweep.sh                          # every strategy × every timeframe, both modes
scripts/sweep.sh -s ema_crossover         # one strategy, all timeframes
scripts/sweep.sh -t 1h,4h                 # all strategies, two timeframes
scripts/sweep.sh -s ema_crossover -t 1h -m sweep
scripts/sweep.sh --dry-run                # print what would run, run nothing
```

- Strategies default to everything `--list-strategies` reports — do not hardcode the list; a new strategy should be picked up without editing the script
- Timeframes default to `1m,5m,15m,1h,4h`
- Modes default to both `cmp` and `sweep`
- `-o` sets the output directory, default `results/`
- Output filename: `<strategy>-<timeframe>-<mode>.json`, matching the existing convention
- `--allow-gaps=skip` is passed by default; `--halt-on-gaps` overrides it
- Extra flags after `--` pass through to every run, so cost-model experiments do not need script changes

### Requirements

- Sources `.env` the same way `scripts/dev.sh` does
- Runs sequentially. Parallel runs would interleave writes to `docs/experiments.md`, and a corrupted log is worse than a slow one
- A failing run does not stop the sweep — record it and continue. Print a summary of failures at the end
- Print a one-line-per-run progress indicator with elapsed time
- Every run appends to the experiment log as normal. **The script must not add `--no-experiment-log`**, and must not offer a flag to do so. Batch running is exactly the situation where the denominator inflates fastest, and hiding that would defeat the log's only purpose
- Print the run-count at the end: how many runs this invocation added to the log

### A warning to print

Before the first run, when more than five runs are queued:

```
This will add N entries to docs/experiments.md.
A strategy picked from N runs has N chances to look good by accident.
```

Not a prompt to confirm. A reminder that the count is part of the result.

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] A run with no new config settings is byte-identical to before this change
- [ ] Limit entry fills, non-fills, and cancellations behave as specified
- [ ] Stop exits pay taker fees under every configuration
- [ ] Header reports the full cost model; statistics report fills by type and cancelled orders
- [ ] Cost sweep scales maker and taker together
- [ ] `scripts/sweep.sh` runs a full matrix, continues past failures, and reports the run count
- [ ] `--dry-run` prints the plan without executing
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- New strategies or parameter changes
- Post-only orders, iceberg orders, or any other order type
- Fee tier modelling based on volume — a single maker and taker rate is enough
- Queue position simulation
- Anything in Phase 07

---

## A note on what this does and does not settle

If a strategy turns positive under maker fees, that is a real result and worth having. But it comes with a condition attached: it holds only if the limit orders actually fill at the rate this model assumes, and the model's optimism is documented in 7.2 for a reason.

The number to trust is not the headline. It is the cancelled-order rate alongside the cost sweep. A strategy that survives 1.5× maker costs with few cancellations is a finding. A strategy that turns positive only because half its intended trades never happened has not been improved — it has been sampled.

---

## How to start

Fix 7 first; Fix 8 is more useful once there is a cost model worth sweeping across. Summarise the plan and wait for approval. Stop and explain if either needs a change larger than described.
