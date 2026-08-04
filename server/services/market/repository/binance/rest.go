package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

// Public market data endpoints. Nothing here can place, amend or cancel an
// order, and no endpoint that could may be added.
const (
	pathKlines     = "/api/v3/klines"
	pathServerTime = "/api/v3/time"
)

// maxResponseBytes caps how much of a response is read, so a malfunctioning
// endpoint cannot exhaust memory.
const maxResponseBytes = 16 << 20 // 16 MiB

// FetchKlines returns one page of closed candles, oldest first.
//
// A candle that has not closed yet is never returned: Binance includes the
// currently forming bar as the last entry of a page whose range reaches the
// present, and storing that would put a flickering bar into the table
// everything downstream trusts.
func (c *client) FetchKlines(ctx context.Context, params market.FetchKlinesParams) ([]models.Candle, error) {
	if params.MarketType != constants.MarketTypeSpot {
		return nil, fmt.Errorf("binance: %s market data is not implemented", params.MarketType)
	}

	limit := params.Limit
	if limit <= 0 || limit > constants.KlineLimit {
		limit = constants.KlineLimit
	}

	query := url.Values{}
	query.Set("symbol", params.Symbol)
	query.Set("interval", params.Timeframe.String())
	query.Set("limit", strconv.Itoa(limit))
	if !params.From.IsZero() {
		query.Set("startTime", strconv.FormatInt(params.From.UTC().UnixMilli(), 10))
	}
	if !params.To.IsZero() {
		query.Set("endTime", strconv.FormatInt(params.To.UTC().UnixMilli(), 10))
	}

	body, err := c.get(ctx, pathKlines, query)
	if err != nil {
		return nil, err
	}

	var raw []restKline
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: decode klines: %w", constants.ErrUnexpectedPayload, err)
	}

	// The exchange clock decides what has closed, not ours: a local clock a
	// few seconds fast would discard good candles, a slow one would store an
	// open bar.
	serverTime, err := c.ServerTime(ctx)
	if err != nil {
		return nil, err
	}

	candles := make([]models.Candle, 0, len(raw))
	for _, k := range raw {
		// Binance close times are the last millisecond of the interval, so a
		// bar is closed once the clock has passed that instant.
		if millisToUTC(k.CloseTime).After(serverTime) {
			continue
		}

		candle, err := k.toCandle(params.Symbol, params.MarketType, params.Timeframe, true)
		if err != nil {
			return nil, err
		}
		candles = append(candles, candle)
	}
	return candles, nil
}

// serverTimeResponse is the /api/v3/time payload.
type serverTimeResponse struct {
	ServerTime int64 `json:"serverTime"`
}

// ServerTime returns the exchange clock.
func (c *client) ServerTime(ctx context.Context) (time.Time, error) {
	body, err := c.get(ctx, pathServerTime, nil)
	if err != nil {
		return time.Time{}, err
	}

	var parsed serverTimeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return time.Time{}, fmt.Errorf("%w: decode server time: %w", constants.ErrUnexpectedPayload, err)
	}
	if parsed.ServerTime <= 0 {
		return time.Time{}, fmt.Errorf("%w: server time is %d", constants.ErrUnexpectedPayload, parsed.ServerTime)
	}
	return millisToUTC(parsed.ServerTime), nil
}

// get performs a rate-limit-aware GET and returns the response body.
func (c *client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if err := c.awaitBudget(ctx); err != nil {
		return nil, err
	}

	endpoint := c.restBaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", path, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	c.weight.observe(resp.Header)

	if isRateLimitStatus(resp.StatusCode) {
		retryAfter, rateErr := rateLimitError(resp.StatusCode, resp.Header)
		c.weight.block(c.now(), retryAfter)
		return nil, rateErr
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned http %d: %s", path, resp.StatusCode, truncate(body, 256))
	}
	return body, nil
}

// awaitBudget sleeps until the rate limiter allows another request.
func (c *client) awaitBudget(ctx context.Context) error {
	wait := c.weight.waitFor(c.now())
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncate keeps an error message readable when an endpoint returns HTML.
func truncate(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
