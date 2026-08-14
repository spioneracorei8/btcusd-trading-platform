package strategy

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Levels describes how a strategy places its stop and target.
//
// # Why ATR and not a percentage
//
// A fixed percentage stop is the wrong default for BTC. The same 0.3% is a
// scratch in a volatile hour and a certainty in a quiet one, so a percentage
// stop silently changes what it means as the market changes regime — and the
// backtest would report the average of two different strategies.
//
// ATR is the market's own answer to "how far is far", measured on the same
// timeframe the decision is made on.
type Levels struct {
	// StopATRMult and TargetATRMult are distances in ATR from the entry.
	StopATRMult   float64
	TargetATRMult float64
}

// RewardRisk is the ratio the levels imply.
func (l Levels) RewardRisk() float64 {
	if l.StopATRMult <= 0 {
		return 0
	}
	return l.TargetATRMult / l.StopATRMult
}

// ReferenceATRPct is the ATR-to-price ratio the construction-time cost check
// assumes, as a percentage.
//
// # Why an assumption is needed at all
//
// The phase-06 rule is that a configuration whose reward cannot clear the
// round trip must fail loudly at construction rather than quietly in the
// results. But at construction there is no ATR: it is a property of the bars,
// which have not been read yet.
//
// So the check uses a reference. 0.10% of price is a typical 1m BTCUSDT ATR —
// around $27 on a $27,000 price. It is a stated assumption rather than a
// measurement, and the error message says so, because a configuration that
// only fails on quiet days is worse than one that fails at startup.
const ReferenceATRPct = 0.10

// MinRewardRisk is the lowest reward-to-risk a configuration may declare.
//
// One means the target is the same distance as the stop, so the strategy needs
// to be right more than half the time merely to break even before costs, and
// meaningfully more than half after them. Below one it needs to be right most
// of the time, which no rule on this list plausibly is.
const MinRewardRisk = 1.0

// Validate rejects levels that cannot clear the cost of trading.
//
// roundTripCostPct is what one entry and one exit cost in total, in percent —
// 0.1 for the default 0.05% taker each way.
func (l Levels) Validate(roundTripCostPct float64) error {
	if l.StopATRMult <= 0 {
		return fmt.Errorf("strategy: stop_atr_mult %v is not positive", l.StopATRMult)
	}
	if l.TargetATRMult <= 0 {
		return fmt.Errorf("strategy: target_atr_mult %v is not positive", l.TargetATRMult)
	}

	if ratio := l.RewardRisk(); ratio < MinRewardRisk {
		return fmt.Errorf(
			"strategy: reward-to-risk %.2f is below %.2f (target %.2f ATR against a stop of %.2f ATR).\n"+
				"A strategy risking more than it targets has to be right most of the time "+
				"before costs are even counted",
			ratio, MinRewardRisk, l.TargetATRMult, l.StopATRMult)
	}

	// The reward must clear the round trip with something left over. A target
	// that merely equals the cost is a strategy that wins its trades and ends
	// the year flat.
	targetPct := l.TargetATRMult * ReferenceATRPct
	if targetPct <= roundTripCostPct {
		return fmt.Errorf(
			"strategy: a target of %.2f ATR is %.3f%% of price at the reference volatility "+
				"(ATR = %.2f%% of price), which does not clear the %.3f%% round trip.\n"+
				"Every winning trade would pay more in fees than it made",
			l.TargetATRMult, targetPct, ReferenceATRPct, roundTripCostPct)
	}
	return nil
}

// StopFor returns the stop price for an entry at price in a direction, given
// the current ATR.
func (l Levels) StopFor(price decimal.Decimal, atr float64, long bool) decimal.Decimal {
	return offset(price, atr, l.StopATRMult, !long)
}

// TargetFor returns the target price for an entry.
func (l Levels) TargetFor(price decimal.Decimal, atr float64, long bool) decimal.Decimal {
	return offset(price, atr, l.TargetATRMult, long)
}

// offset moves a price by a multiple of ATR, up when up is true.
//
// A non-positive result is returned as zero, which the engine reads as "no
// level". That is the right answer for a degenerate ATR: a stop below zero is
// not a stop, and inventing one would put a fill at a price that cannot exist.
func offset(price decimal.Decimal, atr, mult float64, up bool) decimal.Decimal {
	if atr <= 0 || mult <= 0 {
		return decimal.Zero
	}

	distance := decimal.NewFromFloat(atr).Mul(decimal.NewFromFloat(mult))
	if up {
		return price.Add(distance)
	}

	result := price.Sub(distance)
	if !result.IsPositive() {
		return decimal.Zero
	}
	return result
}
