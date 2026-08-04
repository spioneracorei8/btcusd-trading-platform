package binance

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// Options configures the Binance client.
type Options struct {
	// RESTBaseURL and WSBaseURL come from config so futures endpoints can be
	// swapped in without touching call sites.
	RESTBaseURL string
	WSBaseURL   string

	// HTTPClient is optional; tests supply one pointed at httptest.
	HTTPClient *http.Client

	// Now is optional and exists so tests can control the clock.
	Now func() time.Time
}

// client implements market.MarketDataRepository against Binance.
type client struct {
	restBaseURL string
	wsBaseURL   string
	httpClient  *http.Client
	now         func() time.Time

	// weight tracks the REST budget Binance reports back to us.
	weight *weightTracker
}

// requestTimeout bounds a single REST call. A kline page is small; anything
// slower than this is a stuck connection, not a slow response.
const requestTimeout = 30 * time.Second

// NewMarketDataRepoImpl builds the Binance market data repository.
func NewMarketDataRepoImpl(opts Options) market.MarketDataRepository {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	return &client{
		restBaseURL: strings.TrimRight(opts.RESTBaseURL, "/"),
		wsBaseURL:   strings.TrimRight(opts.WSBaseURL, "/"),
		httpClient:  httpClient,
		now:         now,
		weight:      newWeightTracker(),
	}
}

// streamName builds the lower-case stream identifier Binance expects, e.g.
// "btcusdt@kline_1m".
func streamName(symbol string, timeframe constants.Timeframe) string {
	return fmt.Sprintf("%s@kline_%s", strings.ToLower(symbol), timeframe)
}
