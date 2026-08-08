package constants

import "errors"

// Sentinel errors returned by the repository layer.
var (
	// ErrNotFound reports that a query matched no row.
	ErrNotFound = errors.New("not found")

	// ErrDuplicateSignal reports that a signal for the same strategy version
	// and candle already exists, so the owner must not be notified again.
	ErrDuplicateSignal = errors.New("signal already exists")

	// ErrUnclosedCandle reports an attempt to store a candle that is still
	// forming. Only closed candles may reach the strategies.
	ErrUnclosedCandle = errors.New("candle is not closed")
)

// Sentinel errors returned by the backtest engine.
var (
	// ErrDataIncomplete reports that the requested range has unfilled gaps
	// and the run refused to proceed. It is a refusal, not a fault: a number
	// computed over missing bars is worse than no number at all.
	ErrDataIncomplete = errors.New("candle data is incomplete for the requested range")

	// ErrShortOnSpot reports a short entry on a spot market. A spot backtest
	// that shorts is fiction, so this is a hard error rather than a warning.
	ErrShortOnSpot = errors.New("cannot short on a spot market")

	// ErrNoCandles reports that the requested range contains no stored
	// candles at all, so there was nothing to measure.
	ErrNoCandles = errors.New("no candles stored for the requested range")
)

// Sentinel errors returned by the market data client.
var (
	// ErrRateLimited reports an HTTP 429 or 418 from Binance. It is never
	// retried immediately; the caller must back off.
	ErrRateLimited = errors.New("rate limited by the exchange")

	// ErrStreamStalled reports that no message arrived within
	// StreamStallTimeout. The socket may still look open.
	ErrStreamStalled = errors.New("market data stream stalled")

	// ErrStreamClosed reports an ordinary disconnect, including the 24 hour
	// cycle Binance applies to every connection. It is expected, not a fault.
	ErrStreamClosed = errors.New("market data stream closed")

	// ErrUnexpectedPayload reports a message that does not match the shape the
	// client expects.
	ErrUnexpectedPayload = errors.New("unexpected market data payload")
)

// Sentinel errors returned while loading configuration.
var (
	// ErrMissingEnv reports a required environment variable that is unset or empty.
	ErrMissingEnv = errors.New("missing required environment variable")

	// ErrInvalidEnv reports an environment variable whose value could not be accepted.
	ErrInvalidEnv = errors.New("invalid environment variable")
)

// Messages returned to HTTP clients.
const (
	MsgInternalServerError = "internal server error"
	MsgDatabaseUnreachable = "database unreachable"
)
