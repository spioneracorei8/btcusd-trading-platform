package usecase

import (
	"sync"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// LatestCandleCache holds the still-forming candle for each timeframe.
//
// These bars exist so a UI can show a live price. They are deliberately kept
// out of the database: an unclosed candle changes on every tick, and letting
// one reach the candles table would make indicators flicker and a backtest
// disagree with what actually happened.
//
// Unlike the indicators, this is read by the status path while the ingestion
// goroutine writes it, so it does take a lock.
type LatestCandleCache struct {
	mu      sync.RWMutex
	candles map[constants.Timeframe]models.Candle
}

// NewLatestCandleCache builds an empty cache.
func NewLatestCandleCache() *LatestCandleCache {
	return &LatestCandleCache{
		candles: make(map[constants.Timeframe]models.Candle),
	}
}

// Put stores the current forming candle for its timeframe, replacing any
// earlier version of the same bar.
func (c *LatestCandleCache) Put(candle models.Candle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.candles[candle.Timeframe] = candle
}

// Get returns the forming candle for a timeframe.
func (c *LatestCandleCache) Get(timeframe constants.Timeframe) (models.Candle, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	candle, ok := c.candles[timeframe]
	return candle, ok
}

// Drop forgets the forming candle for a timeframe, used once the bar closes
// so a stale open bar cannot be served after its real version is stored.
func (c *LatestCandleCache) Drop(timeframe constants.Timeframe) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.candles, timeframe)
}
