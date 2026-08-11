package usecase

import (
	"fmt"
	"math"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// FilterName and FilterVersion identify the scoring in every report.
//
// Bump the version whenever the scoring changes in any way that could move a
// number. A stored report is only comparable with another produced by the same
// version, and six weeks after a surprising result that is the only useful
// question to be able to answer.
const (
	FilterName    = "ema_rsi_mtf"
	FilterVersion = "v1"
)

type emaRSIFilter struct {
	config trend.Config
}

// NewFilterImpl builds the default multi-timeframe filter.
//
// It is deliberately crude. A complicated score nobody can reason about is
// worse than a simple one whose failures are obvious: when this vetoes
// something it should not have, the reason has to be readable from the
// per-timeframe scores in the report without opening the code.
func NewFilterImpl(config trend.Config) (trend.Filter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &emaRSIFilter{config: config}, nil
}

func (f *emaRSIFilter) Name() string    { return FilterName }
func (f *emaRSIFilter) Version() string { return FilterVersion }

// WarmupPeriod is zero here on purpose.
//
// The filter itself carries no state and needs no history: everything it
// scores arrives already warmed up or already marked not-ready. The warm-up
// that matters is the aligner's, measured in base bars, and it is reported
// separately because it is the one that is easy to underestimate by an order
// of magnitude.
func (f *emaRSIFilter) WarmupPeriod() int { return 0 }

// OnBar scores each contributing timeframe and aggregates.
func (f *emaRSIFilter) OnBar(bar trend.BarContext) trend.TrendState {
	state := trend.TrendState{
		Bias:         constants.BiasNeutral,
		PerTimeframe: make([]trend.TimeframeState, 0, len(f.config.Weights)),
	}

	var (
		weighted float64
		notReady []constants.Timeframe
		missing  []constants.Timeframe
	)
	totalWeight := f.config.TotalWeight()

	for _, weight := range f.config.Weights {
		view, ok := bar.ViewFor(weight.Timeframe)
		if !ok {
			missing = append(missing, weight.Timeframe)
			state.PerTimeframe = append(state.PerTimeframe, trend.TimeframeState{
				Timeframe: weight.Timeframe,
				Weight:    weight.Weight,
			})
			continue
		}

		score := scoreTimeframe(view)
		state.PerTimeframe = append(state.PerTimeframe, trend.TimeframeState{
			Timeframe: weight.Timeframe,
			Score:     score,
			Weight:    weight.Weight,
			CloseTime: view.CloseTime,
			Ready:     view.Ready,
		})

		if !view.Ready {
			notReady = append(notReady, weight.Timeframe)
			continue
		}
		weighted += score * weight.Weight
	}

	// Every contributor must be ready. A partial aggregate would be a real
	// number computed from an incomplete opinion, and nothing downstream could
	// tell it apart from a complete one.
	switch {
	case len(missing) > 0:
		state.NotReadyReason = fmt.Sprintf("no candles yet for %v", missing)
		return state
	case len(notReady) > 0:
		state.NotReadyReason = fmt.Sprintf("warming up or recovering from a gap: %v", notReady)
		return state
	}

	state.Ready = true

	normalised := 0.0
	if totalWeight > 0 {
		normalised = weighted / totalWeight
	}
	normalised = clamp(normalised, -1, 1)

	// The dead zone. Inside it the answer is Neutral and the confidence is
	// zero: not "a weak opinion" but "no permission", which is what phase 06
	// must act on.
	if math.Abs(normalised) < f.config.DeadZone {
		state.Bias = constants.BiasNeutral
		state.Confidence = 0
		return state
	}

	if normalised > 0 {
		state.Bias = constants.BiasBullish
	} else {
		state.Bias = constants.BiasBearish
	}
	state.Confidence = math.Abs(normalised)
	return state
}

// scoreTimeframe derives one timeframe's directional reading in [-1, +1].
//
// Three components, equally weighted, each answering a different question:
//
//   - price against EMA: where is price relative to its own trend
//   - price against VWAP: where is price relative to where volume traded
//   - RSI against 50: is momentum leaning up or down
//
// Each is squashed rather than thresholded, so a marginal reading contributes
// marginally instead of snapping to ±1. That is what lets the dead zone do its
// job: with hard thresholds the aggregate would already be quantised and a
// band around zero would catch almost nothing.
//
// ATR is used as the scale for the price comparisons rather than as a fourth
// component. A move of one ATR means something different at $27,000 than at
// $27, and a percentage would be the wrong denominator in a volatile hour.
func scoreTimeframe(view trend.TimeframeView) float64 {
	closePrice, _ := view.Candle.Close.Float64()
	if closePrice == 0 {
		return 0
	}

	// Scale: one ATR, falling back to a small fraction of price when ATR is
	// unusable, so a flat or degenerate series does not divide by zero.
	scale := view.Indicators.ATR
	if scale <= 0 || math.IsNaN(scale) {
		scale = closePrice * 0.001
	}

	emaScore := squash((closePrice - view.Indicators.EMA) / scale)
	vwapScore := squash((closePrice - view.Indicators.VWAP) / scale)
	rsiScore := squash((view.Indicators.RSI - 50) / 10)

	return clamp((emaScore+vwapScore+rsiScore)/3, -1, 1)
}

// squash maps an unbounded reading into (-1, +1) smoothly.
//
// tanh rather than a threshold: it is monotonic, symmetric about zero, and
// saturates gently, so a reading twice as strong scores higher without a
// reading marginally above some cutoff scoring the same as an overwhelming one.
func squash(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Tanh(v)
}

// clamp bounds v to [low, high].
func clamp(v, low, high float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(low, math.Min(high, v))
}
