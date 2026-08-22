package signal

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// SignalEvaluator turns closed candles into recorded signals.
//
// # One code path, live and backtest
//
// The strategy sees exactly what the backtest engine shows it: a
// strategy.BarContext built from the closed candle and the indicator values at
// its close, from the same indicator Set, through the same OnBar call. There
// is no mode flag anywhere below this interface and nothing a strategy could
// read to tell which one it is running in.
//
// That is not tidiness. Phase 07 exists to compare live outcomes against
// backtest predictions, and the moment the two use different code the
// comparison measures the difference between the code paths instead of the
// difference between prediction and reality.
//
// # What it deliberately does not do
//
// It has no position, no equity and no fills. Those belong to the backtest
// engine, which is measuring a strategy's arithmetic over history; this is
// recording what the strategy said. A signal is advice, and this system never
// turns one into an order.
type SignalEvaluator interface {
	// Warmup replays stored history so the indicators and the strategy are
	// converged before the first live bar arrives.
	//
	// It emits nothing. The bars it replays have already happened, and a
	// notification about a bar that closed three months ago is noise at best
	// — see the implementation for what that costs.
	//
	// It is an optimisation, not a precondition. An evaluator with no history
	// to replay converges on live bars instead; it just spends real time doing
	// what a replay does at once.
	Warmup(ctx context.Context) error

	// OnClosedCandle evaluates one closed candle.
	//
	// It returns the signal it recorded, if any. A bar that produced no
	// decision, or that arrived before the strategy was ready, returns
	// ok=false and no error: silence is a normal outcome and not a failure.
	OnClosedCandle(ctx context.Context, bar models.Candle) (models.Signal, bool, error)

	// Ready reports whether the strategy may decide, and why not when it may
	// not. Silence must be explicable.
	//
	// It describes the evaluator now rather than how its warm-up went, so an
	// evaluator that started with nothing stored becomes ready once enough
	// live bars have closed through it.
	Ready() (bool, string)
}
