package usecase

import (
	"fmt"
	"math"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// RSIReversionConfig parameterises the mean-reversion strategy.
type RSIReversionConfig struct {
	// Oversold and Overbought are the RSI bands. The signal is the *exit*
	// from a band, not being inside one: RSI can sit at 20 for hours while
	// price keeps falling, and buying the moment it arrives there is how a
	// counter-trend rule turns a drawdown into a catastrophe.
	Oversold   float64 `param:"oversold,step=5"`
	Overbought float64 `param:"overbought,step=5"`

	Levels strategy.Levels `param:",inline"`

	RoundTripCostPct float64 `param:"-"`

	// LongOnly suppresses short entries; see EMACrossoverConfig.LongOnly.
	LongOnly bool `param:"-"`
}

// DefaultRSIReversionConfig is the documented starting point.
//
// 30 and 70 are the conventional Wilder bands. The levels are tighter than the
// crossover's and reward-to-risk is lower, at 1.5: mean reversion wins more
// often and smaller, which is the shape of the bet. That also means it needs a
// genuinely high win rate to clear costs, and a high win rate on a
// counter-trend rule is exactly what tends to evaporate out of sample.
func DefaultRSIReversionConfig() RSIReversionConfig {
	return RSIReversionConfig{
		Oversold:         30,
		Overbought:       70,
		Levels:           strategy.Levels{StopATRMult: 1.0, TargetATRMult: 1.5},
		RoundTripCostPct: 0.1,
	}
}

// Validate rejects a configuration that cannot work.
func (c RSIReversionConfig) Validate() error {
	if c.Oversold <= 0 || c.Oversold >= 50 {
		return fmt.Errorf("rsi_reversion: oversold %v is outside (0, 50)", c.Oversold)
	}
	if c.Overbought <= 50 || c.Overbought >= 100 {
		return fmt.Errorf("rsi_reversion: overbought %v is outside (50, 100)", c.Overbought)
	}
	return c.Levels.Validate(c.RoundTripCostPct)
}

// rsiReversion buys the exit from oversold and sells the exit from overbought.
//
// # Known weakness
//
// It fights the trend, and that is not a detail — it is the whole risk. In a
// strong directional move RSI leaves oversold repeatedly on the way down, and
// each exit is an entry into a falling market. The stop is what stands between
// this rule and an unbounded loss, which is why the engine refuses to size a
// position without one.
//
// The trend filter should help here more than anywhere: its entire job is
// refusing entries against the dominant trend, and every one of this rule's
// worst trades is exactly that.
type rsiReversion struct {
	config RSIReversionConfig

	// previousRSI is what makes an *exit* from a band detectable. Being inside
	// a band is a state; leaving one is an event, and only the event is traded.
	previousRSI    float64
	hasPreviousRSI bool
}

// NewRSIReversionImpl builds the mean-reversion strategy.
func NewRSIReversionImpl(config RSIReversionConfig) (strategy.Strategy, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &rsiReversion{config: config}, nil
}

func (s *rsiReversion) Name() string    { return "rsi_reversion" }
func (s *rsiReversion) Version() string { return "v1" }

// WarmupPeriod is zero: the RSI it reads arrives already warmed up from the
// engine's indicator set, which withholds a value until ADR 0007's window has
// passed. Declaring a warm-up here as well would delay the strategy past the
// point where its input was already trustworthy.
func (s *rsiReversion) WarmupPeriod() int { return 0 }

func (s *rsiReversion) OnBar(bar strategy.BarContext) []strategy.Intent {
	rsi := bar.Indicators.RSI
	if math.IsNaN(rsi) {
		return nil
	}

	previous, had := s.previousRSI, s.hasPreviousRSI
	s.previousRSI, s.hasPreviousRSI = rsi, true

	if !had || bar.Position.IsOpen() {
		return nil
	}

	atr := bar.Indicators.ATR
	if math.IsNaN(atr) || atr <= 0 {
		return nil
	}

	switch {
	// Leaving oversold from below: the fall has stopped accelerating.
	case previous < s.config.Oversold && rsi >= s.config.Oversold:
		return []strategy.Intent{strategy.EnterLong(
			s.config.Levels.StopFor(bar.Candle.Close, atr, true),
			s.config.Levels.TargetFor(bar.Candle.Close, atr, true),
			fmt.Sprintf("rsi left oversold (%.1f -> %.1f, band %.0f)", previous, rsi, s.config.Oversold),
		)}

	// Leaving overbought from above.
	case previous > s.config.Overbought && rsi <= s.config.Overbought && !s.config.LongOnly:
		return []strategy.Intent{strategy.EnterShort(
			s.config.Levels.StopFor(bar.Candle.Close, atr, false),
			s.config.Levels.TargetFor(bar.Candle.Close, atr, false),
			fmt.Sprintf("rsi left overbought (%.1f -> %.1f, band %.0f)", previous, rsi, s.config.Overbought),
		)}
	}
	return nil
}
