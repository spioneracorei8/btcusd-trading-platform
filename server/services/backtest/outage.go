package backtest

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// KnownOutages are periods where the exchange itself stopped accepting
// orders, so no fill of any kind was possible.
//
// # Why this is code and not a table
//
// These are facts about the exchange's history, fixed once they happen. They
// ship with the code, get reviewed with the code, and a run over an old range
// produces the same answer next year as it does today. Operational state that
// the collector discovers lives in data_gaps; this does not.
//
// # Why an outage is not a gap
//
// A gap means the data is missing: we do not know what the price did. An
// outage means nothing could have happened — no order could have been placed
// at any price. Backfilling an outage is not merely hard, it is meaningless,
// which is why these windows are always excluded regardless of gap policy,
// including under GapIgnore.
//
// Sources for a new entry belong in the Reason string. Verify a window before
// adding it: too wide silently deletes tradeable history, too narrow lets the
// engine report fills that could not have happened.
var KnownOutages = []UntradeableWindow{
	{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		// Binance halted spot trading during a matching-engine incident.
		//
		// The bounds come from docs/prompts/phase-04.md and have not been
		// re-verified against Binance's own incident record; the window is
		// stated there as "roughly 12:40 to 14:00 UTC". If the true extent
		// differs, correcting it here is the only change needed — nothing
		// else in the engine knows these dates.
		Start:  time.Date(2023, 3, 24, 12, 40, 0, 0, time.UTC),
		End:    time.Date(2023, 3, 24, 14, 0, 0, 0, time.UTC),
		Reason: "binance spot matching engine incident",
	},
}

// OutagesFor returns the known outages of one instrument that intersect
// [from, to], oldest first.
func OutagesFor(symbol string, marketType constants.MarketType, from, to time.Time) []UntradeableWindow {
	var windows []UntradeableWindow

	for _, w := range KnownOutages {
		if w.Symbol != symbol || w.MarketType != marketType {
			continue
		}
		if !w.Overlaps(from, to) {
			continue
		}
		windows = append(windows, w)
	}
	return windows
}
