package repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	_outcome_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/repository"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// planted is one signal with a resolved outcome, written straight in.
type planted struct {
	at      time.Time
	params  string
	version string
	status  constants.OutcomeStatus
	netPct  string
	entry   string
	note    string
}

// plant writes a signal and its outcome, so the aggregate has something real
// to group.
func plant(t *testing.T, pool *pgxpool.Pool, symbol string, p planted) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	version := p.version
	if version == "" {
		version = "v1"
	}

	reason := []byte(`{"trigger":"planted","strategy":{"name":"ema_crossover","version":"` +
		version + `","params":` + p.params + `}}`)

	signal, err := _signal_repo.NewSignalRepoImpl(pool).InsertSignal(ctx, models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe1h,
		SignalTime:      p.at,
		Direction:       constants.DirectionLong,
		Strength:        decimal.NewFromInt(constants.SignalStrengthNotReported),
		SignalPrice:     decimal.NullDecimal{Decimal: decimal.RequireFromString("64000"), Valid: true},
		EntryPrice:      decimal.NullDecimal{Decimal: decimal.RequireFromString(p.entry), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: version,
		Reason:          reason,
	})
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	if _, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 500); err != nil {
		t.Fatalf("EnsureOutcomes() returned error: %v", err)
	}

	row := models.SignalOutcome{
		SignalId:       signal.Id,
		Status:         p.status,
		BarsHeld:       4,
		DivergenceNote: p.note,
	}
	if p.status != constants.OutcomeOpen {
		at := p.at.Add(4 * time.Hour)
		row.ResolvedAt = &at
		if p.status != constants.OutcomeInvalidated {
			row.ResolvedPrice = decimal.NullDecimal{
				Decimal: decimal.RequireFromString("64500"), Valid: true}
			accounting, err := json.Marshal(map[string]string{
				"status": p.status.String(), "net_return_pct": p.netPct, "cost_pct": "0.1000",
			})
			if err != nil {
				t.Fatalf("marshal the accounting: %v", err)
			}
			row.BacktestWouldHave = accounting
		}
	}
	if _, err := repo.SaveOutcome(ctx, row); err != nil {
		t.Fatalf("SaveOutcome() returned error: %v", err)
	}
}

// TestTheAggregateGroupsByParameterSetAndVersion.
//
// The whole comparison rests on this. A parameter change between two signals
// leaves two incomparable groups in one table looking alike, and averaging
// across it produces a number describing nothing — which is indistinguishable
// from a number describing something.
func TestTheAggregateGroupsByParameterSetAndVersion(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRECONGROUP"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fast9 := `[{"name":"fast","value":"9"},{"name":"slow","value":"21"}]`
	fast12 := `[{"name":"fast","value":"12"},{"name":"slow","value":"21"}]`

	// Two wins on fast=9, one loss on fast=12, and one on a newer version.
	plant(t, pool, symbol, planted{at: base, params: fast9,
		status: constants.OutcomeTarget, netPct: "1.5", entry: "64000"})
	plant(t, pool, symbol, planted{at: base.Add(time.Hour), params: fast9,
		status: constants.OutcomeTarget, netPct: "0.9", entry: "64100"})
	plant(t, pool, symbol, planted{at: base.Add(2 * time.Hour), params: fast12,
		status: constants.OutcomeStop, netPct: "-1.2", entry: "64200"})
	plant(t, pool, symbol, planted{at: base.Add(3 * time.Hour), params: fast9,
		version: "v2", status: constants.OutcomeStop, netPct: "-0.4", entry: "64300"})

	repo := _outcome_repo.NewReconcileRepoImpl(pool)
	groups, err := liveGroups(ctx, repo, outcome.ReconcileParams{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		From: base.Add(-time.Hour), To: base.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("LiveSignals() returned error: %v", err)
	}

	if len(groups) != 3 {
		t.Fatalf("three distinct (version, parameter set) combinations produced %d groups", len(groups))
	}

	byKey := map[string]outcome.ReconciledGroup{}
	for _, g := range groups {
		key := g.Version
		for _, p := range g.Params {
			key += " " + p.Name + "=" + p.Value
		}
		byKey[key] = g
	}

	first, ok := byKey["v1 fast=9 slow=21"]
	if !ok {
		t.Fatalf("no group for v1 fast=9; got %v", keys(byKey))
	}
	if first.Live.Resolved != 2 || first.Live.Wins != 2 || first.Live.Losses != 0 {
		t.Errorf("v1 fast=9 = %d resolved, %d wins, %d losses; want 2/2/0",
			first.Live.Resolved, first.Live.Wins, first.Live.Losses)
	}
	if got := first.Live.AverageWinPct.StringFixed(2); got != "1.20" {
		t.Errorf("average win = %s, want 1.20 (the mean of 1.5 and 0.9)", got)
	}
	if got := first.Live.AverageEntryPrice.StringFixed(2); got != "64050.00" {
		t.Errorf("average entry = %s, want 64050.00", got)
	}

	if second, ok := byKey["v1 fast=12 slow=21"]; !ok || second.Live.Losses != 1 {
		t.Errorf("v1 fast=12 did not land in its own group: %+v", second)
	}
	if third, ok := byKey["v2 fast=9 slow=21"]; !ok || third.Live.Resolved != 1 {
		t.Errorf("a new version was merged with the old one: %+v", third)
	}
}

// TestInvalidatedSignalsAreCountedAndThenExcluded.
//
// Their window has missing data, so whether they would have won is not
// knowable. Counting them would put a guess into the win rate; dropping them
// silently would hide a period where the collector was struggling, which is
// itself a finding.
func TestInvalidatedSignalsAreCountedAndThenExcluded(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRECONINVALID"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	params := `[{"name":"fast","value":"9"}]`

	plant(t, pool, symbol, planted{at: base, params: params,
		status: constants.OutcomeTarget, netPct: "1.0", entry: "64000"})
	plant(t, pool, symbol, planted{at: base.Add(time.Hour), params: params,
		status: constants.OutcomeInvalidated, entry: "64100"})
	plant(t, pool, symbol, planted{at: base.Add(2 * time.Hour), params: params,
		status: constants.OutcomeOpen, entry: "64200"})

	repo := _outcome_repo.NewReconcileRepoImpl(pool)
	groups, err := liveGroups(ctx, repo, outcome.ReconcileParams{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		From: base.Add(-time.Hour), To: base.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("LiveSignals() returned error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("one parameter set produced %d groups", len(groups))
	}

	side := groups[0].Live
	if side.Signals != 3 {
		t.Errorf("Signals = %d, want all 3 counted", side.Signals)
	}
	if side.Invalidated != 1 {
		t.Errorf("Invalidated = %d, want 1 reported", side.Invalidated)
	}
	if side.StillOpen != 1 {
		t.Errorf("StillOpen = %d, want 1", side.StillOpen)
	}
	if side.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1: neither the open nor the invalidated one counts",
			side.Resolved)
	}
	if side.WinRate != 1 {
		t.Errorf("WinRate = %v, want 1 over the single measurable signal", side.WinRate)
	}
}

// TestSignalsOutsideTheWindowAreNotCounted.
func TestSignalsOutsideTheWindowAreNotCounted(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRECONWINDOW"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	params := `[{"name":"fast","value":"9"}]`

	plant(t, pool, symbol, planted{at: base.Add(-48 * time.Hour), params: params,
		status: constants.OutcomeTarget, netPct: "1.0", entry: "64000"})
	plant(t, pool, symbol, planted{at: base, params: params,
		status: constants.OutcomeStop, netPct: "-1.0", entry: "64100"})

	repo := _outcome_repo.NewReconcileRepoImpl(pool)
	groups, err := liveGroups(ctx, repo, outcome.ReconcileParams{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		From: base.Add(-time.Hour), To: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("LiveSignals() returned error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("the window produced %d groups", len(groups))
	}
	if groups[0].Live.Resolved != 1 || groups[0].Live.Wins != 0 {
		t.Errorf("a signal outside the window was counted: %+v", groups[0].Live)
	}
}

// TestResolutionsRestingOnAnAssumptionAreCounted.
//
// A win rate resting largely on bars that reached both levels, or on entries
// that gapped past one, rests on an assumption rather than on evidence — and
// the report has to be able to say how much of it does.
func TestResolutionsRestingOnAnAssumptionAreCounted(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRECONNOTED"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	params := `[{"name":"fast","value":"9"}]`

	plant(t, pool, symbol, planted{at: base, params: params,
		status: constants.OutcomeStop, netPct: "-1.0", entry: "64000"})
	plant(t, pool, symbol, planted{at: base.Add(time.Hour), params: params,
		status: constants.OutcomeStop, netPct: "-1.0", entry: "64100",
		note: "one bar reached both the stop and the target"})

	repo := _outcome_repo.NewReconcileRepoImpl(pool)
	groups, err := liveGroups(ctx, repo, outcome.ReconcileParams{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		From: base.Add(-time.Hour), To: base.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("LiveSignals() returned error: %v", err)
	}
	if got := groups[0].Live.Noted; got != 1 {
		t.Errorf("resolutions resting on an assumption = %d, want 1", got)
	}
}

func keys(m map[string]outcome.ReconciledGroup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestAWinIsAPositiveReturnAfterCostAndNotAReachedTarget.
//
// Scalping at these timeframes is dominated by cost. A target reached by less
// than the round trip charged is a losing trade, and counting it as a win
// because of its status would report a strategy as profitable on exactly the
// trades that were not.
func TestAWinIsAPositiveReturnAfterCostAndNotAReachedTarget(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRECONNET"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	params := `[{"name":"fast","value":"9"}]`

	// One target that cost more than it made, and two stops that gapped in
	// the position's favour. Deliberately lopsided: counting wins by status
	// would give one, counting them by return gives two, so the two rules
	// cannot agree by accident on this fixture.
	plant(t, pool, symbol, planted{at: base, params: params,
		status: constants.OutcomeTarget, netPct: "-0.05", entry: "64000"})
	plant(t, pool, symbol, planted{at: base.Add(time.Hour), params: params,
		status: constants.OutcomeStop, netPct: "1.20", entry: "64100"})
	plant(t, pool, symbol, planted{at: base.Add(2 * time.Hour), params: params,
		status: constants.OutcomeStop, netPct: "0.80", entry: "64200"})

	repo := _outcome_repo.NewReconcileRepoImpl(pool)
	groups, err := liveGroups(ctx, repo, outcome.ReconcileParams{
		Symbol: symbol, MarketType: constants.MarketTypeSpot,
		From: base.Add(-time.Hour), To: base.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("LiveSignals() returned error: %v", err)
	}

	side := groups[0].Live
	if side.Targets != 1 || side.Stops != 2 {
		t.Errorf("statuses = %d targets, %d stops; want 1 and 2", side.Targets, side.Stops)
	}
	if side.Wins != 2 || side.Losses != 1 {
		t.Errorf("wins/losses = %d/%d, want 2/1 counted on the return rather than the status",
			side.Wins, side.Losses)
	}
	if got := side.AverageWinPct.StringFixed(2); got != "1.00" {
		t.Errorf("average win = %s, want 1.00 (the mean of the two profitable stops)", got)
	}
	if got := side.AverageLossPct.StringFixed(2); got != "-0.05" {
		t.Errorf("average loss = %s, want the unprofitable target at -0.05", got)
	}
}

// liveGroups aggregates what the repository projects, the same way the
// usecase does — so these tests still assert on groups while the repository
// has gone back to returning rows.
func liveGroups(
	ctx context.Context, repo outcome.ReconcileRepository, params outcome.ReconcileParams,
) ([]outcome.ReconciledGroup, error) {
	signals, err := repo.LiveSignals(ctx, params)
	if err != nil {
		return nil, err
	}

	var (
		order []string
		byKey = map[string][]outcome.LiveSignal{}
	)
	for _, s := range signals {
		key := s.GroupKey()
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], s)
	}

	groups := make([]outcome.ReconciledGroup, 0, len(order))
	for _, key := range order {
		members := byKey[key]
		groups = append(groups, outcome.ReconciledGroup{
			Strategy: members[0].Strategy,
			Version:  members[0].Version,
			Params:   members[0].Params,
			Live:     outcome.SideOf(members),
		})
	}
	return groups, nil
}
