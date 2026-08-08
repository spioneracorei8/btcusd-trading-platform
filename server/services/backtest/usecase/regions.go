package usecase

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// region is a half-open [start, end) span of time the engine will not trade.
type region struct {
	start time.Time
	end   time.Time
}

// covers reports whether t falls inside the region.
func (r region) covers(t time.Time) bool {
	return !t.Before(r.start) && t.Before(r.end)
}

// excludedRegions is every span a run must not evaluate.
//
// # Gaps and outages are excluded for different reasons
//
// A gap means the data is missing: we do not know what the price did, so any
// number computed across it is a guess wearing a decimal point. Whether that
// disqualifies a run is a policy question, which is why gaps are only
// excluded under GapSkip.
//
// An outage means nothing could have happened — the exchange was not matching
// orders, so there was no fill to simulate at any price. That is not a matter
// of policy and no flag relaxes it: a run that traded through one would be
// reporting fills that were impossible, which is worse than reporting fills
// over unknown prices.
type excludedRegions struct {
	regions []region
}

// newExcludedRegions builds the exclusion set for a run.
func newExcludedRegions(
	params backtest.RunParams,
	gaps []models.DataGap,
	outages []backtest.UntradeableWindow,
) excludedRegions {
	var regions []region

	// Outages always, whatever the policy says about gaps.
	for _, outage := range outages {
		regions = append(regions, region{start: outage.Start, end: outage.End})
	}

	if params.GapPolicy == backtest.GapSkip {
		for _, gap := range gaps {
			// GapEnd is the open time of the first candle present again, so
			// the region is half-open and that candle is tradeable.
			regions = append(regions, region{start: gap.GapStart, end: gap.GapEnd})
		}
	}

	return excludedRegions{regions: regions}
}

// tradeableAt reports whether a bar at t may be evaluated.
func (e excludedRegions) tradeableAt(t time.Time) bool {
	for _, r := range e.regions {
		if r.covers(t) {
			return false
		}
	}
	return true
}

// crossedBetween reports whether an excluded region lies between two
// consecutive evaluated bars.
//
// # Why this is not the same question as tradeableAt
//
// tradeableAt only fires on a bar the engine actually sees, which is enough
// for an exchange outage — the candles exist, they simply record a period
// nobody could trade. A data gap is the opposite: the candles are *missing*,
// that being what makes it a gap. The stream therefore jumps straight from
// the bar before the hole to the bar after it, no bar inside the region is
// ever offered, and a check that waits to be handed one waits forever.
//
// Without this, a position would be carried across the hole and the price
// move on the far side booked as profit from a trade that was never
// supervised — under GapSkip, the mode whose entire promise is to exclude it.
func (e excludedRegions) crossedBetween(previous, current time.Time) bool {
	for _, r := range e.regions {
		// The region began after the last bar we saw and before this one, so
		// the run stepped over it.
		if r.start.After(previous) && r.start.Before(current) {
			return true
		}
	}
	return false
}
