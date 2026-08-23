package outcome

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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

	// Live is everything the live path produced in the window.
	Live Side

	// Matched is the subset the engine also emitted, and Surplus the rest.
	// Both are nil when no comparison was run.
	//
	// # Why the population is split
	//
	// Every shipped strategy suppresses an entry while a position is open,
	// and the live evaluator always shows a flat position because it holds
	// nothing. So the live path emits signals on bars where the engine's copy
	// of the same strategy stayed silent — measured at 143 live decisions
	// against 141 engine entries on the development set, and larger for any
	// strategy that holds longer.
	//
	// Comparing the whole live population against the engine's would report
	// that structural difference as a divergence, and worse, would mask a
	// real one: a warm-up bug producing 85% of the expected signals would be
	// pushed back over the threshold by the surplus. Only Matched is compared;
	// Surplus is reported on its own terms.
	Matched *Side
	Surplus *Side

	// Backtest is the same strategy and parameters re-run over the same
	// period. It is absent when the engine could not be run, and Unavailable
	// then says why — a report that quietly dropped the comparison would look
	// like a report that found no divergence.
	Backtest    *Side
	Unavailable string

	// Sample is measured over Matched when there is a comparison, because
	// that is the population the numbers below are drawn from.
	Sample      SampleAdequacy
	Divergences []Divergence

	// Signals are the group's members, kept so the population can be split
	// once the engine has said what it entered on. Not rendered.
	Signals []LiveSignal
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

// LiveSignal is one signal the live path produced, and what became of it.
type LiveSignal struct {
	Strategy string
	Version  string

	// Params is the resolved set it was produced with, sorted. Part of the
	// grouping key: two parameter sets are two strategies for this purpose.
	Params []ParamValue

	// At is signal_time — the close the decision was taken on, which is also
	// the open the entry filled at. It is what matches a live signal to an
	// engine trade.
	At time.Time

	Status     constants.OutcomeStatus
	EntryPrice decimal.NullDecimal
	BarsHeld   int32

	// NetReturnPct is unset while the signal is open, and for one whose
	// window had missing data.
	NetReturnPct decimal.NullDecimal
	CostPct      decimal.NullDecimal

	// RestedOnAssumption marks a resolution that came from an assumption
	// rather than from the data.
	RestedOnAssumption bool
}

// ReconcileRepository reads the live side.
type ReconcileRepository interface {
	// LiveSignals returns every live signal in the window, one row each,
	// carrying its grouping key. Aggregation happens above this.
	LiveSignals(ctx context.Context, params ReconcileParams) ([]LiveSignal, error)
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

// GroupKey identifies the population a signal belongs to.
//
// Strategy, version and the resolved parameter set, because only like may be
// compared with like: averaging across a parameter change produces a number
// describing nothing, and it looks exactly like a number describing something.
func (s LiveSignal) GroupKey() string {
	parts := make([]string, 0, len(s.Params))
	for _, p := range s.Params {
		parts = append(parts, p.Name+"="+p.Value)
	}
	sort.Strings(parts)
	return s.Strategy + "\x00" + s.Version + "\x00" + strings.Join(parts, ",")
}

// SideOf aggregates a set of live signals.
//
// # One aggregation, three views
//
// The report shows the whole live population, the subset the engine also
// emitted, and the surplus it did not. Three aggregates that had to agree
// would be three chances to disagree; this is called three times instead.
//
// # What counts
//
// Invalidated signals are counted and then excluded from every statistic.
// Their window has missing data, so whether they would have won is not
// knowable, and a win rate that quietly counted guesses would be worse than
// one with a smaller sample.
//
// A win is a positive return after modelled cost, never a touched level. At
// these timeframes a target reached by less than the round trip charged is a
// losing trade.
func SideOf(signals []LiveSignal) Side {
	side := Side{Signals: len(signals), WinRate: math.NaN()}

	var (
		winTotal, lossTotal decimal.Decimal
		costTotal           decimal.Decimal
		entryTotal          decimal.Decimal
		barsTotal           int64
		entries             int
	)

	for _, s := range signals {
		if side.First.IsZero() || s.At.Before(side.First) {
			side.First = s.At
		}
		if s.At.After(side.Last) {
			side.Last = s.At
		}
		if s.EntryPrice.Valid {
			entryTotal = entryTotal.Add(s.EntryPrice.Decimal)
			entries++
		}

		switch s.Status {
		case constants.OutcomeOpen:
			side.StillOpen++
			continue
		case constants.OutcomeInvalidated:
			side.Invalidated++
			continue
		case constants.OutcomeTarget:
			side.Targets++
		case constants.OutcomeStop:
			side.Stops++
		case constants.OutcomeExpired:
			side.Expired++
		}

		side.Resolved++
		if s.RestedOnAssumption {
			side.Noted++
		}
		barsTotal += int64(s.BarsHeld)
		costTotal = costTotal.Add(s.CostPct.Decimal)

		if s.NetReturnPct.Decimal.IsPositive() {
			side.Wins++
			winTotal = winTotal.Add(s.NetReturnPct.Decimal)
			continue
		}
		side.Losses++
		lossTotal = lossTotal.Add(s.NetReturnPct.Decimal)
	}

	if side.Resolved > 0 {
		resolved := decimal.NewFromInt(int64(side.Resolved))
		side.WinRate = float64(side.Wins) / float64(side.Resolved)
		side.AverageCostPct = costTotal.Div(resolved)
		side.AverageBarsHeld = decimal.NewFromInt(barsTotal).Div(resolved)
	}
	if side.Wins > 0 {
		side.AverageWinPct = winTotal.Div(decimal.NewFromInt(int64(side.Wins)))
	}
	if side.Losses > 0 {
		side.AverageLossPct = lossTotal.Div(decimal.NewFromInt(int64(side.Losses)))
	}
	if entries > 0 {
		side.AverageEntryPrice = entryTotal.Div(decimal.NewFromInt(int64(entries)))
	}
	return side
}

// SurplusNote explains a live-only population, in one place so the API and
// the CLI cannot describe it differently.
const SurplusNote = "Signals the engine did not emit over the same bars. Every shipped " +
	"strategy suppresses an entry while a position is open, and the live evaluator always " +
	"shows a flat position because it holds nothing — so live decides on bars where the " +
	"engine's copy of the same strategy stayed silent. This is structural, not a " +
	"divergence, and it is excluded from the comparison rather than counted against it. " +
	"An entry the engine asked for and then refused, for lot size or leverage, also lands " +
	"here because it produced no trade to compare against."
