package usecase_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
)

// historyConfig asks for history from a point well before anything stored.
func historyConfig(from time.Time) _market_us.Config {
	cfg := testConfig()
	cfg.BackfillFrom = from
	return cfg
}

// TestBackfillFillsHistoryOlderThanTheStoredSeries is the reported defect.
//
// The forward walk resumes from the newest stored bar, so it can never reach
// history older than whatever was collected first. Moving MARKET_BACKFILL_FROM
// earlier then changed nothing at all: the VPS had .env asking for 2022-07-01
// and a series that still began 2023-01-01, with nothing reporting a problem.
//
// Gap detection could not catch it either — it finds holes between stored bars
// with a window function, and a missing prefix has no bar before it to lag
// against.
func TestBackfillFillsHistoryOlderThanTheStoredSeries(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	data := &fakeMarketData{
		pages: [][]models.Candle{
			{makeCandle(wanted, true), makeCandle(wanted.Add(time.Minute), true)},
		},
		now: stored.Add(72 * time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(
		historyConfig(wanted), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}

	requests := data.fetched()
	if len(requests) == 0 {
		t.Fatal("nothing was requested at all")
	}

	// The first request is the backward one, bounded at both ends: the
	// configured start of history, and the oldest bar already held.
	first := requests[0]
	if !first.From.Equal(wanted) {
		t.Errorf("history fill started at %s, want %s (MARKET_BACKFILL_FROM)",
			first.From.Format(time.RFC3339), wanted.Format(time.RFC3339))
	}
	if !first.To.Equal(stored) {
		t.Errorf("history fill ended at %s, want %s (the oldest stored bar)",
			first.To.Format(time.RFC3339), stored.Format(time.RFC3339))
	}

	got := candles.storedCandles()
	if len(got) != 2 {
		t.Fatalf("stored %d candles, want the 2 the history page carried", len(got))
	}
	if !got[0].OpenTime.Equal(wanted) {
		t.Errorf("the oldest stored candle is %s, want %s", got[0].OpenTime, wanted)
	}
}

// TestTheHistoryFillRunsBeforeTheForwardWalk.
//
// A process interrupted part-way leaves the series shorter but still
// contiguous at its recent end, which is the half the live feed depends on.
// The other order would leave a hole in the middle of a freshly extended
// series if the fill were cut short.
func TestTheHistoryFillRunsBeforeTheForwardWalk(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	data := &fakeMarketData{
		pages: [][]models.Candle{{makeCandle(wanted, true)}},
		now:   stored.Add(72 * time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(
		historyConfig(wanted), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}

	requests := data.fetched()
	if len(requests) < 2 {
		t.Fatalf("made %d requests, want a backward one and a forward one", len(requests))
	}

	// The backward request is bounded; the forward one is open-ended.
	if requests[0].To.IsZero() {
		t.Error("the first request was open-ended; the forward walk ran first")
	}
	forward := requests[len(requests)-1]
	if !forward.To.IsZero() {
		t.Errorf("the last request was bounded at %s; the forward walk never ran", forward.To)
	}
	if !forward.From.After(candles.latest.OpenTime) {
		t.Errorf("the forward walk resumed at %s, not after the newest stored bar %s",
			forward.From, candles.latest.OpenTime)
	}
}

// TestBackfillRequestsNoHistoryItAlreadyHas.
//
// The check costs one indexed lookup per timeframe and must cost nothing
// beyond that. This runs before every reconnect, so a redundant request here
// would be paid over and over for the lifetime of the deployment.
func TestBackfillRequestsNoHistoryItAlreadyHas(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)

	for name, earliest := range map[string]time.Time{
		"exactly at the configured start": wanted,
		"older than it":                   wanted.Add(-24 * time.Hour),
	} {
		candles := &recordingCandleUsecase{
			hasOne:      true,
			latest:      makeCandle(wanted.Add(90*24*time.Hour), true),
			hasEarliest: true,
			earliest:    makeCandle(earliest, true),
		}
		data := &fakeMarketData{now: wanted.Add(91 * 24 * time.Hour)}

		us := _market_us.NewMarketUsecaseImpl(
			historyConfig(wanted), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

		if err := us.Backfill(context.Background()); err != nil {
			t.Fatalf("%s: Backfill() returned error: %v", name, err)
		}

		for _, request := range data.fetched() {
			if !request.To.IsZero() {
				t.Errorf("%s: a bounded history request was made for %s..%s, "+
					"but the series already reaches back far enough",
					name, request.From, request.To)
			}
		}
	}
}

// TestBackfillFillsEverythingWhenNothingIsStored. An empty series has no prefix
// to fill: the forward walk starts at the configured point and covers it all,
// and a backward pass would be a second request for the same bars.
func TestBackfillFillsEverythingWhenNothingIsStored(t *testing.T) {
	cfg := testConfig()
	candles := &recordingCandleUsecase{} // nothing stored
	data := &fakeMarketData{
		pages: [][]models.Candle{{makeCandle(cfg.BackfillFrom, true)}},
		now:   cfg.BackfillFrom.Add(time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(
		cfg, silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}

	for _, request := range data.fetched() {
		if !request.To.IsZero() {
			t.Errorf("an empty series produced a bounded history request for %s..%s",
				request.From, request.To)
		}
	}
	if got := candles.storedCandles(); len(got) != 1 {
		t.Errorf("stored %d candles, want 1", len(got))
	}
}

// TestHistoryTheExchangeDoesNotHaveIsNotAnError.
//
// Binance has no BTCUSDT candles before 2017, and asking for them is a
// configuration mistake rather than a failure. The collector must still come
// up and stream — refusing to start would take the live feed down over history
// nobody can supply.
//
// It is reported rather than swallowed: every warm-up budget computed from
// MARKET_BACKFILL_FROM is wrong when this happens, and a filter that depends on
// the timeframe will never become ready.
func TestHistoryTheExchangeDoesNotHaveIsNotAnError(t *testing.T) {
	wanted := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	// No pages at all: every request comes back empty.
	data := &fakeMarketData{now: stored.Add(72 * time.Hour)}

	us := _market_us.NewMarketUsecaseImpl(
		historyConfig(wanted), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error over history the exchange does not have: %v", err)
	}

	// And the forward walk still ran, so the live feed is not held up by it.
	requests := data.fetched()
	if len(requests) < 2 {
		t.Fatalf("made %d requests; the forward walk was skipped", len(requests))
	}
	if !requests[len(requests)-1].To.IsZero() {
		t.Error("the forward walk never ran after the history fill came back empty")
	}
}

// TestTheHistoryFillIsRepeatableAndConvergent.
//
// It runs before every reconnect and keys entirely on the stored series, so a
// second pass over an already-filled series must ask for nothing. That is what
// makes it safe to have no progress marker: there is no state to fall out of
// step with the data.
func TestTheHistoryFillIsRepeatableAndConvergent(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	data := &fakeMarketData{
		pages: [][]models.Candle{{makeCandle(wanted, true)}},
		now:   stored.Add(72 * time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(
		historyConfig(wanted), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("first Backfill() returned error: %v", err)
	}

	// No hand-adjustment: storing the older bar moved the start of the series,
	// exactly as the upsert does, and the second pass reads that.
	before := len(data.fetched())
	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("second Backfill() returned error: %v", err)
	}

	for _, request := range data.fetched()[before:] {
		if !request.To.IsZero() {
			t.Errorf("the second pass asked for history again: %s..%s",
				request.From, request.To)
		}
	}
}

// TestAnUnchangedStartOfSeriesIsNotProgress is the loop that would otherwise
// never end.
//
// When the exchange's oldest bar is the one already held, the bounded window
// returns that same bar and the upsert rewrites it. A count of stored rows
// then reports success while the series begins exactly where it did — and
// because this runs before every reconnect, the same page would be re-requested
// for the life of the deployment, logging a completion each time.
//
// Progress is the series starting earlier, not rows being written.
func TestAnUnchangedStartOfSeriesIsNotProgress(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	// Every page is the bar already held: the exchange has nothing older.
	data := &fakeMarketData{
		pages: [][]models.Candle{
			{makeCandle(stored, true)},
			{makeCandle(stored, true)},
			{makeCandle(stored, true)},
			{makeCandle(stored, true)},
		},
		now: stored.Add(72 * time.Hour),
	}

	logs := &recordingLogger{}
	us := _market_us.NewMarketUsecaseImpl(
		historyConfig(wanted), logs.logger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	for pass := range 3 {
		if err := us.Backfill(context.Background()); err != nil {
			t.Fatalf("pass %d: Backfill() returned error: %v", pass, err)
		}
	}

	// The start of the series never moved, so nothing was learned.
	got, err := candles.FetchEarliestCandle(
		context.Background(), "BTCUSDT", constants.MarketTypeSpot, constants.Timeframe1m)
	if err != nil {
		t.Fatalf("FetchEarliestCandle() returned error: %v", err)
	}
	if !got.OpenTime.Equal(stored) {
		t.Errorf("the series now starts at %s, want %s; the fixture supplied no older bars",
			got.OpenTime, stored)
	}

	// And nothing was claimed. Reporting a completed fill here is how the
	// original defect stayed invisible: the operator is told history was
	// collected while the series begins exactly where it did.
	for _, message := range logs.messagesAt(slog.LevelInfo) {
		if strings.Contains(message, "history fill complete") {
			t.Errorf("a completed history fill was reported while the series "+
				"still starts at %s", stored.Format(time.RFC3339))
		}
	}

	// It is reported as the problem it is, once, rather than on every pass.
	warnings := logs.messagesAt(slog.LevelWarn)
	if len(warnings) != 1 {
		t.Errorf("logged %d warnings over three passes, want exactly 1: %v",
			len(warnings), warnings)
	}
}

// TestEveryConfiguredTimeframeGetsItsHistory. The 4h and 1d series are the ones
// this was needed for — a 4h contributor needs 1000 closes before the
// development set to be usable at all (ADR 0018).
func TestEveryConfiguredTimeframeGetsItsHistory(t *testing.T) {
	wanted := time.Date(2022, 7, 1, 0, 0, 0, 0, time.UTC)
	stored := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := historyConfig(wanted)
	cfg.Timeframes = []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe4h, constants.Timeframe1d,
	}

	candles := &recordingCandleUsecase{
		hasOne:      true,
		latest:      makeCandle(stored.Add(48*time.Hour), true),
		hasEarliest: true,
		earliest:    makeCandle(stored, true),
	}
	data := &fakeMarketData{now: stored.Add(72 * time.Hour)}

	us := _market_us.NewMarketUsecaseImpl(
		cfg, silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}

	asked := map[constants.Timeframe]bool{}
	for _, request := range data.fetched() {
		if !request.To.IsZero() {
			asked[request.Timeframe] = true
		}
	}
	for _, timeframe := range cfg.Timeframes {
		if !asked[timeframe] {
			t.Errorf("no history was requested for %s", timeframe)
		}
	}
}
