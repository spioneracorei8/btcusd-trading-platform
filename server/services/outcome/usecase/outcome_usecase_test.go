package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	_outcome_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/usecase"
)

// ---------------------------------------------------------------------------
// Fakes. Nothing here touches a database.
// ---------------------------------------------------------------------------

// outcomeStore is signal_outcomes, in a map.
type outcomeStore struct {
	rows     map[uuid.UUID]models.SignalOutcome
	order    []uuid.UUID
	saveErr  error
	fetchErr error
}

func newStore() *outcomeStore {
	return &outcomeStore{rows: map[uuid.UUID]models.SignalOutcome{}}
}

func (s *outcomeStore) open(id uuid.UUID) {
	s.rows[id] = models.SignalOutcome{SignalId: id, Status: constants.OutcomeOpen}
	s.order = append(s.order, id)
}

func (s *outcomeStore) EnsureOutcomes(
	context.Context, string, constants.MarketType, int32,
) ([]models.SignalOutcome, error) {
	return nil, nil
}

func (s *outcomeStore) FetchOpen(
	context.Context, string, constants.MarketType, int32,
) ([]models.SignalOutcome, error) {
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}

	var open []models.SignalOutcome
	for _, id := range s.order {
		if row := s.rows[id]; row.Status == constants.OutcomeOpen {
			open = append(open, row)
		}
	}
	return open, nil
}

func (s *outcomeStore) SaveOutcome(
	_ context.Context, o models.SignalOutcome,
) (models.SignalOutcome, error) {
	if s.saveErr != nil {
		return models.SignalOutcome{}, s.saveErr
	}
	if _, ok := s.rows[o.SignalId]; !ok {
		return models.SignalOutcome{}, errors.New("no outcome for that signal")
	}
	s.rows[o.SignalId] = o
	return o, nil
}

func (s *outcomeStore) FetchOutcome(
	_ context.Context, id uuid.UUID,
) (models.SignalOutcome, error) {
	row, ok := s.rows[id]
	if !ok {
		return models.SignalOutcome{}, constants.ErrNotFound
	}
	return row, nil
}

// signalStore is the signals table, in a map.
type signalStore struct {
	rows map[uuid.UUID]models.Signal
	err  error
}

func (s *signalStore) CreateSignal(
	context.Context, models.Signal, models.Candle,
) (models.Signal, error) {
	return models.Signal{}, errors.New("not used")
}

func (s *signalStore) FetchSignalById(
	_ context.Context, id uuid.UUID,
) (models.Signal, error) {
	if s.err != nil {
		return models.Signal{}, s.err
	}
	row, ok := s.rows[id]
	if !ok {
		return models.Signal{}, constants.ErrNotFound
	}
	return row, nil
}

func (s *signalStore) SetEntryPrice(
	_ context.Context, id uuid.UUID, entry decimal.Decimal,
) (models.Signal, error) {
	row, ok := s.rows[id]
	if !ok {
		return models.Signal{}, constants.ErrNotFound
	}
	if row.EntryPrice.Valid {
		// Write-once, the same answer the query gives when no row matched.
		return models.Signal{}, constants.ErrNotFound
	}
	row.EntryPrice = decimal.NullDecimal{Decimal: entry, Valid: true}
	s.rows[id] = row
	return row, nil
}

// candleStore serves a prepared series.
type candleStore struct {
	series []models.Candle
	err    error
}

func (c *candleStore) FetchCandles(
	_ context.Context, params candle.FetchCandlesParams,
) ([]models.Candle, error) {
	if c.err != nil {
		return nil, c.err
	}

	var out []models.Candle
	for _, bar := range c.series {
		if bar.OpenTime.Before(params.From) || bar.OpenTime.After(params.To) {
			continue
		}
		out = append(out, bar)
	}
	return out, nil
}

func (c *candleStore) StreamCandles(
	context.Context, candle.FetchCandlesParams, func(models.Candle) error,
) error {
	return nil
}
func (c *candleStore) SaveCandle(context.Context, models.Candle) error    { return nil }
func (c *candleStore) SaveCandles(context.Context, []models.Candle) error { return nil }
func (c *candleStore) FetchLatestCandle(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}
func (c *candleStore) FetchEarliestCandle(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}
func (c *candleStore) FindGaps(
	context.Context, string, constants.MarketType, constants.Timeframe,
) ([]candle.Gap, error) {
	return nil, nil
}
func (c *candleStore) CountCandles(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (int64, error) {
	return int64(len(c.series)), nil
}
func (c *candleStore) OpenCursor(candle.FetchCandlesParams) candle.CandleCursor { return nil }

// gapStore answers whether a window has recorded holes.
type gapStore struct {
	unfilled []models.DataGap
	err      error
}

func (g *gapStore) ListUnfilledInRange(
	context.Context, datagap.GapRangeParams,
) ([]models.DataGap, error) {
	return g.unfilled, g.err
}
func (g *gapStore) RecordGap(_ context.Context, gap models.DataGap) (models.DataGap, error) {
	return gap, nil
}
func (g *gapStore) MarkFilled(context.Context, int64) error { return nil }
func (g *gapStore) RecordFillAttempt(context.Context, int64, string) (models.DataGap, error) {
	return models.DataGap{}, nil
}
func (g *gapStore) CountUnfilled(
	context.Context, string, constants.MarketType, constants.Timeframe,
) (int64, error) {
	return int64(len(g.unfilled)), nil
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

var followStart = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

// testCosts are deliberately non-zero: a slippage of nothing would let a
// mistake in which side of the fill it is applied to pass unnoticed.
func testCosts() backtest.Costs {
	return backtest.Costs{
		FeeTakerPct:   decimal.RequireFromString("0.05"),
		TickSize:      decimal.RequireFromString("0.01"),
		SlippageTicks: 1,
	}
}

// slip is one tick, the amount every fill moves against the trade.
func slip() decimal.Decimal { return decimal.RequireFromString("0.01") }

// bar builds one closed 1h candle from whole-number prices.
func bar(index int, open, high, low, close string) models.Candle {
	at := followStart.Add(time.Duration(index) * time.Hour)
	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1h,
		OpenTime:  at, CloseTime: at.Add(time.Hour),
		Open:   decimal.RequireFromString(open),
		High:   decimal.RequireFromString(high),
		Low:    decimal.RequireFromString(low),
		Close:  decimal.RequireFromString(close),
		Volume: decimal.NewFromInt(10), QuoteVolume: decimal.NewFromInt(1000),
		IsClosed: true,
	}
}

// aLongSignal decides on the close of the bar before followStart, so the
// first bar of a fixture series is the one its entry fills on.
func aLongSignal(stop, target string) models.Signal {
	return models.Signal{
		Id:              uuid.New(),
		Symbol:          "BTCUSDT",
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe1h,
		SignalTime:      followStart,
		Direction:       constants.DirectionLong,
		SignalPrice:     decimal.NullDecimal{Decimal: decimal.NewFromInt(100), Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString(stop), Valid: true},
		TakeProfit:      decimal.NullDecimal{Decimal: decimal.RequireFromString(target), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          []byte(`{"trigger":"fast crossed above slow"}`),
	}
}

// follower is one signal, one series, and the follower over both.
type follower struct {
	usecase outcome.OutcomeUsecase
	store   *outcomeStore
	signals *signalStore
	gaps    *gapStore
	signal  models.Signal
}

func silentLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newFollower wires everything over one signal and one series.
func newFollower(
	t *testing.T, sig models.Signal, series []models.Candle, expiryBars int,
) *follower {
	t.Helper()
	return newFollowerWithCosts(t, sig, series, expiryBars, testCosts())
}

// newFollowerWithCosts is the same over a venue the caller chooses.
func newFollowerWithCosts(
	t *testing.T, sig models.Signal, series []models.Candle, expiryBars int,
	costs backtest.Costs,
) *follower {
	t.Helper()

	store := newStore()
	store.open(sig.Id)

	f := &follower{
		store:   store,
		signals: &signalStore{rows: map[uuid.UUID]models.Signal{sig.Id: sig}},
		gaps:    &gapStore{},
		signal:  sig,
	}

	usecase, err := _outcome_us.NewOutcomeUsecaseImpl(
		store, silentLog(), f.signals, &candleStore{series: series}, f.gaps,
		_outcome_us.Config{
			Symbol:     "BTCUSDT",
			MarketType: constants.MarketTypeSpot,
			Costs:      costs,
			ExpiryBars: expiryBars,
			Now:        func() time.Time { return followStart.Add(24 * time.Hour) },
		},
	)
	if err != nil {
		t.Fatalf("NewOutcomeUsecaseImpl() returned error: %v", err)
	}
	f.usecase = usecase
	return f
}

// run advances the follower once and returns the row it left behind.
func (f *follower) run(t *testing.T) models.SignalOutcome {
	t.Helper()

	if _, err := f.usecase.FollowOpen(context.Background()); err != nil {
		t.Fatalf("FollowOpen() returned error: %v", err)
	}
	return f.store.rows[f.signal.Id]
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestTheEntryFillsAtTheNextBarsOpenPlusSlippage.
//
// The decision was taken on the previous bar's close and nothing could have
// filled there. This is the backtest's own rule, and it has to be, or the
// difference between the two would show up in every comparison as slippage
// that nobody introduced.
func TestTheEntryFillsAtTheNextBarsOpenPlusSlippage(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
	}, 48)

	f.run(t)

	stored := f.signals.rows[sig.Id]
	if !stored.EntryPrice.Valid {
		t.Fatal("the entry price was not recorded once the next bar closed")
	}

	// The bar opened at 101 and a buy pays one tick more.
	want := decimal.NewFromInt(101).Add(slip())
	if !stored.EntryPrice.Decimal.Equal(want) {
		t.Errorf("EntryPrice = %s, want %s (the open plus one tick)",
			stored.EntryPrice.Decimal, want)
	}

	// And the close it decided on is left exactly as it was.
	if !stored.SignalPrice.Decimal.Equal(decimal.NewFromInt(100)) {
		t.Errorf("SignalPrice = %s, want the close the strategy saw", stored.SignalPrice.Decimal)
	}
}

// TestTheEntryIsWrittenOnce.
//
// It is the denominator of every return computed from this signal. A second
// answer would silently change every comparison already drawn against the
// first.
func TestTheEntryIsWrittenOnce(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "105", "106", "104", "105"),
	}, 48)

	f.run(t)
	first := f.signals.rows[sig.Id].EntryPrice.Decimal

	// A second pass sees the same signal and a longer series.
	f.store.rows[sig.Id] = models.SignalOutcome{SignalId: sig.Id, Status: constants.OutcomeOpen}
	f.run(t)

	if got := f.signals.rows[sig.Id].EntryPrice.Decimal; !got.Equal(first) {
		t.Errorf("the entry price changed from %s to %s on a second pass", first, got)
	}
}

// TestABarReachingBothLevelsIsRecordedAsAStop.
//
// The rule the whole comparison rests on. A bar records four prices and says
// nothing about the path between them, so a bar spanning both genuinely does
// not say which came first. The backtest takes the stop; if this took the
// target, the two would disagree on outcome for reasons that have nothing to
// do with the strategy.
func TestABarReachingBothLevelsIsRecordedAsAStop(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		// Opens at 101, then spans 94 to 111: both levels, one bar.
		bar(0, "101", "111", "94", "105"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeStop {
		t.Errorf("Status = %q, want stop; the bar reached both levels", stored.Status)
	}
	if stored.DivergenceNote == "" {
		t.Error("nothing records that this outcome rested on an assumption")
	}
	if !stored.ResolvedPrice.Valid {
		t.Fatal("a resolved outcome has no price")
	}

	// The stop is the reference and a sell fills a tick below it.
	want := decimal.NewFromInt(95).Sub(slip())
	if !stored.ResolvedPrice.Decimal.Equal(want) {
		t.Errorf("ResolvedPrice = %s, want %s", stored.ResolvedPrice.Decimal, want)
	}
}

// TestAnEntryAndItsStopCanResolveInsideOneBar.
//
// The engine checks levels against the same bar it filled the entry at.
// Pretending otherwise would hide the worst case — the trade that was over
// before it started.
func TestAnEntryAndItsStopCanResolveInsideOneBar(t *testing.T) {
	sig := aLongSignal("95", "130")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "94", "96"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeStop {
		t.Errorf("Status = %q, want stop", stored.Status)
	}
	if stored.BarsHeld != 1 {
		t.Errorf("BarsHeld = %d, want 1: the entry bar is the bar it resolved on", stored.BarsHeld)
	}
}

// TestATargetResolvesAsATarget, which is the control for the stop cases.
func TestATargetResolvesAsATarget(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeTarget {
		t.Fatalf("Status = %q, want target", stored.Status)
	}
	if stored.DivergenceNote != "" {
		t.Errorf("an unambiguous resolution carries a note: %q", stored.DivergenceNote)
	}
	if stored.BarsHeld != 2 {
		t.Errorf("BarsHeld = %d, want 2", stored.BarsHeld)
	}

	want := decimal.NewFromInt(110).Sub(slip())
	if !stored.ResolvedPrice.Decimal.Equal(want) {
		t.Errorf("ResolvedPrice = %s, want %s", stored.ResolvedPrice.Decimal, want)
	}
}

// TestMAEAndMFEMatchHandComputedValues.
//
// An MAE routinely close to the stop on trades that eventually win means the
// stop is barely surviving and a slightly worse fill would flip the result.
// That is invisible in a win rate and decisive in practice, so the numbers
// have to be right rather than roughly right.
func TestMAEAndMFEMatchHandComputedValues(t *testing.T) {
	sig := aLongSignal("90", "130")
	f := newFollower(t, sig, []models.Candle{
		// Entry fills at 101.01. Low 97 is 4.01 against; high 104 is 2.99 for.
		bar(0, "101", "104", "97", "103"),
		// Low 99 is 2.01 against — less than 4.01, so MAE stands. High 120 is
		// 18.99 for, which beats 2.99.
		bar(1, "103", "120", "99", "119"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeOpen {
		t.Fatalf("Status = %q, want open; neither level was reached", stored.Status)
	}

	entry := decimal.NewFromInt(101).Add(slip())
	wantMAE := entry.Sub(decimal.NewFromInt(97))
	wantMFE := decimal.NewFromInt(120).Sub(entry)

	if !stored.MAE.Valid || !stored.MAE.Decimal.Equal(wantMAE) {
		t.Errorf("MAE = %v, want %s", stored.MAE, wantMAE)
	}
	if !stored.MFE.Valid || !stored.MFE.Decimal.Equal(wantMFE) {
		t.Errorf("MFE = %v, want %s", stored.MFE, wantMFE)
	}
}

// TestTheResolvingBarCountsTowardsMAE.
//
// An MAE that ignored the bar the stop was hit on would understate exactly
// the case it exists to describe.
func TestTheResolvingBarCountsTowardsMAE(t *testing.T) {
	sig := aLongSignal("95", "130")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "101", "102", "93", "94"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeStop {
		t.Fatalf("Status = %q, want stop", stored.Status)
	}

	// The bar that stopped it went to 93, below the stop at 95.
	entry := decimal.NewFromInt(101).Add(slip())
	want := entry.Sub(decimal.NewFromInt(93))
	if !stored.MAE.Decimal.Equal(want) {
		t.Errorf("MAE = %s, want %s: the resolving bar's low counts", stored.MAE.Decimal, want)
	}
}

// TestAShortIsMeasuredInItsOwnDirection.
//
// Every sign in the walk flips. A short measured as though it were a long
// would report its wins as losses, and the mistake would be invisible in
// aggregate because the counts would still add up.
func TestAShortIsMeasuredInItsOwnDirection(t *testing.T) {
	sig := aLongSignal("110", "90")
	sig.Direction = constants.DirectionShort

	f := newFollower(t, sig, []models.Candle{
		// Entry sells at 99 minus a tick. Low 89 reaches the target at 90.
		bar(0, "99", "101", "89", "91"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeTarget {
		t.Fatalf("Status = %q, want target: a short profits when price falls", stored.Status)
	}

	// A short entry sells, so slippage takes a tick off; the exit buys back
	// and pays a tick more.
	entry := decimal.NewFromInt(99).Sub(slip())
	if got := f.signals.rows[sig.Id].EntryPrice.Decimal; !got.Equal(entry) {
		t.Errorf("EntryPrice = %s, want %s", got, entry)
	}
	if want := decimal.NewFromInt(90).Add(slip()); !stored.ResolvedPrice.Decimal.Equal(want) {
		t.Errorf("ResolvedPrice = %s, want %s", stored.ResolvedPrice.Decimal, want)
	}

	// Price above the entry is adverse for a short.
	wantMAE := decimal.NewFromInt(101).Sub(entry)
	if !stored.MAE.Decimal.Equal(wantMAE) {
		t.Errorf("MAE = %s, want %s", stored.MAE.Decimal, wantMAE)
	}
}

// TestASignalThatReachesNeitherLevelExpires.
//
// Expired is a real outcome and it counts. A strategy whose signals mostly
// expire is a strategy that mostly does nothing, and dropping those rows
// would leave a win rate computed over only the trades that went somewhere.
func TestASignalThatReachesNeitherLevelExpires(t *testing.T) {
	sig := aLongSignal("90", "130")

	var series []models.Candle
	for i := range 4 {
		series = append(series, bar(i, "101", "102", "100", "101"))
	}

	f := newFollower(t, sig, series, 4)
	stored := f.run(t)

	if stored.Status != constants.OutcomeExpired {
		t.Fatalf("Status = %q, want expired", stored.Status)
	}
	if stored.BarsHeld != 4 {
		t.Errorf("BarsHeld = %d, want 4", stored.BarsHeld)
	}
	if !stored.ResolvedPrice.Valid {
		t.Error("an expired signal has no price; it left at the last close")
	}
}

// TestASignalIsStillOpenOnTheLastBarBeforeItsWindowIsUp.
//
// One bar short, which is the boundary worth pinning: an expiry firing early
// would close signals that still had room, and every one of them would be
// recorded as having gone nowhere.
func TestASignalIsStillOpenOnTheLastBarBeforeItsWindowIsUp(t *testing.T) {
	const window = 4

	sig := aLongSignal("90", "130")
	var series []models.Candle
	for i := range window - 1 {
		series = append(series, bar(i, "101", "102", "100", "101"))
	}

	f := newFollower(t, sig, series, window)
	stored := f.run(t)

	if stored.Status != constants.OutcomeOpen {
		t.Errorf("Status = %q, want open on bar %d of a %d-bar window",
			stored.Status, window-1, window)
	}
	if stored.ResolvedAt != nil {
		t.Error("an open outcome carries a resolution time")
	}
	if stored.BarsHeld != window-1 {
		t.Errorf("BarsHeld = %d, want %d: progress is saved so a restart does not lose it",
			stored.BarsHeld, window-1)
	}
}

// TestARecordedGapInvalidatesTheWindow.
//
// A hole in the window means what happened is not knowable. Counting it as a
// loss — or as anything — would put a guess into a win rate, which is worse
// than a smaller sample.
func TestARecordedGapInvalidatesTheWindow(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "111", "94", "105"),
	}, 48)

	f.gaps.unfilled = []models.DataGap{{
		Id: 1, Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1h,
		GapStart:  followStart, GapEnd: followStart.Add(time.Hour),
	}}

	stored := f.run(t)

	if stored.Status != constants.OutcomeInvalidated {
		t.Fatalf("Status = %q, want invalidated", stored.Status)
	}
	if stored.Status.Measurable() {
		t.Error("an invalidated outcome is counted in statistics")
	}
	if stored.ResolvedPrice.Valid {
		t.Errorf("ResolvedPrice = %s; what happened is not knowable", stored.ResolvedPrice.Decimal)
	}
	if len(stored.BacktestWouldHave) != 0 {
		t.Errorf("an accounting was drawn across missing data: %s", stored.BacktestWouldHave)
	}
	if stored.DivergenceNote == "" {
		t.Error("nothing says why this signal was excluded")
	}
}

// TestABreakInTheSeriesInvalidatesTheWindow.
//
// The other kind of hole: one nobody recorded. The bars either side of it
// look continuous, and walking them would produce a confident answer drawn
// across data that is not there.
func TestABreakInTheSeriesInvalidatesTheWindow(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		// Bar 1 is missing.
		bar(2, "101", "111", "94", "105"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeInvalidated {
		t.Errorf("Status = %q, want invalidated: the series skips a bar", stored.Status)
	}
}

// TestTheBacktestAccountingIsRecordedNetOfCost.
//
// Scalping at these timeframes is dominated by cost. A gross figure on its
// own is not a result, and a comparison drawn against one would flatter every
// strategy equally.
func TestTheBacktestAccountingIsRecordedNetOfCost(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}, 48)

	stored := f.run(t)

	if len(stored.BacktestWouldHave) == 0 {
		t.Fatal("a resolved outcome carries no accounting")
	}

	var accounting struct {
		EntryPrice     string `json:"entry_price"`
		ExitPrice      string `json:"exit_price"`
		Status         string `json:"status"`
		BarsHeld       int    `json:"bars_held"`
		GrossReturnPct string `json:"gross_return_pct"`
		NetReturnPct   string `json:"net_return_pct"`
		CostPct        string `json:"cost_pct"`
	}
	if err := json.Unmarshal(stored.BacktestWouldHave, &accounting); err != nil {
		t.Fatalf("the accounting is not readable: %v", err)
	}

	if accounting.Status != constants.OutcomeTarget.String() {
		t.Errorf("accounting status = %q, want target", accounting.Status)
	}
	if accounting.BarsHeld != 2 {
		t.Errorf("accounting bars_held = %d, want 2", accounting.BarsHeld)
	}

	// Two taker sides at 0.05% each.
	if accounting.CostPct != "0.1000" {
		t.Errorf("cost_pct = %q, want 0.1000", accounting.CostPct)
	}

	gross := decimal.RequireFromString(accounting.GrossReturnPct)
	net := decimal.RequireFromString(accounting.NetReturnPct)
	if !gross.Sub(net).Equal(decimal.RequireFromString("0.1")) {
		t.Errorf("gross %s and net %s do not differ by the cost", gross, net)
	}
	if !net.LessThan(gross) {
		t.Error("the net return is not below the gross one")
	}

	// Hand-checked: entry 101.01, exit 109.99.
	entry := decimal.NewFromInt(101).Add(slip())
	exit := decimal.NewFromInt(110).Sub(slip())
	wantGross := exit.Sub(entry).Div(entry).Mul(decimal.NewFromInt(100))
	if accounting.GrossReturnPct != wantGross.StringFixed(4) {
		t.Errorf("gross_return_pct = %q, want %q", accounting.GrossReturnPct, wantGross.StringFixed(4))
	}
}

// TestOneUnreadableSignalDoesNotStopThePass.
//
// A follower that stopped at its first bad row would leave every signal
// behind it unresolved, and the ones behind it are the older ones.
func TestOneUnreadableSignalDoesNotStopThePass(t *testing.T) {
	good := aLongSignal("95", "110")
	missing := aLongSignal("95", "110")

	store := newStore()
	store.open(missing.Id)
	store.open(good.Id)

	signals := &signalStore{rows: map[uuid.UUID]models.Signal{good.Id: good}}
	usecase, err := _outcome_us.NewOutcomeUsecaseImpl(
		store, silentLog(), signals,
		&candleStore{series: []models.Candle{bar(0, "101", "111", "102", "110")}},
		&gapStore{},
		_outcome_us.Config{
			Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
			Costs: testCosts(), ExpiryBars: 48,
		},
	)
	if err != nil {
		t.Fatalf("NewOutcomeUsecaseImpl() returned error: %v", err)
	}

	report, err := usecase.FollowOpen(context.Background())
	if err != nil {
		t.Fatalf("FollowOpen() returned error: %v", err)
	}
	if report.Followed != 1 {
		t.Errorf("Followed = %d, want 1: the readable signal behind the broken one", report.Followed)
	}
	if store.rows[good.Id].Status != constants.OutcomeTarget {
		t.Errorf("the readable signal was left %q", store.rows[good.Id].Status)
	}
}

// TestNothingIsRecordedBeforeABarHasClosed.
//
// A signal made a moment ago has no bar after it yet. That is the ordinary
// case on the very next pass, not a failure, and certainly not an entry price
// invented from the close it decided on.
func TestNothingIsRecordedBeforeABarHasClosed(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, nil, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeOpen {
		t.Errorf("Status = %q, want open", stored.Status)
	}
	if f.signals.rows[sig.Id].EntryPrice.Valid {
		t.Error("an entry price was recorded before any bar had closed")
	}
}

// TestRunStopsWhenTheContextIsCancelled, so a shutdown is not held by the
// follower.
func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	f := newFollower(t, aLongSignal("95", "110"), nil, 48)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.usecase.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() returned %v on cancellation, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return when its context was cancelled")
	}
}

// TestAnEntryThatGappedPastItsStopIsMarked.
//
// A signal decided on one bar's close fills at the next bar's open, and the
// market can gap between the two. The engine takes the position anyway and
// closes it at the stop, which on such a bar is a price the market never
// traded at — so a long that gapped down is recorded as a stop that made
// money.
//
// The follower does the same thing on purpose: matching the engine is what
// makes the comparison mean anything. What it must not do is stay quiet about
// it, or a reconciliation averages a fictional profit into the stop bucket.
func TestAnEntryThatGappedPastItsStopIsMarked(t *testing.T) {
	sig := aLongSignal("95", "130")
	f := newFollower(t, sig, []models.Candle{
		// Decided at 100, opens at 90 — below its own stop at 95.
		bar(0, "90", "92", "88", "91"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeStop {
		t.Fatalf("Status = %q, want stop", stored.Status)
	}
	if stored.DivergenceNote == "" {
		t.Fatal("a fill beyond its own stop is recorded with no note")
	}
	for _, want := range []string{"gapped past", "stop"} {
		if !strings.Contains(stored.DivergenceNote, want) {
			t.Errorf("the note does not mention %q: %s", want, stored.DivergenceNote)
		}
	}

	var accounting struct {
		GappedPast   string `json:"gapped_past"`
		NetReturnPct string `json:"net_return_pct"`
	}
	if err := json.Unmarshal(stored.BacktestWouldHave, &accounting); err != nil {
		t.Fatalf("the accounting is not readable: %v", err)
	}
	if accounting.GappedPast != string(backtest.ExitStop) {
		t.Errorf("gapped_past = %q, want stop", accounting.GappedPast)
	}

	// The point of the note: this stop is recorded as a gain, because the exit
	// is priced at a level the bar never reached.
	if !decimal.RequireFromString(accounting.NetReturnPct).IsPositive() {
		t.Log("the fixture no longer produces a profitable stop; the note matters less " +
			"but is still correct")
	}
}

// TestAnOrdinaryEntryIsNotMarkedAsGapped, so the note means something when it
// does appear.
func TestAnOrdinaryEntryIsNotMarkedAsGapped(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeTarget {
		t.Fatalf("Status = %q, want target", stored.Status)
	}
	if stored.DivergenceNote != "" {
		t.Errorf("an entry between its levels carries a gap note: %s", stored.DivergenceNote)
	}
}

// TestAnEntryThatGappedPastItsTargetIsMarkedToo, which is the same fault in
// the flattering direction: a target recorded as reached on a bar that opened
// beyond it.
func TestAnEntryThatGappedPastItsTargetIsMarkedToo(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		// Decided at 100, opens at 115 — above its own target at 110.
		bar(0, "115", "117", "113", "116"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeTarget {
		t.Fatalf("Status = %q, want target", stored.Status)
	}
	if !strings.Contains(stored.DivergenceNote, "target") {
		t.Errorf("the note does not name the target: %q", stored.DivergenceNote)
	}
}

// TestTheAccountingUsesTheConfiguredCostModel.
//
// This used to be FeeTakerPct x 2 regardless of the model, so a spread venue
// had every live signal priced with a fee it does not charge — and the
// reconciliation reported that as a difference in the strategy rather than in
// the configuration.
func TestTheAccountingUsesTheConfiguredCostModel(t *testing.T) {
	sig := aLongSignal("95", "110")
	series := []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}

	// A spread venue: 200 points at 0.05 is 10.00 of price, so a round trip
	// gives up 10.00 against an entry near 101 — about 9.9%.
	spread := testCosts()
	spread.Model = constants.CostModelSpread
	spread.SpreadPoints = 200
	spread.PointValue = decimal.RequireFromString("0.05")

	f := newFollowerWithCosts(t, sig, series, 48, spread)
	stored := f.run(t)

	var accounting struct {
		CostPct      string `json:"cost_pct"`
		CostModel    string `json:"cost_model"`
		CostExcludes string `json:"cost_excludes"`
	}
	if err := json.Unmarshal(stored.BacktestWouldHave, &accounting); err != nil {
		t.Fatalf("the accounting is not readable: %v", err)
	}

	if accounting.CostModel != constants.CostModelSpread.String() {
		t.Errorf("cost_model = %q, want spread", accounting.CostModel)
	}
	if accounting.CostPct == "0.1000" {
		t.Fatal("the spread venue was priced with the percentage taker fee")
	}

	// 10.00 of price over an entry of 101.01.
	entry := decimal.NewFromInt(101).Add(slip())
	want := decimal.NewFromInt(10).Div(entry).Mul(decimal.NewFromInt(100))
	if accounting.CostPct != want.StringFixed(4) {
		t.Errorf("cost_pct = %q, want %q", accounting.CostPct, want.StringFixed(4))
	}
	if accounting.CostExcludes != "" {
		t.Errorf("nothing was excluded but the row says %q", accounting.CostExcludes)
	}
}

// TestAPerLotCommissionIsReportedAsExcluded.
//
// It cannot be expressed as a share of price without a size, and the live path
// sizes nothing. A figure that is quietly short is worse than one that says so.
func TestAPerLotCommissionIsReportedAsExcluded(t *testing.T) {
	sig := aLongSignal("95", "110")
	series := []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}

	withCommission := testCosts()
	withCommission.CommissionPerLot = decimal.NewFromInt(6)
	withCommission.ContractSize = decimal.NewFromInt(1)

	f := newFollowerWithCosts(t, sig, series, 48, withCommission)
	stored := f.run(t)

	var accounting struct {
		CostExcludes string `json:"cost_excludes"`
	}
	if err := json.Unmarshal(stored.BacktestWouldHave, &accounting); err != nil {
		t.Fatalf("the accounting is not readable: %v", err)
	}
	if !strings.Contains(accounting.CostExcludes, "commission") {
		t.Errorf("cost_excludes = %q, does not say the commission is missing", accounting.CostExcludes)
	}
}

// TestAMissingFirstBarInvalidatesTheWindow.
//
// The bar the entry should have filled on is absent and nothing recorded it as
// a gap. Taking the entry from whatever bar came next produces a trade
// resolved against prices that have nothing to do with the decision — and when
// the price has not moved much, no other check notices.
//
// Both variants are covered: the one the gapped-past-level note happens to
// catch, and the one it does not.
func TestAMissingFirstBarInvalidatesTheWindow(t *testing.T) {
	tests := map[string][]models.Candle{
		"the price jumped while the bar was missing": {
			bar(1, "140", "141", "139", "140"),
			bar(2, "140", "141", "139", "140"),
		},
		"the price barely moved, so nothing else would notice": {
			bar(1, "101", "102", "100", "101"),
			bar(2, "101", "111", "100", "110"),
		},
	}

	for name, series := range tests {
		t.Run(name, func(t *testing.T) {
			sig := aLongSignal("95", "110")
			f := newFollower(t, sig, series, 48)

			stored := f.run(t)

			if stored.Status != constants.OutcomeInvalidated {
				t.Errorf("Status = %q, want invalidated: the entry would have come from a bar "+
					"an hour after the decision", stored.Status)
			}
			if stored.Status.Measurable() {
				t.Error("a fabricated outcome was counted in the statistics")
			}
			if f.signals.rows[sig.Id].EntryPrice.Valid {
				t.Errorf("an entry price of %s was recorded from the wrong bar",
					f.signals.rows[sig.Id].EntryPrice.Decimal)
			}
		})
	}
}

// TestTheFirstBarBeingTheRightOneIsNotInvalidated, so the check means
// something when it fires.
func TestTheFirstBarBeingTheRightOneIsNotInvalidated(t *testing.T) {
	sig := aLongSignal("95", "110")
	f := newFollower(t, sig, []models.Candle{
		bar(0, "101", "102", "100", "101"),
		bar(1, "103", "111", "102", "110"),
	}, 48)

	stored := f.run(t)

	if stored.Status != constants.OutcomeTarget {
		t.Errorf("Status = %q, want target on a contiguous window", stored.Status)
	}
}
