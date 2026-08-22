package usecase_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	_outcome_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/usecase"
)

// groupSource is the live half of the comparison, prepared.
type groupSource struct {
	groups []outcome.ReconciledGroup
	err    error
	seen   outcome.ReconcileParams
}

func (g *groupSource) LiveGroups(
	_ context.Context, params outcome.ReconcileParams,
) ([]outcome.ReconciledGroup, error) {
	g.seen = params
	return g.groups, g.err
}

// engineSide answers with a prepared backtest side, or a refusal.
type engineSide struct {
	side outcome.Side
	err  error
	runs int
}

func (e *engineSide) Compare(
	context.Context, outcome.ReconcileParams, outcome.ReconciledGroup,
) (outcome.Side, error) {
	e.runs++
	if e.err != nil {
		return outcome.Side{}, e.err
	}
	return e.side, nil
}

var window = struct{ from, to time.Time }{
	from: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	to:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
}

// liveSide builds a live half with a given win count out of a resolved count.
func liveSide(resolved, wins int, spanDays int) outcome.Side {
	side := outcome.Side{
		Signals:  resolved,
		Resolved: resolved,
		Wins:     wins,
		Losses:   resolved - wins,
		WinRate:  math.NaN(),
		First:    window.from,
		Last:     window.from.AddDate(0, 0, spanDays),

		AverageWinPct:     decimal.RequireFromString("1.0"),
		AverageLossPct:    decimal.RequireFromString("-0.8"),
		AverageEntryPrice: decimal.RequireFromString("64000"),
		AverageCostPct:    decimal.RequireFromString("0.1"),
	}
	if resolved > 0 {
		side.WinRate = float64(wins) / float64(resolved)
	}
	return side
}

// reconciler wires a usecase over prepared halves.
func reconciler(
	t *testing.T, groups []outcome.ReconciledGroup, engine _outcome_us.BacktestComparer,
) (outcome.ReconcileUsecase, *groupSource) {
	t.Helper()

	source := &groupSource{groups: groups}
	usecase, err := _outcome_us.NewReconcileUsecaseImpl(source, silentLog(),
		_outcome_us.ReconcileConfig{
			Backtest: engine,
			Now:      func() time.Time { return window.to },
		})
	if err != nil {
		t.Fatalf("NewReconcileUsecaseImpl() returned error: %v", err)
	}
	return usecase, source
}

func reconcile(t *testing.T, usecase outcome.ReconcileUsecase) outcome.Reconciliation {
	t.Helper()

	report, err := usecase.Reconcile(context.Background(), outcome.ReconcileParams{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		From: window.from, To: window.to,
	})
	if err != nil {
		t.Fatalf("Reconcile() returned error: %v", err)
	}
	return report
}

// TestFewerThanAHundredResolvedSignalsProducesTheBanner.
//
// The endpoint has to state its own reliability. Twenty trades and three
// years of trades produce the same-looking win rate, and only this line
// separates them.
func TestFewerThanAHundredResolvedSignalsProducesTheBanner(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1",
		Live: liveSide(23, 10, 230),
	}}

	usecase, _ := reconciler(t, groups, &engineSide{side: liveSide(100, 43, 365)})
	report := reconcile(t, usecase)

	sample := report.Groups[0].Sample
	if sample.Sufficient {
		t.Fatal("23 resolved signals were treated as enough")
	}
	if sample.Required != constants.ReconcileMinResolved {
		t.Errorf("Required = %d, want %d", sample.Required, constants.ReconcileMinResolved)
	}

	banner := outcome.SampleBanner(sample)
	for _, want := range []string{"signals resolved: 23", "NOT ENOUGH DATA", "100"} {
		if !strings.Contains(banner, want) {
			t.Errorf("the banner does not carry %q:\n%s", want, banner)
		}
	}
}

// TestTheBannerStatesTheExpectedWait.
//
// At a tenth of a trade a day, a hundred signals is nearly three years. That
// is better known up front than discovered after acting on twenty trades —
// and if the wait is unacceptable the answer is a higher-frequency strategy,
// not a smaller sample.
func TestTheBannerStatesTheExpectedWait(t *testing.T) {
	// Ten resolved over a hundred days: a tenth a day, and ninety to go.
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1",
		Live: liveSide(10, 4, 100),
	}}

	usecase, _ := reconciler(t, groups, nil)
	report := reconcile(t, usecase)

	sample := report.Groups[0].Sample
	if !sample.Known {
		t.Fatal("the wait was not estimated from an observable rate")
	}

	// Ninety more at a tenth a day is nine hundred days, about 2.5 years.
	wantDays := 900.0
	if got := sample.Wait.Hours() / 24; math.Abs(got-wantDays) > 1 {
		t.Errorf("the wait is %.0f days, want about %.0f", got, wantDays)
	}

	banner := outcome.SampleBanner(sample)
	if !strings.Contains(banner, "years") {
		t.Errorf("a multi-year wait is not stated in years:\n%s", banner)
	}
	if !strings.Contains(banner, "0.10") {
		t.Errorf("the banner does not state the observed rate:\n%s", banner)
	}
}

// TestASufficientSampleHasNoBanner, so the banner means something when it is
// there.
func TestASufficientSampleHasNoBanner(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1",
		Live: liveSide(100, 43, 365),
	}}

	usecase, _ := reconciler(t, groups, &engineSide{side: liveSide(100, 43, 365)})
	report := reconcile(t, usecase)

	if !report.Groups[0].Sample.Sufficient {
		t.Fatal("100 resolved signals were treated as too few")
	}
	if banner := outcome.SampleBanner(report.Groups[0].Sample); banner != "" {
		t.Errorf("a sufficient sample carries a banner:\n%s", banner)
	}
}

// TestNothingIsReadIntoAnInsufficientSample.
//
// The banner says the differences are within normal variation. Printing a
// divergence beside it would contradict it, and the reader would believe the
// more specific of the two.
func TestNothingIsReadIntoAnInsufficientSample(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1",
		Live: liveSide(12, 1, 120), // a win rate far below the engine's
	}}

	usecase, _ := reconciler(t, groups, &engineSide{side: liveSide(200, 90, 365)})
	report := reconcile(t, usecase)

	if got := report.Groups[0].Divergences; len(got) != 0 {
		t.Errorf("a 12-signal sample produced %d readings: %+v", len(got), got)
	}
}

// TestGroupsAreNotAveragedTogether.
//
// Two parameter sets are two strategies for this purpose. A total across them
// would describe neither, and would look exactly like a number describing
// something.
func TestGroupsAreNotAveragedTogether(t *testing.T) {
	groups := []outcome.ReconciledGroup{
		{
			Strategy: "ema_crossover", Version: "v1",
			Params: []outcome.ParamValue{{Name: "fast", Value: "9"}},
			Live:   liveSide(120, 60, 365),
		},
		{
			Strategy: "ema_crossover", Version: "v1",
			Params: []outcome.ParamValue{{Name: "fast", Value: "12"}},
			Live:   liveSide(120, 20, 365),
		},
	}

	usecase, _ := reconciler(t, groups, &engineSide{side: liveSide(120, 55, 365)})
	report := reconcile(t, usecase)

	if len(report.Groups) != 2 {
		t.Fatalf("two parameter sets produced %d groups", len(report.Groups))
	}
	if report.Groups[0].Sample.Resolved != 120 || report.Groups[1].Sample.Resolved != 120 {
		t.Error("the groups were merged")
	}

	// The 50% group matches the engine; the 17% group does not. If they had
	// been averaged, neither reading would fire.
	first := symptoms(report.Groups[0])
	second := symptoms(report.Groups[1])
	if !strings.Contains(first, "match closely") {
		t.Errorf("the matching group does not read as matching: %s", first)
	}
	if !strings.Contains(second, "much lower") {
		t.Errorf("the diverging group does not read as diverging: %s", second)
	}
}

// TestAPipelineThatMatchesIsSaidSoRatherThanLeftSilent.
//
// A faithful pipeline delivering a thin edge is a different problem from a
// broken pipeline, and they demand opposite responses. Only this row
// distinguishes them, so it cannot be the absence of the others.
func TestAPipelineThatMatchesIsSaidSoRatherThanLeftSilent(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1",
		Live: liveSide(150, 64, 365),
	}}

	usecase, _ := reconciler(t, groups, &engineSide{side: liveSide(150, 66, 365)})
	report := reconcile(t, usecase)

	readings := report.Groups[0].Divergences
	if len(readings) != 1 {
		t.Fatalf("a matching pipeline produced %d readings: %+v", len(readings), readings)
	}
	if !strings.Contains(readings[0].Symptom, "match closely") {
		t.Errorf("Symptom = %q", readings[0].Symptom)
	}
	if !strings.Contains(readings[0].LikelyCause, "pipeline is sound") {
		t.Errorf("LikelyCause = %q", readings[0].LikelyCause)
	}
	if readings[0].Detail == "" {
		t.Error("the reading carries none of the numbers that produced it")
	}
}

// TestEachDivergenceRowFiresOnItsOwnSymptom.
func TestEachDivergenceRowFiresOnItsOwnSymptom(t *testing.T) {
	tests := []struct {
		name string
		live func() outcome.Side
		back func() outcome.Side
		want string
	}{
		{
			// Entries match, so the entries are not the explanation and the
			// rule the backtest was fitted to is.
			name: "no real edge",
			live: func() outcome.Side { return liveSide(150, 30, 365) },
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live win rate much lower, entries match",
		},
		{
			// The same shortfall with entries that do not match points the
			// other way entirely: at the pipeline, not at the edge. Reading
			// one as the other would send the search in the wrong direction
			// for months.
			name: "win rate lower and the entries differ",
			live: func() outcome.Side {
				s := liveSide(150, 30, 365)
				s.AverageEntryPrice = decimal.RequireFromString("64300")
				return s
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live win rate much lower, entries do not match",
		},
		{
			name: "slippage exceeds the model",
			live: func() outcome.Side {
				s := liveSide(150, 64, 365)
				s.AverageEntryPrice = decimal.RequireFromString("64200")
				return s
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "entry prices consistently worse",
		},
		{
			name: "fills too optimistic",
			live: func() outcome.Side {
				s := liveSide(150, 64, 365)
				s.AverageWinPct = decimal.RequireFromString("0.5")
				return s
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live wins smaller than backtest",
		},
		{
			name: "fewer signals than expected",
			live: func() outcome.Side {
				s := liveSide(150, 64, 365)
				s.Signals = 100
				return s
			},
			back: func() outcome.Side {
				s := liveSide(150, 66, 365)
				s.Signals = 400
				return s
			},
			want: "live signals fewer than expected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := []outcome.ReconciledGroup{{
				Strategy: "ema_crossover", Version: "v1", Live: tt.live(),
			}}
			usecase, _ := reconciler(t, groups, &engineSide{side: tt.back()})
			report := reconcile(t, usecase)

			if got := symptoms(report.Groups[0]); !strings.Contains(got, tt.want) {
				t.Errorf("readings are %q, want one saying %q", got, tt.want)
			}
			if strings.Contains(symptoms(report.Groups[0]), "match closely") {
				t.Error("a divergent group also reads as matching")
			}

			// The two win-rate readings blame opposite things, so a group
			// must never carry both.
			got := symptoms(report.Groups[0])
			if strings.Contains(got, "entries match") && strings.Contains(got, "entries do not match") {
				t.Errorf("both win-rate readings fired at once: %s", got)
			}
		})
	}
}

// TestAnUnavailableBacktestIsSaidRatherThanLeftBlank.
//
// A report that quietly dropped the comparison would look like a report that
// found no divergence, which is the one conclusion it must never imply by
// accident.
func TestAnUnavailableBacktestIsSaidRatherThanLeftBlank(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "retired_strategy", Version: "v1",
		Live: liveSide(150, 64, 365),
	}}

	engine := &engineSide{err: errors.New("this binary no longer ships retired_strategy")}
	usecase, _ := reconciler(t, groups, engine)
	report := reconcile(t, usecase)

	group := report.Groups[0]
	if group.Backtest != nil {
		t.Error("a refused comparison produced a backtest side anyway")
	}
	if !strings.Contains(group.Unavailable, "retired_strategy") {
		t.Errorf("Unavailable = %q, does not say why", group.Unavailable)
	}
	if len(group.Divergences) != 0 {
		t.Errorf("a group with no comparison produced readings: %+v", group.Divergences)
	}
	// The live side still stands on its own.
	if group.Live.Resolved != 150 {
		t.Error("the live side was dropped with the comparison")
	}
}

// TestSkippingTheBacktestDoesNotRunIt, because the engine replays history and
// somebody asking for the live side alone is asking not to wait for it.
func TestSkippingTheBacktestDoesNotRunIt(t *testing.T) {
	groups := []outcome.ReconciledGroup{{
		Strategy: "ema_crossover", Version: "v1", Live: liveSide(150, 64, 365),
	}}

	engine := &engineSide{side: liveSide(150, 66, 365)}
	usecase, _ := reconciler(t, groups, engine)

	report, err := usecase.Reconcile(context.Background(), outcome.ReconcileParams{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		From: window.from, To: window.to, SkipBacktest: true,
	})
	if err != nil {
		t.Fatalf("Reconcile() returned error: %v", err)
	}
	if engine.runs != 0 {
		t.Errorf("the engine ran %d times with the comparison skipped", engine.runs)
	}
	if report.Groups[0].Unavailable == "" {
		t.Error("a skipped comparison is not reported as skipped")
	}
}

// TestABackwardsWindowIsRefused, rather than answered with an empty report
// that reads as "no signals".
func TestABackwardsWindowIsRefused(t *testing.T) {
	usecase, _ := reconciler(t, nil, nil)

	_, err := usecase.Reconcile(context.Background(), outcome.ReconcileParams{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		From: window.to, To: window.from,
	})
	if err == nil {
		t.Error("a window ending before it starts was accepted")
	}
}

// symptoms joins a group's readings for a contains check.
func symptoms(group outcome.ReconciledGroup) string {
	parts := make([]string, 0, len(group.Divergences))
	for _, d := range group.Divergences {
		parts = append(parts, d.Symptom)
	}
	return strings.Join(parts, " | ")
}
