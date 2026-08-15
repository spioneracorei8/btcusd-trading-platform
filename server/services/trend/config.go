package trend

import (
	"errors"
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

	// Dropped records contributors that were configured but do not close less
	// often than the run's base timeframe, and were therefore removed by
	// ForBase.
	//
	// It is not configuration — it is what happened to the configuration — but
	// it lives here because this struct is what the report header renders, and
	// silently discarding part of a configuration is its own defect. A reader
	// must be able to see that the 5m contributor they configured took no part
	// in the numbers in front of them.
	Dropped []constants.Timeframe
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

// defaultContributors maps a base timeframe to the contributors above it.
//
// # Why this is per-base rather than one shared list
//
// There is no reason the right contributor set for a 1m base should also be
// right for a 1h base. One shared list would force two different situations to
// use the same value because it is convenient, not because it is correct.
//
// The concrete cost of the shared version decided it: adding 4h and 1d to a
// single default would have changed the filter for a 1m base too, and the
// three completed 1m runs would have stopped being comparable with anything
// run afterwards. Seven evaluation runs exist; discarding the comparability of
// three of them to save a few lines is the wrong trade. ADR 0018.
//
// A slice rather than a map, for the determinism reason that applies
// everywhere here: Go randomises map iteration and a report must be
// byte-identical across runs.
var defaultContributors = []struct {
	Base   constants.Timeframe
	Higher []constants.Timeframe
}{
	// Unchanged, and pinned by a test. This is what the completed runs used.
	{constants.Timeframe1m, []constants.Timeframe{
		constants.Timeframe5m, constants.Timeframe15m, constants.Timeframe1h}},
	{constants.Timeframe5m, []constants.Timeframe{
		constants.Timeframe15m, constants.Timeframe1h, constants.Timeframe4h}},

	// 1d is deliberately absent from the 15m and 1h rows. It is not that a
	// daily contributor says nothing useful — it is the strongest trend signal
	// available — but that it cannot say anything in time.
	//
	// The filter waits WarmupMultiplier × EMAPeriod = 1000 closes of every
	// contributor. For 1d that is 2.7 years *before* the range starts, so a
	// run over the development set from 2023-01-01 needs daily candles back to
	// 2020-04-06. They do not exist here, and the observed result was a 1h run
	// reporting 100% of bars not-ready and zero trades: a filter that blocks
	// everything is not a conservative filter, it is a broken one.
	//
	// 4h needs 1000 × 4h = 167 days, which July 2022 onward covers. ADR 0018.
	{constants.Timeframe15m, []constants.Timeframe{
		constants.Timeframe1h, constants.Timeframe4h}},
	{constants.Timeframe1h, []constants.Timeframe{
		constants.Timeframe4h}},

	// 4h has no usable contributor. 1d is the only thing above it and cannot
	// warm up over the collected history, so the row is empty rather than
	// hopeful: an entry here produced 100% not-ready and zero trades across
	// all three strategies, which is the same defect that removed 1d from the
	// 15m and 1h rows one shelf down.
	//
	// An empty row means "run unfiltered", not "refuse to run". See
	// ErrNoUsableContributor.
	{constants.Timeframe4h, nil},

	// 1d has nothing above it at all, and is absent for that reason. It
	// resolves the same way: unfiltered.
}

// ErrNoUsableContributor means the filter cannot be built for a base, and the
// run should proceed without one.
//
// # Why this is not an error the caller should give up on
//
// Round two made a base with nothing above it a hard error. That was right
// when the alternative was a filter that silently gated on nothing. It is
// wrong when the alternative is not running the experiment at all: the 4h
// column of the evaluation is empty because three strategies refused to run
// there, and an unmeasured cell in an otherwise monotonic series is exactly
// where guessing is least defensible.
//
// So the caller drops the filter and says so. The distinction that matters in
// the report is between a filter the operator turned off and one that was
// never available — the first is a choice, the second is a limit of the data.
var ErrNoUsableContributor = errors.New("trend: no usable contributor")

// DefaultBases lists every base timeframe the default table has an opinion
// about, in table order.
//
// It exists so a test can check the table rather than a list of bases somebody
// remembered to keep in step with it. The warm-up test originally enumerated
// four bases by hand and silently stopped covering the table when a fifth row
// was added — which is how a 4h base shipped with a contributor that could
// never warm up.
func DefaultBases() []constants.Timeframe {
	out := make([]constants.Timeframe, 0, len(defaultContributors))
	for _, entry := range defaultContributors {
		out = append(out, entry.Base)
	}
	return out
}

// defaultWeights are the shares, shortest contributor first, for a set of the
// given size.
//
// The three-contributor row is the shipped 1m configuration. The shorter rows
// are the top of it renormalised, so the proportions between the surviving
// contributors are the same whichever base they are serving: the highest
// timeframe always carries 5/8ths of the weight of the pair below it, and the
// dominant-trend veto stays the strongest voice.
func defaultWeights(count int) []float64 {
	full := []float64{0.2, 0.3, 0.5}
	if count >= len(full) {
		return full
	}

	// Take the heaviest `count` of them and renormalise to 1.
	taken := full[len(full)-count:]
	total := 0.0
	for _, weight := range taken {
		total += weight
	}

	out := make([]float64, 0, count)
	for _, weight := range taken {
		out = append(out, weight/total)
	}
	return out
}

// DefaultConfigFor is the documented contributor set for a given base.
//
// # These numbers are not tuned and must not be
//
// The same warning as DefaultConfig: tuning weights against the data used to
// evaluate the result fits the filter to the past. The per-base sets below are
// chosen by the same reasoning — each base is watched by the two or three
// timeframes above it, weighted towards the slowest.
func DefaultConfigFor(base constants.Timeframe) (Config, error) {
	if !base.Valid() {
		return Config{}, fmt.Errorf("trend: base timeframe %q is not valid", base)
	}

	for _, entry := range defaultContributors {
		if entry.Base != base {
			continue
		}
		if len(entry.Higher) == 0 {
			return Config{}, fmt.Errorf(
				"%w: nothing above %s can warm up over the collected history",
				ErrNoUsableContributor, base)
		}

		weights := defaultWeights(len(entry.Higher))
		config := Config{DeadZone: DefaultConfig().DeadZone}
		for i, timeframe := range entry.Higher {
			config.Weights = append(config.Weights,
				Weight{Timeframe: timeframe, Weight: weights[i]})
		}
		return config, nil
	}

	return Config{}, fmt.Errorf(
		"%w: nothing this system collects closes less often than %s",
		ErrNoUsableContributor, base)
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

// ForBase adapts the configuration to the base timeframe of a run.
//
// # Why a contributor is dropped rather than fatal
//
// A contributor that does not close strictly less often than the base is a
// look-ahead hazard, and phase 05 §1 rejects one outright. That rejection is
// right about the danger and wrong about the response: at a 15m base, the
// sensible reading of a configured 5m contributor is that it has nothing to
// say here, not that the whole filter is misconfigured. Treating it as fatal
// made --compare and --trend-filter unusable at every base except 1m, which is
// the one the evidence says to move away from.
//
// So the contributors are partitioned. Survivors keep the look-ahead rule
// enforced against them by the aligner, which is unchanged.
//
// # Why the weights are rescaled
//
// The filter divides by TotalWeight, so dropping 5m from the default set would
// leave the survivors normalised over 0.8 instead of 1.0 — arithmetically fine
// and impossible to read. Rescaling to the original total means the printed
// weights are the shares each timeframe actually had: at a 15m base, 1h is not
// "0.5 of a filter that lost its other half", it is the whole of it.
func (c Config) ForBase(base constants.Timeframe) (Config, error) {
	if !base.Valid() {
		return Config{}, fmt.Errorf("trend: base timeframe %q is not valid", base)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}

	adapted := Config{DeadZone: c.DeadZone}
	survivingTotal := 0.0

	for _, weight := range c.Weights {
		if weight.Timeframe.Duration() > base.Duration() {
			adapted.Weights = append(adapted.Weights, weight)
			survivingTotal += weight.Weight
			continue
		}
		adapted.Dropped = append(adapted.Dropped, weight.Timeframe)
	}

	if len(adapted.Weights) == 0 {
		return Config{}, fmt.Errorf(
			"%w: no configured contributor (%s) closes less often than %s",
			ErrNoUsableContributor, describeTimeframes(c.Timeframes()), base)
	}

	// Rescale to the total the caller configured, so the filter's influence is
	// unchanged and the header shows each survivor's real share.
	factor := c.TotalWeight() / survivingTotal
	for i := range adapted.Weights {
		adapted.Weights[i].Weight *= factor
	}
	return adapted, nil
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
	out = fmt.Sprintf("%s deadzone=%.2f", out, c.DeadZone)

	if len(c.Dropped) > 0 {
		out += fmt.Sprintf(" (dropped %s: not above the base timeframe; "+
			"remaining weights rescaled)", describeTimeframes(c.Dropped))
	}
	return out
}

// describeTimeframes joins timeframes for a message.
func describeTimeframes(timeframes []constants.Timeframe) string {
	out := ""
	for i, timeframe := range timeframes {
		if i > 0 {
			out += ", "
		}
		out += timeframe.String()
	}
	return out
}
