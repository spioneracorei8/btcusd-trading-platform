package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

const readSymbol = "BTCUSDT"

var readNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

func quietRead() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeReader serves a prepared page of outcomes.
type fakeReader struct {
	page  []outcome.Resolved
	total int64
	err   error

	asked outcome.ListParams
	calls int
}

func (f *fakeReader) ListOutcomes(_ context.Context, params outcome.ListParams) ([]outcome.Resolved, int64, error) {
	f.asked = params
	f.calls++
	return f.page, f.total, f.err
}

func (f *fakeReader) FollowOpen(context.Context) (outcome.FollowReport, error) {
	return outcome.FollowReport{}, nil
}
func (f *fakeReader) Run(context.Context) error { return nil }

// fakeReconciler returns a prepared report and records how it was asked.
type fakeReconciler struct {
	report outcome.Reconciliation
	err    error

	asked outcome.ReconcileParams
	calls int
}

func (f *fakeReconciler) Reconcile(_ context.Context, params outcome.ReconcileParams) (outcome.Reconciliation, error) {
	f.asked = params
	f.calls++
	return f.report, f.err
}

func newReadHandler(reconciler outcome.ReconcileUsecase, reader outcome.OutcomeUsecase) outcome.OutcomeHandler {
	h := NewOutcomeHandlerImpl(reconciler, reader, quietRead(), readSymbol, constants.MarketTypeSpot)
	h.(*outcomeHandler).now = func() time.Time { return readNow }
	return h
}

func resolved(status constants.OutcomeStatus, at time.Time, netReturn string) outcome.Resolved {
	id := uuid.New()

	row := outcome.Resolved{
		Signal: models.Signal{
			Id: id, Symbol: readSymbol, MarketType: constants.MarketTypeSpot,
			Timeframe: constants.Timeframe4h, SignalTime: at,
			Direction:   constants.DirectionLong,
			SignalPrice: decimal.NewNullDecimal(decimal.RequireFromString("64000")),
			EntryPrice:  decimal.NewNullDecimal(decimal.RequireFromString("64010.01")),

			StrategyName: "ema_crossover", StrategyVersion: "v1",
		},
		Outcome: models.SignalOutcome{
			SignalId: id, Status: status, BarsHeld: 3,
			ResolvedPrice: decimal.NewNullDecimal(decimal.RequireFromString("65000")),
			MAE:           decimal.NewNullDecimal(decimal.RequireFromString("120.5")),
			MFE:           decimal.NewNullDecimal(decimal.RequireFromString("990")),
		},
	}
	if netReturn != "" {
		row.Outcome.BacktestWouldHave = []byte(`{"net_return_pct":"` + netReturn + `"}`)
	}
	return row
}

func getOutcomes(t *testing.T, h outcome.OutcomeHandler, query string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	recorder := httptest.NewRecorder()
	h.Outcomes(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/outcomes"+query, nil))
	return recorder, recorder.Body.Bytes()
}

func getPerformance(t *testing.T, h outcome.OutcomeHandler, query string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	recorder := httptest.NewRecorder()
	h.Performance(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/performance"+query, nil))
	return recorder, recorder.Body.Bytes()
}

// TestOutcomesAreUnavailableRatherThanEmptyWhereNoFollowerIsWired.
//
// # What this prevents
//
// The follower runs in the collector. A process without one has no outcomes to
// serve, and an empty list would say "nothing has resolved" — which is a
// statement about the market rather than about the deployment, and it is the
// wrong one.
func TestOutcomesAreUnavailableRatherThanEmptyWhereNoFollowerIsWired(t *testing.T) {
	recorder, body := getOutcomes(t, newReadHandler(&fakeReconciler{}, nil), "")

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", recorder.Code)
	}
	if !strings.Contains(string(body), "not available on this process") {
		t.Errorf("the body does not say why: %s", body)
	}
}

// TestTheStatusFilterIsValidatedAgainstTheKnownStatuses.
//
// An unknown status passed through to the query returns nothing, which reads
// as "no trades ended that way" rather than "that is not a way a trade can
// end". The second is a typo the client can fix.
func TestTheStatusFilterIsValidatedAgainstTheKnownStatuses(t *testing.T) {
	reader := &fakeReader{}
	recorder, _ := getOutcomes(t, newReadHandler(&fakeReconciler{}, reader), "?status=banana")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	if reader.calls != 0 {
		t.Error("the query ran with an unknown status")
	}

	recorder, _ = getOutcomes(t, newReadHandler(&fakeReconciler{}, reader), "?status=stop")
	if recorder.Code != http.StatusOK {
		t.Fatalf("a known status returned %d, want 200", recorder.Code)
	}
	if reader.asked.Status != constants.OutcomeStop {
		t.Errorf("queried status %q, want stop", reader.asked.Status)
	}
}

// TestTheWindowAndPagingReachTheQuery.
func TestTheWindowAndPagingReachTheQuery(t *testing.T) {
	reader := &fakeReader{}
	getOutcomes(t, newReadHandler(&fakeReconciler{}, reader),
		"?from=2024-01-01T00:00:00Z&to=2024-02-01T00:00:00Z&limit=10&offset=20")

	if want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC); !reader.asked.From.Equal(want) {
		t.Errorf("queried from %s, want %s", reader.asked.From, want)
	}
	if want := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC); !reader.asked.To.Equal(want) {
		t.Errorf("queried to %s, want %s", reader.asked.To, want)
	}
	if reader.asked.Limit != 10 || reader.asked.Offset != 20 {
		t.Errorf("queried limit=%d offset=%d, want 10 and 20", reader.asked.Limit, reader.asked.Offset)
	}
}

// TestTheDefaultOutcomeWindowIsAYearEndingNow, rather than everything: the
// table grows without bound and a phone opening the screen should not page in
// the whole history.
func TestTheDefaultOutcomeWindowIsAYearEndingNow(t *testing.T) {
	reader := &fakeReader{}
	getOutcomes(t, newReadHandler(&fakeReconciler{}, reader), "")

	if !reader.asked.To.Equal(readNow) {
		t.Errorf("default to = %s, want now (%s)", reader.asked.To, readNow)
	}
	if want := readNow.AddDate(-1, 0, 0); !reader.asked.From.Equal(want) {
		t.Errorf("default from = %s, want %s", reader.asked.From, want)
	}
}

// TestTheNetReturnComesFromTheStoredAccounting.
//
// It is already computed, already net of costs, and recomputing it here would
// be a second implementation of the cost model — which is the one thing that
// must not have two. An outcome with no accounting renders null rather than
// zero: no return was computed, as against a return of nothing.
func TestTheNetReturnComesFromTheStoredAccounting(t *testing.T) {
	reader := &fakeReader{
		page: []outcome.Resolved{
			resolved(constants.OutcomeTarget, readNow.Add(-time.Hour), "1.2345"),
			resolved(constants.OutcomeOpen, readNow.Add(-2*time.Hour), ""),
		},
		total: 2,
	}

	_, body := getOutcomes(t, newReadHandler(&fakeReconciler{}, reader), "")

	var page struct {
		Outcomes []struct {
			Status       string          `json:"status"`
			Measurable   bool            `json:"measurable"`
			NetReturnPct json.RawMessage `json:"net_return_pct"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Outcomes) != 2 {
		t.Fatalf("returned %d outcomes, want 2", len(page.Outcomes))
	}

	if got := string(page.Outcomes[0].NetReturnPct); got != `"1.2345"` {
		t.Errorf("net_return_pct = %s, want \"1.2345\" straight from the accounting", got)
	}
	if got := string(page.Outcomes[1].NetReturnPct); got != "null" {
		t.Errorf("net_return_pct with no accounting = %s, want null", got)
	}
}

// TestAnInvalidatedOutcomeIsFlaggedUnmeasurable.
//
// Its window had missing data, so whether it would have won is not knowable
// and it is excluded from every statistic. The flag is carried explicitly so a
// client is not left inferring it from the status string.
func TestAnInvalidatedOutcomeIsFlaggedUnmeasurable(t *testing.T) {
	reader := &fakeReader{
		page: []outcome.Resolved{
			resolved(constants.OutcomeInvalidated, readNow.Add(-time.Hour), ""),
			resolved(constants.OutcomeTarget, readNow.Add(-2*time.Hour), "0.9"),
		},
		total: 2,
	}

	_, body := getOutcomes(t, newReadHandler(&fakeReconciler{}, reader), "")

	var page struct {
		Outcomes []struct {
			Status     string `json:"status"`
			Measurable bool   `json:"measurable"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Outcomes[0].Measurable {
		t.Errorf("%s is flagged measurable", page.Outcomes[0].Status)
	}
	if !page.Outcomes[1].Measurable {
		t.Errorf("%s is not flagged measurable", page.Outcomes[1].Status)
	}
}

// TestPerformanceRunsTheLiveHalfOnly.
//
// # Why this matters
//
// Performance and reconciliation share one implementation deliberately: two
// definitions of a win rate is how the two screens end up disagreeing. What
// separates them is the backtest half, which replays history and is expensive.
// Performance must ask for it to be skipped — otherwise a screen a phone opens
// would replay years of candles.
func TestPerformanceRunsTheLiveHalfOnly(t *testing.T) {
	reconciler := &fakeReconciler{}
	getPerformance(t, newReadHandler(reconciler, &fakeReader{}), "")

	if reconciler.calls != 1 {
		t.Fatalf("reconcile called %d times, want 1", reconciler.calls)
	}
	if !reconciler.asked.SkipBacktest {
		t.Error("performance asked for the backtest half; it must be skipped")
	}
	if reconciler.asked.Symbol != readSymbol {
		t.Errorf("asked for %q, want %q", reconciler.asked.Symbol, readSymbol)
	}
}

// TestExpectancyIsWinRateTimesTheWinPlusLossRateTimesTheLoss.
//
// The number that decides whether a strategy is worth running, and it is not
// derivable from a win rate alone: a 30% win rate at a 3:1 payoff beats a 60%
// one at 1:2. If the arithmetic were wrong the sign would often still be
// right, so the assertion is on the value.
func TestExpectancyIsWinRateTimesTheWinPlusLossRateTimesTheLoss(t *testing.T) {
	reconciler := &fakeReconciler{report: outcome.Reconciliation{
		GeneratedAt: readNow,
		Groups: []outcome.ReconciledGroup{{
			Strategy: "ema_crossover", Version: "v1",
			Live: outcome.Side{
				Signals: 10, Resolved: 10, Targets: 3, Stops: 7, Wins: 3, Losses: 7,
				WinRate:        0.3,
				AverageWinPct:  decimal.RequireFromString("3"),
				AverageLossPct: decimal.RequireFromString("-1"),
				AverageCostPct: decimal.RequireFromString("0.1"),
			},
			Sample: outcome.SampleAdequacy{Resolved: 10, Required: 100},
		}},
	}}

	_, body := getPerformance(t, newReadHandler(reconciler, &fakeReader{}), "")

	var page struct {
		Groups []struct {
			WinRate       *float64 `json:"win_rate"`
			ExpectancyPct *string  `json:"expectancy_pct"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Groups) != 1 {
		t.Fatalf("returned %d groups, want 1", len(page.Groups))
	}

	// 0.3 x 3 + 0.7 x -1 = 0.2
	if page.Groups[0].ExpectancyPct == nil {
		t.Fatal("expectancy_pct is null with ten resolved signals")
	}
	if got := *page.Groups[0].ExpectancyPct; got != "0.2000" {
		t.Errorf("expectancy_pct = %s, want 0.2000", got)
	}
	if page.Groups[0].WinRate == nil || *page.Groups[0].WinRate != 0.3 {
		t.Errorf("win_rate = %v, want 0.3", page.Groups[0].WinRate)
	}
}

// TestAWinRateOverNothingIsNullRatherThanZero.
//
// A zero win rate reads as a strategy that never wins. Nothing having resolved
// yet is a different statement, and the two must not look alike on a screen
// somebody makes a decision from.
func TestAWinRateOverNothingIsNullRatherThanZero(t *testing.T) {
	reconciler := &fakeReconciler{report: outcome.Reconciliation{
		GeneratedAt: readNow,
		Groups: []outcome.ReconciledGroup{{
			Strategy: "ema_crossover", Version: "v1",
			Live:   outcome.Side{Signals: 4, StillOpen: 4, WinRate: math.NaN()},
			Sample: outcome.SampleAdequacy{Required: 100},
		}},
	}}

	_, body := getPerformance(t, newReadHandler(reconciler, &fakeReader{}), "")

	var page struct {
		Groups []map[string]json.RawMessage `json:"groups"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if got := string(page.Groups[0]["win_rate"]); got != "null" {
		t.Errorf("win_rate = %s, want null when nothing has resolved", got)
	}
	if got := string(page.Groups[0]["expectancy_pct"]); got != "null" {
		t.Errorf("expectancy_pct = %s, want null when nothing has resolved", got)
	}
}

// TestAThinSampleCarriesItsBanner.
//
// A win rate over nine trades and one over nine hundred must not be able to
// look alike. The banner travels with the numbers so a client cannot render
// them without it.
func TestAThinSampleCarriesItsBanner(t *testing.T) {
	reconciler := &fakeReconciler{report: outcome.Reconciliation{
		GeneratedAt: readNow,
		Groups: []outcome.ReconciledGroup{{
			Strategy: "ema_crossover", Version: "v1",
			Live: outcome.Side{Signals: 9, Resolved: 9, Wins: 5, Losses: 4, WinRate: 5.0 / 9.0},
			Sample: outcome.SampleAdequacy{
				Resolved: 9, Required: 100, Sufficient: false,
				PerDay: 1.5, Wait: 60 * 24 * time.Hour, Known: true,
			},
		}},
	}}

	_, body := getPerformance(t, newReadHandler(reconciler, &fakeReader{}), "")

	var page struct {
		Groups []struct {
			Sample struct {
				Resolved   int    `json:"resolved"`
				Required   int    `json:"required"`
				Sufficient bool   `json:"sufficient"`
				Banner     string `json:"banner"`
			} `json:"sample"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sample := page.Groups[0].Sample
	if sample.Sufficient {
		t.Error("nine resolved signals reported as a sufficient sample")
	}
	if sample.Banner == "" {
		t.Fatal("a thin sample carries no banner")
	}
	if !strings.Contains(sample.Banner, "NOT ENOUGH DATA") {
		t.Errorf("banner = %q, want it to say NOT ENOUGH DATA", sample.Banner)
	}
	if sample.Required != 100 || sample.Resolved != 9 {
		t.Errorf("sample = %d of %d, want 9 of 100", sample.Resolved, sample.Required)
	}
}

// TestPerformanceRefusesAReversedWindow rather than reporting an empty period
// as a strategy that produced nothing.
func TestPerformanceRefusesAReversedWindow(t *testing.T) {
	reconciler := &fakeReconciler{}
	recorder, _ := getPerformance(t, newReadHandler(reconciler, &fakeReader{}),
		"?from=2024-02-01T00:00:00Z&to=2024-01-01T00:00:00Z")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", recorder.Code)
	}
	if reconciler.calls != 0 {
		t.Error("the report was computed for a reversed window")
	}
}

// TestThereIsNoTotalAcrossGroups.
//
// Averaging across a parameter change produces a number describing nothing,
// and a client that found a total on the page would render it. The absence is
// the contract.
func TestThereIsNoTotalAcrossGroups(t *testing.T) {
	reconciler := &fakeReconciler{report: outcome.Reconciliation{
		GeneratedAt: readNow,
		Groups: []outcome.ReconciledGroup{
			{Strategy: "a", Version: "v1", Live: outcome.Side{Signals: 5, WinRate: math.NaN()}},
			{Strategy: "b", Version: "v1", Live: outcome.Side{Signals: 7, WinRate: math.NaN()}},
		},
	}}

	_, body := getPerformance(t, newReadHandler(reconciler, &fakeReader{}), "")

	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, forbidden := range []string{"total", "totals", "overall", "combined"} {
		if _, found := page[forbidden]; found {
			t.Errorf("the response carries %q; there is deliberately no total across groups", forbidden)
		}
	}
	if _, ok := page["note"]; !ok {
		t.Error("the response carries no note explaining why there is no total")
	}
}
