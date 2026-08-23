package constants

import "time"

// Defaults applied when an optional environment variable is unset.
const (
	DefaultMarketSymbol     = "BTCUSDT"
	DefaultMarketTimeframes = "1m,5m,15m,1h"

	// DefaultFeeTakerPct and DefaultSlippageTicks are only the starting
	// point: trading costs are configuration, never constants baked into
	// calculation code, because a backtest that does not subtract them
	// reports numbers that cannot be acted on.
	DefaultFeeTakerPct   = "0.05"
	DefaultSlippageTicks = 1

	// DefaultFeeMakerPct is what a resting order pays instead. It is roughly
	// 40% of the taker rate, which makes the order type the single largest
	// lever on this system's economics: a round trip costs 0.04% at maker
	// rates against 0.1% at taker rates.
	//
	// It buys nothing on its own. A limit order only pays this fee when it
	// fills, and it only fills if price comes to it.
	DefaultFeeMakerPct = "0.02"

	// DefaultEntryOrderType and DefaultExitOrderType reproduce the behaviour
	// every completed evaluation was run under. Market is also the
	// conservative choice — it always fills and pays the higher fee — so an
	// unset order type cannot flatter a result.
	DefaultEntryOrderType = "market"
	DefaultExitOrderType  = "market"

	// The venue model. Defaults describe an IUX Standard account trading
	// BTCUSD as a CFD, but they only take effect under COST_MODEL=spread —
	// the default model is percentage, so a run that mentions none of this
	// prices exactly as it always did.
	//
	// A quoted spread of 25 USD is 2500 points at 0.01 USD of price per point.
	// One lot is one BTC, so a round trip at the 0.01 lot minimum costs
	// 25 x 0.01 = 0.25 USD whatever the price level — against 0.63 USD for the
	// same size on Binance at 63,000.
	// SignalStrengthNotReported is the strength recorded when a strategy does
	// not report one.
	//
	// The column is NOT NULL and every strategy here emits a decision rather
	// than a confidence, so some value has to go in. Zero is chosen because no
	// strategy would ever report zero confidence *and* emit an entry, which
	// makes it unambiguous — and because a plausible-looking 50 would invite
	// somebody to average it.
	SignalStrengthNotReported = 0

	// DefaultStrategyTimeframe is the base timeframe the live signal path
	// decides on when none is configured.
	DefaultStrategyTimeframe = "4h"

	// NotificationMaxAttempts is how many times one notification is tried
	// before it is given up on.
	//
	// A push notification about a scalping signal has a short useful life. A
	// row still retrying an hour later is not going to help anybody, and the
	// budget exists so a permanently broken destination cannot hold the queue
	// behind it forever.
	NotificationMaxAttempts = 5

	// NotifyRetryBase is the first retry delay; each further attempt doubles
	// it. Five attempts therefore span about eight minutes, which outlasts a
	// brief outage without outlasting the signal's usefulness.
	NotifyRetryBase = 30 * time.Second

	// NotifySendTimeout bounds one delivery. A send that hangs would hold the
	// worker and everything queued behind it.
	NotifySendTimeout = 10 * time.Second

	// NotifyBatchSize is how many due notifications one pass takes.
	NotifyBatchSize = 20

	// NotifyErrorBodyLimit bounds how much of a rejection is kept.
	// last_error is shown to a person; an unbounded response would put an
	// arbitrary amount of somebody else's text into it.
	NotifyErrorBodyLimit = 2048

	// DefaultNotifyInterval is how often the delivery queue is swept.
	DefaultNotifyInterval = 10 * time.Second

	// DefaultSignalExpiryBars is how long a signal is followed before it is
	// recorded as expired.
	//
	// Forty-eight bars: eight hours on 10m, eight days on 4h. Long enough
	// that a trade with room to work is not cut off by the clock, short
	// enough that a signal nobody could still act on stops being counted as
	// live. It is a measurement window and not a trading rule — nothing here
	// places or closes anything — so the only cost of it being wrong is that
	// some outcomes read "expired" where a longer window would have resolved
	// them, which the reconciliation reports rather than hides.
	DefaultSignalExpiryBars = 48

	// DefaultOutcomeInterval is how often open signals are followed against
	// newly stored candles.
	DefaultOutcomeInterval = time.Minute

	// OutcomeBatchSize is how many open signals one resolution pass takes.
	OutcomeBatchSize = 50

	// DefaultInitialEquity is the starting balance a run is scored against,
	// in quote currency.
	//
	// It matters to the reconciliation only through position sizing: the two
	// sides must be sized the same way or their average wins are not
	// comparable. It is the backtest CLI's own default, so a reconciliation
	// and a hand-run backtest agree without anybody passing a flag.
	DefaultInitialEquity = "10000"

	// APIVersion is the path segment every endpoint sits under.
	//
	// Versioned from the first release so phase 09's app can keep working
	// while the shape changes: a deployed phone cannot be redeployed with the
	// server.
	APIVersion = "v1"

	// APICandleLimit is the most candles one request may return, and
	// APICandleLimitDefault what it returns when no limit is asked for.
	//
	// A phone asking for three years of 1m candles is asking for 1.5 million
	// rows. Refusing that clearly is better than serving it slowly: the
	// request would time out somewhere, and where it timed out would be the
	// thing that got investigated.
	APICandleLimit        = 5000
	APICandleLimitDefault = 500

	// APIPageLimit and APIPageLimitDefault bound the list endpoints.
	APIPageLimit        = 500
	APIPageLimitDefault = 50

	// APIStalePipelineBars is how many bars of the strategy's own timeframe
	// may pass with no signal before /status says the pipeline looks quiet.
	//
	// It is not an alarm. A strategy at a tenth of a signal a day is silent
	// for weeks by design, and the endpoint reports the age rather than
	// judging it — but a number with no reference point is one nobody can
	// act on.
	APIStalePipelineBars = 200

	// StreamFeedMaxBackoff bounds the api display feed's reconnect wait. It
	// is not the ingestion path: a gap here costs a redraw, not a hole in the
	// candle series.
	StreamFeedMaxBackoff = 30 * time.Second

	// StreamPollInterval is how often the api looks for new signals and
	// outcomes to push.
	//
	// The collector writes them and the api serves them, sharing a database
	// and nothing else, so a poll is the whole mechanism. Two seconds is
	// under human reaction time for an alert whose bar closed minutes ago.
	StreamPollInterval = 2 * time.Second

	// StreamStatusInterval is how often the pipeline status is pushed. Slow,
	// because it is a health view rather than a feed and its query touches
	// three tables.
	StreamStatusInterval = 15 * time.Second

	// StreamPollBatch bounds one poll.
	StreamPollBatch = 50

	// StreamQueueSize is how many events a subscriber may fall behind before
	// they are dropped and counted.
	//
	// Dropping is deliberate: buffering without limit means one stalled phone
	// grows the server's memory until something dies, and the thing that dies
	// is not the phone.
	StreamQueueSize = 256

	// StreamPingInterval is how often the server pings an idle connection.
	// A phone on a mobile network drops without closing, and a socket nobody
	// writes to can stay open for hours after the client is gone.
	StreamPingInterval = 20 * time.Second

	// StreamWriteTimeout bounds one write to one client.
	StreamWriteTimeout = 10 * time.Second

	// ReconcileMinResolved is how many resolved signals a group needs before
	// its numbers are treated as saying anything.
	//
	// A hundred, and the report says so rather than leaving the reader to
	// judge. At the 4h strategy's rate of about a tenth of a trade a day that
	// is nearly three years — which is better known up front than discovered
	// after acting on twenty trades. If the wait is unacceptable the answer is
	// a higher-frequency strategy, not a smaller sample.
	ReconcileMinResolved = 100

	// ReconcileWinRateTolerance is how far below the backtest's win rate the
	// live one has to fall before it is called a divergence, as a fraction.
	//
	// Ten points. Below that, the difference between a hundred-signal sample
	// and a backtest over years is ordinary variation, and firing on it would
	// train the reader to ignore the report.
	ReconcileWinRateTolerance = 0.10

	// ReconcileEntryTolerancePct is how far the live average entry may sit
	// from the backtest's before slippage is suspected, in percent.
	ReconcileEntryTolerancePct = 0.05

	// ReconcileWinSizeTolerance is the share of the backtest's average win
	// below which the live one is called smaller.
	ReconcileWinSizeTolerance = 0.80

	// ReconcileSignalCountTolerance is the share of the engine's signal count
	// below which the live count is called short.
	ReconcileSignalCountTolerance = 0.80

	// DefaultSignalMode records signals and delivers nothing.
	//
	// Silent is the default deliberately. Beginning to send alerts should be a
	// decision somebody made, not something that happened because a deploy
	// went out.
	DefaultSignalMode = "silent"

	DefaultCostModel        = "percentage"
	DefaultSpreadPoints     = 2500
	DefaultPointValue       = "0.01"
	DefaultContractSize     = "1"
	DefaultMinLot           = "0.01"
	DefaultLotStep          = "0.01"
	DefaultCommissionPerLot = "0"

	// DefaultMaxLeverage is a cash account: a position may be no larger than
	// the balance can pay for outright.
	//
	// One, and not the venue's maximum, because an unstated leverage must
	// never silently make positions larger than the run before it. A margin
	// venue is opted into.
	DefaultMaxLeverage = "1"

	// DefaultLimitOrderTimeoutBars is how long an unfilled limit order rests
	// before it is cancelled. One bar means the order gets a single bar of
	// opportunity, which is the same window a market entry would have used.
	DefaultLimitOrderTimeoutBars = 1

	// DefaultMarketTickSize is the smallest price increment of the
	// instrument, which is what turns DefaultSlippageTicks into money.
	//
	// 0.01 is the BTCUSDT spot tick. It is configuration rather than a
	// constant for the same reason the fee is: a different symbol or the
	// futures book has a different tick, and a backtest that silently used
	// the wrong one would misprice every fill it simulated.
	DefaultMarketTickSize = "0.01"

	DefaultDatabaseMaxConns = 10

	// Binance public market-data endpoints. No API key is used anywhere in
	// this system, and no key with trading rights may ever be introduced.
	DefaultBinanceRESTBaseURL = "https://api.binance.com"
	DefaultBinanceWSBaseURL   = "wss://stream.binance.com:9443"

	// DefaultMarketBackfillFrom is how far back history is fetched for a
	// timeframe that has no stored candle at all.
	DefaultMarketBackfillFrom = "2023-01-01T00:00:00Z"
)

// Ingestion tuning.
const (
	// DefaultGapcheckInterval is how often the candle series is scanned for
	// holes.
	DefaultGapcheckInterval = 15 * time.Minute

	// DefaultHeartbeatInterval is how often the collector writes its status
	// row for the api to read.
	DefaultHeartbeatInterval = 5 * time.Second

	// KlineLimit is the maximum number of klines Binance returns per REST
	// call. Paging is built around this number.
	KlineLimit = 1000

	// UpsertBatchSize is how many candles are written per database round trip
	// during backfill. A row-at-a-time loop over years of 1m candles is not
	// acceptable.
	UpsertBatchSize = 1000

	// CandlePageSize is how many candles a keyset scan reads per round trip
	// when replaying a series. It trades round trips against resident memory:
	// at this size a backtest over years of 1m candles holds kilobytes rather
	// than the hundreds of megabytes the whole series would occupy.
	CandlePageSize = 1000

	// ClosedCandleBufferSize bounds the channel between the WebSocket reader
	// and the writer. When it fills the reader blocks: dropping a candle
	// silently is the worst failure this system can have.
	ClosedCandleBufferSize = 1000

	// StreamStallTimeout is how long the WebSocket may stay silent before it
	// is treated as dead. A stalled-but-open connection is the failure mode
	// that quietly corrupts data, so it is never waited out.
	StreamStallTimeout = 3 * time.Minute

	// StaleCandleThreshold is how old the latest 1m candle may get while the
	// stream still reports itself connected. Beyond it, something is wrong in
	// a way no other check catches.
	StaleCandleThreshold = 3 * time.Minute

	// MaxGapFillAttempts is how many times an unfilled gap is retried before
	// it is left alone. Some ranges genuinely do not exist.
	MaxGapFillAttempts = 3
)

// Reconnect backoff. Jitter keeps a fleet of clients from retrying in
// lockstep after an exchange-side outage.
const (
	BackoffInitial = time.Second
	BackoffMax     = 60 * time.Second
	BackoffFactor  = 2
	BackoffJitter  = 0.2
)

// Binance rate limiting. The published spot cap is 6000 weight per minute;
// this system stays far below it, but the header is still honoured so a
// backfill cannot creep up on the limit.
const (
	// UsedWeightHeader carries the weight consumed in the current minute.
	UsedWeightHeader = "X-MBX-USED-WEIGHT-1M"

	// RetryAfterHeader is sent with 429 and 418 responses.
	RetryAfterHeader = "Retry-After"

	// WeightLimitPerMinute is the cap the client backs off before reaching.
	WeightLimitPerMinute = 6000

	// WeightSoftLimitRatio is the fraction of the cap at which the client
	// starts pausing rather than waiting to be told off.
	WeightSoftLimitRatio = 0.8

	// RateLimitCooldown is how long the client waits after a 429 or 418 that
	// carries no Retry-After header.
	RateLimitCooldown = time.Minute
)

// Timeouts used by the HTTP server and the database pool.
const (
	DefaultConnectTimeout = 5 * time.Second

	// ShutdownTimeout bounds how long in-flight requests may finish after a
	// SIGTERM or SIGINT before the process exits anyway.
	ShutdownTimeout = 10 * time.Second

	// ReadyCheckTimeout bounds the database ping behind /ready so a hung
	// database cannot make the readiness probe itself hang.
	ReadyCheckTimeout = 2 * time.Second

	HTTPReadHeaderTimeout = 5 * time.Second
	HTTPReadTimeout       = 15 * time.Second
	HTTPWriteTimeout      = 30 * time.Second
	HTTPIdleTimeout       = 60 * time.Second

	PoolMaxConnLifetime = time.Hour
	PoolMaxConnIdleTime = 30 * time.Minute
)

// Indicator warm-up.
const (
	// WarmupMultiplier scales an indicator's period into the number of
	// candles required before it emits.
	//
	// EMA and Wilder-smoothed indicators never fully forget their seed, so an
	// EMA(200) fed exactly 200 candles still carries most of its arbitrary
	// starting mean. Emitting from there lets a backtest score its earliest
	// bars against unconverged numbers and report the result as history.
	// See docs/decisions/0007-indicator-warmup-multiplier.md.
	WarmupMultiplier = 5
)

// Health check payload values.
const (
	StatusOK          = "ok"
	StatusReady       = "ready"
	StatusUnavailable = "unavailable"
)

// PgUniqueViolation is the SQLSTATE PostgreSQL raises for a unique constraint.
const PgUniqueViolation = "23505"
