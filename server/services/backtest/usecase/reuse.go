package usecase

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
)

// strategyGuard refuses to replay a strategy instance that has already run.
//
// # The bug this exists to make impossible
//
// A strategy carries state: EMAs mid-stream, a previous-crossing flag, a
// pullback in progress. Running the same instance twice starts the second run
// with whatever the first left behind, so it sees a warmed-up strategy where a
// real run would see a cold one.
//
// It was found in the cost sweep, where the 1.0x row — which should be
// identical to the headline run by construction — reported a different trade
// count. That is the mildest possible symptom. The same defect in --compare
// would have made the filtered and unfiltered runs incomparable, which is the
// entire point of the feature, and nothing in the output would have said so.
//
// A caller that wants two runs builds two strategies. This turns the silent
// wrong answer into an error naming the fix.
type strategyGuard struct {
	mu   sync.Mutex
	seen map[uintptr]claimed
}

// claimed keeps the strategy itself, not only a description of it.
//
// That reference is load-bearing. The map is keyed by address, and Go reuses
// the address of a collected object — the first version of this guard reported
// a false positive when a freed strategy's address was handed to the next
// allocation, which is a worse failure than the one it was written to catch.
// Holding the value keeps it reachable, so its address stays its own for as
// long as the engine could still be asked about it.
type claimed struct {
	strategy    strategy.Strategy
	description string
}

func newStrategyGuard() *strategyGuard {
	return &strategyGuard{seen: map[uintptr]claimed{}}
}

// claim records an instance, or reports that it has already been used.
//
// Only pointer-backed strategies are tracked. A value type is copied into the
// interface and cannot accumulate state across runs, so there is nothing to
// catch — and reflect.Pointer would panic on one, which CLAUDE.md §4 rules out
// of business logic.
func (g *strategyGuard) claim(strat strategy.Strategy) error {
	value := reflect.ValueOf(strat)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	address := value.Pointer()
	if previous, used := g.seen[address]; used {
		return fmt.Errorf(
			"backtest: strategy %s has already been run by this engine.\n"+
				"A strategy carries state — moving averages mid-stream, a pending\n"+
				"pullback — so a second run would start where the first stopped and\n"+
				"the two results would not be comparable. Build a fresh instance per\n"+
				"run; that is what makes --compare and --cost-sweep mean anything.\n"+
				"(first run: %s)", strat.Name(), previous.description)
	}

	g.seen[address] = claimed{
		strategy:    strat,
		description: fmt.Sprintf("%s %s", strat.Name(), strat.Version()),
	}
	return nil
}
