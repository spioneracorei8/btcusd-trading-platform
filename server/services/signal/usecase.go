package signal

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// SignalUsecase holds the rules that apply to emitting a signal.
type SignalUsecase interface {
	// CreateSignal records a strategy decision, refusing a duplicate so the
	// owner is never notified twice for the same candle.
	//
	// It also refuses a signal that did not come from a closed bar. That rule
	// lives here rather than at the call site for the same reason the
	// closed-candle rule lives in candle.CandleUsecase: it is a statement
	// about what the system may act on, and a rule enforced only where
	// somebody remembered to enforce it is not enforced.
	CreateSignal(ctx context.Context, signal models.Signal, bar models.Candle) (models.Signal, error)
}
