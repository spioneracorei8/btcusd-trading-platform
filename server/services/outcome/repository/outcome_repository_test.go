package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_outcome_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome/repository"
	_signal_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// storeSignal writes one signal so an outcome has something to point at.
func storeSignal(t *testing.T, pool *pgxpool.Pool, symbol string, at time.Time) models.Signal {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stored, err := _signal_repo.NewSignalRepoImpl(pool).InsertSignal(ctx, models.Signal{
		Symbol:          symbol,
		MarketType:      constants.MarketTypeSpot,
		Timeframe:       constants.Timeframe1h,
		SignalTime:      at,
		Direction:       constants.DirectionLong,
		Strength:        decimal.NewFromInt(constants.SignalStrengthNotReported),
		SignalPrice:     decimal.NullDecimal{Decimal: decimal.RequireFromString("64000"), Valid: true},
		StopLoss:        decimal.NullDecimal{Decimal: decimal.RequireFromString("63000"), Valid: true},
		TakeProfit:      decimal.NullDecimal{Decimal: decimal.RequireFromString("66000"), Valid: true},
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          []byte(`{"trigger":"integration test"}`),
	})
	if err != nil {
		t.Fatalf("InsertSignal() returned error: %v", err)
	}
	return stored
}

// TestEverySignalGetsFollowedExactlyOnce.
//
// A follower starting against a table of signals it has never seen — a first
// deploy, or a restart after an outage — has to pick all of them up. Doing it
// twice would double-count every one of them in the reconciliation, which is
// the one number this whole phase exists to produce.
func TestEverySignalGetsFollowedExactlyOnce(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTOUTCOMEENSURE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	var signals []models.Signal
	for i := range 3 {
		signals = append(signals, storeSignal(t, pool, symbol, base.Add(time.Duration(i)*time.Hour)))
	}

	opened, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 100)
	if err != nil {
		t.Fatalf("EnsureOutcomes() returned error: %v", err)
	}
	if len(opened) != len(signals) {
		t.Fatalf("opened %d outcomes for %d signals", len(opened), len(signals))
	}
	for _, o := range opened {
		if o.Status != constants.OutcomeOpen {
			t.Errorf("a newly opened outcome has status %q, want open", o.Status)
		}
	}

	// A second pass adds nothing.
	again, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 100)
	if err != nil {
		t.Fatalf("second EnsureOutcomes() returned error: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a second pass opened %d more outcomes", len(again))
	}

	open, err := repo.FetchOpen(ctx, symbol, constants.MarketTypeSpot, 100)
	if err != nil {
		t.Fatalf("FetchOpen() returned error: %v", err)
	}
	if len(open) != len(signals) {
		t.Errorf("FetchOpen() returned %d of %d signals", len(open), len(signals))
	}
	// Oldest first, so a backlog is worked through in the order it happened.
	for i := range open {
		if open[i].SignalId != signals[i].Id {
			t.Errorf("position %d holds %s, want %s", i, open[i].SignalId, signals[i].Id)
		}
	}
}

// TestProgressAndResolutionSurviveTheRoundTrip.
func TestProgressAndResolutionSurviveTheRoundTrip(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTOUTCOMESAVE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	signal := storeSignal(t, pool, symbol, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC))
	if _, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 10); err != nil {
		t.Fatalf("EnsureOutcomes() returned error: %v", err)
	}

	// Still running: excursions and a bar count, no resolution.
	running := models.SignalOutcome{
		SignalId: signal.Id,
		Status:   constants.OutcomeOpen,
		MAE:      decimal.NullDecimal{Decimal: decimal.RequireFromString("120.50000000"), Valid: true},
		MFE:      decimal.NullDecimal{Decimal: decimal.RequireFromString("340.25000000"), Valid: true},
		BarsHeld: 3,
	}
	saved, err := repo.SaveOutcome(ctx, running)
	if err != nil {
		t.Fatalf("SaveOutcome() on an open outcome returned error: %v", err)
	}
	if !saved.MAE.Decimal.Equal(running.MAE.Decimal) || !saved.MFE.Decimal.Equal(running.MFE.Decimal) {
		t.Errorf("excursions came back as %v / %v", saved.MAE, saved.MFE)
	}
	if saved.BarsHeld != 3 {
		t.Errorf("BarsHeld = %d, want 3", saved.BarsHeld)
	}
	if saved.Status != constants.OutcomeOpen {
		t.Errorf("Status = %q, want open", saved.Status)
	}

	// And still open, so a later pass picks it back up.
	open, err := repo.FetchOpen(ctx, symbol, constants.MarketTypeSpot, 10)
	if err != nil {
		t.Fatalf("FetchOpen() returned error: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("FetchOpen() returned %d rows, want the one still running", len(open))
	}

	// Then it resolves.
	at := time.Date(2026, 9, 2, 6, 0, 0, 0, time.UTC)
	running.Status = constants.OutcomeStop
	running.ResolvedAt = &at
	running.ResolvedPrice = decimal.NullDecimal{Decimal: decimal.RequireFromString("62999.99000000"), Valid: true}
	running.BarsHeld = 6
	running.BacktestWouldHave = []byte(`{"status":"stop","net_return_pct":"-1.6"}`)
	running.DivergenceNote = "one bar reached both levels"

	resolved, err := repo.SaveOutcome(ctx, running)
	if err != nil {
		t.Fatalf("SaveOutcome() on a resolution returned error: %v", err)
	}
	if resolved.Status != constants.OutcomeStop {
		t.Errorf("Status = %q, want stop", resolved.Status)
	}
	if resolved.ResolvedAt == nil || !resolved.ResolvedAt.Equal(at) {
		t.Errorf("ResolvedAt = %v, want %v", resolved.ResolvedAt, at)
	}
	if !resolved.ResolvedPrice.Decimal.Equal(running.ResolvedPrice.Decimal) {
		t.Errorf("ResolvedPrice = %v", resolved.ResolvedPrice)
	}
	if string(resolved.BacktestWouldHave) == "" {
		t.Error("the accounting was not stored")
	}
	if resolved.DivergenceNote != running.DivergenceNote {
		t.Errorf("DivergenceNote = %q", resolved.DivergenceNote)
	}

	// A resolved outcome is out of the working set.
	open, err = repo.FetchOpen(ctx, symbol, constants.MarketTypeSpot, 10)
	if err != nil {
		t.Fatalf("FetchOpen() returned error: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("a resolved outcome is still being followed: %+v", open)
	}
}

// TestAHalfWrittenResolutionIsRefusedByTheDatabase.
//
// A row saying "target" with no price and no time would read as a finished
// trade to every query that follows, and there is no later moment at which
// the missing half arrives.
func TestAHalfWrittenResolutionIsRefusedByTheDatabase(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTOUTCOMEHALF"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	signal := storeSignal(t, pool, symbol, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	if _, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 10); err != nil {
		t.Fatalf("EnsureOutcomes() returned error: %v", err)
	}

	at := time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC)
	price := decimal.NullDecimal{Decimal: decimal.RequireFromString("66000"), Valid: true}

	tests := map[string]models.SignalOutcome{
		"resolved with no price": {
			SignalId: signal.Id, Status: constants.OutcomeTarget, ResolvedAt: &at,
		},
		"resolved with no time": {
			SignalId: signal.Id, Status: constants.OutcomeTarget, ResolvedPrice: price,
		},
		"open with a price": {
			SignalId: signal.Id, Status: constants.OutcomeOpen,
			ResolvedAt: &at, ResolvedPrice: price,
		},
		"a status nothing knows": {
			SignalId: signal.Id, Status: constants.OutcomeStatus("winner"),
			ResolvedAt: &at, ResolvedPrice: price,
		},
	}

	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := repo.SaveOutcome(ctx, row); err == nil {
				t.Error("it was accepted")
			}
		})
	}

	// The control: a complete resolution goes in, so the cases above fail for
	// their own reason rather than because everything fails.
	if _, err := repo.SaveOutcome(ctx, models.SignalOutcome{
		SignalId: signal.Id, Status: constants.OutcomeTarget,
		ResolvedAt: &at, ResolvedPrice: price,
	}); err != nil {
		t.Errorf("the control case failed: %v", err)
	}
}

// TestDeletingASignalTakesItsOutcomeWithIt, so the two cannot drift apart.
func TestDeletingASignalTakesItsOutcomeWithIt(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTOUTCOMECASCADE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	signal := storeSignal(t, pool, symbol, time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC))
	if _, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 10); err != nil {
		t.Fatalf("EnsureOutcomes() returned error: %v", err)
	}
	if _, err := repo.FetchOutcome(ctx, signal.Id); err != nil {
		t.Fatalf("FetchOutcome() returned error: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM signals WHERE id = $1", signal.Id); err != nil {
		t.Fatalf("delete the signal: %v", err)
	}

	if _, err := repo.FetchOutcome(ctx, signal.Id); err == nil {
		t.Error("the outcome outlived the signal it describes")
	}
}

// TestAnEntryPriceIsWrittenOnce.
//
// It is the denominator of every return computed from this signal. A second
// answer would silently change every comparison already drawn against the
// first, and the write-once is the database's rather than the follower's so
// two passes racing cannot both win.
func TestAnEntryPriceIsWrittenOnce(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTENTRYONCE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _signal_repo.NewSignalRepoImpl(pool)
	signal := storeSignal(t, pool, symbol, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	if signal.EntryPrice.Valid {
		t.Fatal("a newly recorded signal already has an entry price")
	}

	first := decimal.RequireFromString("64123.45000000")
	stored, err := repo.SetEntryPrice(ctx, signal.Id, first)
	if err != nil {
		t.Fatalf("SetEntryPrice() returned error: %v", err)
	}
	if !stored.EntryPrice.Valid || !stored.EntryPrice.Decimal.Equal(first) {
		t.Fatalf("EntryPrice = %v, want %s", stored.EntryPrice, first)
	}

	if _, err := repo.SetEntryPrice(ctx, signal.Id, decimal.RequireFromString("99999")); err == nil {
		t.Error("a second entry price was accepted")
	}

	again, err := repo.FetchSignalById(ctx, signal.Id)
	if err != nil {
		t.Fatalf("FetchSignalById() returned error: %v", err)
	}
	if !again.EntryPrice.Decimal.Equal(first) {
		t.Errorf("EntryPrice is now %s, want the first answer %s", again.EntryPrice.Decimal, first)
	}
}

// TestSettingAnEntryPriceOnAMissingSignalIsReported.
func TestSettingAnEntryPriceOnAMissingSignalIsReported(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	repo := _signal_repo.NewSignalRepoImpl(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.SetEntryPrice(ctx, uuid.New(), decimal.NewFromInt(1)); err == nil {
		t.Error("setting an entry price on a signal that does not exist returned no error")
	}
}

// TestASignalIsNotStarvedByOlderOnesAlreadyFollowed.
//
// The batch is smaller than the backlog can be. If the statement picked the
// oldest signals rather than the oldest *unfollowed* ones, every pass would
// return the same already-followed rows and a new signal would never get an
// outcome — silently, because each pass looks like it did its work.
//
// ON CONFLICT DO NOTHING does not save it: the conflict is what stops a
// duplicate, not what finds the signal that still needs a row.
func TestASignalIsNotStarvedByOlderOnesAlreadyFollowed(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTOUTCOMESTARVE"
	testhelper.CleanupSymbol(t, pool, symbol)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := _outcome_repo.NewOutcomeRepoImpl(pool)
	base := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		storeSignal(t, pool, symbol, base.Add(time.Duration(i)*time.Hour))
	}

	// A batch smaller than the backlog, run until it stops finding work —
	// which is what the follower does, one pass a minute.
	total := 0
	for range 5 {
		opened, err := repo.EnsureOutcomes(ctx, symbol, constants.MarketTypeSpot, 2)
		if err != nil {
			t.Fatalf("EnsureOutcomes() returned error: %v", err)
		}
		total += len(opened)
	}
	if total != 3 {
		t.Errorf("opened %d outcomes for 3 signals across repeated passes", total)
	}
}
