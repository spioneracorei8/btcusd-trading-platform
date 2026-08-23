package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
)

var (
	testSymbol     = "BTCUSDT"
	testTimeframes = []constants.Timeframe{constants.Timeframe1m, constants.Timeframe5m}
	fixedNow       = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeCandles serves a prepared series and records what it was asked for.
type fakeCandles struct {
	series []models.Candle
	err    error

	asked candle.FetchCandlesParams
	calls int
}

func (f *fakeCandles) FetchCandles(_ context.Context, params candle.FetchCandlesParams) ([]models.Candle, error) {
	f.asked = params
	f.calls++
	return f.series, f.err
}

func (f *fakeCandles) SaveCandle(context.Context, models.Candle) error    { return nil }
func (f *fakeCandles) SaveCandles(context.Context, []models.Candle) error { return nil }
func (f *fakeCandles) FindGaps(context.Context, string, constants.MarketType, constants.Timeframe) ([]candle.Gap, error) {
	return nil, nil
}
func (f *fakeCandles) OpenCursor(candle.FetchCandlesParams) candle.CandleCursor { return nil }
func (f *fakeCandles) StreamCandles(context.Context, candle.FetchCandlesParams, func(models.Candle) error) error {
	return nil
}
func (f *fakeCandles) FetchLatestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}
func (f *fakeCandles) FetchEarliestCandle(context.Context, string, constants.MarketType, constants.Timeframe) (models.Candle, error) {
	return models.Candle{}, constants.ErrNotFound
}
func (f *fakeCandles) CountCandles(context.Context, string, constants.MarketType, constants.Timeframe) (int64, error) {
	return int64(len(f.series)), nil
}

func newHandler(t *testing.T, fake *fakeCandles) candle.CandleHandler {
	t.Helper()

	h := NewCandleHandlerImpl(fake, quiet(), testSymbol, constants.MarketTypeSpot, testTimeframes)
	// The default clock is time.Now, which would make every window assertion
	// depend on when the test ran.
	h.(*candleHandler).now = func() time.Time { return fixedNow }
	return h
}

func series(n int, from time.Time, step time.Duration) []models.Candle {
	out := make([]models.Candle, 0, n)
	for i := 0; i < n; i++ {
		open := from.Add(time.Duration(i) * step)
		out = append(out, models.Candle{
			Symbol: testSymbol, MarketType: constants.MarketTypeSpot,
			Timeframe: constants.Timeframe1m,
			OpenTime:  open, CloseTime: open.Add(step - time.Millisecond),
			Open:  decimal.NewFromInt(int64(64000 + i)),
			High:  decimal.NewFromInt(int64(64100 + i)),
			Low:   decimal.NewFromInt(int64(63900 + i)),
			Close: decimal.NewFromInt(int64(64050 + i)),

			Volume:   decimal.NewFromInt(1),
			IsClosed: true,
		})
	}
	return out
}

func get(t *testing.T, h candle.CandleHandler, query string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()

	recorder := httptest.NewRecorder()
	h.Candles(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/candles"+query, nil))

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return recorder, body
}

// TestATimeframeThatIsNotCollectedIsRefused.
//
// # What this prevents
//
// A deployment collects the timeframes in MARKET_TIMEFRAMES and nothing else.
// Asked for one it does not collect, an empty list would be the honest-looking
// answer and the wrong one: it reads as "the market did nothing", when the
// truth is "this server was never told to watch that". The first sends
// somebody looking at the strategy; the second at the .env.
func TestATimeframeThatIsNotCollectedIsRefused(t *testing.T) {
	fake := &fakeCandles{series: series(3, fixedNow.Add(-3*time.Minute), time.Minute)}
	h := newHandler(t, fake)

	for _, query := range []string{"?timeframe=3m", "?timeframe=1h", ""} {
		recorder, body := get(t, h, query)

		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400", query, recorder.Code)
		}
		if _, ok := body["error"]; !ok {
			t.Errorf("%q returned no error body: %s", query, recorder.Body)
		}
		if fake.calls != 0 {
			t.Fatalf("%q reached the database; the timeframe is checked first", query)
		}
	}
}

// TestTheWindowIsPassedThroughToTheQuery, rather than being widened or
// ignored. A handler that dropped from and to would return the newest page of
// the whole series, which looks correct on a chart of recent data and is wrong
// everywhere else.
func TestTheWindowIsPassedThroughToTheQuery(t *testing.T) {
	fake := &fakeCandles{}
	h := newHandler(t, fake)

	get(t, h, "?timeframe=1m&from=2024-02-01T00:00:00Z&to=2024-02-02T00:00:00Z")

	wantFrom := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC)

	if !fake.asked.From.Equal(wantFrom) {
		t.Errorf("queried from %s, want %s", fake.asked.From, wantFrom)
	}
	if !fake.asked.To.Equal(wantTo) {
		t.Errorf("queried to %s, want %s", fake.asked.To, wantTo)
	}
	if fake.asked.Timeframe != constants.Timeframe1m {
		t.Errorf("queried timeframe %s, want 1m", fake.asked.Timeframe)
	}
	if fake.asked.Symbol != testSymbol {
		t.Errorf("queried symbol %q, want %q", fake.asked.Symbol, testSymbol)
	}
}

// TestTheDefaultWindowIsTheLimitWorthOfBars.
//
// Without from and to, a chart opening on a fresh screen should get the most
// recent limit bars. A default of "everything" would page in the whole series
// on the first request of a cold app.
func TestTheDefaultWindowIsTheLimitWorthOfBars(t *testing.T) {
	fake := &fakeCandles{}
	h := newHandler(t, fake)

	get(t, h, "?timeframe=5m&limit=10")

	wantFrom := fixedNow.Add(-10 * 5 * time.Minute)
	if !fake.asked.From.Equal(wantFrom) {
		t.Errorf("default from = %s, want %s (10 bars of 5m before now)", fake.asked.From, wantFrom)
	}
	if !fake.asked.To.Equal(fixedNow) {
		t.Errorf("default to = %s, want now (%s)", fake.asked.To, fixedNow)
	}
}

// TestAWindowWiderThanTheLimitReturnsTheNewestBarsAndSaysSo.
//
// Truncating from the wrong end is the failure this guards: a chart would open
// on the oldest bars of the range and look like the market had stopped. The
// truncated flag is what lets a client tell a short page from the last page.
func TestAWindowWiderThanTheLimitReturnsTheNewestBarsAndSaysSo(t *testing.T) {
	start := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	fake := &fakeCandles{series: series(10, start, time.Minute)}
	h := newHandler(t, fake)

	_, body := get(t, h, "?timeframe=1m&limit=3&from=2024-02-01T00:00:00Z&to=2024-02-01T01:00:00Z")

	var page struct {
		Count     int  `json:"count"`
		Limit     int  `json:"limit"`
		Truncated bool `json:"truncated"`
		Candles   []struct {
			OpenTime time.Time `json:"open_time"`
			IsClosed bool      `json:"is_closed"`
		} `json:"candles"`
	}
	if err := json.Unmarshal(mustAll(t, body), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if page.Count != 3 || page.Limit != 3 {
		t.Fatalf("count=%d limit=%d, want 3 and 3", page.Count, page.Limit)
	}
	if !page.Truncated {
		t.Error("truncated is false; the window held 10 bars and 3 were returned")
	}
	if got := page.Candles[0].OpenTime; !got.Equal(start.Add(7 * time.Minute)) {
		t.Errorf("first bar is %s, want the 8th of 10 (%s): the newest are kept",
			got, start.Add(7*time.Minute))
	}
	for i, c := range page.Candles {
		if !c.IsClosed {
			t.Errorf("bar %d is not flagged closed; only closed candles are stored", i)
		}
	}
}

// TestALimitAboveTheMaximumIsRefusedWithItsOwnCode, so a client can tell
// "page instead" from "that was not a number" without reading English.
func TestALimitAboveTheMaximumIsRefusedWithItsOwnCode(t *testing.T) {
	fake := &fakeCandles{}
	h := newHandler(t, fake)

	recorder, _ := get(t, h, "?timeframe=1m&limit=999999")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}

	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Error.Code != string(constants.APIErrLimitExceeded) {
		t.Errorf("code = %q, want %q", failure.Error.Code, constants.APIErrLimitExceeded)
	}

	recorder, _ = get(t, h, "?timeframe=1m&limit=banana")
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Error.Code != string(constants.APIErrInvalidParameter) {
		t.Errorf("code for a non-number = %q, want %q",
			failure.Error.Code, constants.APIErrInvalidParameter)
	}
	if fake.calls != 0 {
		t.Error("a rejected limit still reached the database")
	}
}

// TestAReversedWindowIsRefused. Left alone it returns an empty list, which
// reads as "no data in that range" rather than "you asked backwards".
func TestAReversedWindowIsRefused(t *testing.T) {
	fake := &fakeCandles{}
	h := newHandler(t, fake)

	recorder, _ := get(t, h, "?timeframe=1m&from=2024-02-02T00:00:00Z&to=2024-02-01T00:00:00Z")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	if fake.calls != 0 {
		t.Error("a reversed window still reached the database")
	}
}

// TestAReadFailureIsFiveHundredAndSaysNothingElse.
//
// The message must not carry the driver's error: a connection string with a
// password in it has reached a client that way before, in other systems.
func TestAReadFailureIsFiveHundredAndSaysNothingElse(t *testing.T) {
	fake := &fakeCandles{err: context.DeadlineExceeded}
	h := newHandler(t, fake)

	recorder, _ := get(t, h, "?timeframe=1m")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "context deadline") {
		t.Errorf("the internal error leaked to the client: %s", body)
	}
}

// TestAnEmptyResultIsAnEmptyArray. A null candles field would make a client
// handle two shapes for a quiet window.
func TestAnEmptyResultIsAnEmptyArray(t *testing.T) {
	fake := &fakeCandles{}
	h := newHandler(t, fake)

	_, body := get(t, h, "?timeframe=1m")
	if got := string(body["candles"]); got != "[]" {
		t.Fatalf("candles = %s, want []", got)
	}
}

func mustAll(t *testing.T, body map[string]json.RawMessage) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	return encoded
}
