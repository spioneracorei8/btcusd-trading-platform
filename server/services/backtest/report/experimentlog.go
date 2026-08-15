package report

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// ExperimentLogPath is where every evaluation run is recorded.
const ExperimentLogPath = "docs/experiments.md"

// ExperimentEntry is one run's line in the log.
//
// # Why this is written by the tool and not by hand
//
// The log's value depends entirely on being complete: a strategy chosen out of
// fifty has fifty chances to look good by accident, and only the log records
// the denominator. Written by hand it was already incomplete — seven runs
// reconstructed afterwards from scrollback, in one batch.
//
// The runs that get skipped are never the interesting ones. They are the ones
// abandoned halfway, and the ones whose result was disappointing enough to
// move on from quickly. Those are exactly the entries that make the
// denominator honest, which is why recording them cannot be left to whoever
// has just been disappointed.
type ExperimentEntry struct {
	Number int
	At     time.Time

	StrategyName    string
	StrategyVersion string

	Dataset   string
	From      To
	Timeframe string
	GapPolicy string

	// FilterName is empty for an unfiltered run. FilterConfig is the resolved
	// contributor set — after the base-aware selection and any drop — because
	// that is what actually gated the run.
	FilterName    string
	FilterVersion string
	FilterConfig  string
	VetoedPct     float64
	NotReadyPct   float64

	Sizing string

	NetReturn      float64
	ProfitFactor   float64
	MaxDrawdown    float64
	TradeCount     int
	CostShare      float64
	CostShareKnown bool
	Costs          string
	GrossProfit    string
	Concentration  float64

	// Comparison is present when the run was a --compare, so one entry can
	// carry what two separate runs used to record separately.
	Comparison *ComparisonLine

	// CostSweep is empty unless --cost-sweep ran. It matters more than most
	// lines here: costs are the dominant variable in this system, so how fast
	// an edge decays as they rise is closer to the real question than the
	// headline number is.
	CostSweep []CostSensitivity

	Verdict Verdict

	// Suppressed marks an entry written for a run whose details were withheld
	// with --no-experiment-log.
	Suppressed bool
}

// ComparisonLine is the filtered-versus-unfiltered result of one --compare.
//
// It exists so that --compare and --cost-sweep together produce a single
// entry. Running them as two invocations recorded the same configuration twice
// and inflated the log's denominator by roughly a factor of two — and the
// denominator is the only thing that says how much weight a result deserves.
type ComparisonLine struct {
	UnfilteredNetReturn float64
	UnfilteredTrades    int
	FilteredNetReturn   float64
	FilteredTrades      int
}

// To is the range a run covered, kept as one field so the formatter cannot
// print a start without its end.
type To struct {
	Start time.Time
	End   time.Time
}

// entryHeading matches "### 8. 2026-08-15 — ema_crossover v1" and the
// unnumbered form the first seven entries use.
var entryHeading = regexp.MustCompile(`^###\s+(?:([0-9]+)\.\s+)?[0-9]{4}-[0-9]{2}-[0-9]{2}\s+—`)

// NextExperimentNumber works out what to number the next entry.
//
// It counts the entries already present and takes the highest explicit number,
// so the seven hand-written unnumbered entries are counted and the eighth is
// numbered 8. A count that has to be derived by scrolling is a count nobody
// checks, which defeats the purpose of the file.
func NextExperimentNumber(content string) int {
	highest, count := 0, 0

	// Only headings below the Entries section count. The format example near
	// the top of the file is inside a fence and must not be mistaken for one.
	body := content
	if index := strings.LastIndex(content, "\n## Entries"); index >= 0 {
		body = content[index:]
	}

	for _, line := range strings.Split(body, "\n") {
		match := entryHeading.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		count++
		if match[1] != "" {
			if number, err := strconv.Atoi(match[1]); err == nil && number > highest {
				highest = number
			}
		}
	}

	if highest > count {
		return highest + 1
	}
	return count + 1
}

// AppendExperiment adds one entry to the log, creating it if absent, and
// returns the number it was given.
func AppendExperiment(path string, entry ExperimentEntry) (int, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("create the experiment log directory: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read the experiment log: %w", err)
	}

	var b strings.Builder
	if len(existing) == 0 {
		b.WriteString(experimentHeader)
	}
	if entry.Number == 0 {
		entry.Number = NextExperimentNumber(string(existing))
	}
	b.WriteString(renderExperiment(entry))

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := file.WriteString(b.String()); err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("append to %s: %w", path, err)
	}
	// Closed explicitly rather than deferred: a write that fails on flush is a
	// truncated entry, and an entry that is silently half-written is worse for
	// this file than one that is missing.
	if err := file.Close(); err != nil {
		return 0, fmt.Errorf("close %s: %w", path, err)
	}
	return entry.Number, nil
}

const experimentHeader = `# Experiment log

Appended automatically after every evaluation run.

## Entries
`

// renderExperiment formats one entry in the shape the hand-written ones use.
func renderExperiment(entry ExperimentEntry) string {
	var b strings.Builder

	label := entry.StrategyName
	if entry.Suppressed {
		label = "suppressed"
	}

	fmt.Fprintf(&b, "\n### %d. %s — %s", entry.Number,
		entry.At.UTC().Format("2006-01-02"), label)
	if !entry.Suppressed {
		fmt.Fprintf(&b, " %s (%s)", entry.StrategyVersion, entry.Timeframe)
	}
	b.WriteString("\n\n")

	if entry.Suppressed {
		// The number is still spent. A run that happened and was not written
		// up is exactly the kind the denominator needs to include, so the gap
		// is made visible rather than silent.
		b.WriteString("- **Suppressed:** details withheld with `--no-experiment-log`. " +
			"The number is spent so the count of runs stays honest.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "- **Dataset:** %s (%s .. %s), `--allow-gaps=%s`\n",
		entry.Dataset,
		entry.From.Start.UTC().Format("2006-01-02"),
		entry.From.End.UTC().Format("2006-01-02"),
		entry.GapPolicy)
	fmt.Fprintf(&b, "- **Parameters:** defaults, timeframe %s\n", entry.Timeframe)

	if entry.FilterName == "" {
		b.WriteString("- **Filter:** none (unfiltered)\n")
	} else {
		fmt.Fprintf(&b, "- **Filter:** %s %s (%s) — vetoed %.2f%%, not-ready %.2f%%\n",
			entry.FilterName, entry.FilterVersion, entry.FilterConfig,
			entry.VetoedPct, entry.NotReadyPct)
	}

	fmt.Fprintf(&b, "- **Sizing:** %s\n", entry.Sizing)
	fmt.Fprintf(&b, "- **Net return after costs:** %+.4f%%\n", entry.NetReturn*100)
	fmt.Fprintf(&b, "- **Profit factor / max drawdown / trades:** %s / %.2f%% / %s\n",
		formatProfitFactor(entry.ProfitFactor), entry.MaxDrawdown*100,
		thousands(entry.TradeCount))

	if entry.CostShareKnown {
		fmt.Fprintf(&b, "- **Costs as share of gross profit:** %.0f%% (costs %s vs gross profit %s)\n",
			entry.CostShare*100, entry.Costs, entry.GrossProfit)
	} else {
		fmt.Fprintf(&b, "- **Costs as share of gross profit:** n/a — gross is not positive (costs %s)\n",
			entry.Costs)
	}

	fmt.Fprintf(&b, "- **Concentration (best 5):** %.2f%%\n", entry.Concentration*100)

	if entry.Comparison != nil {
		fmt.Fprintf(&b, "- **Filter comparison:** unfiltered %+.2f%% (%s trades) | "+
			"filtered %+.2f%% (%s trades)\n",
			entry.Comparison.UnfilteredNetReturn*100, thousands(entry.Comparison.UnfilteredTrades),
			entry.Comparison.FilteredNetReturn*100, thousands(entry.Comparison.FilteredTrades))
	}

	if len(entry.CostSweep) > 0 {
		b.WriteString("- **Cost sweep:** ")
		for i, pass := range entry.CostSweep {
			if i > 0 {
				b.WriteString(" | ")
			}
			fmt.Fprintf(&b, "%.1fx %+.2f%% (PF %s)",
				pass.Multiplier, pass.NetReturn*100, formatProfitFactor(pass.ProfitFactor))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "- **Verdict:** %s\n", entry.Verdict.String())
	// Deliberately left for a human. The note is a judgement about what was
	// learned, and a generated sentence there would be a guess occupying the
	// one line that is supposed to carry thought.
	b.WriteString("- **Note:** _(to fill in — what you learned, not what you hoped)_\n")

	return b.String()
}

// formatProfitFactor keeps an undefined profit factor legible.
//
// The three cases are genuinely different and were previously conflated:
//
//   - NaN means there were no trades, so there is no ratio. It read as "inf"
//     here, which in a permanent log invites being taken months later for an
//     extraordinary result rather than an empty set. That is what the 4h runs
//     recorded.
//   - +Inf means trades happened and none of them lost. Rare, real, and worth
//     distinguishing from the above.
//   - Zero means gross profit was zero against real losses. That is a result,
//     not a missing value, and it was previously printed as "n/a".
func formatProfitFactor(value float64) string {
	switch {
	case math.IsNaN(value):
		return "n/a (no trades)"
	case math.IsInf(value, 1):
		return "inf (no losing trades)"
	case math.IsInf(value, -1):
		return "-inf"
	default:
		return strconv.FormatFloat(value, 'f', 2, 64)
	}
}

// thousands groups an integer, matching the hand-written entries.
func thousands(value int) string {
	digits := strconv.Itoa(value)
	if len(digits) <= 3 {
		return digits
	}

	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// ExperimentEntryFor assembles the entry from a finished run.
func ExperimentEntryFor(
	result backtest.Result,
	stats Statistics,
	analysis Analysis,
	criteria Criteria,
	criteriaErr error,
	dataset string,
	at time.Time,
	sweep []CostSensitivity,
) ExperimentEntry {
	entry := ExperimentEntry{
		At:              at,
		StrategyName:    result.StrategyName,
		StrategyVersion: result.StrategyVersion,
		Dataset:         dataset,
		From:            To{Start: result.Params.From, End: result.Params.To},
		Timeframe:       result.Params.Timeframe.String(),
		GapPolicy:       result.Params.GapPolicy.String(),
		FilterName:      result.TrendFilterName,
		FilterVersion:   result.TrendFilterVersion,
		FilterConfig:    result.TrendFilterConfig,
		Sizing:          describeSizing(result.Params.Sizing),
		NetReturn:       stats.NetReturn,
		ProfitFactor:    stats.ProfitFactor,
		MaxDrawdown:     stats.MaxDrawdown.Percent,
		TradeCount:      stats.TradeCount,
		Costs:           stats.TotalCosts.StringFixed(0),
		GrossProfit:     stats.TotalGrossPnL.StringFixed(0),
		Concentration:   analysis.Concentration.Share,
		CostSweep:       sweep,
	}

	if result.BarsEvaluated > 0 {
		entry.VetoedPct = float64(result.BarsVetoed) / float64(result.BarsEvaluated) * 100
		entry.NotReadyPct = float64(result.BarsFilterNotReady) / float64(result.BarsEvaluated) * 100
	}

	if stats.TotalGrossPnL.IsPositive() {
		share, _ := stats.TotalCosts.Div(stats.TotalGrossPnL).Float64()
		entry.CostShare = share
		entry.CostShareKnown = true
	}

	if criteriaErr != nil {
		entry.Verdict = Verdict{Reason: "criteria file unreadable"}
	} else {
		entry.Verdict = criteria.Judge(stats, analysis)
	}
	return entry
}

// describeSizing renders the sizing mode for the log.
func describeSizing(sizing backtest.Sizing) string {
	if sizing.Mode == backtest.SizingAllIn {
		return "all-in"
	}
	return fmt.Sprintf("risk %s%% of equity", sizing.RiskPct.String())
}
