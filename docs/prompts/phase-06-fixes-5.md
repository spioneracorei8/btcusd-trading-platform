# Phase 06 — Spread-based costs and a low-frequency 1m strategy

> Two items driven by a change in the target venue. The first makes the cost model match where trading will actually happen; the second is the only shape of strategy that fits the account's constraints.
> `CLAUDE.md` rules apply as always — in particular, this system still does not place orders.

---

## Context

The evaluation so far assumed Binance spot: 0.05% taker per side, charged as a percentage of notional. The actual venue is different in both structure and magnitude.

**IUX Standard account, BTCUSD CFD on MT5:**
- Floating spread, roughly 25 USD typical, wider during news and thin liquidity
- No commission on Standard — cost is entirely in the spread
- Contract size 1 lot = 1 BTC; minimum 0.01 lot; 1 point movement on 0.01 lot = 0.01 USD
- Round-trip cost at minimum size: **about 0.25 USD, independent of price**

Against the current model at BTC 63,000, a 0.01 BTC round trip costs 0.63 USD on Binance versus 0.25 USD here. Costs are roughly 2.5× lower — a material change, and the dominant variable in every result so far.

**Account size is 100 USD.** This is the harder constraint and it points the opposite way to the evidence. The sweep found monotonic improvement with timeframe across all three strategies, but higher timeframes need wider stops: a 4h stop of 500–800 USD risks 5–8 USD per trade at 0.01 lot, which is 5–8% of the account. Position size cannot be reduced — 0.01 lot is the floor.

So the account is pushed toward short timeframes for stop-distance reasons, into the regime the data says is worst for cost reasons. The only shape that resolves the tension is a 1m strategy that trades **rarely** — the existing 1m runs took 8,619 trades over two years, roughly 12 a day. The target is closer to 1–2.

---

## Fix 11 — Spread-based cost model

### 11.1 Configuration

Add a cost model selector alongside the existing settings:

- `COST_MODEL` — `percentage` | `spread`, default `percentage`
- `SPREAD_POINTS` — typical spread in points, default `2500` (25 USD at 0.01 USD per point)
- `POINT_VALUE` — USD per point per unit of position size, default `0.01`
- `CONTRACT_SIZE` — units per lot, default `1` (1 lot = 1 BTC)
- `MIN_LOT` / `LOT_STEP` — default `0.01` / `0.01`
- `COMMISSION_PER_LOT` — default `0` (Standard account; Raw would be 4)

Defaults preserve current behaviour exactly. A run with no new settings must be byte-identical to today — the golden-file test from round three covers this and must keep passing.

### 11.2 How spread costs apply

Under `COST_MODEL=spread`, the cost of a round trip is the spread crossed once on entry and once on exit, plus commission per lot per side if configured.

- Cost is in **price points, not percent of notional**. This is the whole reason for the change: at 1m, price moves and spread are the same order of magnitude, and a percentage model misprices that badly in both directions depending on price level.
- Apply spread as half on each side of the mid, or as a full crossing on entry and again on exit — pick one, document it, and be explicit in the header. Do not leave it ambiguous.
- `SLIPPAGE_TICKS` still applies on top for market fills. Spread is the cost of crossing; slippage is the cost of the book moving while you cross. They are different things.
- The cost sweep scales spread the same way it scales fees. **Spread widening is the realistic failure mode on this venue** — 25 USD is a typical figure, not a guaranteed one, and it widens exactly when a strategy most wants to trade. A 2× sweep here is not pessimism, it is a normal Tuesday during a news release.

### 11.3 Position sizing under lot constraints

Fixed-fractional sizing currently computes a continuous position size from the stop distance. That is not achievable on this venue.

- Round the computed size down to the nearest `LOT_STEP`
- If the result is below `MIN_LOT`, the trade **cannot be taken**. Do not silently take it at minimum size — that is a different, larger bet than the strategy asked for, and quietly upsizing risk is how an account dies while the report looks fine.
- Count and report skipped-for-size trades. With a 100 USD account and a 0.01 lot floor, this count may be large, and it is a fact about the account rather than the strategy.

Add `ACCOUNT_CURRENCY_BALANCE` (default `10000`) separately from `INITIAL_EQUITY` if that helps keep percentage reporting readable while modelling a small real balance. Report both the percentage return and the absolute USD figure — on a 100 USD account, "+13%" and "+13 USD" land very differently, and the second is the one that matters for judging whether a drawdown is survivable.

### 11.4 Reporting

Header states the model in force:

```
  cost model:                spread (25.00 USD typical, 2500 points)
  commission:                0.00 USD per lot per side
  contract size:             1 BTC per lot, min 0.01, step 0.01
  point value:               0.01 USD per point at 0.01 lot
  slippage applied:          1 tick(s), market fills only
```

Statistics add:
- Average and total spread cost paid, in USD and as a share of gross profit
- Trades skipped because the sized position fell below `MIN_LOT`
- Risk per trade in USD and as a percentage of balance, averaged and worst-case
- **Worst-case cumulative drawdown in USD**, not only percent

That last line is the important one for this account. A 72-trade losing streak — which the 1m runs actually produced — at 0.5–0.8 USD a trade is 36–58 USD, over half the balance. The report should make that arithmetic visible rather than leaving it to be discovered live.

### 11.5 Tests

- Defaults byte-identical to current behaviour
- Spread cost is independent of price level: the same trade at 20,000 and at 100,000 costs the same USD
- Sizing rounds down to lot step; below-minimum trades are skipped and counted, never rounded up
- Cost sweep scales spread
- Commission applies per lot per side when configured, and is zero on the Standard defaults
- Risk-in-USD and worst-case-drawdown-in-USD match hand-computed values on a fixture

---

## Fix 12 — Multi-timeframe alignment strategy

### 12.1 What it is and why

Register a fourth strategy: `mtf_alignment`.

The three existing strategies all fire on a single condition on the base timeframe. On 1m that produces thousands of trades, and the sweep showed the cost of that frequency swamps any edge in the entry rule.

This one inverts the priority: enter on 1m, but only when several higher timeframes already agree on direction. The alignment requirement is the frequency control. It is not expected to find better entries than EMA crossover — it is expected to find far fewer of them.

**Structural difference from the trend filter.** The filter is a veto applied to another strategy's signals. Here alignment is the entry condition itself. That distinction matters for evaluation: a filter's contribution is measured by `--compare`, but this strategy has no unfiltered counterpart to compare against.

### 12.2 Rules

Direction is established top-down, on closed candles only — the Phase 05 §1 alignment rule applies exactly as it does to the trend filter, and a higher timeframe may only contribute candles whose `close_time <= t`.

- **1d and 4h** — dominant direction. Both must agree, or no trade in either direction.
- **1h and 15m** — intermediate confirmation. Both must agree with the dominant direction.
- **1m** — trigger only. Entry fires on a pullback-and-resume within the established direction, not on a crossover.

Direction on each contributing timeframe is derived from the Phase 03 indicators: price relative to EMA and the sign of the EMA slope. Use the same derivation at every level — a different rule per timeframe is four free parameters wearing a disguise.

Parameters, all with documented defaults, none tuned in this phase:

- `ema_period` per role (dominant, intermediate, trigger)
- `pullback_atr` — how far price must retrace before the trigger arms
- `resume_bars` — bars of resumption required to fire
- `stop_atr_mult` / `target_atr_mult` on the 1m ATR

Stops and targets come from 1m ATR, which is what makes this fit the account: 1m stops are narrow enough that 0.01 lot risks a tolerable fraction of 100 USD.

### 12.3 Warm-up

This is the practical obstacle, and it has bitten twice already.

A 1d contributor with EMA(200) at the 5× warm-up rule needs 1000 daily closes — about 2.7 years — before the development set begins. History starts 2022-07-01. **This will not warm up**, exactly as it failed for the 1h and 4h bases.

Do not work around it by shortening the warm-up. Options, in order of preference:

1. Use 4h as the dominant timeframe and drop 1d entirely. 4h needs 1000 closes ≈ 167 days, which the history covers.
2. Backfill 1d to 2020-04-06 or earlier. Daily candles are tiny — roughly 1,000 rows — and Binance has BTCUSDT dailies from 2017. This is a data collection task, not a code change.

Take option 1 for now so the strategy can be evaluated, and structure it so 1d can be reintroduced when the data exists. `TestEveryContributorCanWarmUpBeforeTheDevelopmentSet` must cover this strategy's contributors too, and must fail rather than silently producing a zero-trade run.

### 12.4 Expected trade count

State the expected frequency in the strategy's documentation, and report actual frequency prominently.

The target is roughly 1–2 trades a day — 700–1,500 over the development set. That range matters in both directions:

- Far more, and the alignment requirement is not doing its job and costs will dominate again
- Far fewer than about 200 total, and the acceptance criteria's trade-count floor cannot be met, and the statistics will not support a conclusion either way

If the first run lands far outside that range, the honest response is to say so and reconsider the design — not to loosen the alignment until the count looks right. Loosening until the count is comfortable is fitting the strategy to the criteria rather than to the market.

### 12.5 Tests

- Alignment is computed only from closed higher-timeframe candles — reuse the Phase 05 §1 test shape
- No entry when dominant timeframes disagree
- No entry when intermediate timeframes contradict the dominant direction
- Long and short are symmetric
- Stops and targets are issued with entry, never separately
- Deterministic over a fixture
- Warm-up test covers every contributor

---

## Definition of Done

- [ ] `go build ./... && go vet ./... && go test ./...` passes
- [ ] A run with no new config is byte-identical to before this change
- [ ] `COST_MODEL=spread` prices a round trip independently of price level
- [ ] Below-minimum-lot trades are skipped and counted, never upsized
- [ ] Report shows risk and worst-case drawdown in USD as well as percent
- [ ] `mtf_alignment` is registered, runnable, and appears in `--list-strategies`
- [ ] Its contributors all warm up over the available history
- [ ] Cost sweep scales spread
- [ ] No code touches any order, trade, account, or withdrawal endpoint

---

## Out of scope

- Modelling MT5 execution specifics beyond spread and commission
- Swap and rollover — free swap on this account
- Sourcing IUX price data; Binance candles remain the input, with the caveat below
- Parameter tuning of any strategy
- Anything in Phase 07

---

## A caveat that belongs in the report

Every candle in this system is Binance BTCUSDT. The intended venue is IUX BTCUSD CFD. Those are different instruments: different liquidity, different quotes, different spread behaviour, and a CFD tracks the underlying rather than being it.

The cost model can be made to match. The **price series cannot**, without collecting from the venue itself.

Add a line to the report header when `COST_MODEL=spread`:

```
  note: prices are Binance BTCUSDT; costs model IUX BTCUSD CFD.
        Spread behaviour and quotes on the trading venue will differ.
```

This is not a reason to stop — the price series is close enough for the direction of a result to mean something. It is a reason not to treat any number produced here as a prediction of what the account will do, and the report should say so on every run rather than relying on it being remembered.

---

## How to start

Fix 11 first — the strategy in Fix 12 cannot be judged without a cost model that matches the venue. Summarise the plan and wait for approval. Stop and explain if either needs a change larger than described.
