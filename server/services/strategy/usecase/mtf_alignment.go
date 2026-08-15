package usecase

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// MTFAlignmentConfig parameterises the multi-timeframe alignment strategy.
type MTFAlignmentConfig struct {
	// Dominant is the timeframe that decides which direction is permitted at
	// all. Nothing trades against it.
	Dominant constants.Timeframe

	// Intermediate must all agree with the dominant direction. Any one of
	// them disagreeing is a veto, not a reduced weight: this rule is an AND,
	// and a score that could be outvoted would be a different strategy with a
	// tuning knob attached.
	Intermediate []constants.Timeframe

	// TriggerPeriod is the base-timeframe EMA the pullback is measured
	// against. It is the only EMA this strategy computes for itself; see
	// higherDirection for why the higher timeframes use the engine's.
	TriggerPeriod int

	// PullbackATR is how close to the trigger EMA price must come to count as
	// a retracement, in ATR. A number in the market's own units rather than a
	// shape someone recognises, for the reason TrendPullbackConfig gives.
	PullbackATR float64

	// ResumeBars is how many consecutive base bars must move back in the
	// established direction before entering. Without it the rule buys every
	// touch, including the touch that carries straight through — which is what
	// a trend ending looks like.
	ResumeBars int

	Levels strategy.Levels

	RoundTripCostPct float64

	// LongOnly suppresses short entries; see EMACrossoverConfig.LongOnly.
	LongOnly bool
}

// DefaultMTFAlignmentConfig is the documented starting point. Nothing here is
// tuned, and the defaults are chosen to be defensible rather than good.
//
// # Why 4h is dominant and 1d is absent
//
// The design calls for 1d and 4h to agree on direction. 1d cannot be used:
// EMA(200) at the 5x warm-up rule (ADR 0007) needs 1000 daily closes, about
// 2.7 years, before the development set opens — and stored history begins
// 2022-07-01. It would never warm up, and the run would report a clean zero
// rather than an error.
//
// 4h needs 1000 closes, roughly 167 days, which the history covers. So the
// dominant timeframe is 4h alone. This is the same reasoning that removed 1d
// from the trend filter's contributor table (ADR 0018), and the same remedy
// applies: if daily candles are ever backfilled to 2020 or earlier, 1d comes
// back as a second dominant timeframe. That is a data-collection task, not a
// code change.
//
// # The trigger parameters
//
// EMA(50) on the base timeframe is slow enough that a 1m pullback to it is a
// retracement rather than noise. Half an ATR is close enough to count as a
// touch without demanding an exact one. Two resume bars is the smallest number
// that is evidence rather than a single bar's wick. All three are borrowed
// from trend_pullback deliberately: this strategy is not trying to find better
// entries than that one, it is trying to take far fewer of them.
//
// Reward-to-risk is 2.5. This rule should trade least often of the four, so
// each trade has to carry more.
func DefaultMTFAlignmentConfig() MTFAlignmentConfig {
	return MTFAlignmentConfig{
		Dominant:         constants.Timeframe4h,
		Intermediate:     []constants.Timeframe{constants.Timeframe15m, constants.Timeframe1h},
		TriggerPeriod:    50,
		PullbackATR:      0.5,
		ResumeBars:       2,
		Levels:           strategy.Levels{StopATRMult: 1.2, TargetATRMult: 3.0},
		RoundTripCostPct: 0.1,
	}
}

// Validate rejects a configuration that cannot work.
func (c MTFAlignmentConfig) Validate() error {
	if !c.Dominant.Valid() {
		return fmt.Errorf("mtf_alignment: %q is not a timeframe", c.Dominant)
	}
	if len(c.Intermediate) == 0 {
		return fmt.Errorf("mtf_alignment: no intermediate timeframes; " +
			"with only a dominant direction this is a trend filter wearing a strategy's name")
	}

	seen := map[constants.Timeframe]bool{c.Dominant: true}
	for _, timeframe := range c.Intermediate {
		if !timeframe.Valid() {
			return fmt.Errorf("mtf_alignment: %q is not a timeframe", timeframe)
		}
		if seen[timeframe] {
			return fmt.Errorf("mtf_alignment: %s appears twice; "+
				"a timeframe agreeing with itself is not confirmation", timeframe)
		}
		seen[timeframe] = true

		// An intermediate at or above the dominant one inverts the hierarchy
		// the rule is built on.
		if timeframe.Duration() >= c.Dominant.Duration() {
			return fmt.Errorf("mtf_alignment: intermediate %s is not below the dominant %s",
				timeframe, c.Dominant)
		}
	}

	if c.TriggerPeriod < 2 {
		return fmt.Errorf("mtf_alignment: trigger period %d is below 2", c.TriggerPeriod)
	}
	if c.PullbackATR <= 0 {
		return fmt.Errorf("mtf_alignment: pullback distance %v ATR is not positive", c.PullbackATR)
	}
	if c.ResumeBars < 1 {
		return fmt.Errorf("mtf_alignment: resume bars %d is below 1", c.ResumeBars)
	}
	return c.Levels.Validate(c.RoundTripCostPct)
}

// Timeframes lists every contributor, shortest first.
func (c MTFAlignmentConfig) Timeframes() []constants.Timeframe {
	all := append([]constants.Timeframe(nil), c.Intermediate...)
	all = append(all, c.Dominant)

	// Sorted by duration rather than by name: "15m" and "1h" do not order
	// lexically, and the aligner's contract is shortest first.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].Duration() < all[j-1].Duration(); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all
}

// mtfAlignment enters on the base timeframe, but only where several higher
// timeframes already agree on direction.
//
// # What this is, and what it is not
//
// The other three strategies fire on a single condition on the base timeframe.
// At 1m that produced 8,619 trades over two years — about twelve a day — and
// the phase-06 sweep showed the cost of that frequency swamping any edge in
// the entry rule itself.
//
// This one inverts the priority. The alignment requirement *is* the frequency
// control. It is not expected to find better entries than an EMA crossover; it
// is expected to find far fewer of them, so that a 0.25 USD round trip on a
// 100 USD account is a rounding error rather than the result.
//
// # Expected frequency, stated before the first run
//
// Roughly 1-2 trades a day: 700-1,500 over the development set. The range
// matters in both directions.
//
//   - Far more, and the alignment requirement is not doing its job. Costs will
//     dominate again and this is the same strategy as the others wearing a
//     longer rule.
//   - Fewer than about 200 in total, and the acceptance criteria's trade-count
//     floor cannot be met. The statistics would not support a conclusion in
//     either direction.
//
// If the first run lands far outside that range the honest response is to say
// so and reconsider the design. Loosening the alignment until the count looks
// comfortable is fitting the strategy to the criteria rather than to the
// market, and the loosened version would be a different strategy reported
// under this one's name.
//
// # Known weaknesses
//
// It has no unfiltered counterpart. A trend filter's contribution is measured
// by running with and without it; alignment here is the entry condition, so
// there is nothing to subtract. --compare refuses it for that reason, and the
// only available comparison is against the other strategies at the same base.
//
// And it inherits trend_pullback's definitional problem twice over: "pullback"
// and "resume" are choices, and now so is "agree". A nearby choice may behave
// very differently, which is what the parameter-neighbourhood report is for.
type mtfAlignment struct {
	config MTFAlignmentConfig

	trigger *ema

	// higher tracks the last two EMA values seen on each contributing
	// timeframe, keyed by timeframe. It is advanced only when that
	// timeframe's reading moves to a new bar — a higher-timeframe reading
	// repeats across many base bars, and feeding the repeats would make the
	// slope read as flat forever.
	higher map[constants.Timeframe]*higherTrack

	// pulledBack records that price has retraced to the trigger EMA and is
	// waiting for the move to resume. Cleared when the aligned direction
	// changes: a pullback within a trend that has since turned is not a
	// pullback into that trend.
	pulledBack     bool
	pullbackIsLong bool
	resumeCount    int

	previousClose  float64
	hasPreviousBar bool
}

// higherTrack is one contributing timeframe's EMA history.
type higherTrack struct {
	lastClose time.Time

	value    float64
	previous float64
	seen     int
}

// NewMTFAlignmentImpl builds the multi-timeframe alignment strategy.
func NewMTFAlignmentImpl(config MTFAlignmentConfig) (strategy.MultiTimeframe, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &mtfAlignment{
		config:  config,
		trigger: newEMA(config.TriggerPeriod),
		higher:  make(map[constants.Timeframe]*higherTrack),
	}, nil
}

func (s *mtfAlignment) Name() string    { return "mtf_alignment" }
func (s *mtfAlignment) Version() string { return "v1" }

// WarmupPeriod covers this strategy's own EMA, following ADR 0007.
//
// It does not and cannot cover the higher timeframes: those warm up in the
// aligner, which starts its cursors before the run's range precisely so their
// indicators are converged when the range opens. A strategy counting base bars
// has no way to express "1000 four-hour closes". What it can do is refuse to
// act until every reading reports itself ready, which is what OnBar does.
func (s *mtfAlignment) WarmupPeriod() int { return 5 * s.config.TriggerPeriod }

// RequiredTimeframes lists the contributors, shortest first.
func (s *mtfAlignment) RequiredTimeframes() []constants.Timeframe {
	return s.config.Timeframes()
}

func (s *mtfAlignment) OnBar(bar strategy.BarContext) []strategy.Intent {
	closePrice, _ := bar.Candle.Close.Float64()

	triggerEMA, triggerReady := s.trigger.update(closePrice)

	previousClose, hadPrevious := s.previousClose, s.hasPreviousBar
	s.previousClose, s.hasPreviousBar = closePrice, true

	// Every reading is fed regardless of whether this bar can trade, for the
	// same reason the engine feeds its indicators on skipped bars: the tracks
	// are stateful, and skipping one would leave the slope permanently out of
	// step with the series.
	s.observe(bar.Higher)

	if !triggerReady || !hadPrevious {
		return nil
	}

	atr := bar.Indicators.ATR
	if math.IsNaN(atr) || atr <= 0 {
		return nil
	}

	direction := s.alignedDirection(bar.Higher)
	if direction == constants.DirectionFlat {
		// Disagreement clears any retracement in progress. Waiting through a
		// period where the timeframes stopped agreeing and then entering on
		// the old direction is trading on evidence that has since expired.
		s.pulledBack = false
		s.resumeCount = 0
		return nil
	}

	long := direction == constants.DirectionLong
	if !long && s.config.LongOnly {
		return nil
	}

	if s.pulledBack && s.pullbackIsLong != long {
		s.pulledBack = false
		s.resumeCount = 0
	}

	// Close to the trigger EMA: the retracement has arrived, and the wait for
	// resumption starts now.
	if math.Abs(closePrice-triggerEMA) <= s.config.PullbackATR*atr {
		s.pulledBack = true
		s.pullbackIsLong = long
		s.resumeCount = 0
		return nil
	}
	if !s.pulledBack {
		return nil
	}

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

	reason := s.describeEntry(direction)
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

// observe advances each contributing timeframe's EMA history.
//
// A reading is only recorded when its close_time moves on. The aligner repeats
// the same 4h reading across 240 one-minute bars, and treating each repeat as
// a new observation would set previous equal to value on every bar and make
// every slope read as flat.
func (s *mtfAlignment) observe(snapshot *models.TrendSnapshot) {
	if snapshot == nil {
		return
	}

	for _, reading := range snapshot.Readings {
		if !reading.Ready || math.IsNaN(reading.Indicators.EMA) {
			continue
		}

		track := s.higher[reading.Timeframe]
		if track == nil {
			track = &higherTrack{}
			s.higher[reading.Timeframe] = track
		}
		if !reading.CloseTime.After(track.lastClose) {
			continue
		}

		track.lastClose = reading.CloseTime
		track.previous = track.value
		track.value = reading.Indicators.EMA
		track.seen++
	}
}

// alignedDirection is the direction every contributing timeframe agrees on, or
// flat when any of them does not.
//
// # Why the dominant timeframe is checked apart from the rest
//
// It is not an extra vote, it is the gate. A 4h downtrend means no long exists
// to confirm, whatever 15m and 1h are doing — checking it first says that,
// where folding it into a unanimity test over four equals would let the code
// read as though a majority could carry it.
func (s *mtfAlignment) alignedDirection(snapshot *models.TrendSnapshot) constants.Direction {
	// All of them, warm. A strategy requiring agreement cannot act on a
	// subset: the missing timeframe is the one that would have disagreed as
	// often as not, and absence is not consent.
	if !snapshot.Ready(s.config.Timeframes()...) {
		return constants.DirectionFlat
	}

	dominant := s.directionOf(snapshot, s.config.Dominant)
	if dominant == constants.DirectionFlat {
		return constants.DirectionFlat
	}

	for _, timeframe := range s.config.Intermediate {
		if s.directionOf(snapshot, timeframe) != dominant {
			return constants.DirectionFlat
		}
	}
	return dominant
}

// directionOf reads one timeframe's direction.
//
// # The same derivation at every level
//
// Price relative to the EMA, and the sign of the EMA's slope. Both must point
// the same way, so a price above a falling average is flat rather than long.
//
// It is deliberately identical for the dominant, the intermediate and any
// timeframe added later. A different rule per timeframe would be several free
// parameters wearing a disguise: each one could be adjusted until the backtest
// improved, and nothing in the result would show that it had been.
//
// # Why this reads the engine's EMA rather than one of its own
//
// A strategy-local EMA over higher-timeframe closes would need 1000 four-hour
// bars to warm up, and it could only start counting them once the run's range
// opened — 167 days into a run, having traded nothing. The aligner's readings
// are already warm at the first bar, because its cursors start before the
// range for exactly this purpose. The cost is that the period is the engine's
// (200) rather than a per-role parameter, which is a real limitation and the
// alternative is a strategy that cannot be evaluated at all.
func (s *mtfAlignment) directionOf(
	snapshot *models.TrendSnapshot,
	timeframe constants.Timeframe,
) constants.Direction {
	reading, ok := snapshot.For(timeframe)
	if !ok || !reading.Ready {
		return constants.DirectionFlat
	}

	track := s.higher[timeframe]
	// Two bars are needed before a slope exists. Reporting flat until then is
	// what stops the first reading of a run from being scored as a trend.
	if track == nil || track.seen < 2 {
		return constants.DirectionFlat
	}

	closePrice, _ := reading.Candle.Close.Float64()
	if math.IsNaN(track.value) || math.IsNaN(track.previous) {
		return constants.DirectionFlat
	}

	switch {
	case closePrice > track.value && track.value > track.previous:
		return constants.DirectionLong
	case closePrice < track.value && track.value < track.previous:
		return constants.DirectionShort
	default:
		return constants.DirectionFlat
	}
}

// describeEntry names the evidence, so a signal is actionable rather than
// merely emitted.
func (s *mtfAlignment) describeEntry(direction constants.Direction) string {
	names := make([]string, 0, len(s.config.Intermediate))
	for _, timeframe := range s.config.Intermediate {
		names = append(names, timeframe.String())
	}

	return fmt.Sprintf("%s on %s, confirmed by %s, entered on a pullback to ema(%d) then %d bars resuming",
		direction, s.config.Dominant, strings.Join(names, " and "), s.config.TriggerPeriod, s.config.ResumeBars)
}
