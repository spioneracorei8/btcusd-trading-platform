package signal

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// SignalUsecase holds the rules that apply to emitting a signal.
type SignalUsecase interface {
	// CreateSignal records a strategy decision, refusing a duplicate so the
	// owner is never notified twice for the same candle.
	CreateSignal(ctx context.Context, signal models.Signal) (models.Signal, error)
}
