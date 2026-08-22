package outcome

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// ReconcileParams bounds one comparison.
type ReconcileParams struct {
	Symbol     string
	MarketType constants.MarketType

	// From and To bound the signals considered, on signal_time.
	From time.Time
	To   time.Time

	// MinResolved is how many resolved signals a group needs before its
	// numbers are treated as saying anything. Zero means the default.
	MinResolved int

	// SkipBacktest answers with the live side alone. The engine re-run is the
	// expensive half and the only half that needs a strategy this binary can
	// still build, so it is optional rather than assumed.
	SkipBacktest bool
}

// Reconciliation is the whole report.
type Reconciliation struct {
	Symbol      string
	MarketType  constants.MarketType
	From, To    time.Time
	GeneratedAt time.Time

	// Groups are one per (strategy, version, resolved parameter set). There is
	// deliberately no total across them: averaging across a parameter change
	// produces a number describing nothing.
	Groups []ReconciledGroup
}

// ReconciledGroup is one strategy at one parameter set.
type ReconciledGroup struct {
	Strategy string
	Version  string

	// Params is the resolved set these signals were produced with, sorted.
	Params []ParamValue

	Live Side

	// Backtest is the same strategy and parameters re-run over the same
	// period. It is absent when the engine could not be run, and Unavailable
	// then says why — a report that quietly dropped the comparison would look
	// like a report that found no divergence.
	Backtest    *Side
	Unavailable string

	Sample      SampleAdequacy
	Divergences []Divergence
}

// ParamValue is one resolved parameter.
type ParamValue struct {
	Name  string
	Value string
}

// Side is one half of the comparison.
type Side struct {
	// Signals is everything produced in the window; Resolved is what can be
	// counted. Invalidated is the difference that is not knowable rather than
	// not finished.
	Signals     int
	StillOpen   int
	Invalidated int
	Resolved    int

	Targets int
	Stops   int
	Expired int

	// Noted counts resolutions that rested on an assumption rather than on
	// the data: a bar reaching both levels, or an entry that gapped past one.
	Noted int

	Wins   int
	Losses int

	// WinRate is over Resolved, and is NaN when nothing has resolved — which
	// is honest, where a zero would read as a strategy that never wins.
	WinRate float64

	AverageWinPct  decimal.Decimal
	AverageLossPct decimal.Decimal
	AverageCostPct decimal.Decimal

	AverageEntryPrice decimal.Decimal
	AverageBarsHeld   decimal.Decimal

	First, Last time.Time
}

// SampleAdequacy is the report's statement about its own reliability.
type SampleAdequacy struct {
	Resolved int
	Required int

	// Sufficient is the only thing that should decide whether the numbers
	// above are acted on.
	Sufficient bool

	// PerDay is the resolution rate observed over the window, and Wait how
	// long Required would take at it. Unknown when nothing has resolved yet.
	PerDay float64
	Wait   time.Duration
	Known  bool
}

// Divergence is one row of the table in docs, fired because a threshold was
// crossed.
type Divergence struct {
	Symptom     string
	LikelyCause string

	// Detail is the numbers that fired it, so the reader does not have to
	// find them in the report above.
	Detail string
}

// ReconcileUsecase compares live outcomes against backtest predictions.
type ReconcileUsecase interface {
	// Reconcile builds the report.
	Reconcile(ctx context.Context, params ReconcileParams) (Reconciliation, error)
}

// ReconcileRepository reads the live side.
type ReconcileRepository interface {
	// LiveGroups aggregates resolved outcomes by strategy, version and
	// resolved parameter set.
	LiveGroups(ctx context.Context, params ReconcileParams) ([]ReconciledGroup, error)
}

// SampleBanner is the sentence printed when a group's sample is too small.
//
// It lives here rather than in the handler or the CLI because both print it,
// and two wordings of "this number does not mean anything yet" is one wording
// too many — somebody would eventually read the softer one.
//
// It states the expected wait rather than only the shortfall. At the 4h
// strategy's rate of about a tenth of a trade a day, a hundred signals is
// nearly three years; that is better known up front than discovered after
// acting on twenty trades. If the wait is unacceptable the answer is a
// higher-frequency strategy, not a smaller sample.
func SampleBanner(s SampleAdequacy) string {
	if s.Sufficient {
		return ""
	}

	banner := fmt.Sprintf(
		"signals resolved: %d\n"+
			"NOT ENOUGH DATA — differences below are within normal variation.\n"+
			"A meaningful comparison needs at least %d resolved signals.",
		s.Resolved, s.Required)

	if !s.Known {
		return banner + "\nNothing has resolved yet, so there is no rate to estimate a wait from."
	}
	return banner + fmt.Sprintf(
		"\nAt the observed %.2f resolved signals a day, the remaining %d would take about %s.",
		s.PerDay, s.Required-s.Resolved, HumanDuration(s.Wait))
}

// HumanDuration renders a wait in units somebody can act on.
//
// Years and months, not hours: the point of printing it is that three years
// and three weeks are different decisions, and "26280h0m0s" does not make
// that difference land.
func HumanDuration(d time.Duration) string {
	if d <= 0 {
		return "no time at all"
	}

	days := d.Hours() / 24
	switch {
	case days < 1:
		return fmt.Sprintf("%.0f hours", d.Hours())
	case days < 60:
		return fmt.Sprintf("%.0f days", days)
	case days < 730:
		return fmt.Sprintf("%.1f months", days/30.44)
	default:
		return fmt.Sprintf("%.1f years", days/365.25)
	}
}
