package usecase

import "github.com/spioneracorei8/btcusd-trading-platform/server/models"

// Prices arrive as decimal.Decimal and are converted to float64 here, at the
// point of use.
//
// This is the one place float64 is correct (CLAUDE.md §4): indicator values
// are statistics, not money. Nothing that leaves an indicator is ever a price
// to be stored, compared for equality, or summed into a balance — those stay
// decimal all the way through. The conversion is deliberately confined to
// these four functions so the boundary is visible rather than scattered.
//
// InexactFloat64 is used rather than Float64: the error return of the latter
// only reports that the decimal had more precision than a float64 can hold,
// which is expected for an 8-decimal price and not actionable here.

func closeFloat(c models.Candle) float64 { return c.Close.InexactFloat64() }

func highFloat(c models.Candle) float64 { return c.High.InexactFloat64() }

func lowFloat(c models.Candle) float64 { return c.Low.InexactFloat64() }

func volumeFloat(c models.Candle) float64 { return c.Volume.InexactFloat64() }
