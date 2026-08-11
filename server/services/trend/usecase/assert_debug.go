//go:build trenddebug

package usecase

import (
	"fmt"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

// assertClosedBy panics when a view comes from a bar that had not closed.
//
// This is the loud version, compiled under -tags trenddebug, which is how the
// test suite runs. Reaching it means the merge in aligner.go has a bug that
// nothing downstream could detect: a backtest with cross-timeframe look-ahead
// does not fail, it quietly reports numbers live will never reproduce.
//
// Shipped binaries compile the empty version in assert.go instead, so no
// running process can be brought down by this.
func assertClosedBy(view trend.TimeframeView, at time.Time) {
	if view.CloseTime.After(at) {
		panic(fmt.Sprintf(
			"trend: look-ahead across timeframes — the %s view comes from a bar "+
				"closing at %s, %s after the decision instant %s",
			view.Timeframe,
			view.CloseTime.Format(time.RFC3339Nano),
			view.CloseTime.Sub(at),
			at.Format(time.RFC3339Nano)))
	}
}
