package usecase_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// The March 2023 outage. Binance halted spot trading during a matching-engine
// incident, so during this window no order could have been placed at any
// price. It is the case the whole untradeable-window mechanism exists for, and
// the phase-04 spec names it explicitly.
//
// The bounds are taken from that spec and have not been re-verified against
// Binance's incident record from this environment, which has no route to the
// exchange. They live in one place — backtest.KnownOutages — so correcting
// them is a one-line change that this test then follows automatically.
var (
	outageDay   = time.Date(2023, 3, 24, 0, 0, 0, 0, time.UTC)
	outageStart = time.Date(2023, 3, 24, 12, 40, 0, 0, time.UTC)
	outageEnd   = time.Date(2023, 3, 24, 14, 0, 0, 0, time.UTC)
)

// marchSeries builds 1m candles across the outage day, from an hour before
// the halt to an hour after it. The prices rise steadily so a position held
// across the window would show an obvious, and entirely fictional, profit.
func marchSeries() []models.Candle {
	start := outageStart.Add(-time.Hour)
	end := outageEnd.Add(time.Hour)

	var series []models.Candle
	price := int64(27000)
	for at := start; at.Before(end); at = at.Add(time.Minute) {
		p := decimalString(price)
		series = append(series, bar(at, p, p, p, p))
		price++
	}
	return series
}

// decimalString renders an integer price.
func decimalString(v int64) string { return strconv.FormatInt(v, 10) }

// marchParams runs over the outage day.
func marchParams(t *testing.T, series []models.Candle, policy backtest.GapPolicy) backtest.RunParams {
	t.Helper()

	params := scoredParams(t, series, buyAndHold{})
	params.To = series[len(series)-1].OpenTime
	params.GapPolicy = policy
	return params
}

// marchGap is the data gap corresponding to the halt, as the collector would
// have recorded it: no candles arrived because no trades happened.
func marchGap() models.DataGap {
	return models.DataGap{
		Id:           1,
		Symbol:       testSymbol,
		MarketType:   constants.MarketTypeSpot,
		Timeframe:    constants.Timeframe1m,
		GapStart:     outageStart,
		GapEnd:       outageEnd,
		FillAttempts: 5,
		Note:         "binance returned no klines for this range",
	}
}

// ---------------------------------------------------------------------------
// The three gap policies.
// ---------------------------------------------------------------------------

// TestHaltIsTheDefaultAndRefusesToRun is the trust gate. A run over a range
// with unfilled gaps must produce no number at all, because a plausible
// number computed over missing bars is worse than no number: it gets acted on.
func TestHaltIsTheDefaultAndRefusesToRun(t *testing.T) {
	series := marchSeries()
	candles := &fakeCandles{series: series}
	gaps := &fakeGaps{gaps: []models.DataGap{marchGap()}}

	params := marchParams(t, series, backtest.GapHalt)
	result, err := newEngine(candles, gaps).Run(context.Background(), params)

	if !errors.Is(err, constants.ErrDataIncomplete) {
		t.Fatalf("Run() returned %v, want ErrDataIncomplete", err)
	}
	if len(result.Trades) != 0 {
		t.Errorf("a halted run produced %d trades", len(result.Trades))
	}
	// The refusal has to say what is missing, or the reader is sent back to
	// psql — which is the workflow this gate replaces.
	if len(result.UnfilledGaps) != 1 {
		t.Fatalf("the halted result carries %d gaps, want 1: the caller cannot print what it was not given",
			len(result.UnfilledGaps))
	}
	if !result.UnfilledGaps[0].GapStart.Equal(outageStart) {
		t.Errorf("reported gap starts at %s, want %s", result.UnfilledGaps[0].GapStart, outageStart)
	}
}

// TestHaltIsTheDefaultPolicy pins that the safe behaviour is what you get
// without asking. A default of skip or ignore would mean every casual run
// silently accepted incomplete data.
func TestHaltIsTheDefaultPolicy(t *testing.T) {
	if backtest.GapHalt.String() != "halt" {
		t.Fatalf("GapHalt renders as %q", backtest.GapHalt)
	}

	var unset backtest.GapPolicy
	if unset.Valid() {
		t.Error("the zero GapPolicy is valid, so a caller that forgot to set one would run under it")
	}
}

// TestSkipExcludesTheOutageAndForcesTheOpenPositionOut is the spec's required
// behaviour over the 2023-03-24 range.
//
// A position held across an exchange halt is the specific fiction being
// prevented: the price on the far side is higher, and a backtest that carried
// the position through would book that move as profit from a trade nobody
// could have been in.
func TestSkipExcludesTheOutageAndForcesTheOpenPositionOut(t *testing.T) {
	series := marchSeries()
	candles := &fakeCandles{series: series}
	gaps := &fakeGaps{gaps: []models.DataGap{marchGap()}}

	params := marchParams(t, series, backtest.GapSkip)
	result := runEngine(t, candles, gaps, params)

	if result.BarsSkippedGap == 0 {
		t.Fatal("no bars were skipped, so the outage was traded straight through")
	}

	// Nothing inside the window may have been evaluated.
	for _, point := range result.Equity {
		if !point.OpenTime.Before(outageStart) && point.OpenTime.Before(outageEnd) {
			t.Fatalf("bar at %s was evaluated inside the halt", point.OpenTime.Format(time.RFC3339))
		}
	}

	// The position open when the halt began must have been closed at the last
	// price before it, and flagged.
	var forced *backtest.Trade
	for i := range result.Trades {
		if result.Trades[i].ForcedByGap {
			forced = &result.Trades[i]
			break
		}
	}
	if forced == nil {
		t.Fatal("no trade was force-closed, so a position was carried across the halt")
	}
	if forced.ExitReason != backtest.ExitGapForced {
		t.Errorf("exit reason is %q, want %q", forced.ExitReason, backtest.ExitGapForced)
	}
	if !forced.ExitTime.Before(outageStart) {
		t.Errorf("forced exit is timed at %s, inside or after the halt; it must use the last tradeable bar",
			forced.ExitTime.Format(time.RFC3339))
	}
}

// TestIgnoreRunsThroughButStampsTheResult. There is deliberately no policy
// that produces a clean-looking report over dirty data.
func TestIgnoreRunsThroughButStampsTheResult(t *testing.T) {
	series := marchSeries()
	candles := &fakeCandles{series: series}
	gaps := &fakeGaps{gaps: []models.DataGap{marchGap()}}

	params := marchParams(t, series, backtest.GapIgnore)
	result := runEngine(t, candles, gaps, params)

	if !result.DataIncomplete {
		t.Fatal("an ignore run is not stamped incomplete, so its report would read as trustworthy")
	}
	if len(result.UnfilledGaps) != 1 {
		t.Errorf("the result carries %d gaps, want 1", len(result.UnfilledGaps))
	}
}

// TestOutageIsExcludedEvenUnderIgnore is the difference between a gap and an
// outage.
//
// A gap means we do not know what the price did, and whether that stops a run
// is a policy question. An outage means nothing could have happened at all, so
// no flag relaxes it: trading through one would report fills that were
// impossible, which no amount of stamping makes acceptable.
func TestOutageIsExcludedEvenUnderIgnore(t *testing.T) {
	series := marchSeries()
	candles := &fakeCandles{series: series}

	// No recorded gaps at all: the candles are present. Only the outage
	// registry knows this window was untradeable.
	params := marchParams(t, series, backtest.GapIgnore)
	result := runEngine(t, candles, &fakeGaps{}, params)

	if len(result.UntradeableWindows) != 1 {
		t.Fatalf("the run found %d untradeable windows, want the March 2023 halt", len(result.UntradeableWindows))
	}
	if result.BarsSkippedGap == 0 {
		t.Fatal("bars inside the halt were evaluated under --allow-gaps=ignore; an outage is not a policy question")
	}

	for _, point := range result.Equity {
		if !point.OpenTime.Before(outageStart) && point.OpenTime.Before(outageEnd) {
			t.Fatalf("bar at %s inside the halt reached the equity curve", point.OpenTime.Format(time.RFC3339))
		}
	}
	for _, trade := range result.Trades {
		if !trade.EntryTime.Before(outageStart) && trade.EntryTime.Before(outageEnd) {
			t.Errorf("a trade was entered at %s, during the halt", trade.EntryTime.Format(time.RFC3339))
		}
	}
}

// TestOutagesForFindsTheWindowOnlyForItsInstrument keeps the registry from
// silently deleting history from an instrument it was never about.
func TestOutagesForFindsTheWindowOnlyForItsInstrument(t *testing.T) {
	from := outageDay
	to := outageDay.Add(24 * time.Hour)

	if got := backtest.OutagesFor(testSymbol, constants.MarketTypeSpot, from, to); len(got) != 1 {
		t.Errorf("found %d windows for BTCUSDT spot on the outage day, want 1", len(got))
	}
	if got := backtest.OutagesFor("ETHUSDT", constants.MarketTypeSpot, from, to); len(got) != 0 {
		t.Errorf("found %d windows for ETHUSDT, want 0: the halt was recorded for BTCUSDT", len(got))
	}
	if got := backtest.OutagesFor(testSymbol, constants.MarketTypeFutures, from, to); len(got) != 0 {
		t.Errorf("found %d windows for BTCUSDT futures, want 0: the recorded halt is the spot one", len(got))
	}

	// A range that ends before the window starts must not match.
	early := backtest.OutagesFor(testSymbol, constants.MarketTypeSpot, outageDay, outageStart.Add(-time.Hour))
	if len(early) != 0 {
		t.Errorf("found %d windows in a range that ends before the halt", len(early))
	}
}

// TestUntradeableWindowBoundsAreHalfOpen pins the edges, because an outage one
// minute too wide silently deletes a tradeable bar and one minute too narrow
// lets an impossible fill through.
func TestUntradeableWindowBoundsAreHalfOpen(t *testing.T) {
	window := backtest.UntradeableWindow{Start: outageStart, End: outageEnd}

	if !window.Covers(outageStart) {
		t.Error("the first minute of the halt is not covered")
	}
	if !window.Covers(outageEnd.Add(-time.Minute)) {
		t.Error("the last minute of the halt is not covered")
	}
	if window.Covers(outageEnd) {
		t.Error("the bar at the end bound is covered; the market had reopened by then")
	}
	if window.Covers(outageStart.Add(-time.Minute)) {
		t.Error("the bar before the halt is covered")
	}
}

// TestGapErrorIsReportedNotSwallowed: a database that cannot answer whether
// the data is complete must stop the run, not be treated as "no gaps".
func TestGapErrorIsReportedNotSwallowed(t *testing.T) {
	series := flatSeries(40, "100")
	candles := &fakeCandles{series: series}
	gaps := &fakeGaps{err: errors.New("connection refused")}

	_, err := newEngine(candles, gaps).Run(context.Background(), scoredParams(t, series, alwaysFlat{}))
	if err == nil {
		t.Fatal("Run() succeeded despite being unable to check for gaps")
	}
}

// seriesWithHole builds a series that actually stops for holeBars minutes,
// which is what a real data gap looks like: the candles are absent.
//
// The price on the far side is far above the near side, so a position carried
// across the hole books an obvious and entirely fictional profit.
func seriesWithHole(t *testing.T, before, holeBars, after int) (series []models.Candle, gapStart, gapEnd time.Time) {
	t.Helper()

	at := seriesStart
	for range before {
		series = append(series, bar(at, "100", "100", "100", "100"))
		at = at.Add(time.Minute)
	}
	gapStart = at
	at = at.Add(time.Duration(holeBars) * time.Minute)
	gapEnd = at
	for range after {
		series = append(series, bar(at, "200", "200", "200", "200"))
		at = at.Add(time.Minute)
	}
	return series, gapStart, gapEnd
}

// TestRealGapForcesThePositionOut is a regression test for a bug that made
// --allow-gaps=skip do nothing at all for the case it exists for.
//
// The original check asked "is this bar inside an excluded region", which is
// the right question for an exchange outage — the candles exist and record a
// period nobody could trade. It is the wrong question for a data gap, where
// the candles are missing by definition: the stream jumps from the bar before
// the hole to the bar after it, no bar inside the region is ever offered, and
// a check waiting to be handed one waits forever.
//
// The result was a position carried straight across a 30 minute hole, booking
// the 100% move on the far side as profit, under the very policy whose promise
// is to exclude it.
func TestRealGapForcesThePositionOut(t *testing.T) {
	warmup := warmupBars(t)
	series, gapStart, gapEnd := seriesWithHole(t, warmup+10, 30, 10)

	gaps := &fakeGaps{gaps: []models.DataGap{{
		Id: 1, Symbol: testSymbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m,
		GapStart:  gapStart, GapEnd: gapEnd,
		Note: "no klines returned for this range",
	}}}

	params := scoredParams(t, series, buyAndHold{})
	params.To = series[len(series)-1].OpenTime
	params.GapPolicy = backtest.GapSkip

	result := runEngine(t, &fakeCandles{series: series}, gaps, params)

	var forced *backtest.Trade
	for i := range result.Trades {
		if result.Trades[i].ForcedByGap {
			forced = &result.Trades[i]
			break
		}
	}
	if forced == nil {
		t.Fatalf("no trade was force-closed; the position was carried across the hole.\ntrades: %+v",
			result.Trades)
	}
	if forced.ExitReason != backtest.ExitGapForced {
		t.Errorf("exit reason is %q, want %q", forced.ExitReason, backtest.ExitGapForced)
	}
	if !forced.ExitTime.Before(gapStart) {
		t.Errorf("forced exit is timed at %s, want a bar before the hole began at %s",
			forced.ExitTime.Format(time.RFC3339), gapStart.Format(time.RFC3339))
	}

	// The exit price must come from the near side of the hole. Anything at or
	// near 200 means the far side was used, which is the fiction being
	// prevented.
	if forced.ExitPrice.GreaterThan(decimal.RequireFromString("150")) {
		t.Errorf("forced exit filled at %s, which is the price on the far side of the hole",
			forced.ExitPrice)
	}
}

// TestRealGapIsCrossedFreelyUnderIgnore is the other half: --allow-gaps=ignore
// means exactly what it says. The run proceeds across the hole and the report
// carries the stamp; it does not quietly behave like skip.
func TestRealGapIsCrossedFreelyUnderIgnore(t *testing.T) {
	warmup := warmupBars(t)
	series, gapStart, gapEnd := seriesWithHole(t, warmup+10, 30, 10)

	gaps := &fakeGaps{gaps: []models.DataGap{{
		Id: 1, Symbol: testSymbol, MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m, GapStart: gapStart, GapEnd: gapEnd,
	}}}

	params := scoredParams(t, series, buyAndHold{})
	params.To = series[len(series)-1].OpenTime
	params.GapPolicy = backtest.GapIgnore

	result := runEngine(t, &fakeCandles{series: series}, gaps, params)

	for _, trade := range result.Trades {
		if trade.ForcedByGap {
			t.Errorf("a trade was force-closed under ignore; that is what skip is for")
		}
	}
	if !result.DataIncomplete {
		t.Error("the run is not stamped incomplete")
	}
}
