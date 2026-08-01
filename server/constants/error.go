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
