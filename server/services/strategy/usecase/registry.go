package usecase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// Registered is one strategy the CLI can run.
type Registered struct {
	Name string

	// Defaults returns a **pointer** to a fresh configuration at its
	// documented defaults. A pointer, and fresh each call, because callers
	// write parameter overrides into it and two runs must not share one.
	Defaults func() any

	// BuildFrom constructs the strategy from a configuration Defaults
	// produced.
	//
	// It returns an error rather than a value because a configuration whose
	// reward cannot clear the round trip must fail loudly at construction —
	// and because that is now the *only* place a parameter is validated. The
	// --param mechanism parses values and nothing more, so there is one set of
	// rules rather than two that can disagree.
	//
	// longOnly comes from the market type: a spot account cannot short, and a
	// two-sided rule that tried would end the run on its first bearish signal.
	BuildFrom func(config any, roundTripCostPct float64, longOnly bool) (strategy.Strategy, error)
}

// Build constructs the strategy at its documented defaults.
func (r Registered) Build(roundTripCostPct float64, longOnly bool) (strategy.Strategy, error) {
	return r.BuildFrom(r.Defaults(), roundTripCostPct, longOnly)
}

// BuildWith constructs the strategy with parameter overrides applied.
//
// It returns the configuration alongside the strategy so the caller can report
// what actually differs from the defaults. A run whose parameters are not
// recorded is not reproducible, and the experiment log's whole value is that it
// can be trusted months later.
func (r Registered) BuildWith(
	overrides map[string]string,
	roundTripCostPct float64,
	longOnly bool,
) (strategy.Strategy, any, error) {
	config := r.Defaults()
	if err := helper.ApplyParams(config, overrides); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", r.Name, err)
	}

	strat, err := r.BuildFrom(config, roundTripCostPct, longOnly)
	if err != nil {
		return nil, nil, err
	}
	return strat, config, nil
}

// Params lists the strategy's settable parameters.
func (r Registered) Params() ([]helper.ParamSpec, error) {
	return helper.DescribeParams(r.Defaults())
}

// Describe renders the configuration for a report header.
//
// Derived from the parameter descriptors rather than written out by hand.
// The hand-written version listed each parameter and its default in a format
// string, which is a second copy of the same facts and drifts from the first
// the moment a default changes — the class of mistake this whole mechanism
// exists to remove.
func (r Registered) Describe() string {
	specs, err := helper.DescribeParams(r.Defaults())
	if err != nil {
		return "(parameters could not be described: " + err.Error() + ")"
	}

	parts := make([]string, 0, len(specs))
	for _, spec := range specs {
		parts = append(parts, spec.Name+"="+spec.Default)
	}
	return strings.Join(parts, " ")
}

// registry is a slice, not a map.
//
// Map iteration is randomised, and this list is printed by --list-strategies
// and iterated when building comparison runs. A map here would reorder output
// between runs, which is the same determinism problem ADR 0012 solved for
// reports.
var registry = []Registered{
	{
		Name:     "ema_crossover",
		Defaults: func() any { c := DefaultEMACrossoverConfig(); return &c },
		BuildFrom: func(config any, cost float64, longOnly bool) (strategy.Strategy, error) {
			typed, ok := config.(*EMACrossoverConfig)
			if !ok {
				return nil, fmt.Errorf("ema_crossover: wrong configuration type: got a %T", config)
			}
			typed.RoundTripCostPct = cost
			typed.LongOnly = longOnly
			return NewEMACrossoverImpl(*typed)
		},
	},
	{
		Name:     "rsi_reversion",
		Defaults: func() any { c := DefaultRSIReversionConfig(); return &c },
		BuildFrom: func(config any, cost float64, longOnly bool) (strategy.Strategy, error) {
			typed, ok := config.(*RSIReversionConfig)
			if !ok {
				return nil, fmt.Errorf("rsi_reversion: wrong configuration type: got a %T", config)
			}
			typed.RoundTripCostPct = cost
			typed.LongOnly = longOnly
			return NewRSIReversionImpl(*typed)
		},
	},
	{
		Name:     "trend_pullback",
		Defaults: func() any { c := DefaultTrendPullbackConfig(); return &c },
		BuildFrom: func(config any, cost float64, longOnly bool) (strategy.Strategy, error) {
			typed, ok := config.(*TrendPullbackConfig)
			if !ok {
				return nil, fmt.Errorf("trend_pullback: wrong configuration type: got a %T", config)
			}
			typed.RoundTripCostPct = cost
			typed.LongOnly = longOnly
			return NewTrendPullbackImpl(*typed)
		},
	},
	{
		Name:     "mtf_alignment",
		Defaults: func() any { c := DefaultMTFAlignmentConfig(); return &c },
		BuildFrom: func(config any, cost float64, longOnly bool) (strategy.Strategy, error) {
			typed, ok := config.(*MTFAlignmentConfig)
			if !ok {
				return nil, fmt.Errorf("mtf_alignment: wrong configuration type: got a %T", config)
			}
			typed.RoundTripCostPct = cost
			typed.LongOnly = longOnly
			return NewMTFAlignmentImpl(*typed)
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
