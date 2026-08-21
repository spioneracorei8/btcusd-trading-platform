// Package usecase holds the strategy implementations.
//
// # These are experiments, not recommendations
//
// Nobody knows which rules work. Each one here is a widely-used pattern, and
// most of them will fail at 1m-5m once costs are applied. Their value is that
// they fail in legible ways: three structurally different rules —
// trend-following, counter-trend, trend-continuation — say something about the
// market rather than about parameter choices.
//
// The number to judge every one of them against is the round trip: 0.05% taker
// each way, 0.1% before slippage. A strategy averaging 0.15% gross a trade
// keeps a third of it.
package usecase

import (
	"fmt"
	"math"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// EMACrossoverConfig parameterises the crossover strategy.
//
// Every value is here rather than in the logic. A magic number in a rule is
// invisible to the parameter-neighbourhood report, which is what would have
// caught it being a fitted artefact.
type EMACrossoverConfig struct {
	// FastPeriod and SlowPeriod are the two EMAs whose crossing is the signal.
	FastPeriod int `param:"fast,step=1"`
	SlowPeriod int `param:"slow,step=1"`

	Levels strategy.Levels `param:",inline"`

	// RoundTripCostPct is what the configuration is validated against, in
	// percent. It is passed in rather than assumed so a different fee tier
	// changes what is accepted.
	RoundTripCostPct float64 `param:"-"`

	// LongOnly suppresses short entries.
	//
	// It is set from the market type at construction, not read from the bar:
	// a spot account cannot short, and that is a property of the instrument
	// rather than a mode the strategy is running in. The engine still refuses
	// a short on spot outright — this is what stops a two-sided rule from
	// tripping that refusal on its first bearish cross and taking the run down
	// with it.
	LongOnly bool `param:"-"`
}

// DefaultEMACrossoverConfig is the documented starting point.
//
// 9 and 21 are the conventional fast pair for intraday work. They are not
// tuned and must not be tuned against the evaluation data — the
// parameter-neighbourhood report exists to show whether a nearby pair behaves
// the same, which is the only thing that would make either believable.
//
// The 1.5/3.0 ATR levels give a reward-to-risk of 2. That is deliberately
// generous: at a 2:1 payoff a trend-following rule can be wrong twice as often
// as it is right and still break even before costs, which is roughly what
// crossover systems actually do.
func DefaultEMACrossoverConfig() EMACrossoverConfig {
	return EMACrossoverConfig{
		FastPeriod:       9,
		SlowPeriod:       21,
		Levels:           strategy.Levels{StopATRMult: 1.5, TargetATRMult: 3.0},
		RoundTripCostPct: 0.1,
	}
}

// Validate rejects a configuration that cannot work.
func (c EMACrossoverConfig) Validate() error {
	if c.FastPeriod < 2 {
		return fmt.Errorf("ema_crossover: fast period %d is below 2", c.FastPeriod)
	}
	if c.SlowPeriod <= c.FastPeriod {
		return fmt.Errorf("ema_crossover: slow period %d does not exceed fast period %d",
			c.SlowPeriod, c.FastPeriod)
	}
	return c.Levels.Validate(c.RoundTripCostPct)
}

// emaCrossover enters when the fast EMA crosses the slow one.
//
// # Known weakness
//
// It whipsaws badly in chop, and it fires often. Frequency is expensive here:
// at 0.1% a round trip, a rule that trades every few hours needs a real edge
// per trade rather than a small one. Expect the trend filter to help this one
// more than any other on the list, and expect it to still struggle.
//
// # Why it holds its own EMAs
//
// The engine supplies one indicator snapshot per bar, at the configured
// periods. A strategy needing different periods computes them itself, from the
// same closed bars, bar by bar — exactly as it would live. There is no branch
// that behaves differently in a replay.
type emaCrossover struct {
	config EMACrossoverConfig

	fast *ema
	slow *ema

	// previousFastAboveSlow is what makes a *crossing* detectable rather than
	// merely a state. Nil until both EMAs have a value, so the first bar after
	// warm-up cannot be mistaken for a cross.
	previousFastAboveSlow *bool
}

// NewEMACrossoverImpl builds the crossover strategy.
func NewEMACrossoverImpl(config EMACrossoverConfig) (strategy.Strategy, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &emaCrossover{
		config: config,
		fast:   newEMA(config.FastPeriod),
		slow:   newEMA(config.SlowPeriod),
	}, nil
}

func (s *emaCrossover) Name() string    { return "ema_crossover" }
func (s *emaCrossover) Version() string { return "v1" }

// WarmupPeriod follows ADR 0007: five times the longest period, so the slow
// EMA has forgotten its seed before anything is traded on it.
func (s *emaCrossover) WarmupPeriod() int { return 5 * s.config.SlowPeriod }

func (s *emaCrossover) OnBar(bar strategy.BarContext) []strategy.Intent {
	closePrice, _ := bar.Candle.Close.Float64()

	fast, fastOK := s.fast.update(closePrice)
	slow, slowOK := s.slow.update(closePrice)
	if !fastOK || !slowOK {
		return nil
	}

	above := fast > slow
	previous := s.previousFastAboveSlow
	s.previousFastAboveSlow = &above

	// The first bar with both values is a state, not a crossing.
	if previous == nil || *previous == above {
		return nil
	}
	// One position at a time; the engine would drop a second entry anyway, but
	// asking for one it will refuse makes the veto counts misleading.
	if bar.Position.IsOpen() {
		return nil
	}

	atr := bar.Indicators.ATR
	if math.IsNaN(atr) || atr <= 0 {
		return nil
	}

	if above {
		return []strategy.Intent{strategy.EnterLong(
			s.config.Levels.StopFor(bar.Candle.Close, atr, true),
			s.config.Levels.TargetFor(bar.Candle.Close, atr, true),
			fmt.Sprintf("ema(%d) crossed above ema(%d)", s.config.FastPeriod, s.config.SlowPeriod),
		)}
	}
	if s.config.LongOnly {
		return nil
	}
	return []strategy.Intent{strategy.EnterShort(
		s.config.Levels.StopFor(bar.Candle.Close, atr, false),
		s.config.Levels.TargetFor(bar.Candle.Close, atr, false),
		fmt.Sprintf("ema(%d) crossed below ema(%d)", s.config.FastPeriod, s.config.SlowPeriod),
	)}
}
