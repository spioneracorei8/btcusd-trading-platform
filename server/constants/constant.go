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
