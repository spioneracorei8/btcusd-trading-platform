package usecase

import (
	"fmt"
	"math"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// TrendPullbackConfig parameterises the trend-continuation strategy.
type TrendPullbackConfig struct {
	// TrendPeriod is the EMA defining the trend the pullback is against.
	TrendPeriod int

	// PullbackATR is how close to the EMA price must come to count as a
	// pullback, in ATR. "Pullback" is the word this whole strategy turns on,
	// and a vague definition is how a rule becomes unfalsifiable — so it is a
	// number, in the market's own units, not a shape someone recognises.
	PullbackATR float64

	// ResumeBars is how many consecutive bars must move back in the trend's
	// direction before entering.
	//
	// Without it the rule buys every touch of the EMA, including the touch
	// that carries straight through it, which is what a trend ending looks
	// like. Waiting is what distinguishes a pullback from a reversal, at the
	// cost of a worse entry price.
	ResumeBars int

	Levels strategy.Levels

	RoundTripCostPct float64
}

// DefaultTrendPullbackConfig is the documented starting point.
//
// EMA(50) is slow enough that a 1m pullback to it is a real retracement rather
// than noise. Half an ATR is close enough to count as a touch without
// requiring an exact one. Two resume bars is the smallest number that is
// evidence rather than a single bar's wick.
//
// Reward-to-risk is 2.5, the highest of the three: this rule trades least
// often, so each trade has to carry more. Fewer, better trades is the entire
// premise, and if it turns out to trade often the premise was wrong.
func DefaultTrendPullbackConfig() TrendPullbackConfig {
	return TrendPullbackConfig{
		TrendPeriod:      50,
		PullbackATR:      0.5,
		ResumeBars:       2,
		Levels:           strategy.Levels{StopATRMult: 1.2, TargetATRMult: 3.0},
		RoundTripCostPct: 0.1,
	}
}

// Validate rejects a configuration that cannot work.
func (c TrendPullbackConfig) Validate() error {
	if c.TrendPeriod < 2 {
		return fmt.Errorf("trend_pullback: trend period %d is below 2", c.TrendPeriod)
	}
	if c.PullbackATR <= 0 {
		return fmt.Errorf("trend_pullback: pullback distance %v ATR is not positive", c.PullbackATR)
	}
	if c.ResumeBars < 1 {
		return fmt.Errorf("trend_pullback: resume bars %d is below 1", c.ResumeBars)
	}
	return c.Levels.Validate(c.RoundTripCostPct)
}

// trendPullback enters on a retracement to the trend EMA, once price has begun
// moving again in the trend's direction.
//
// # Known weakness
//
// It trades rarely, which is its point and also its problem: the acceptance
// criteria require 200 trades on the development set, and a rule this
// selective may not produce them on a single year. Too few trades is not a
// small sample of a good strategy, it is no measurement at all.
//
// The other weakness is definitional. "Pullback" and "resume" are choices, and
// a nearby choice may behave very differently — which is exactly what the
// parameter-neighbourhood report is for.
type trendPullback struct {
	config TrendPullbackConfig
	trend  *ema

	// pulledBack records that price has come near the EMA and is waiting for
	// the move to resume. It is cleared when the trend side flips, because a
	// pullback in a trend that has since reversed is not a pullback.
	pulledBack     bool
	pullbackIsLong bool

	// resumeCount counts consecutive bars moving in the trend's direction
	// since the pullback.
	resumeCount    int
	previousClose  float64
	hasPreviousBar bool
}

// NewTrendPullbackImpl builds the trend-continuation strategy.
func NewTrendPullbackImpl(config TrendPullbackConfig) (strategy.Strategy, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &trendPullback{config: config, trend: newEMA(config.TrendPeriod)}, nil
}

func (s *trendPullback) Name() string    { return "trend_pullback" }
func (s *trendPullback) Version() string { return "v1" }

// WarmupPeriod follows ADR 0007 for the strategy's own EMA.
func (s *trendPullback) WarmupPeriod() int { return 5 * s.config.TrendPeriod }

func (s *trendPullback) OnBar(bar strategy.BarContext) []strategy.Intent {
	closePrice, _ := bar.Candle.Close.Float64()

	trendEMA, ok := s.trend.update(closePrice)
	if !ok {
		return nil
	}

	previousClose, hadPrevious := s.previousClose, s.hasPreviousBar
	s.previousClose, s.hasPreviousBar = closePrice, true

	atr := bar.Indicators.ATR
	if math.IsNaN(atr) || atr <= 0 || !hadPrevious {
		return nil
	}

	long := closePrice > trendEMA
	distance := math.Abs(closePrice - trendEMA)

	// A trend that has flipped side invalidates any pullback in progress.
	if s.pulledBack && s.pullbackIsLong != long {
		s.pulledBack = false
		s.resumeCount = 0
	}

	// Close to the EMA: the retracement has arrived. Waiting starts now.
	if distance <= s.config.PullbackATR*atr {
		s.pulledBack = true
		s.pullbackIsLong = long
		s.resumeCount = 0
		return nil
	}

	if !s.pulledBack {
		return nil
	}

	// Count consecutive bars moving with the trend since the pullback.
	movingWithTrend := (long && closePrice > previousClose) || (!long && closePrice < previousClose)
	if !movingWithTrend {
		s.resumeCount = 0
		return nil
	}
	s.resumeCount++

	if s.resumeCount < s.config.ResumeBars || bar.Position.IsOpen() {
		return nil
	}

	s.pulledBack = false
	s.resumeCount = 0

	side := "above"
	if !long {
		side = "below"
	}
	reason := fmt.Sprintf("pullback to ema(%d) then %d bars resuming %s it",
		s.config.TrendPeriod, s.config.ResumeBars, side)

	if long {
		return []strategy.Intent{strategy.EnterLong(
			s.config.Levels.StopFor(bar.Candle.Close, atr, true),
			s.config.Levels.TargetFor(bar.Candle.Close, atr, true),
			reason,
		)}
	}
	return []strategy.Intent{strategy.EnterShort(
		s.config.Levels.StopFor(bar.Candle.Close, atr, false),
		s.config.Levels.TargetFor(bar.Candle.Close, atr, false),
		reason,
	)}
}
