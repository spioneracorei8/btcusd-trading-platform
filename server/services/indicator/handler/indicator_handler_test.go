package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
)

const testSymbol = "BTCUSDT"

var (
	testTimeframes = []constants.Timeframe{constants.Timeframe1m, constants.Timeframe1h}
	seriesStart    = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fixedNow       = seriesStart.Add(2000 * time.Minute)
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// smallConfig keeps the warm-up short enough that a test series can cover it
// several times over, which is what makes "with warm-up" and "without" two
// visibly different numbers.
func smallConfig() _indicator_us.SetConfig {
	return _indicator_us.SetConfig{EMAPeriod: 10, RSIPeriod: 5, ATRPeriod: 5}
}

// fakeCandles serves a window out of a prepared series, the way the repository
// does, and records what it was asked for.
type fakeCandles struct {
	series []models.Candle
	err    error

	asked candle.FetchCandlesParams
	calls int
}

func (f *fakeCandles) FetchCandles(_ context.Context, params candle.FetchCandlesParams) ([]models.Candle, error) {
	f.asked = params
	f.calls++
	if f.err != nil {
		return nil, f.err
	}

	var window []models.Candle
	for _, c := range f.series {
		if c.OpenTime.Before(params.From) || c.OpenTime.After(params.To) {
			continue
		}
		window = append(window, c)
	}
	return window, nil
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

// wavy builds a series whose price actually moves, so an EMA converging from
// two different starting points gives two different answers. A flat or a
// perfectly linear series would make the warm-up assertion vacuous.
func wavy(n int) []models.Candle {
	out := make([]models.Candle, 0, n)
	for i := 0; i < n; i++ {
		open := seriesStart.Add(time.Duration(i) * time.Minute)
		price := 64000 + 900*math.Sin(float64(i)/7) + 300*math.Cos(float64(i)/3)

		out = append(out, models.Candle{
			Symbol: testSymbol, MarketType: constants.MarketTypeSpot,
			Timeframe: constants.Timeframe1m,
			OpenTime:  open, CloseTime: open.Add(time.Minute - time.Millisecond),
			Open:  decimal.NewFromFloat(price),
			High:  decimal.NewFromFloat(price + 40),
			Low:   decimal.NewFromFloat(price - 40),
			Close: decimal.NewFromFloat(price),

			Volume:   decimal.NewFromInt(1),
			IsClosed: true,
		})
	}
	return out
}

type indicatorPage struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	WarmupBars int       `json:"warmup_bars"`
	BarsRead   int       `json:"bars_read"`
	Count      int       `json:"count"`
	Values     []struct {
		OpenTime time.Time `json:"open_time"`
		EMA      float64   `json:"ema"`
		RSI      float64   `json:"rsi"`
		ATR      float64   `json:"atr"`
		VWAP     float64   `json:"vwap"`
	} `json:"values"`
}

func request(t *testing.T, fake *fakeCandles, query string) (*httptest.ResponseRecorder, indicatorPage) {
	t.Helper()

	h := NewIndicatorHandlerImpl(fake, smallConfig(), quiet(),
		testSymbol, constants.MarketTypeSpot, testTimeframes)
	h.(*indicatorHandler).now = func() time.Time { return fixedNow }

	recorder := httptest.NewRecorder()
	h.Indicators(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/indicators"+query, nil))

	var page indicatorPage
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode %s: %v", recorder.Body, err)
		}
	}
	return recorder, page
}

// TestTheWarmUpIsReadAndThenDiscarded.
//
// # What this prevents
//
// Indicators are recomputed on every request rather than stored, so the values
// are only meaningful once the set has converged. Reading exactly the window a
// client asked for would serve numbers from a cold EMA — plausible, charted
// without complaint, and wrong.
//
// The test asserts both halves. The read must reach back a warm-up before
// `from`, and nothing before `from` may appear in the response: a client that
// asked for one hour must not be handed the previous three because the server
// happened to read them.
func TestTheWarmUpIsReadAndThenDiscarded(t *testing.T) {
	fake := &fakeCandles{series: wavy(600)}

	from := seriesStart.Add(500 * time.Minute)
	to := seriesStart.Add(560 * time.Minute)

	_, page := request(t, fake,
		"?timeframe=1m&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339))

	if page.WarmupBars <= 0 {
		t.Fatalf("warmup_bars = %d, want a positive warm-up", page.WarmupBars)
	}

	wantReadFrom := from.Add(-time.Duration(page.WarmupBars) * time.Minute)
	if !fake.asked.From.Equal(wantReadFrom) {
		t.Errorf("read from %s, want %s (%d bars of warm-up before the window)",
			fake.asked.From, wantReadFrom, page.WarmupBars)
	}
	if !fake.asked.To.Equal(to) {
		t.Errorf("read to %s, want %s", fake.asked.To, to)
	}

	if page.BarsRead <= page.Count {
		t.Errorf("bars_read = %d and count = %d; the read must be wider than the answer",
			page.BarsRead, page.Count)
	}
	for _, value := range page.Values {
		if value.OpenTime.Before(from) {
			t.Fatalf("the response carries %s, from before the requested window %s",
				value.OpenTime, from)
		}
		if value.OpenTime.After(to) {
			t.Fatalf("the response carries %s, from after the requested window %s",
				value.OpenTime, to)
		}
	}
}

// TestAValueIsWarmRatherThanConvergingFromTheWindowsOwnFirstBar.
//
// # What this prevents
//
// This is what the warm-up read buys, stated as a number rather than as a
// comment. The same bar is asked for twice: once with the series reaching well
// back before the window, and once with the series beginning at the window. If
// the handler ignored the warm-up the two would agree, because both would be a
// cold set started at `from`.
//
// The window is wide enough that the cold set converges inside it, so there is
// a bar both runs produce and the comparison is a real one rather than a
// missing value. No skip and no early return: this test either compares two
// numbers or it fails.
func TestAValueIsWarmRatherThanConvergingFromTheWindowsOwnFirstBar(t *testing.T) {
	full := wavy(800)
	from := seriesStart.Add(400 * time.Minute)
	to := from.Add(200 * time.Minute)
	window := "?timeframe=1m&from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)

	_, warm := request(t, &fakeCandles{series: full}, window)

	// The same request against a series that begins at the window, so there
	// is no warm-up to read however hard the handler tries.
	var truncated []models.Candle
	for _, c := range full {
		if !c.OpenTime.Before(from) {
			truncated = append(truncated, c)
		}
	}
	_, cold := request(t, &fakeCandles{series: truncated}, window)

	if len(warm.Values) == 0 || len(cold.Values) == 0 {
		t.Fatalf("warm returned %d values and cold %d; both must produce some for the "+
			"comparison to mean anything", len(warm.Values), len(cold.Values))
	}

	// The warm run starts at `from`; the cold one only once it has converged.
	// That difference is itself the point, so assert it before comparing.
	if !warm.Values[0].OpenTime.Before(cold.Values[0].OpenTime) {
		t.Fatalf("warm starts at %s and cold at %s; with a warm-up the first value must "+
			"come earlier", warm.Values[0].OpenTime, cold.Values[0].OpenTime)
	}

	// The cold run's first bar exists in both. At that instant the warm set
	// has seen the warm-up plus the window so far, the cold set only the
	// window, and the two EMAs must differ.
	at := cold.Values[0].OpenTime
	coldEMA := cold.Values[0].EMA

	var warmEMA *float64
	for i := range warm.Values {
		if warm.Values[i].OpenTime.Equal(at) {
			warmEMA = &warm.Values[i].EMA
			break
		}
	}
	if warmEMA == nil {
		t.Fatalf("the warm run has no value at %s, which the cold run produced", at)
	}

	if math.Abs(*warmEMA-coldEMA) < 1e-6 {
		t.Fatalf("the EMA at %s is %v with warm-up and %v without; they must differ, "+
			"or the warm-up read is not reaching the computation", at, *warmEMA, coldEMA)
	}
}

// TestAWindowTooShortToConvergeReturnsNothingAndExplainsWhy.
//
// Zero values with no explanation reads as "the market did nothing". The
// warm_up and bars_read fields are what turn it into "the series does not
// reach back far enough yet", which is a different thing to go and fix.
func TestAWindowTooShortToConvergeReturnsNothingAndExplainsWhy(t *testing.T) {
	// A series that begins at the requested window: nothing to warm up on.
	from := seriesStart
	to := seriesStart.Add(3 * time.Minute)

	fake := &fakeCandles{series: wavy(4)}
	_, page := request(t, fake,
		"?timeframe=1m&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339))

	if page.Count != 0 {
		t.Fatalf("count = %d, want 0: four bars cannot converge a 10 period EMA", page.Count)
	}
	if page.BarsRead != 4 {
		t.Errorf("bars_read = %d, want 4 so the shortfall is visible", page.BarsRead)
	}
	if page.WarmupBars <= page.BarsRead {
		t.Errorf("warmup_bars = %d and bars_read = %d; the response must show that the "+
			"series is shorter than the warm-up", page.WarmupBars, page.BarsRead)
	}
}

// TestTheValuesAreTheSameAsEvaluatingTheSetDirectly.
//
// The endpoint must not be a second implementation of anything. Whatever it
// returns has to equal running the same set over the same bars, or the chart
// and the strategy are looking at different numbers.
func TestTheValuesAreTheSameAsEvaluatingTheSetDirectly(t *testing.T) {
	full := wavy(600)
	from := seriesStart.Add(500 * time.Minute)
	to := seriesStart.Add(520 * time.Minute)

	fake := &fakeCandles{series: full}
	_, page := request(t, fake,
		"?timeframe=1m&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339))

	set, err := _indicator_us.NewSet(smallConfig())
	if err != nil {
		t.Fatalf("build the set: %v", err)
	}

	var fed []models.Candle
	for _, c := range full {
		if c.OpenTime.Before(fake.asked.From) || c.OpenTime.After(fake.asked.To) {
			continue
		}
		fed = append(fed, c)
	}

	var expected []indicator.Snapshot
	for _, snapshot := range _indicator_us.EvaluateSet(set, fed) {
		if snapshot.OpenTime.Before(from) {
			continue
		}
		expected = append(expected, snapshot)
	}

	if len(page.Values) != len(expected) {
		t.Fatalf("returned %d values, want %d", len(page.Values), len(expected))
	}
	for i, want := range expected {
		got := page.Values[i]
		if !got.OpenTime.Equal(want.OpenTime.UTC()) {
			t.Fatalf("value %d is at %s, want %s", i, got.OpenTime, want.OpenTime)
		}
		for _, pair := range []struct {
			name      string
			got, want float64
		}{
			{"ema", got.EMA, want.EMA},
			{"rsi", got.RSI, want.RSI},
			{"atr", got.ATR, want.ATR},
			{"vwap", got.VWAP, want.VWAP},
		} {
			if math.Abs(pair.got-pair.want) > 1e-9 {
				t.Errorf("value %d %s = %v, want %v", i, pair.name, pair.got, pair.want)
			}
		}
	}
}

// TestAWindowWiderThanTheCapIsRefusedBeforeReading.
//
// Every request pays a warm-up read on top of the window it asks for. An
// unbounded window would page the whole series into memory to answer one
// request, so the cap is checked before anything is read rather than after.
func TestAWindowWiderThanTheCapIsRefusedBeforeReading(t *testing.T) {
	fake := &fakeCandles{series: wavy(10)}

	from := seriesStart
	to := seriesStart.Add(time.Duration(constants.APICandleLimit+10) * time.Minute)

	recorder, _ := request(t, fake,
		"?timeframe=1m&from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	if fake.calls != 0 {
		t.Error("the cap was checked after reading; it must be checked before")
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
}

// TestATimeframeThatIsNotCollectedIsRefused, for the same reason as on the
// candles endpoint: an empty list reads as "no data" rather than "not
// configured".
func TestATimeframeThatIsNotCollectedIsRefused(t *testing.T) {
	fake := &fakeCandles{series: wavy(600)}

	for _, query := range []string{"", "?timeframe=4h", "?timeframe=nonsense"} {
		recorder, _ := request(t, fake, query)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400", query, recorder.Code)
		}
	}
	if fake.calls != 0 {
		t.Error("a refused timeframe still reached the database")
	}
}

// TestPeriodsAreReportedSoNothingIsComparedBlindly.
//
// A chart showing an EMA has to say which one. Without the periods a client
// comparing two deployments, or one deployment before and after a config
// change, is comparing different indicators under one name.
func TestPeriodsAreReportedSoNothingIsComparedBlindly(t *testing.T) {
	fake := &fakeCandles{series: wavy(600)}
	recorder, _ := request(t, fake, "?timeframe=1m")

	var body struct {
		Periods struct {
			EMA int `json:"ema"`
			RSI int `json:"rsi"`
			ATR int `json:"atr"`
		} `json:"periods"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := smallConfig()
	if body.Periods.EMA != want.EMAPeriod || body.Periods.RSI != want.RSIPeriod ||
		body.Periods.ATR != want.ATRPeriod {
		t.Fatalf("periods = %+v, want ema=%d rsi=%d atr=%d",
			body.Periods, want.EMAPeriod, want.RSIPeriod, want.ATRPeriod)
	}
}
