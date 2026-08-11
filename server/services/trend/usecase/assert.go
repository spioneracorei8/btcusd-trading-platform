//go:build !trenddebug

package usecase

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// assertClosedBy is a no-op in ordinary builds.
//
// The phase-05 spec asks for a debug assertion that panics loudly when a
// higher-timeframe contribution has not closed by the decision instant.
// CLAUDE.md §4 forbids panic() in business logic, and both are right: a
// programming error should be impossible to miss while being developed, and a
// long-running collector should never die of an assertion on a VPS at 3am.
//
// The build tag settles it. Tests compile the panicking version (see
// assert_debug.go and the -tags trenddebug in the Makefile); shipped binaries
// compile this one, where the compiler removes the call entirely.
func assertClosedBy(trend.TimeframeView, time.Time) {}
