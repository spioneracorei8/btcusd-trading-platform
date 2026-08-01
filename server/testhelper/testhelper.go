// Package testhelper provides the shared setup for repository integration
// tests.
//
// These tests need a real PostgreSQL/TimescaleDB with the migrations already
// applied. Point TEST_DATABASE_URL at one — `make test-integration` starts the
// compose database, migrates it and sets the variable for you.
//
// Without TEST_DATABASE_URL the tests skip, so `go test ./...` stays green on
// a machine that has no Docker.
package testhelper

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
)

// TestDatabaseURLEnv names the variable that enables the integration tests.
const TestDatabaseURLEnv = "TEST_DATABASE_URL"

// NewTestPool connects to the test database or skips the calling test.
func NewTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(TestDatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping integration test", TestDatabaseURLEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, database.PoolOptions{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("connect to %s: %v", TestDatabaseURLEnv, err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	if _, err := pool.Exec(ctx, "SELECT 1 FROM candles LIMIT 0"); err != nil {
		t.Fatalf("test database has no candles table, run `make migrate-up` first: %v", err)
	}
	return pool
}

// CleanupSymbol removes every row a test wrote for the given symbol.
func CleanupSymbol(t *testing.T, pool *pgxpool.Pool, symbol string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		for _, table := range []string{"candles", "signals", "data_gaps"} {
			if _, err := pool.Exec(ctx, "DELETE FROM "+table+" WHERE symbol = $1", symbol); err != nil {
				t.Errorf("cleanup %s for %s: %v", table, symbol, err)
			}
		}
	})
}
