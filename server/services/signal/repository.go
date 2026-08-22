// Package signal declares the signal service contracts.
//
// A signal is advice for the owner: a direction, a strength and the indicator
// values behind it. The system never turns one into an order.
package signal

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// SignalRepository stores strategy decisions.
type SignalRepository interface {
	// InsertSignal stores one signal and returns the persisted row. It
	// returns constants.ErrDuplicateSignal when the same strategy version
	// already emitted a signal for that candle.
	InsertSignal(ctx context.Context, signal models.Signal) (models.Signal, error)

	// FetchSignalById returns one signal, or constants.ErrNotFound when no
	// row has that id.
	FetchSignalById(ctx context.Context, id uuid.UUID) (models.Signal, error)

	// SetEntryPrice fills in the entry price, only when it is still unset. It
	// returns constants.ErrNotFound when the signal is gone or already has
	// one, which the usecase distinguishes.
	SetEntryPrice(ctx context.Context, id uuid.UUID, entry decimal.Decimal) (models.Signal, error)
}
