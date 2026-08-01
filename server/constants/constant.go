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

// Health check payload values.
const (
	StatusOK          = "ok"
	StatusReady       = "ready"
	StatusUnavailable = "unavailable"
)

// PgUniqueViolation is the SQLSTATE PostgreSQL raises for a unique constraint.
const PgUniqueViolation = "23505"
