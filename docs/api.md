# HTTP and WebSocket API

Everything the mobile app consumes. Written against a running server: every
`curl` below was executed and the responses are real, edited only to shorten
long arrays.

**There is no authentication.** The api binds to loopback and the host's
tailnet address and nothing else, and `ufw` denies anything that does not
arrive on `tailscale0`. That is the whole access control, it is deliberate, and
ADR 0024 records what would have to exist before this is reachable from
anywhere else.

## Contents

- [Conventions](#conventions)
- [Errors](#errors)
- [GET /api/v1/candles](#get-apiv1candles)
- [GET /api/v1/indicators](#get-apiv1indicators)
- [GET /api/v1/signals](#get-apiv1signals)
- [GET /api/v1/signals/{id}](#get-apiv1signalsid)
- [GET /api/v1/outcomes](#get-apiv1outcomes)
- [GET /api/v1/performance](#get-apiv1performance)
- [GET /api/v1/status](#get-apiv1status)
- [GET /api/v1/stream](#get-apiv1stream-websocket) (websocket)
- [Endpoints outside /api/v1](#endpoints-outside-apiv1)

---

## Conventions

**Base URL.** `http://<host>:8080/api/v1`. The examples below use
`http://127.0.0.1:8099` because that is where they were run.

**Versioning.** The version is in the path. A deployed phone cannot be
redeployed with the server: phase 09 is written against this shape, and the
first time it has to change there will be an app in somebody's pocket still
asking for the old one.

**Symbol and market type** are not parameters. One deployment analyses one
instrument (`MARKET_SYMBOL`, `MARKET_TYPE`) and every response says which,
so a screenshot is unambiguous.

**Time.** Every timestamp in and out is RFC3339 UTC. `from` and `to` are
inclusive of a bar whose `open_time` equals the bound.

**Prices are strings**, never JSON numbers. `numeric(20,8)` does not fit a
`float64`, and a phone parsing `0.1 + 0.2` is the same hazard as a server
doing it. Indicator values *are* numbers: they are derived statistics, never
added to a balance (`CLAUDE.md` §4).

**Absent is `null`, never zero.** A price that is not yet known, an instant
that has not happened, a rate over no sample. A zero would be charted,
averaged and compared like a real value.

**Empty collections are `[]`, never `null`**, so a client handles one shape on
a quiet week.

**Every candle carries `is_closed`.** On REST it is always `true` — only closed
candles are stored. On the websocket it may be `false`, which is the only place
in this system a bar that has not closed is sent anywhere (`CLAUDE.md` §3.1).

---

## Errors

One shape for every failure:

```json
{
  "error": {
    "code": "invalid_parameter",
    "message": "timeframe=3m is not collected; this deployment collects 1m, 5m, 15m, 1h, 4h, 1d"
  }
}
```

The `code` is stable and may be branched on. The `message` is for the person
reading it and may change.

| Code | Status | Means |
|---|---|---|
| `invalid_parameter` | 400 | A malformed or out-of-range parameter. |
| `limit_exceeded` | 400 | More rows asked for than the endpoint returns. Page instead. |
| `not_found` | 404 | No such resource. |
| `unavailable` | 503 | A dependency this process does not have. `/outcomes` on a process with no follower wired up. |
| `internal` | 500 | Anything else. The message is deliberately vague; the detail is in the server log against the request id. |

```console
$ curl -s "$B/candles?timeframe=3m" | jq -c
{"error":{"code":"invalid_parameter","message":"timeframe=3m is not collected; this deployment collects 1m, 5m, 15m, 1h, 4h, 1d"}}

$ curl -s "$B/candles?timeframe=4h&limit=999999" | jq -c
{"error":{"code":"limit_exceeded","message":"limit=999999 is above the maximum of 5000; page instead"}}

$ curl -s "$B/signals/not-a-uuid" | jq -c
{"error":{"code":"invalid_parameter","message":"id=not-a-uuid is not a uuid"}}

$ curl -s "$B/signals/00000000-0000-0000-0000-000000000000" | jq -c
{"error":{"code":"not_found","message":"no signal has that id"}}
```

Throughout: `B=http://127.0.0.1:8099/api/v1`.

---

## GET /api/v1/candles

Stored candles for one timeframe. All closed.

| Parameter | Default | Notes |
|---|---|---|
| `timeframe` | **required** | Must be one this deployment collects, or 400. |
| `from` | `to` minus `limit` bars | RFC3339. |
| `to` | now | RFC3339. Must not be before `from`. |
| `limit` | 500 | Max 5000. |

`timeframe` is required rather than defaulted because an empty list from a
timeframe nothing collects reads as *the market did nothing*, when the truth is
*this server was never told to watch that*. The first sends somebody looking at
the strategy, the second at the `.env`.

A window holding more bars than `limit` returns the **newest** ones and sets
`truncated`. Truncating from the other end would open a chart on the oldest
bars of the range and look like the market had stopped.

```console
$ curl -s "$B/candles?timeframe=4h&from=2024-03-01T00:00:00Z&to=2024-03-01T12:00:00Z" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "timeframe": "4h",
  "from": "2024-03-01T00:00:00Z",
  "to": "2024-03-01T12:00:00Z",
  "count": 4,
  "limit": 500,
  "truncated": false,
  "candles": [
    {
      "open_time": "2024-03-01T00:00:00Z",
      "close_time": "2024-03-01T04:00:00Z",
      "open": "28200",
      "high": "28225",
      "low": "28175",
      "close": "28200",
      "volume": "1",
      "is_closed": true
    }
  ]
}
```

Without `from`/`to` the window is the last `limit` bars ending now, which is
what a chart opens on.

---

## GET /api/v1/indicators

EMA, RSI, ATR and VWAP over a window, recomputed on request.

| Parameter | Default | Notes |
|---|---|---|
| `timeframe` | **required** | As above. |
| `from` | 500 bars before `to` | RFC3339. |
| `to` | now | RFC3339. |

The window may cover at most 5000 bars; wider is `limit_exceeded`, checked
before anything is read.

**These are recomputed, not stored.** Phase 03 decided against storing
indicator values and that decision stands: a stored EMA computed by an older
version of the code looks exactly like a current one, and the disagreement
would be silent.

The cost is a warm-up read. Values mean nothing until the indicators have
converged, so the read reaches `warmup_bars` *before* `from` and the extension
is then discarded — a client that asked for one hour is never handed the
previous three because the server happened to read them.

`warmup_bars` and `bars_read` are on every response because a short `count` on
a wide window is otherwise unexplained. Here the series does not reach back far
enough, so nothing has converged:

```console
$ curl -s "$B/indicators?timeframe=4h&from=2024-03-01T00:00:00Z&to=2024-03-02T00:00:00Z" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "timeframe": "4h",
  "from": "2024-03-01T00:00:00Z",
  "to": "2024-03-02T00:00:00Z",
  "periods": { "ema": 200, "rsi": 14, "atr": 14 },
  "warmup_bars": 1000,
  "bars_read": 367,
  "count": 0,
  "values": []
}
```

`bars_read` (367) below `warmup_bars` (1000) is the whole explanation: this
series begins in January and a 4h EMA(200) needs about 167 days of runway.
Further in, it converges:

```console
$ curl -s "$B/indicators?timeframe=4h&from=2024-08-01T00:00:00Z&to=2024-08-01T12:00:00Z" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "timeframe": "4h",
  "from": "2024-08-01T00:00:00Z",
  "to": "2024-08-01T12:00:00Z",
  "periods": { "ema": 200, "rsi": 14, "atr": 14 },
  "warmup_bars": 1000,
  "bars_read": 1004,
  "count": 4,
  "values": [
    {
      "open_time": "2024-08-01T00:00:00Z",
      "ema": 29368.36172179541,
      "rsi": 42.80507213888372,
      "atr": 976.8977033384298,
      "vwap": 27400
    }
  ]
}
```

1004 bars read to answer with 4. `periods` is on the response so a client is
never comparing an EMA(200) against an EMA(50) without knowing it.

---

## GET /api/v1/signals

A page of the signal history, newest first.

| Parameter | Default | Notes |
|---|---|---|
| `limit` | 50 | Max 500. |
| `offset` | 0 | |
| `direction` | both | `long` or `short`. |

`total` is the size of the collection the page came from, so a client can tell
a short page from the last page without a second request.

**The list carries no `reason`.** It is large — the indicator snapshot, the
trend state and the resolved parameter set — and a page of fifty would be
mostly reason, over a mobile connection, to render a list that shows none of
it. Fetch a signal by id for that.

```console
$ curl -s "$B/signals?limit=2" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "count": 2,
  "total": 31,
  "limit": 2,
  "offset": 0,
  "signals": [
    {
      "id": "e6d0e070-39a3-4fa2-907c-1dc470c83d3d",
      "symbol": "RSSBENCH",
      "market_type": "spot",
      "timeframe": "4h",
      "signal_time": "2024-03-06T00:00:00Z",
      "direction": "long",
      "signal_price": "30800",
      "entry_price": "30200.01",
      "stop_loss": "29900",
      "take_profit": "30500",
      "strategy_name": "ema_crossover",
      "strategy_version": "v1",
      "created_at": "2026-08-23T05:38:15.167197Z"
    }
  ]
}
```

There is no `status` parameter here. A signal has no status of its own — what
happened to it lives in `signal_outcomes` — and `/outcomes?status=` already
answers that question while carrying the signal's fields alongside the result.
A second, thinner way to ask it would be a second thing to keep consistent.

`signal_price` is the close the strategy decided on; `entry_price` is what a
position would have opened at — the next bar's open plus slippage. They are
different numbers, and `entry_price` is `null` until that bar closes. Nothing
here opens a position: the system does not place orders.

---

## GET /api/v1/signals/{id}

One signal with its full reason.

```console
$ curl -s "$B/signals/e6d0e070-39a3-4fa2-907c-1dc470c83d3d" | jq
{
  "id": "e6d0e070-39a3-4fa2-907c-1dc470c83d3d",
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "timeframe": "4h",
  "signal_time": "2024-03-06T00:00:00Z",
  "direction": "long",
  "signal_price": "30800",
  "entry_price": "30200.01",
  "stop_loss": "29900",
  "take_profit": "30500",
  "strategy_name": "ema_crossover",
  "strategy_version": "v1",
  "created_at": "2026-08-23T05:38:15.167197Z",
  "reason": {
    "trigger": "planted",
    "strategy": {
      "name": "ema_crossover",
      "params": [ { "name": "fast", "value": "9" } ],
      "version": "v1"
    }
  }
}
```

The reason is what makes a signal reviewable months later. Indicators are never
stored, so the values behind a decision cannot be recomputed against the
warm-up state the live process actually had — the reason is the only record.

A malformed id is 400 `invalid_parameter`; an id that is well-formed and
unknown is 404 `not_found`. Two different mistakes.

---

## GET /api/v1/outcomes

What became of each signal.

| Parameter | Default | Notes |
|---|---|---|
| `from` | one year ago | RFC3339. |
| `to` | now | RFC3339. |
| `status` | all | `open`, `target`, `stop`, `expired`, `invalidated`. |
| `limit` | 50 | Max 500. |
| `offset` | 0 | |

```console
$ curl -s "$B/outcomes?from=2024-01-01T00:00:00Z&to=2024-12-31T00:00:00Z&status=stop&limit=1" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "from": "2024-01-01T00:00:00Z",
  "to": "2024-12-31T00:00:00Z",
  "count": 1,
  "total": 26,
  "limit": 1,
  "offset": 0,
  "outcomes": [
    {
      "signal_id": "e6d0e070-39a3-4fa2-907c-1dc470c83d3d",
      "signal_time": "2024-03-06T00:00:00Z",
      "direction": "long",
      "timeframe": "4h",
      "strategy_name": "ema_crossover",
      "strategy_version": "v1",
      "status": "stop",
      "bars_held": 2,
      "measurable": true,
      "resolved_at": "2024-03-06T08:00:00Z",
      "signal_price": "30800",
      "entry_price": "30200.01",
      "resolved_price": "29899.99",
      "mae": "625.01",
      "mfe": "24.99",
      "net_return_pct": "-1.0934"
    }
  ]
}
```

`measurable` is `false` for an `invalidated` outcome: its window had missing
data, so whether it would have won is not knowable and it is excluded from
every statistic. It is carried explicitly rather than left to be inferred from
the status string.

`mae` and `mfe` are distances in price from the entry. An MAE routinely close
to the stop on trades that eventually win means the stop is barely surviving,
which is invisible in a win rate.

`net_return_pct` comes from the accounting stored with the outcome and is
already net of modelled costs. It is `null` for an open or invalidated
outcome — no return was computed, as against a return of nothing.

This endpoint answers 503 `unavailable` on a process with no follower wired up,
rather than an empty list. An empty list would be a statement about the market;
the truth is a statement about the deployment.

---

## GET /api/v1/performance

Aggregate outcomes, grouped by strategy, version and resolved parameter set.

| Parameter | Default | Notes |
|---|---|---|
| `from` | one year ago | RFC3339. |
| `to` | now | RFC3339, not before `from`. |

Live outcomes only — what the signals this system produced actually did, not
what a backtest predicted. The comparison between the two is
`/internal/signals/reconciliation`, which replays history and is a page
somebody opens deliberately.

It shares one implementation with the reconciliation, with the backtest half
switched off. Two definitions of a win rate is how two screens end up
disagreeing.

```console
$ curl -s "$B/performance?from=2024-01-01T00:00:00Z&to=2024-12-31T00:00:00Z" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "from": "2024-01-01T00:00:00Z",
  "to": "2024-12-31T00:00:00Z",
  "generated_at": "2026-08-23T05:43:18.322018673Z",
  "groups": [
    {
      "strategy": "ema_crossover",
      "version": "v1",
      "params": [ { "name": "fast", "value": "9" } ],
      "sample": {
        "resolved": 30,
        "required": 100,
        "sufficient": false,
        "banner": "signals resolved: 30\nNOT ENOUGH DATA — differences below are within normal variation.\nA meaningful comparison needs at least 100 resolved signals.\nAt the observed 6.21 resolved signals a day, the remaining 70 would take about 11 days.",
        "resolved_per_day": 6.206896551724138,
        "expected_wait": "11 days"
      },
      "signals": 30,
      "resolved": 30,
      "still_open": 0,
      "invalidated_excluded": 0,
      "targets": 4,
      "stops": 26,
      "expired": 0,
      "wins": 4,
      "losses": 26,
      "win_rate": 0.13333333333333333,
      "average_win_pct": "1.0049",
      "average_loss_pct": "-1.1106",
      "average_cost_pct": "0.1000",
      "expectancy_pct": "-0.8285",
      "rested_on_assumption": 0
    },
    {
      "strategy": "ema_crossover",
      "version": "v1",
      "params": [],
      "sample": {
        "resolved": 1,
        "required": 100,
        "sufficient": false,
        "banner": "signals resolved: 1\nNOT ENOUGH DATA — differences below are within normal variation.\nA meaningful comparison needs at least 100 resolved signals.\nThese resolved over no measurable span, so there is no rate to estimate a wait from.",
        "resolved_per_day": null
      },
      "signals": 1,
      "resolved": 1,
      "targets": 1,
      "stops": 0,
      "wins": 1,
      "losses": 0,
      "win_rate": 1,
      "average_win_pct": "0.9012",
      "expectancy_pct": "0.9012",
      "rested_on_assumption": 0
    }
  ],
  "note": "Grouped by strategy, version and resolved parameter set, with no total across groups: averaging across a parameter change produces a number describing nothing. Every figure is after modelled costs."
}
```

Two groups, because the two sets of signals were produced with different
parameters. The second group's 100% win rate over one trade is exactly what
the sample banner exists for: it is a fact about one trade, not about the
strategy. `expected_wait` is absent there rather than guessed — one signal
spans no time, so no rate can be measured from it, and the banner says which
of the two reasons applies.

**`sample` comes first because it decides whether anything below it means
anything.** A win rate over nine trades and one over nine hundred must not be
able to look alike, so the banner travels with the numbers and a client cannot
render them without it.

**`expectancy_pct` is the number that decides whether a strategy is worth
running** — win rate × average win + loss rate × average loss, after costs. It
is not derivable from a win rate alone: a 30% win rate at a 3:1 payoff beats a
60% one at 1:2.

`win_rate` and `expectancy_pct` are `null` when nothing has resolved. A zero
would read as a strategy that never wins, which is a different statement.

**There is deliberately no total across groups.** Averaging across a parameter
change produces a number describing nothing.

`rested_on_assumption` counts resolutions that came from an assumption rather
than from the data — a bar reaching both levels, or an entry that gapped past
one (ADR 0022, ADR 0023).

---

## GET /api/v1/status

Whether the signal pipeline is alive. No parameters.

This is the endpoint the phase 07 audit asked for. Of each component: *if this
stopped working entirely, how long before anyone noticed?* For everything after
ingestion the answer was "indefinitely".

The difficulty is that **silence is the normal output**. A strategy at a tenth
of a signal a day is quiet for weeks by design, so an endpoint that only said
"no signals recently" would be useless. What it reports instead is the state of
each stage and the age of the last thing each one did, and leaves the judgement
to a person who knows what they configured.

```console
$ curl -s "$B/status" | jq
{
  "symbol": "RSSBENCH",
  "market_type": "spot",
  "observed_at": "2026-08-23T05:43:23.521997089Z",
  "collector": {
    "reachable": true,
    "state": "reconnecting",
    "ws_connected": false,
    "started_at": "2026-08-23T05:38:15.692618Z",
    "updated_at": "2026-08-23T05:38:31.215179Z",
    "heartbeat_age_seconds": 292.306818089,
    "reconnect_count": 0,
    "last_disconnect_note": ""
  },
  "evaluator": {
    "configured": true,
    "strategy": "ema_crossover",
    "timeframe": "4h",
    "ready": true,
    "reason": "",
    "last_signal_at": "2024-03-06T00:00:00Z",
    "last_signal_age_seconds": 77780603.5219971,
    "signals_total": 30
  },
  "outcomes": {
    "open": 0,
    "oldest_open_at": null,
    "oldest_open_age_seconds": null,
    "missing_outcome_rows": 0
  },
  "delivery": {
    "mode": "silent",
    "pending": 0,
    "sent": 0,
    "failed": 0,
    "last_sent_at": null
  },
  "concerns": [
    { "component": "collector", "detail": "the last heartbeat was 4m52s ago, more than three intervals of 5s — the collector process may be gone" },
    { "component": "collector", "detail": "the market data stream is not connected" }
  ],
  "note": "Silence is the normal output of this pipeline. ..."
}
```

Reading it:

- **`evaluator.configured`** false means no strategy is running. That is a
  configuration, not a fault, and the distinction is the point: *switched off*
  and *stuck warming up* both produce no signals.
- **`evaluator.ready` / `evaluator.reason`** are the pair to read beside an old
  `last_signal_at`. The collector publishes them on every heartbeat, which is
  what lets a separate api process answer this at all.
- **`collector.reachable`** false means no collector has ever registered for
  this symbol — a deployment never started, which is not the same as one that
  died.
- **`outcomes.missing_outcome_rows`** above zero for longer than one follower
  pass means the follower is not opening them. A follower that has stopped has
  no other symptom.
- **`delivery.failed`** is the number that matters: nothing retries a failed
  row, so a permanently broken destination shows up here and nowhere else.
- **`delivery.pending`** means different things per mode. In `notify` a queue
  that does not drain is a broken worker; in `silent` nothing should be queued
  at all, so anything there was queued before delivery was switched off and
  will never be sent.

**`concerns` is a list, not a boolean.** "Healthy" is not answerable here, so
every entry carries the number that produced it rather than a verdict. It is
`[]` when there is nothing wrong — a missing field would read as a check that
did not run.

---

## GET /api/v1/stream (websocket)

Live push. Upgrade to a websocket; a plain GET is `426 Upgrade Required`.

```console
$ curl -s -i "$B/stream" | head -1
HTTP/1.1 426 Upgrade Required
```

| Parameter | Default | Notes |
|---|---|---|
| `topics` | all four | Comma-separated: `candles`, `signals`, `outcomes`, `status`. |
| `since` | none | `topic:sequence` pairs, comma-separated. |

Connect to:

```
ws://<host>:8080/api/v1/stream?topics=candles,signals,outcomes,status
```

The frames below were captured with a small client built on
`github.com/coder/websocket`, the same library the server uses. Any websocket
client will do — there is no subprotocol and nothing to send after connecting.

### The forming candle

**This is the only place in the system permitted to send a candle that has not
closed**, and `CLAUDE.md` §3.1 is the rule most easily broken here by accident.
Two things make it safe:

1. **Every candle carries `is_closed`**, and a forming one carries `false`. A
   client that ignores the flag is charting a price that can still change,
   which is legitimate for display and for nothing else.
2. **Nothing on the server computes from it.** The api's feed holds a market
   data client and nothing else — no repository, no candle usecase, no database
   handle — so there is nothing here that could persist a forming bar even by
   mistake. The guarantee is the absence of the capability, not a rule somebody
   has to remember.

The second is what a future change could quietly break, so it is enforced by
`TestNothingInTheStreamCanPersistACandle` and
`TestTheStreamsMarketFeedCanOnlyWatch` in `server/architecture_test.go`, which
fail on the import rather than on the behaviour.

Three forming updates of one bar and then its close, as they arrived:

```console
{"type":"subscribed","sent_at":"2026-08-23T05:45:40.047Z","topics":["candles"],
 "note":"The hub keeps no history, so missed events are not replayed. ..."}

{"type":"event","topic":"candles","sequence":14,"at":"2026-08-23T06:03:00Z",
 "data":{"open_time":"2026-08-23T06:03:00Z","close_time":"2026-08-23T06:03:59.999Z",
         "open":"64000","high":"64011","low":"63990","close":"64010","volume":"1.5",
         "is_closed":false}}

{"type":"event","topic":"candles","sequence":15,"at":"2026-08-23T06:03:00Z",
 "data":{...,"close":"64015","is_closed":false}}

{"type":"event","topic":"candles","sequence":16,"at":"2026-08-23T06:03:00Z",
 "data":{...,"close":"64020","is_closed":true}}

{"type":"event","topic":"candles","sequence":17,"at":"2026-08-23T06:04:00Z",
 "data":{"open_time":"2026-08-23T06:04:00Z",...,"is_closed":false}}
```

The candle in `data` is byte-for-byte the shape `GET /api/v1/candles` returns.
One renderer serves both, so `is_closed` cannot mean one thing on one transport
and something else on the other.

### Envelope

Every message has the same shape; branch on `type`.

| `type` | When | Fields |
|---|---|---|
| `subscribed` | Once, first | `topics`, `behind`, `note` |
| `event` | Per event | `topic`, `sequence`, `at`, `data` |
| `gap` | Before the next event after a drop | `dropped`, `note` |

`at` is the instant the event *describes*, not the instant it was sent — the
bar's open time, the signal's time. A cursor has to mean something about the
market rather than about delivery.

`sequence` is per topic. A candle cursor does not move because a signal was
published.

### Topics

| Topic | Payload | Cadence |
|---|---|---|
| `candles` | Same shape as `/candles` | Every kline the exchange sends |
| `signals` | Same shape as `/signals` (no `reason`) | Polled every 2s |
| `outcomes` | Same shape as `/outcomes` | Polled every 2s |
| `status` | Same shape as `/status` | Every 15s |

`signals` and `outcomes` are polled because the collector writes them and the
api serves them; they share a database and nothing else. The latency is honest
and visible — the event carries the signal's own time.

The first poll of each establishes a watermark without publishing. A client
connecting should not receive the whole history as though it had just happened.

```console
{"type":"event","topic":"signals","sequence":1,"at":"2024-03-06T04:00:00Z",
 "data":{"id":"6e414f25-...","direction":"short","signal_price":"30200",
         "entry_price":"29900.01","stop_loss":"30500","take_profit":"29600",...}}

{"type":"event","topic":"outcomes","sequence":1,"at":"2024-03-06T04:00:00Z",
 "data":{"signal_id":"6e414f25-...","status":"target","bars_held":3,
         "measurable":true,"resolved_price":"29600","mae":"120.5","mfe":"300.01",
         "net_return_pct":"0.9012"}}
```

### Reconnect

A phone drops constantly. On reconnect, send what you last saw:

Reconnecting to
`ws://127.0.0.1:8099/api/v1/stream?topics=candles&since=candles:68` after five
events had gone by:

```console
{"type":"subscribed","sent_at":"2026-08-23T05:51:32.449Z","topics":["candles"],
 "behind":{"candles":5},
 "note":"The hub keeps no history, so missed events are not replayed. `behind` is how many were issued while you were away — refetch that range over REST."}
```

`behind` is how many events were issued while you were away. **They are not
replayed.** The hub holds no history, deliberately: replaying a candle topic
from the beginning would be the entire series, and on a mobile connection the
connection would drop again before it finished. Refetch the affected range over
REST, which is the interface built for range queries.

### Backpressure

Each subscriber has a queue of 256. An event that would block is dropped and
counted, and the count is reported to that subscriber as a `gap` before the
next event — so a client's view has a marked hole rather than a silent one.

```json
{"type":"gap","sent_at":"...","dropped":12,
 "note":"events were dropped because this connection was not keeping up; refetch over REST if the gap matters"}
```

Publishing never blocks. Buffering without limit is the alternative and it is
worse: one stalled client would grow the server's memory until something died,
and the thing that died would not be the client.

The server pings every 20s and a write that stalls for 10s ends the connection.
A phone drops without closing, and a socket nobody writes to stays open long
after the client is gone.

### Errors

Parameter errors happen before the upgrade, so they arrive as ordinary HTTP:

```console
$ curl -s "$B/stream?topics=prices" | jq -c
{"error":{"code":"invalid_parameter","message":"topics=prices contains \"prices\", which is not a topic; the topics are candles, signals, outcomes, status"}}

$ curl -s "$B/stream?since=candles" | jq -c
{"error":{"code":"invalid_parameter","message":"since=candles is not a list of topic:sequence pairs"}}
```

---

## Endpoints outside /api/v1

Not part of the app's contract and not versioned.

| Path | Purpose |
|---|---|
| `GET /health` | Liveness. A literal; does not touch the database. |
| `GET /ready` | Readiness. Pings the database. |
| `GET /internal/market/status` | Ingestion detail per timeframe. Superseded for app purposes by `/api/v1/status`. |
| `GET /internal/signals/reconciliation` | Live signals against a backtest of the same strategy. Expensive — it replays history. Same report as `make reconcile`. See `docs/reading-a-divergence.md`. |

`/internal` is operational detail rather than anything a client should depend
on. `/health` and `/ready` are the only two paths `deploy/Caddyfile` would
proxy on a public hostname; everything else 404s there (ADR 0024).

---

## Cost of a request

Measured against the sandbox database, single connection, warm cache. Orders of
magnitude rather than benchmarks:

| Endpoint | Work |
|---|---|
| `/candles` | One indexed range scan. |
| `/indicators` | One range scan of `warmup_bars + window`, then an in-memory pass. A 4h EMA(200) reads about 1000 extra bars per request. |
| `/signals`, `/outcomes` | One page plus one count. |
| `/performance` | Aggregates every outcome in the window. Seconds on a year. |
| `/status` | Three queries and the collector's status row. |
| `/internal/signals/reconciliation` | Replays history through the backtest engine. Not a page to poll. |

`/indicators` is the one to watch: it is the only endpoint whose cost is set by
the indicator periods rather than by what was asked for.
