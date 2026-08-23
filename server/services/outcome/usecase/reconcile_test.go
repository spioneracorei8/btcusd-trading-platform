package usecase_test

import (
	"context"
	"errors"
	"math"
	"strconv"
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
	signals []outcome.LiveSignal
	err     error
	seen    outcome.ReconcileParams
}

func (g *groupSource) LiveSignals(
	_ context.Context, params outcome.ReconcileParams,
) ([]outcome.LiveSignal, error) {
	g.seen = params
	return g.signals, g.err
}

// engineSide answers with a prepared backtest side, or a refusal.
//
// entryAt decides which live signals have a counterpart. When it is nil the
// engine is taken to have entered on every live signal, which is the shape
// most of these tests want: they are about the arithmetic, not the split.
type engineSide struct {
	side    outcome.Side
	entryAt []time.Time
	all     bool
	err     error
	runs    int
}

func (e *engineSide) Compare(
	_ context.Context, _ outcome.ReconcileParams, group outcome.ReconciledGroup,
) (_outcome_us.Comparison, error) {
	e.runs++
	if e.err != nil {
		return _outcome_us.Comparison{}, e.err
	}

	at := e.entryAt
	if at == nil && !e.all {
		for _, s := range group.Signals {
			at = append(at, s.At)
		}
	}
	return _outcome_us.Comparison{Side: e.side, EntryAt: at}, nil
}

var window = struct{ from, to time.Time }{
	from: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	to:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
}

// liveSignals builds a group of resolved signals: wins first, then losses,
// spread evenly across spanDays.
func liveSignals(resolved, wins, spanDays int, params ...outcome.ParamValue) []outcome.LiveSignal {
	out := make([]outcome.LiveSignal, 0, resolved)
	for i := range resolved {
		at := window.from
		if resolved > 1 {
			at = window.from.Add(time.Duration(i) * time.Duration(spanDays) *
				24 * time.Hour / time.Duration(resolved-1))
		}

		net, status := decimal.RequireFromString("-0.8"), constants.OutcomeStop
		if i < wins {
			net, status = decimal.RequireFromString("1.0"), constants.OutcomeTarget
		}

		out = append(out, outcome.LiveSignal{
			Strategy: "ema_crossover", Version: "v1", Params: params,
			At: at, Status: status, BarsHeld: 4,
			EntryPrice:   decimal.NullDecimal{Decimal: decimal.RequireFromString("64000"), Valid: true},
			NetReturnPct: decimal.NullDecimal{Decimal: net, Valid: true},
			CostPct:      decimal.NullDecimal{Decimal: decimal.RequireFromString("0.1"), Valid: true},
		})
	}
	return out
}

// liveSide is what liveSignals aggregates to, for use as a backtest side.
func liveSide(resolved, wins, spanDays int) outcome.Side {
	return outcome.SideOf(liveSignals(resolved, wins, spanDays))
}

// reconciler wires a usecase over prepared halves.
func reconciler(
	t *testing.T, signals []outcome.LiveSignal, engine _outcome_us.BacktestComparer,
) (outcome.ReconcileUsecase, *groupSource) {
	t.Helper()

	source := &groupSource{signals: signals}
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
	signals := liveSignals(23, 10, 230)

	usecase, _ := reconciler(t, signals, &engineSide{side: liveSide(100, 43, 365)})
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
	signals := liveSignals(10, 4, 100)

	usecase, _ := reconciler(t, signals, nil)
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

// TestTheBannerSaysWhyThereIsNoWaitRatherThanGuessing.
//
// # What this prevents
//
// There are two reasons a wait cannot be stated and they are different facts.
// Nothing has resolved yet — the ordinary early case. Or things have resolved
// but at one instant, so no rate can be measured from them: one signal, or a
// batch that landed together.
//
// Both used to print "Nothing has resolved yet", which on a group showing
// "signals resolved: 1" contradicted the line above it. A banner that
// disagrees with its own numbers is worse than no banner: it is the line that
// exists to stop somebody acting on a thin sample.
func TestTheBannerSaysWhyThereIsNoWaitRatherThanGuessing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sample  outcome.SampleAdequacy
		want    string
		notWant string
	}{
		{
			name:    "nothing resolved",
			sample:  outcome.SampleAdequacy{Resolved: 0, Required: 100},
			want:    "Nothing has resolved yet",
			notWant: "no measurable span",
		},
		{
			name:    "resolved at one instant",
			sample:  outcome.SampleAdequacy{Resolved: 1, Required: 100},
			want:    "no measurable span",
			notWant: "Nothing has resolved yet",
		},
		{
			name:    "several resolved at one instant",
			sample:  outcome.SampleAdequacy{Resolved: 12, Required: 100},
			want:    "no measurable span",
			notWant: "Nothing has resolved yet",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			banner := outcome.SampleBanner(tc.sample)

			if !strings.Contains(banner, tc.want) {
				t.Errorf("the banner does not say %q:\n%s", tc.want, banner)
			}
			if strings.Contains(banner, tc.notWant) {
				t.Errorf("the banner says %q, which is not what happened:\n%s", tc.notWant, banner)
			}
			if !strings.Contains(banner, "signals resolved: "+strconv.Itoa(tc.sample.Resolved)) {
				t.Errorf("the banner does not carry its own count:\n%s", banner)
			}
		})
	}
}

// TestASufficientSampleHasNoBanner, so the banner means something when it is
// there.
func TestASufficientSampleHasNoBanner(t *testing.T) {
	signals := liveSignals(100, 43, 365)

	usecase, _ := reconciler(t, signals, &engineSide{side: liveSide(100, 43, 365)})
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
	// A win rate far below the engine's, on far too small a sample.
	signals := liveSignals(12, 1, 120)

	usecase, _ := reconciler(t, signals, &engineSide{side: liveSide(200, 90, 365)})
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
	signals := append(
		liveSignals(120, 60, 365, outcome.ParamValue{Name: "fast", Value: "9"}),
		liveSignals(120, 20, 365, outcome.ParamValue{Name: "fast", Value: "12"})...)

	usecase, _ := reconciler(t, signals, &engineSide{side: liveSide(120, 55, 365)})
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
	signals := liveSignals(150, 64, 365)

	usecase, _ := reconciler(t, signals, &engineSide{side: liveSide(150, 66, 365)})
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
		live func() []outcome.LiveSignal
		back func() outcome.Side
		want string
	}{
		{
			// Entries match, so the entries are not the explanation and the
			// rule the backtest was fitted to is.
			name: "no real edge",
			live: func() []outcome.LiveSignal { return liveSignals(150, 30, 365) },
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live win rate much lower, entries match",
		},
		{
			// The same shortfall with entries that do not match points the
			// other way entirely: at the pipeline, not at the edge. Reading
			// one as the other would send the search in the wrong direction
			// for months.
			name: "win rate lower and the entries differ",
			live: func() []outcome.LiveSignal {
				return withEntry(liveSignals(150, 30, 365), "64300")
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live win rate much lower, entries do not match",
		},
		{
			name: "slippage exceeds the model",
			live: func() []outcome.LiveSignal {
				return withEntry(liveSignals(150, 64, 365), "64200")
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "entry prices consistently worse",
		},
		{
			name: "fills too optimistic",
			live: func() []outcome.LiveSignal {
				return withWin(liveSignals(150, 64, 365), "0.5")
			},
			back: func() outcome.Side { return liveSide(150, 66, 365) },
			want: "live wins smaller than backtest",
		},
		{
			name: "fewer signals than expected",
			// A hundred live signals where the engine entered four hundred
			// times: the shortfall the split exists to keep visible.
			live: func() []outcome.LiveSignal { return liveSignals(100, 43, 365) },
			back: func() outcome.Side {
				s := liveSide(400, 176, 365)
				return s
			},
			want: "live signals fewer than expected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usecase, _ := reconciler(t, tt.live(), &engineSide{side: tt.back()})
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
	signals := liveSignals(150, 64, 365)

	engine := &engineSide{err: errors.New("this binary no longer ships retired_strategy")}
	usecase, _ := reconciler(t, signals, engine)
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
	engine := &engineSide{side: liveSide(150, 66, 365)}
	usecase, _ := reconciler(t, liveSignals(150, 64, 365), engine)

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

// withEntry rewrites every signal's entry price.
func withEntry(signals []outcome.LiveSignal, price string) []outcome.LiveSignal {
	for i := range signals {
		signals[i].EntryPrice = decimal.NullDecimal{
			Decimal: decimal.RequireFromString(price), Valid: true}
	}
	return signals
}

// withWin rewrites the return on every winning signal.
func withWin(signals []outcome.LiveSignal, pct string) []outcome.LiveSignal {
	for i := range signals {
		if signals[i].NetReturnPct.Decimal.IsPositive() {
			signals[i].NetReturnPct = decimal.NullDecimal{
				Decimal: decimal.RequireFromString(pct), Valid: true}
		}
	}
	return signals
}

// TestASurplusIsReportedSeparatelyAndNotCompared.
//
// Every shipped strategy suppresses an entry while a position is open, and the
// live evaluator always shows a flat position because it holds nothing. So
// live decides on bars the engine's copy stayed silent on. Those signals have
// no counterpart, and folding them into the comparison would report a
// structural difference as a divergence.
func TestASurplusIsReportedSeparatelyAndNotCompared(t *testing.T) {
	signals := liveSignals(120, 60, 365)

	// The engine entered on the first hundred only.
	entryAt := make([]time.Time, 0, 100)
	for _, s := range signals[:100] {
		entryAt = append(entryAt, s.At)
	}

	usecase, _ := reconciler(t, signals, &engineSide{
		side: liveSide(100, 50, 365), entryAt: entryAt,
	})
	report := reconcile(t, usecase)
	group := report.Groups[0]

	if group.Live.Signals != 120 {
		t.Errorf("Live.Signals = %d, want all 120 still reported", group.Live.Signals)
	}
	if group.Matched == nil || group.Matched.Signals != 100 {
		t.Fatalf("Matched = %+v, want the 100 the engine also emitted", group.Matched)
	}
	if group.Surplus == nil || group.Surplus.Signals != 20 {
		t.Fatalf("Surplus = %+v, want the 20 it did not", group.Surplus)
	}
	if group.Matched.Signals+group.Surplus.Signals != group.Live.Signals {
		t.Error("the split loses or duplicates signals")
	}

	// The comparison is drawn against the matched subset, so a group whose
	// matched half agrees with the engine reads as matching.
	if got := symptoms(group); !strings.Contains(got, "match closely") {
		t.Errorf("readings are %q; the matched subset agrees with the engine", got)
	}
	if strings.Contains(symptoms(group), "fewer than expected") {
		t.Error("a surplus was read as a shortfall")
	}
}

// TestASurplusCannotMaskAShortfall.
//
// This is the reason for the split. A warm-up bug losing 30% of the signals
// the engine would have produced is exactly what the reconciliation exists to
// catch — and a structural surplus padding the total would push it back over
// the threshold, leaving a broken pipeline reading as a healthy one.
func TestASurplusCannotMaskAShortfall(t *testing.T) {
	signals := liveSignals(200, 86, 365)

	// The engine entered two hundred times; live matched only a hundred and
	// forty of them — a 30% shortfall — and produced sixty of its own. The
	// total, 200, is exactly what the engine produced, so comparing totals
	// would find nothing wrong at all.
	entryAt := make([]time.Time, 0, 140)
	for _, s := range signals[:140] {
		entryAt = append(entryAt, s.At)
	}

	usecase, _ := reconciler(t, signals, &engineSide{
		side: liveSide(200, 86, 365), entryAt: entryAt,
	})
	report := reconcile(t, usecase)
	group := report.Groups[0]

	if group.Matched.Signals != 140 || group.Surplus.Signals != 60 {
		t.Fatalf("split is %d matched / %d surplus, want 140/60",
			group.Matched.Signals, group.Surplus.Signals)
	}
	if group.Live.Signals < group.Backtest.Signals {
		t.Fatal("the fixture no longer has a total that would have masked the shortfall")
	}
	if !group.Sample.Sufficient {
		t.Fatal("the matched sample is too small for any reading to fire")
	}

	if got := symptoms(group); !strings.Contains(got, "fewer than expected") {
		t.Errorf("readings are %q; 70 matched against 100 expected is a shortfall", got)
	}
}

// TestTheSampleIsMeasuredOverWhatIsCompared.
//
// The banner speaks for the numbers beside it, and those are drawn from the
// matched subset. Counting the surplus towards the sample would claim a
// reliability the comparison does not have.
func TestTheSampleIsMeasuredOverWhatIsCompared(t *testing.T) {
	signals := liveSignals(120, 60, 365)

	entryAt := make([]time.Time, 0, 40)
	for _, s := range signals[:40] {
		entryAt = append(entryAt, s.At)
	}

	usecase, _ := reconciler(t, signals, &engineSide{
		side: liveSide(40, 20, 365), entryAt: entryAt,
	})
	group := reconcile(t, usecase).Groups[0]

	if group.Sample.Resolved != 40 {
		t.Errorf("Sample.Resolved = %d, want the 40 that are compared, not the 120 produced",
			group.Sample.Resolved)
	}
	if group.Sample.Sufficient {
		t.Error("40 compared signals were treated as enough because 120 were produced")
	}
}
