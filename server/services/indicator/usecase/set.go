package usecase

import (
	"fmt"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator"
)

// SetConfig chooses the periods for one timeframe's indicator set.
type SetConfig struct {
	EMAPeriod int
	RSIPeriod int
	ATRPeriod int
}

// DefaultSetConfig is the conventional parameter set.
func DefaultSetConfig() SetConfig {
	return SetConfig{EMAPeriod: 200, RSIPeriod: 14, ATRPeriod: 14}
}

// Set drives the indicators required for one timeframe together.
//
// One Set belongs to one (symbol, timeframe). It is not goroutine-safe, for
// the same reason its members are not.
type Set struct {
	ema  *EMA
	rsi  *RSI
	atr  *ATR
	vwap *VWAP
}

// NewSet builds the four indicators for a timeframe.
func NewSet(cfg SetConfig) (*Set, error) {
	ema, err := NewEMA(cfg.EMAPeriod)
	if err != nil {
		return nil, fmt.Errorf("build indicator set: %w", err)
	}
	rsi, err := NewRSI(cfg.RSIPeriod)
	if err != nil {
		return nil, fmt.Errorf("build indicator set: %w", err)
	}
	atr, err := NewATR(cfg.ATRPeriod)
	if err != nil {
		return nil, fmt.Errorf("build indicator set: %w", err)
	}

	return &Set{ema: ema, rsi: rsi, atr: atr, vwap: NewVWAP()}, nil
}

// Update feeds one closed candle to every member.
//
// Every member is updated even while the set is warming up: they are stateful
// and skipping one would leave it permanently behind the others. The snapshot
// is withheld until all of them are ready, so a partial one — with some real
// values and some placeholders — can never escape into a strategy.
func (s *Set) Update(c models.Candle) (indicator.Snapshot, bool) {
	emaValue, emaOK := s.ema.Update(c)
	rsiValue, rsiOK := s.rsi.Update(c)
	atrValue, atrOK := s.atr.Update(c)
	vwapValue, vwapOK := s.vwap.Update(c)

	if !emaOK || !rsiOK || !atrOK || !vwapOK {
		return indicator.Snapshot{}, false
	}

	return indicator.Snapshot{
		OpenTime: c.OpenTime.UTC(),
		EMA:      emaValue,
		RSI:      rsiValue,
		ATR:      atrValue,
		VWAP:     vwapValue,
	}, true
}

// WarmupPeriod is the longest warm-up across the members.
func (s *Set) WarmupPeriod() int {
	return indicator.MaxWarmupPeriod(s.ema, s.rsi, s.atr, s.vwap)
}

// Ready reports whether every member is ready.
func (s *Set) Ready() bool {
	return s.ema.Ready() && s.rsi.Ready() && s.atr.Ready() && s.vwap.Ready()
}

// Reset clears every member.
func (s *Set) Reset() {
	s.ema.Reset()
	s.rsi.Reset()
	s.atr.Reset()
	s.vwap.Reset()
}

// EvaluateSet runs a set over a series and returns the snapshots it emitted.
//
// Like indicator.Evaluate, this is a loop over Update and nothing more.
func EvaluateSet(set *Set, candles []models.Candle) []indicator.Snapshot {
	snapshots := make([]indicator.Snapshot, 0, len(candles))

	for _, c := range candles {
		snapshot, ok := set.Update(c)
		if !ok {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

// Compile-time proof that every member satisfies the contract.
var (
	_ indicator.Indicator = (*EMA)(nil)
	_ indicator.Indicator = (*RSI)(nil)
	_ indicator.Indicator = (*ATR)(nil)
)
