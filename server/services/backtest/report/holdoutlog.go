package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// HoldoutLogPath is where holdout uses are recorded, relative to the
// repository root.
const HoldoutLogPath = "docs/holdout-log.md"

// HoldoutEntry is one recorded use of the holdout set.
type HoldoutEntry struct {
	At time.Time

	StrategyName    string
	StrategyVersion string
	TrendFilter     string

	From time.Time
	To   time.Time

	NetReturn  float64
	TradeCount int
	Note       string
}

// AppendHoldoutUse records a holdout run.
//
// # Why a log and not a lock
//
// The holdout rule cannot be enforced by software. Anyone can pass explicit
// dates, run against a copy of the database, or simply ignore the result they
// do not like. A lock would create the illusion of a guarantee it cannot give,
// and would be worked around the first time it was inconvenient.
//
// What a log does instead is make the rule's violation *visible*, including
// to the person violating it. Two entries in this file for two different
// strategies is a complete account of what happened: the second result was
// selected with knowledge of the first, and whatever it says about the second
// strategy, it no longer says it about an untouched set.
//
// The entry is appended before the numbers are read, so a run whose result was
// disliked is still on the record.
func AppendHoldoutUse(path string, entry HoldoutEntry) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var b strings.Builder
	if len(existing) == 0 {
		b.WriteString(holdoutHeader)
	}

	previous := strings.Count(string(existing), "\n## ")
	fmt.Fprintf(&b, "\n## Use %d — %s\n\n", previous+1, entry.At.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Strategy:** %s %s\n", entry.StrategyName, entry.StrategyVersion)
	fmt.Fprintf(&b, "- **Trend filter:** %s\n", orNone(entry.TrendFilter))
	fmt.Fprintf(&b, "- **Range:** %s .. %s\n",
		entry.From.Format("2006-01-02"), entry.To.Format("2006-01-02"))
	fmt.Fprintf(&b, "- **Net return after costs:** %s\n", formatPercent(entry.NetReturn))
	fmt.Fprintf(&b, "- **Trades:** %d\n", entry.TradeCount)
	if entry.Note != "" {
		fmt.Fprintf(&b, "- **Note:** %s\n", entry.Note)
	}

	if previous > 0 {
		fmt.Fprintf(&b, "\n> This is holdout use number %d. The set was already spent by use 1.\n"+
			"> Whatever this run says, it is not a result on untouched data — it was\n"+
			"> chosen in the knowledge of what came before.\n", previous+1)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := file.WriteString(b.String()); err != nil {
		_ = file.Close()
		return fmt.Errorf("append to %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func orNone(v string) string {
	if v == "" {
		return "none (unfiltered)"
	}
	return v
}

const holdoutHeader = `# Holdout log

Every run against the holdout set (2025-01-01 onward) is appended here
automatically by ` + "`backtest --dataset=holdout`" + `.

**This is a mirror, not a lock.** Nothing here prevents the holdout being used
more than once — that cannot be enforced by software, and pretending otherwise
would be worse than not trying. What it does is make a second use visible,
including to the person making it.

The rule it records: the holdout is run **once**, at the end, on the single
strategy already chosen on the development set. If it fails, that is the
answer. Returning to development and retesting here spends the only thing this
set was worth.
`

// HoldoutEntryFor builds a log entry from a finished run.
func HoldoutEntryFor(result backtest.Result, stats Statistics, at time.Time, note string) HoldoutEntry {
	return HoldoutEntry{
		At:              at,
		StrategyName:    result.StrategyName,
		StrategyVersion: result.StrategyVersion,
		TrendFilter:     result.TrendFilterName,
		From:            result.Params.From,
		To:              result.Params.To,
		NetReturn:       stats.NetReturn,
		TradeCount:      stats.TradeCount,
		Note:            note,
	}
}
