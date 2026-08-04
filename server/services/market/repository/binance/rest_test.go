package binance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// fakeExchange is a stand-in for Binance. Tests never reach the network.
type fakeExchange struct {
	klines     []byte
	serverTime time.Time

	klineStatus  int
	klineHeaders map[string]string

	// requests records the query of every klines call, so paging can be
	// asserted.
	requests []string
}

func (f *fakeExchange) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(pathServerTime, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"serverTime":%d}`, f.serverTime.UnixMilli())
	})

	mux.HandleFunc(pathKlines, func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.URL.RawQuery)
		for k, v := range f.klineHeaders {
			w.Header().Set(k, v)
		}
		if f.klineStatus != 0 && f.klineStatus != http.StatusOK {
			w.WriteHeader(f.klineStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(f.klines)
	})
	return mux
}

// newTestClient wires a client to a fake exchange with a fixed clock.
func newTestClient(t *testing.T, fake *fakeExchange, now time.Time) (market.MarketDataRepository, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	repo := NewMarketDataRepoImpl(Options{
		RESTBaseURL: srv.URL,
		WSBaseURL:   "wss://example.invalid",
		HTTPClient:  srv.Client(),
		Now:         func() time.Time { return now },
	})
	return repo, srv
}

func fetchParams() market.FetchKlinesParams {
	return market.FetchKlinesParams{
		Symbol:     "BTCUSDT",
		MarketType: constants.MarketTypeSpot,
		Timeframe:  constants.Timeframe1m,
	}
}

func TestFetchKlinesReturnsClosedCandles(t *testing.T) {
	fake := &fakeExchange{
		klines: readFixture(t, "klines_1m.json"),
		// Well after both fixture bars have closed.
		serverTime: time.UnixMilli(1767225800000).UTC(),
	}
	repo, _ := newTestClient(t, fake, time.Now())

	candles, err := repo.FetchKlines(context.Background(), fetchParams())
	if err != nil {
		t.Fatalf("FetchKlines() returned error: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("FetchKlines() returned %d candles, want 2", len(candles))
	}
	for i, c := range candles {
		if !c.IsClosed {
			t.Errorf("candle %d is not marked closed", i)
		}
	}
	if got := candles[0].Close.String(); got != "64080.25" {
		t.Errorf("first close = %s, want 64080.25", got)
	}
}

// TestFetchKlinesDiscardsTheStillOpenBar is the rule that keeps a forming
// candle out of the table: Binance includes the current bar as the last entry
// of a page reaching the present, and only the exchange clock reveals it.
func TestFetchKlinesDiscardsTheStillOpenBar(t *testing.T) {
	fake := &fakeExchange{
		klines: readFixture(t, "klines_1m.json"),
		// Between the two bars: the first has closed, the second has not.
		serverTime: time.UnixMilli(1767225700000).UTC(),
	}
	repo, _ := newTestClient(t, fake, time.Now())

	candles, err := repo.FetchKlines(context.Background(), fetchParams())
	if err != nil {
		t.Fatalf("FetchKlines() returned error: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("FetchKlines() returned %d candles, want 1 (the open bar must be dropped)", len(candles))
	}
	if got := candles[0].OpenTime.UnixMilli(); got != 1767225600000 {
		t.Errorf("kept the wrong candle: open time %d", got)
	}
}

func TestFetchKlinesSendsRangeAndLimit(t *testing.T) {
	fake := &fakeExchange{
		klines:     []byte("[]"),
		serverTime: time.UnixMilli(1767225800000).UTC(),
	}
	repo, _ := newTestClient(t, fake, time.Now())

	params := fetchParams()
	params.From = time.UnixMilli(1767225600000).UTC()
	params.To = time.UnixMilli(1767225900000).UTC()
	params.Limit = 500

	if _, err := repo.FetchKlines(context.Background(), params); err != nil {
		t.Fatalf("FetchKlines() returned error: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("made %d kline requests, want 1", len(fake.requests))
	}

	query := fake.requests[0]
	for _, want := range []string{
		"symbol=BTCUSDT", "interval=1m", "limit=500",
		"startTime=1767225600000", "endTime=1767225900000",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q is missing %q", query, want)
		}
	}
}

func TestFetchKlinesCapsLimitAtExchangeMaximum(t *testing.T) {
	fake := &fakeExchange{klines: []byte("[]"), serverTime: time.Now().UTC()}
	repo, _ := newTestClient(t, fake, time.Now())

	params := fetchParams()
	params.Limit = 5000

	if _, err := repo.FetchKlines(context.Background(), params); err != nil {
		t.Fatalf("FetchKlines() returned error: %v", err)
	}
	if want := "limit=" + strconv.Itoa(constants.KlineLimit); !strings.Contains(fake.requests[0], want) {
		t.Errorf("query %q does not cap the limit to %d", fake.requests[0], constants.KlineLimit)
	}
}

// TestFetchKlinesTreatsRateLimitAsHardStop covers 429 and 418. Retrying
// either immediately is how a temporary throttle becomes an IP ban, which
// would take the collector off the air for hours.
func TestFetchKlinesTreatsRateLimitAsHardStop(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusTeapot} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			fake := &fakeExchange{
				klines:       []byte("[]"),
				serverTime:   now,
				klineStatus:  status,
				klineHeaders: map[string]string{constants.RetryAfterHeader: "30"},
			}
			repo, _ := newTestClient(t, fake, now)

			_, err := repo.FetchKlines(context.Background(), fetchParams())
			if err == nil {
				t.Fatalf("http %d did not produce an error", status)
			}
			if !errors.Is(err, constants.ErrRateLimited) {
				t.Fatalf("error %v does not wrap ErrRateLimited", err)
			}

			// The next call must not even be attempted until the block expires.
			before := len(fake.requests)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if _, err := repo.FetchKlines(ctx, fetchParams()); err == nil {
				t.Error("a request was allowed through while rate limited")
			}
			if len(fake.requests) != before {
				t.Errorf("made %d more requests while blocked", len(fake.requests)-before)
			}
		})
	}
}

func TestFetchKlinesRejectsNonSpotMarket(t *testing.T) {
	fake := &fakeExchange{klines: []byte("[]"), serverTime: time.Now().UTC()}
	repo, _ := newTestClient(t, fake, time.Now())

	params := fetchParams()
	params.MarketType = constants.MarketTypeFutures

	if _, err := repo.FetchKlines(context.Background(), params); err == nil {
		t.Fatal("futures market data is not implemented and must be refused")
	}
}

func TestServerTime(t *testing.T) {
	want := time.UnixMilli(1767225800000).UTC()
	fake := &fakeExchange{klines: []byte("[]"), serverTime: want}
	repo, _ := newTestClient(t, fake, time.Now())

	got, err := repo.ServerTime(context.Background())
	if err != nil {
		t.Fatalf("ServerTime() returned error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("ServerTime() = %s, want %s", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("ServerTime() location = %v, want UTC", got.Location())
	}
}

// TestWeightTrackerPausesBeforeTheCap checks the voluntary pause: the client
// slows down as it approaches the published budget rather than waiting to be
// refused.
func TestWeightTrackerPausesBeforeTheCap(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 30, 0, time.UTC)
	w := newWeightTracker()

	header := http.Header{}
	header.Set(constants.UsedWeightHeader, strconv.Itoa(softLimit()-1))
	w.observe(header)
	if got := w.waitFor(now); got != 0 {
		t.Errorf("waitFor() = %s below the soft limit, want 0", got)
	}

	header.Set(constants.UsedWeightHeader, strconv.Itoa(softLimit()))
	w.observe(header)
	// 30 seconds into the minute, the counter resets in another 30.
	if got := w.waitFor(now); got != 30*time.Second {
		t.Errorf("waitFor() = %s at the soft limit, want 30s", got)
	}
}

func TestWeightTrackerHonoursRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	header := http.Header{}
	header.Set(constants.RetryAfterHeader, "45")
	retryAfter, err := rateLimitError(http.StatusTooManyRequests, header)
	if err == nil {
		t.Fatal("rateLimitError() returned no error")
	}
	if retryAfter != 45*time.Second {
		t.Errorf("retryAfter = %s, want 45s", retryAfter)
	}

	// Without the header the client still waits rather than retrying at once.
	bare, err := rateLimitError(http.StatusTeapot, http.Header{})
	if err == nil {
		t.Fatal("rateLimitError() returned no error")
	}
	if bare != constants.RateLimitCooldown {
		t.Errorf("default cooldown = %s, want %s", bare, constants.RateLimitCooldown)
	}

	w := newWeightTracker()
	w.block(now, 45*time.Second)
	if got := w.waitFor(now.Add(10 * time.Second)); got != 35*time.Second {
		t.Errorf("waitFor() = %s, want 35s remaining", got)
	}
	if got := w.waitFor(now.Add(time.Minute)); got != 0 {
		t.Errorf("waitFor() = %s after the block expired, want 0", got)
	}
}
