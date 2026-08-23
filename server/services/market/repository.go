// Package market declares the contracts for ingesting exchange market data.
//
// The exchange client is a repository, not a new layer: it is the outbound
// edge of the system exactly as PostgreSQL is. Its DTOs stop at the
// repository implementation, so nothing downstream is shaped by Binance.
package market

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// FetchKlinesParams selects a page of historical candles.
type FetchKlinesParams struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframe  constants.Timeframe

	// From bounds the page on open_time, inclusive. To is optional; a zero
	// value means "up to now".
	From time.Time
	To   time.Time

	// Limit caps the page size. Zero means the exchange maximum.
	Limit int
}

// MarketDataRepository reads market data from the exchange.
//
// Only public market data is available through this interface. There is
// deliberately no method that could place, amend or cancel an order, and none
// may be added: this system's entire output is a signal and a notification.
type MarketDataRepository interface {
	// FetchKlines returns one page of closed candles, oldest first. A candle
	// that has not closed yet is never returned.
	FetchKlines(ctx context.Context, params FetchKlinesParams) ([]models.Candle, error)

	// StreamKlines opens one combined stream for every timeframe given and
	// delivers each kline to onKline until ctx is cancelled or the connection
	// fails. It returns constants.ErrStreamClosed for an ordinary disconnect
	// and constants.ErrStreamStalled when the connection goes silent.
	//
	// Both closed and unclosed klines are delivered; deciding what may be
	// stored is the usecase's job, not the transport's.
	StreamKlines(ctx context.Context, params StreamParams, onKline func(StreamedKline)) error

	// ServerTime returns the exchange clock, used to decide whether the last
	// candle of a REST page is still open.
	ServerTime(ctx context.Context) (time.Time, error)
}

// StreamParams describes the combined stream to open.
type StreamParams struct {
	Symbol     string
	MarketType constants.MarketType
	Timeframes []constants.Timeframe
}

// StreamedKline is one kline delivered by the live stream.
type StreamedKline struct {
	// Candle carries the values. IsClosed mirrors the exchange's own flag.
	Candle models.Candle
}

// CollectorStatusRepository persists what the collector knows about itself.
//
// The api serves /internal/market/status but runs in a different container,
// so it cannot read the collector's memory. This is the channel between them.
type CollectorStatusRepository interface {
	// RegisterStart records a fresh process start. It is the only call that
	// moves started_at, which is what makes a crash loop distinguishable from
	// genuine uptime.
	RegisterStart(ctx context.Context, symbol string, marketType constants.MarketType) (models.CollectorStatus, error)

	// Heartbeat bumps updated_at, leaving started_at alone, and publishes
	// what the live signal evaluator is doing.
	//
	// The evaluator's state rides on the heartbeat rather than having its own
	// write because the two answer one question — is this pipeline alive —
	// and two rows updated at different instants would invite the reader to
	// reconcile them.
	Heartbeat(ctx context.Context, symbol string, marketType constants.MarketType, wsConnected bool, evaluator models.EvaluatorState) error

	// MarkConnected records the stream coming up. reconnect reports whether
	// this was a reconnect rather than the first connection.
	MarkConnected(ctx context.Context, symbol string, marketType constants.MarketType, reconnect bool) error

	// MarkDisconnected records the stream going down, with the reason.
	MarkDisconnected(ctx context.Context, symbol string, marketType constants.MarketType, note string) error

	// SetState records a lifecycle transition.
	SetState(ctx context.Context, symbol string, marketType constants.MarketType, state constants.CollectorState) error

	// FetchStatus reads the current row, returning constants.ErrNotFound when
	// the collector has never started.
	FetchStatus(ctx context.Context, symbol string, marketType constants.MarketType) (models.CollectorStatus, error)
}
