package trend

import (
	"fmt"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// Weight is one timeframe's share of the aggregate score.
type Weight struct {
	Timeframe constants.Timeframe
	Weight    float64
}

// Config is the scoring configuration. It is data, not code, so that a run
// can record exactly what produced its numbers.
type Config struct {
	// Weights are the contributing timeframes and their shares, ordered
	// shortest first. A slice rather than a map, for the determinism reason
	// that applies everywhere in this system: Go randomises map iteration and
	// a backtest report must be byte-identical across runs.
	Weights []Weight

	// DeadZone is the band around zero reported as Neutral.
	//
	// Without one the filter flips on noise: in chop the weighted score
	// wanders across zero every few bars, the bias alternates, and a strategy
	// gated on it is permitted and forbidden in turn — which is worse than no
	// filter, because it adds cost without adding information.
	DeadZone float64
}

// DefaultConfig is the documented starting point for the 1m scalping setup.
//
// # These numbers are not tuned and must not be
//
// Phase 05 ships defaults chosen by reasoning, not by fitting. Tuning weights
// against the same data used to evaluate the result is how a filter is fitted
// to the past: it will look excellent on that data and mean nothing anywhere
// else. That work belongs in phase 06 with a proper train/test split, and
// touching it here contaminates the only clean data there is.
//
// The reasoning behind each:
//
//   - 1h carries the most weight because a scalping entry against the dominant
//     trend is the trade this filter exists to refuse. It is the strongest
//     veto by design.
//   - 5m and 15m confirm; they move often enough to be useful and often enough
//     to be noise, so neither outweighs the hourly on its own. 1h at 0.5
//     against 5m + 15m at 0.5 combined means the two shorter frames must agree
//     with each other *and* be decisive before they can outvote the hourly.
//   - The 0.15 dead zone is about a seventh of the range. It is wide enough to
//     absorb one timeframe disagreeing mildly and narrow enough that a genuine
//     alignment of all three clears it comfortably.
func DefaultConfig() Config {
	return Config{
		Weights: []Weight{
			{Timeframe: constants.Timeframe5m, Weight: 0.2},
			{Timeframe: constants.Timeframe15m, Weight: 0.3},
			{Timeframe: constants.Timeframe1h, Weight: 0.5},
		},
		DeadZone: 0.15,
	}
}

// Timeframes lists the configured contributors, in configuration order.
func (c Config) Timeframes() []constants.Timeframe {
	out := make([]constants.Timeframe, 0, len(c.Weights))
	for _, weight := range c.Weights {
		out = append(out, weight.Timeframe)
	}
	return out
}

// TotalWeight is the sum of the configured weights, used to normalise the
// aggregate into [-1, +1].
func (c Config) TotalWeight() float64 {
	total := 0.0
	for _, weight := range c.Weights {
		total += weight.Weight
	}
	return total
}

// Validate rejects a configuration that could not produce a meaningful score.
func (c Config) Validate() error {
	if len(c.Weights) == 0 {
		return fmt.Errorf("trend: no contributing timeframes configured")
	}

	seen := make(map[constants.Timeframe]struct{}, len(c.Weights))
	for _, weight := range c.Weights {
		if !weight.Timeframe.Valid() {
			return fmt.Errorf("trend: %q is not a timeframe", weight.Timeframe)
		}
		if _, dup := seen[weight.Timeframe]; dup {
			return fmt.Errorf("trend: timeframe %s is weighted twice", weight.Timeframe)
		}
		seen[weight.Timeframe] = struct{}{}

		if weight.Weight <= 0 {
			return fmt.Errorf("trend: weight %v for %s is not positive; drop the timeframe instead",
				weight.Weight, weight.Timeframe)
		}
	}

	if c.DeadZone < 0 || c.DeadZone >= 1 {
		return fmt.Errorf("trend: dead zone %v is outside [0, 1)", c.DeadZone)
	}
	return nil
}

// Describe renders the configuration for a report header, so a stored result
// says what produced it rather than only which version did.
func (c Config) Describe() string {
	out := ""
	for i, weight := range c.Weights {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%.2f", weight.Timeframe, weight.Weight)
	}
	return fmt.Sprintf("%s deadzone=%.2f", out, c.DeadZone)
}
