package usecase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// Registered is one strategy the CLI can run.
type Registered struct {
	Name string

	// Describe renders the configuration for a report header, so a stored
	// result says what produced it and not only which name did.
	Describe func() string

	// Build constructs the strategy at its documented defaults.
	//
	// It returns an error rather than a value because a configuration whose
	// reward cannot clear the round trip must fail loudly at construction.
	//
	// longOnly comes from the market type: a spot account cannot short, and a
	// two-sided rule that tried would end the run on its first bearish signal.
	Build func(roundTripCostPct float64, longOnly bool) (strategy.Strategy, error)
}

// registry is a slice, not a map.
//
// Map iteration is randomised, and this list is printed by --list-strategies
// and iterated when building comparison runs. A map here would reorder output
// between runs, which is the same determinism problem ADR 0012 solved for
// reports.
var registry = []Registered{
	{
		Name: "ema_crossover",
		Describe: func() string {
			c := DefaultEMACrossoverConfig()
			return fmt.Sprintf("fast=%d slow=%d stop=%.2fATR target=%.2fATR",
				c.FastPeriod, c.SlowPeriod, c.Levels.StopATRMult, c.Levels.TargetATRMult)
		},
		Build: func(cost float64, longOnly bool) (strategy.Strategy, error) {
			config := DefaultEMACrossoverConfig()
			config.RoundTripCostPct = cost
			config.LongOnly = longOnly
			return NewEMACrossoverImpl(config)
		},
	},
	{
		Name: "rsi_reversion",
		Describe: func() string {
			c := DefaultRSIReversionConfig()
			return fmt.Sprintf("oversold=%.0f overbought=%.0f stop=%.2fATR target=%.2fATR",
				c.Oversold, c.Overbought, c.Levels.StopATRMult, c.Levels.TargetATRMult)
		},
		Build: func(cost float64, longOnly bool) (strategy.Strategy, error) {
			config := DefaultRSIReversionConfig()
			config.RoundTripCostPct = cost
			config.LongOnly = longOnly
			return NewRSIReversionImpl(config)
		},
	},
	{
		Name: "trend_pullback",
		Describe: func() string {
			c := DefaultTrendPullbackConfig()
			return fmt.Sprintf("trend=%d pullback=%.2fATR resume=%d stop=%.2fATR target=%.2fATR",
				c.TrendPeriod, c.PullbackATR, c.ResumeBars, c.Levels.StopATRMult, c.Levels.TargetATRMult)
		},
		Build: func(cost float64, longOnly bool) (strategy.Strategy, error) {
			config := DefaultTrendPullbackConfig()
			config.RoundTripCostPct = cost
			config.LongOnly = longOnly
			return NewTrendPullbackImpl(config)
		},
	},
}

// Names lists the registered strategies in a stable order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for _, entry := range registry {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	return names
}

// All returns every registered strategy, in registration order.
func All() []Registered {
	return append([]Registered(nil), registry...)
}

// Lookup resolves a strategy by name.
func Lookup(name string) (Registered, error) {
	for _, entry := range registry {
		if entry.Name == name {
			return entry, nil
		}
	}
	return Registered{}, fmt.Errorf("unknown strategy %q; this binary ships %s",
		name, strings.Join(Names(), ", "))
}
