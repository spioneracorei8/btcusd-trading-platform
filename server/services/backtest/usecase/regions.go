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
