package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	_market_repo "github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository"
	"github.com/spioneracorei8/btcusd-trading-platform/server/testhelper"
)

// cleanupCollectorStatus removes the row a test wrote.
func cleanupCollectorStatus(t *testing.T, pool *pgxpool.Pool, symbol string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := pool.Exec(ctx, "DELETE FROM collector_status WHERE symbol = $1", symbol); err != nil {
			t.Errorf("cleanup collector_status for %s: %v", symbol, err)
		}
	})
}

// TestHeartbeatLeavesStartedAtAlone is the whole point of having both
// columns: a heartbeat must not be able to disguise a crash loop as uptime.
func TestHeartbeatLeavesStartedAtAlone(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTHEARTBEAT"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	started, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("RegisterStart() returned error: %v", err)
	}
	if started.StartedAt.IsZero() || started.UpdatedAt.IsZero() {
		t.Fatalf("RegisterStart() returned an incomplete row: %+v", started)
	}
	if started.WSConnected {
		t.Error("a freshly started collector must not claim to be connected")
	}

	// A heartbeat later, only updated_at may have moved.
	time.Sleep(10 * time.Millisecond)
	if err := repo.Heartbeat(ctx, symbol, constants.MarketTypeSpot, true, models.EvaluatorState{}); err != nil {
		t.Fatalf("Heartbeat() returned error: %v", err)
	}

	afterBeat, err := repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}
	if !afterBeat.StartedAt.Equal(started.StartedAt) {
		t.Errorf("a heartbeat moved started_at: %s -> %s", started.StartedAt, afterBeat.StartedAt)
	}
	if !afterBeat.UpdatedAt.After(started.UpdatedAt) {
		t.Errorf("a heartbeat did not move updated_at: still %s", afterBeat.UpdatedAt)
	}
	if !afterBeat.WSConnected {
		t.Error("Heartbeat(true) did not record the connection")
	}
}

// TestRegisterStartMovesStartedAt covers the other half: a restart must be
// visible, so a collector that keeps dying does not look like one that has
// been up for days.
func TestRegisterStartMovesStartedAt(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTRESTART"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("first RegisterStart() returned error: %v", err)
	}
	if err := repo.MarkConnected(ctx, symbol, constants.MarketTypeSpot, true); err != nil {
		t.Fatalf("MarkConnected() returned error: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	second, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("second RegisterStart() returned error: %v", err)
	}

	if !second.StartedAt.After(first.StartedAt) {
		t.Errorf("a restart did not move started_at: %s -> %s", first.StartedAt, second.StartedAt)
	}
	// A restart starts a fresh life: the previous run's reconnect count would
	// otherwise be read as instability of the current process.
	if second.ReconnectCount != 0 {
		t.Errorf("ReconnectCount = %d after restart, want 0", second.ReconnectCount)
	}
	if second.WSConnected {
		t.Error("a restarted collector must not inherit the old connected flag")
	}
}

func TestMarkConnectedAndDisconnected(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTCONNSTATE"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot); err != nil {
		t.Fatalf("RegisterStart() returned error: %v", err)
	}

	// The first connection is not a reconnect and must not be counted as one.
	if err := repo.MarkConnected(ctx, symbol, constants.MarketTypeSpot, false); err != nil {
		t.Fatalf("MarkConnected() returned error: %v", err)
	}
	got, err := repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}
	if !got.WSConnected || got.LastConnectedAt == nil {
		t.Errorf("connection not recorded: %+v", got)
	}
	if got.ReconnectCount != 0 {
		t.Errorf("ReconnectCount = %d after the first connection, want 0", got.ReconnectCount)
	}

	const note = "binance closed the connection after 24h"
	if err := repo.MarkDisconnected(ctx, symbol, constants.MarketTypeSpot, note); err != nil {
		t.Fatalf("MarkDisconnected() returned error: %v", err)
	}
	if err := repo.MarkConnected(ctx, symbol, constants.MarketTypeSpot, true); err != nil {
		t.Fatalf("reconnect MarkConnected() returned error: %v", err)
	}

	got, err = repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}
	if got.ReconnectCount != 1 {
		t.Errorf("ReconnectCount = %d after one reconnect, want 1", got.ReconnectCount)
	}
	if got.LastDisconnectedAt == nil {
		t.Error("LastDisconnectedAt was not recorded")
	}
	if got.LastDisconnectNote != note {
		t.Errorf("LastDisconnectNote = %q, want %q", got.LastDisconnectNote, note)
	}
	if !got.WSConnected {
		t.Error("the reconnect did not clear the disconnected state")
	}
}

// TestSetStateMovesStateChangedAtOnlyOnChange covers the persistence half of
// the lifecycle state: the api reads this row rather than the collector's
// memory, so a transition that never reaches the row is invisible.
//
// state_changed_at must move on a real transition and stand still on a repeat,
// otherwise "how long has it been backfilling" resets on every write.
func TestSetStateMovesStateChangedAtOnlyOnChange(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTSETSTATE"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	started, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("RegisterStart() returned error: %v", err)
	}
	// A fresh process is starting, whatever the previous run ended on.
	if started.State != constants.CollectorStarting {
		t.Errorf("State = %s after RegisterStart(), want %s", started.State, constants.CollectorStarting)
	}

	time.Sleep(10 * time.Millisecond)
	if err := repo.SetState(ctx, symbol, constants.MarketTypeSpot, constants.CollectorBackfilling); err != nil {
		t.Fatalf("SetState(backfilling) returned error: %v", err)
	}
	backfilling, err := repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}
	if backfilling.State != constants.CollectorBackfilling {
		t.Errorf("State = %s, want %s", backfilling.State, constants.CollectorBackfilling)
	}
	if !backfilling.StateChangedAt.After(started.StateChangedAt) {
		t.Errorf("StateChangedAt did not move on a transition: still %s", backfilling.StateChangedAt)
	}
	// The lifecycle state is not the process lifetime; it must not disturb it.
	if !backfilling.StartedAt.Equal(started.StartedAt) {
		t.Errorf("SetState moved started_at: %s -> %s", started.StartedAt, backfilling.StartedAt)
	}

	time.Sleep(10 * time.Millisecond)
	if err := repo.SetState(ctx, symbol, constants.MarketTypeSpot, constants.CollectorBackfilling); err != nil {
		t.Fatalf("repeated SetState(backfilling) returned error: %v", err)
	}
	repeated, err := repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}
	if !repeated.StateChangedAt.Equal(backfilling.StateChangedAt) {
		t.Errorf("StateChangedAt moved on a repeated write: %s -> %s",
			backfilling.StateChangedAt, repeated.StateChangedAt)
	}
}

// TestSetStateRejectsUnknownState proves the CHECK constraint is doing its
// job: the column is text, so without it a typo would be stored happily and
// only fail much later when the api parsed the row back.
func TestSetStateRejectsUnknownState(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTBADSTATE"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot); err != nil {
		t.Fatalf("RegisterStart() returned error: %v", err)
	}

	_, err := pool.Exec(ctx,
		"UPDATE collector_status SET state = 'nonsense' WHERE symbol = $1", symbol)
	if err == nil {
		t.Fatal("the database accepted a state outside the enum")
	}

	// never_started is the absence of a row, so storing it would be a lie the
	// constraint must also refuse.
	_, err = pool.Exec(ctx,
		"UPDATE collector_status SET state = $2 WHERE symbol = $1",
		symbol, constants.CollectorNeverStarted.String())
	if err == nil {
		t.Error("the database accepted never_started as a stored state")
	}
}

func TestFetchStatusWithoutCollector(t *testing.T) {
	pool := testhelper.NewTestPool(t)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := repo.FetchStatus(ctx, "TESTNEVERSTARTED", constants.MarketTypeSpot)
	if !errors.Is(err, constants.ErrNotFound) {
		t.Fatalf("FetchStatus() for an unknown collector returned %v, want ErrNotFound", err)
	}
}

// TestUptimeAndHeartbeatAgeAreIndependent pins the semantic difference the
// two columns exist for.
func TestUptimeAndHeartbeatAgeAreIndependent(t *testing.T) {
	pool := testhelper.NewTestPool(t)
	const symbol = "TESTUPTIME"
	cleanupCollectorStatus(t, pool, symbol)

	repo := _market_repo.NewCollectorStatusRepoImpl(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := repo.RegisterStart(ctx, symbol, constants.MarketTypeSpot); err != nil {
		t.Fatalf("RegisterStart() returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := repo.Heartbeat(ctx, symbol, constants.MarketTypeSpot, true, models.EvaluatorState{}); err != nil {
		t.Fatalf("Heartbeat() returned error: %v", err)
	}

	got, err := repo.FetchStatus(ctx, symbol, constants.MarketTypeSpot)
	if err != nil {
		t.Fatalf("FetchStatus() returned error: %v", err)
	}

	now := time.Now().UTC()
	uptime := got.Uptime(now)
	age := got.HeartbeatAge(now)

	if uptime <= age {
		t.Errorf("uptime (%s) should exceed heartbeat age (%s) after a later beat", uptime, age)
	}
	if uptime < 50*time.Millisecond {
		t.Errorf("uptime = %s, want at least the 50ms the test waited", uptime)
	}
}
