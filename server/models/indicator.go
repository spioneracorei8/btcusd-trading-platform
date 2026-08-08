package models

import "time"

// IndicatorSnapshot is one row of indicator values at a candle close.
//
// It is a plain value type with no pointers and no behaviour: it is what the
// backtest engine and the live strategy runner consume, and a shared map or
// slice inside it would let a later mutation rewrite a snapshot somebody
// already took.
//
// It lives here rather than in services/indicator because a strategy's
// BarContext carries it, and a service interface file may import models and
// constants only. services/indicator aliases it as indicator.Snapshot.
type IndicatorSnapshot struct {
	OpenTime time.Time

	EMA  float64
	RSI  float64
	ATR  float64
	VWAP float64
}
