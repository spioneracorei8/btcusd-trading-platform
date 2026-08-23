package usecase_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	_market_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/usecase"
)

// ---------------------------------------------------------------------------
// Fakes. None of them touch the network or a database.
// ---------------------------------------------------------------------------

// fakeMarketData replays a scripted stream and serves canned REST pages.
type fakeMarketData struct {
	mu sync.Mutex

	// stream is delivered once, in order, then the stream ends.
	stream []market.StreamedKline
	// pages is consumed one call at a time by FetchKlines.
	pages [][]models.Candle
	calls int

	streamErr error
	now       time.Time

	// requests records every FetchKlines call, so a test can assert which
	// window was asked for and not merely what came back.
	requests []market.FetchKlinesParams

	// delivered marks the scripted stream as consumed. A real connection
	// stays open after the last message rather than replaying it, so later
	// calls block until the context ends.
	delivered bool
}

func (f *fakeMarketData) FetchKlines(_ context.Context, params market.FetchKlinesParams) ([]models.Candle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, params)

	if f.calls >= len(f.pages) {
		return nil, nil
	}
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func (f *fakeMarketData) StreamKlines(ctx context.Context, _ market.StreamParams, onKline func(market.StreamedKline)) error {
	f.mu.Lock()
	alreadyDelivered := f.delivered
	f.delivered = true
	f.mu.Unlock()

	if alreadyDelivered {
		// The reconnect loop is working; hold the connection open so the
		// scripted messages are not replayed into storage a second time.
		<-ctx.Done()
		return ctx.Err()
	}

	for _, kline := range f.stream {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onKline(kline)
	}
	if f.streamErr != nil {
		return f.streamErr
	}
	return constants.ErrStreamClosed
}

func (f *fakeMarketData) ServerTime(context.Context) (time.Time, error) { return f.now, nil }

// fetched returns the windows that were requested.
func (f *fakeMarketData) fetched() []market.FetchKlinesParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]market.FetchKlinesParams(nil), f.requests...)
}

// recordingCandleUsecase records everything the pipeline tried to store.
type recordingCandleUsecase struct {
	mu     sync.Mutex
	stored []models.Candle
	gaps   []candle.Gap
	latest models.Candle
	hasOne bool

	// earliest is the oldest stored bar. It falls back to latest when unset,
	// which is what a one-candle series looks like.
	earliest    models.Candle
	hasEarliest bool
}

func (r *recordingCandleUsecase) SaveCandle(_ context.Context, c models.Candle) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Mirror the real usecase: this rule must hold on every path.
	if !c.IsClosed {
		return constants.ErrUnclosedCandle
	}
	r.stored = append(r.stored, c)
	return nil
}

func (r *recordingCandleUsecase) SaveCandles(_ context.Context, candles []models.Candle) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, c := range candles {
		if !c.IsClosed {
			return constants.ErrUnclosedCandle
		}
	}
	r.stored = append(r.stored, candles...)

	// Mirror what the table does: an upsert of an older bar moves the start of
	// the series, and one that is not older moves nothing. Without this the
	// fake reports progress from the row count alone, which is exactly the
	// distinction the backward fill has to make.
	for _, c := range candles {
		if r.hasEarliest && !c.OpenTime.Before(r.earliest.OpenTime) {
			continue
		}
		if !r.hasOne {
			r.hasOne, r.latest = true, c
		}
		r.earliest, r.hasEarliest = c, true
	}
	return nil
}

func (r *recordingCandleUsecase) FetchCandles(context.Context, candle.FetchCandlesParams) ([]models.Candle, error) {
	return nil, nil
}

func (r *recordingCandleUsecase) FetchLatestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.hasOne {
		return models.Candle{}, constants.ErrNotFound
	}
	return r.latest, nil
}

func (r *recordingCandleUsecase) FetchEarliestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.hasOne {
		return models.Candle{}, constants.ErrNotFound
	}
	if r.hasEarliest {
		return r.earliest, nil
	}
	return r.latest, nil
}

func (r *recordingCandleUsecase) CountCandles(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return 0, nil
}

func (r *recordingCandleUsecase) StreamCandles(context.Context, candle.FetchCandlesParams, func(models.Candle) error) error {
	return nil
}

func (r *recordingCandleUsecase) OpenCursor(candle.FetchCandlesParams) candle.CandleCursor {
	return emptyCursor{}
}

// emptyCursor is exhausted immediately; ingestion never reads a cursor.
type emptyCursor struct{}

func (emptyCursor) Next(context.Context) (models.Candle, bool, error) {
	return models.Candle{}, false, nil
}

func (r *recordingCandleUsecase) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gaps, nil
}

func (r *recordingCandleUsecase) storedCandles() []models.Candle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.Candle(nil), r.stored...)
}

// stubGapUsecase records gaps without a database.
type stubGapUsecase struct {
	mu       sync.Mutex
	recorded []models.DataGap
	filled   []int64
	attempts []string
	nextId   int64
}

func (s *stubGapUsecase) RecordGap(_ context.Context, gap models.DataGap) (models.DataGap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextId++
	gap.Id = s.nextId
	s.recorded = append(s.recorded, gap)
	return gap, nil
}

func (s *stubGapUsecase) MarkFilled(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filled = append(s.filled, id)
	return nil
}

func (s *stubGapUsecase) RecordFillAttempt(_ context.Context, id int64, note string) (models.DataGap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, note)
	return models.DataGap{Id: id, FillAttempts: int32(len(s.attempts))}, nil
}

func (s *stubGapUsecase) CountUnfilled(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return 0, nil
}

func (s *stubGapUsecase) ListUnfilledInRange(context.Context, datagap.GapRangeParams) ([]models.DataGap, error) {
	return nil, nil
}

// stubStatusRepo records collector status without a database.
type stubStatusRepo struct {
	mu       sync.Mutex
	status   models.CollectorStatus
	starts   int
	connects int
	states   []constants.CollectorState

	// notFound makes FetchStatus behave as it does before any collector has
	// registered; fetchErr makes it fail outright.
	notFound bool
	fetchErr error
}

func (s *stubStatusRepo) RegisterStart(_ context.Context, symbol string, marketType constants.MarketType) (models.CollectorStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.starts++
	s.status = models.CollectorStatus{
		Symbol: symbol, MarketType: marketType,
		State:     constants.CollectorStarting,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	return s.status, nil
}

func (s *stubStatusRepo) Heartbeat(
	_ context.Context, _ string, _ constants.MarketType,
	connected bool, evaluator models.EvaluatorState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.WSConnected = connected
	s.status.Evaluator = evaluator
	s.status.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *stubStatusRepo) MarkConnected(_ context.Context, _ string, _ constants.MarketType, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connects++
	s.status.WSConnected = true
	return nil
}

func (s *stubStatusRepo) MarkDisconnected(_ context.Context, _ string, _ constants.MarketType, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.WSConnected = false
	return nil
}

func (s *stubStatusRepo) SetState(_ context.Context, _ string, _ constants.MarketType, state constants.CollectorState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = state
	s.states = append(s.states, state)
	return nil
}

func (s *stubStatusRepo) FetchStatus(context.Context, string, constants.MarketType) (models.CollectorStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fetchErr != nil {
		return models.CollectorStatus{}, s.fetchErr
	}
	if s.notFound {
		return models.CollectorStatus{}, constants.ErrNotFound
	}
	return s.status, nil
}

// recordedStates returns the transitions seen so far.
func (s *stubStatusRepo) recordedStates() []constants.CollectorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]constants.CollectorState(nil), s.states...)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testConfig() _market_us.Config {
	return _market_us.Config{
		Symbol:            "BTCUSDT",
		MarketType:        constants.MarketTypeSpot,
		Timeframes:        []constants.Timeframe{constants.Timeframe1m},
		BackfillFrom:      time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		GapcheckInterval:  time.Hour,
		HeartbeatInterval: time.Hour,
	}
}

func makeCandle(openTime time.Time, isClosed bool) models.Candle {
	return models.Candle{
		Symbol:      "BTCUSDT",
		MarketType:  constants.MarketTypeSpot,
		Timeframe:   constants.Timeframe1m,
		OpenTime:    openTime,
		CloseTime:   openTime.Add(time.Minute),
		Open:        decimal.RequireFromString("64000"),
		High:        decimal.RequireFromString("64100"),
		Low:         decimal.RequireFromString("63900"),
		Close:       decimal.RequireFromString("64050"),
		Volume:      decimal.RequireFromString("1"),
		QuoteVolume: decimal.RequireFromString("64000"),
		TradeCount:  10,
		IsClosed:    isClosed,
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingLogger captures what was logged, so a test can assert on what the
// operator would have been told.
type recordingLogger struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *recordingLogger) Enabled(context.Context, slog.Level) bool { return true }

func (l *recordingLogger) Handle(_ context.Context, record slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, record)
	return nil
}

func (l *recordingLogger) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *recordingLogger) WithGroup(string) slog.Handler      { return l }

func (l *recordingLogger) logger() *slog.Logger { return slog.New(l) }

// messagesAt returns the messages logged at a level.
func (l *recordingLogger) messagesAt(level slog.Level) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []string
	for _, record := range l.records {
		if record.Level == level {
			out = append(out, record.Message)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestOnlyClosedCandlesReachStorage is the central rule of this phase. A
// forming bar changes on every tick; letting one into the candles table would
// make indicators flicker and a backtest disagree with what really happened.
func TestOnlyClosedCandlesReachStorage(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// A realistic stream: the same bar updates repeatedly while forming, then
	// closes, then the next bar begins.
	stream := []market.StreamedKline{
		{Candle: makeCandle(start, false)},
		{Candle: makeCandle(start, false)},
		{Candle: makeCandle(start, true)},
		{Candle: makeCandle(start.Add(time.Minute), false)},
		{Candle: makeCandle(start.Add(time.Minute), true)},
		{Candle: makeCandle(start.Add(2*time.Minute), false)},
	}

	data := &fakeMarketData{stream: stream, now: start}
	candles := &recordingCandleUsecase{}
	gaps := &stubGapUsecase{}
	status := &stubStatusRepo{}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, status, candles, gaps)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = us.Run(ctx)

	stored := candles.storedCandles()
	if len(stored) != 2 {
		t.Fatalf("stored %d candles, want 2 (only the closed ones): %+v", len(stored), stored)
	}
	for i, c := range stored {
		if !c.IsClosed {
			t.Errorf("stored candle %d is not closed", i)
		}
	}
	if !stored[0].OpenTime.Equal(start) || !stored[1].OpenTime.Equal(start.Add(time.Minute)) {
		t.Errorf("stored the wrong bars: %s, %s", stored[0].OpenTime, stored[1].OpenTime)
	}

	// The last forming bar is available in memory for display, and only there.
	open, ok := us.LatestOpenCandle(constants.Timeframe1m)
	if !ok {
		t.Fatal("the forming candle was not cached")
	}
	if open.IsClosed {
		t.Error("the cached candle is marked closed")
	}
	if !open.OpenTime.Equal(start.Add(2 * time.Minute)) {
		t.Errorf("cached the wrong forming bar: %s", open.OpenTime)
	}
}

// TestClosingABarClearsTheOpenCache guards a subtle staleness bug: once a bar
// closes, the forming version of the same bar must not still be served.
func TestClosingABarClearsTheOpenCache(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	data := &fakeMarketData{
		stream: []market.StreamedKline{
			{Candle: makeCandle(start, false)},
			{Candle: makeCandle(start, true)},
		},
		now: start,
	}
	candles := &recordingCandleUsecase{}
	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = us.Run(ctx)

	if _, ok := us.LatestOpenCandle(constants.Timeframe1m); ok {
		t.Error("a forming candle is still cached after its bar closed")
	}
}

// TestBackfillResumesFromLatestStoredCandle covers restartability: a process
// killed part way through must continue where it stopped, which falls out of
// keying on the newest stored bar rather than any bespoke progress state.
func TestBackfillResumesFromLatestStoredCandle(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	candles := &recordingCandleUsecase{
		hasOne: true,
		latest: makeCandle(start.Add(10*time.Minute), true),
	}
	data := &fakeMarketData{
		pages: [][]models.Candle{
			{makeCandle(start.Add(11*time.Minute), true), makeCandle(start.Add(12*time.Minute), true)},
		},
		now: start.Add(time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}

	stored := candles.storedCandles()
	if len(stored) != 2 {
		t.Fatalf("stored %d candles, want 2", len(stored))
	}
	if !stored[0].OpenTime.Equal(start.Add(11 * time.Minute)) {
		t.Errorf("backfill restarted from %s, want the bar after the stored one", stored[0].OpenTime)
	}
}

// TestBackfillStartsFromConfiguredPointWhenEmpty covers the first ever run.
func TestBackfillStartsFromConfiguredPointWhenEmpty(t *testing.T) {
	cfg := testConfig()
	candles := &recordingCandleUsecase{} // nothing stored
	data := &fakeMarketData{
		pages: [][]models.Candle{{makeCandle(cfg.BackfillFrom, true)}},
		now:   cfg.BackfillFrom.Add(time.Hour),
	}

	us := _market_us.NewMarketUsecaseImpl(cfg, silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	if err := us.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill() returned error: %v", err)
	}
	if got := candles.storedCandles(); len(got) != 1 {
		t.Fatalf("stored %d candles, want 1", len(got))
	}
}

// TestBackfillTerminatesOnRepeatedPage guards against the paging loop that
// never ends. If the exchange keeps returning the same bar, advancing the
// cursor by open_time+1ms must still make progress or stop.
func TestBackfillTerminatesOnRepeatedPage(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	repeated := []models.Candle{makeCandle(start, true)}

	data := &fakeMarketData{
		pages: [][]models.Candle{repeated, repeated, repeated, repeated},
		now:   start.Add(time.Hour),
	}
	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, &stubStatusRepo{}, &recordingCandleUsecase{}, &stubGapUsecase{})

	done := make(chan error, 1)
	go func() { done <- us.Backfill(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Backfill() returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Backfill() did not terminate on a repeating page")
	}
}

// TestRunStopsOnContextCancellation covers the shutdown contract: SIGTERM
// must bring the collector down rather than leave goroutines running.
func TestRunStopsOnContextCancellation(t *testing.T) {
	data := &fakeMarketData{streamErr: constants.ErrStreamClosed, now: time.Now().UTC()}
	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, &stubStatusRepo{}, &recordingCandleUsecase{}, &stubGapUsecase{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- us.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run() returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(constants.ShutdownTimeout):
		t.Fatal("Run() did not return after the context was cancelled")
	}
}

// TestStatusReportsUptimeAndFreshnessSeparately pins the reason the status
// table carries both started_at and updated_at.
func TestStatusReportsUptimeAndFreshnessSeparately(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	status := &stubStatusRepo{
		status: models.CollectorStatus{
			Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
			State:       constants.CollectorLive,
			WSConnected: true,
			StartedAt:   now.Add(-72 * time.Hour), // up for three days
			UpdatedAt:   now.Add(-2 * time.Second),
		},
	}
	candles := &recordingCandleUsecase{hasOne: true, latest: makeCandle(now.Add(-time.Minute), true)}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, candles, &stubGapUsecase{})

	got, err := us.Status(context.Background(), now)
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}

	if uptime := got.Collector.Uptime(now); uptime != 72*time.Hour {
		t.Errorf("uptime = %s, want 72h", uptime)
	}
	if age := got.Collector.HeartbeatAge(now); age != 2*time.Second {
		t.Errorf("heartbeat age = %s, want 2s", age)
	}
	if got.Stale == nil || *got.Stale {
		t.Errorf("Stale = %v, want false: a live collector with a one-minute-old candle is fine", got.Stale)
	}
	if len(got.Timeframes) != 1 || got.Timeframes[0].LatestOpenTime == nil {
		t.Fatalf("timeframe status is incomplete: %+v", got.Timeframes)
	}
}

// TestStatusFlagsStaleWhenConnectedButNotAdvancing covers the combination no
// other check catches.
func TestStatusFlagsStaleWhenConnectedButNotAdvancing(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	status := &stubStatusRepo{
		status: models.CollectorStatus{
			State:       constants.CollectorLive,
			WSConnected: true,
			StartedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Second), // collector is alive
		},
	}
	// ...but the newest candle is far older than the threshold.
	candles := &recordingCandleUsecase{
		hasOne: true,
		latest: makeCandle(now.Add(-10*time.Minute), true),
	}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, candles, &stubGapUsecase{})

	got, err := us.Status(context.Background(), now)
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	if got.Stale == nil || !*got.Stale {
		t.Errorf("Stale = %v, want true: live and connected with a 10 minute old candle", got.Stale)
	}
}

// TestStatusHasNoStaleAnswerWhenDisconnected checks the other half. The
// staleness question is specifically "connected, yet not advancing"; with no
// connection it has no answer, so the result is null rather than a false that
// would read as an all-clear.
func TestStatusHasNoStaleAnswerWhenDisconnected(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	status := &stubStatusRepo{
		status: models.CollectorStatus{
			State:       constants.CollectorLive,
			WSConnected: false,
			StartedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Second),
		},
	}
	candles := &recordingCandleUsecase{hasOne: true, latest: makeCandle(now.Add(-time.Hour), true)}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, candles, &stubGapUsecase{})

	got, err := us.Status(context.Background(), now)
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	if got.Stale != nil {
		t.Errorf("Stale = %v, want null: a disconnected collector is not stale, it is disconnected", *got.Stale)
	}
}

// TestBufferedCandlesSurviveDisconnect pins a bug that silently loses data.
//
// errgroup cancels the shared context the instant StreamKlines returns, which
// happens on every ordinary disconnect. If the writer selects on ctx.Done()
// it exits while the buffer still holds candles the exchange already
// confirmed, and those bars vanish — no error, no log, just a hole that only
// a much later gap scan notices.
func TestBufferedCandlesSurviveDisconnect(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// A burst of closed candles delivered immediately before the stream ends,
	// so they are still in flight when the context is cancelled.
	const burst = 200
	stream := make([]market.StreamedKline, 0, burst)
	for i := range burst {
		stream = append(stream, market.StreamedKline{
			Candle: makeCandle(start.Add(time.Duration(i)*time.Minute), true),
		})
	}

	data := &fakeMarketData{stream: stream, now: start}
	candles := &recordingCandleUsecase{}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, &stubStatusRepo{}, candles, &stubGapUsecase{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = us.Run(ctx)

	stored := candles.storedCandles()
	if len(stored) != burst {
		t.Fatalf("stored %d of %d candles: the disconnect discarded buffered bars", len(stored), burst)
	}

	// Order must be preserved too; the series is only meaningful in sequence.
	for i := 1; i < len(stored); i++ {
		if !stored[i].OpenTime.After(stored[i-1].OpenTime) {
			t.Fatalf("candles were stored out of order at %d: %s after %s",
				i, stored[i].OpenTime, stored[i-1].OpenTime)
		}
	}
}

// TestStatusReportsNeverStartedWithoutError is fix 2: an absent
// collector_status row is a valid state, not a failure.
//
// A dead collector is the single most important thing this endpoint has to be
// able to say. Returning 500 sent the reader back to the container logs —
// exactly the workflow the endpoint exists to replace.
func TestStatusReportsNeverStartedWithoutError(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	status := &stubStatusRepo{notFound: true}
	// The candles table is independent of collector liveness, so per-timeframe
	// data must still render.
	candles := &recordingCandleUsecase{hasOne: true, latest: makeCandle(now.Add(-time.Minute), true)}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, candles, &stubGapUsecase{})

	got, err := us.Status(context.Background(), now)
	if err != nil {
		t.Fatalf("Status() returned error for an empty collector_status: %v", err)
	}
	if got.Collector.State != constants.CollectorNeverStarted {
		t.Errorf("State = %q, want %q", got.Collector.State, constants.CollectorNeverStarted)
	}
	if got.Stale != nil {
		t.Errorf("Stale = %v, want null: no collector ran, so no check ran", *got.Stale)
	}
	if len(got.Timeframes) != 1 || got.Timeframes[0].LatestOpenTime == nil {
		t.Errorf("per-timeframe data must still render from candles: %+v", got.Timeframes)
	}
}

// TestStatusDatabaseFailureIsStillAnError keeps 500 reserved for genuine
// failures, so making the absent row a valid state did not swallow real ones.
func TestStatusDatabaseFailureIsStillAnError(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	status := &stubStatusRepo{fetchErr: errors.New("connection refused")}
	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, &recordingCandleUsecase{}, &stubGapUsecase{})

	if _, err := us.Status(context.Background(), now); err == nil {
		t.Fatal("an unreachable database must still be an error")
	}
}

// TestStaleIsNullOutsideLive is fix 3: during a backfill the newest candle is
// legitimately years old, and reporting false there is indistinguishable from
// a genuine all-clear.
func TestStaleIsNullOutsideLive(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	for _, state := range []constants.CollectorState{
		constants.CollectorStarting,
		constants.CollectorBackfilling,
		constants.CollectorReconnecting,
	} {
		t.Run(state.String(), func(t *testing.T) {
			status := &stubStatusRepo{
				status: models.CollectorStatus{
					State:       state,
					WSConnected: true,
					StartedAt:   now.Add(-time.Hour),
					UpdatedAt:   now.Add(-time.Second),
				},
			}
			// Deliberately ancient, the way a mid-backfill series looks.
			candles := &recordingCandleUsecase{
				hasOne: true,
				latest: makeCandle(now.Add(-3*365*24*time.Hour), true),
			}

			us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), &fakeMarketData{now: now}, status, candles, &stubGapUsecase{})

			got, err := us.Status(context.Background(), now)
			if err != nil {
				t.Fatalf("Status() returned error: %v", err)
			}
			if got.Stale != nil {
				t.Errorf("Stale = %v in state %s, want null: the check does not apply here",
					*got.Stale, state)
			}
		})
	}
}

// TestRunPublishesLifecycleTransitions covers the states moving in the order
// the collector actually goes through them.
func TestRunPublishesLifecycleTransitions(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	data := &fakeMarketData{
		stream: []market.StreamedKline{{Candle: makeCandle(start, true)}},
		now:    start,
	}
	status := &stubStatusRepo{}

	us := _market_us.NewMarketUsecaseImpl(testConfig(), silentLogger(), data, status,
		&recordingCandleUsecase{}, &stubGapUsecase{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = us.Run(ctx)

	states := status.recordedStates()
	if len(states) < 2 {
		t.Fatalf("recorded %d transitions, want at least backfilling then live: %v", len(states), states)
	}
	if states[0] != constants.CollectorBackfilling {
		t.Errorf("first transition is %q, want %q", states[0], constants.CollectorBackfilling)
	}

	sawLive := false
	for _, state := range states {
		if state == constants.CollectorLive {
			sawLive = true
		}
		if !state.Valid() {
			t.Errorf("published an unknown state %q", state)
		}
	}
	if !sawLive {
		t.Errorf("never reached %q: %v", constants.CollectorLive, states)
	}
}
