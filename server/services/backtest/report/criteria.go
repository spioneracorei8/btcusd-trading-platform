package report

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// CriteriaPath is where the pre-registered acceptance criteria live.
const CriteriaPath = "docs/acceptance-criteria.md"

// Criterion is one pre-registered threshold.
type Criterion struct {
	// Key identifies which measurement it applies to.
	Key string

	// Label is the criterion's own words, used when reporting a failure so
	// the log says what was missed rather than which array index missed.
	Label string

	// Comparator is one of >, <, >=, <=.
	Comparator string

	// Threshold is the number, already converted to the units the measurement
	// uses — a percentage in the file becomes a fraction here, because that is
	// what Statistics holds.
	Threshold float64
}

// Criteria is the parsed acceptance file.
type Criteria []Criterion

// Verdict is the result of judging one run.
type Verdict struct {
	// Evaluated is false when the criteria could not be read. A verdict that
	// guessed would be worse than none: a wrong pass in a permanent log is
	// the one failure this whole apparatus exists to prevent.
	Evaluated bool
	Reason    string

	Passed []string
	Failed []string
}

// String renders the verdict for the experiment log.
func (v Verdict) String() string {
	if !v.Evaluated {
		return "not evaluated (" + v.Reason + ")"
	}
	if len(v.Failed) == 0 {
		return "pass — all criteria"
	}
	return "fail — " + strings.Join(v.Failed, ", ")
}

// criterionKeys maps the wording in the criteria file to a measurement.
//
// Ordered, and matched first-wins, because the phrases overlap in both
// directions and the order is the only thing resolving them:
//
//   - "Concentration: profit from the best 5 trades" contains "trades", so it
//     must be matched before the trade-count criterion
//   - "Net return after costs" contains "costs", so it must be matched before
//     the cost-share criterion
//
// The second one is not hypothetical. With "costs" listed first, the net
// return row was filed as cost share, the real cost row was then discarded as
// a duplicate, and the file failed the completeness check — reported as
// "criteria file unreadable" on a file that was perfectly readable.
var criterionKeys = []struct {
	Key      string
	Contains string
}{
	{"concentration", "concentration"},
	{"losing_streak", "losing streak"},
	{"net_return", "net return"},
	{"profit_factor", "profit factor"},
	{"max_drawdown", "drawdown"},
	{"cost_share", "costs"},
	{"trade_count", "trades"},
}

// fractionKeys are measurements Statistics holds as fractions, so a percentage
// threshold in the file has to be divided by 100 before it can be compared.
var fractionKeys = map[string]bool{
	"net_return": true, "max_drawdown": true,
	"cost_share": true, "concentration": true,
}

// thresholdPattern matches "**> 1.3**", "< 20%", "≥ 200", "< 50% of total".
var thresholdPattern = regexp.MustCompile(`^(>=|<=|≥|≤|>|<)\s*([0-9]+(?:\.[0-9]+)?)\s*(%?)`)

// LoadCriteria reads the pre-registered thresholds.
//
// It parses the file rather than hardcoding the numbers so that changing a
// threshold changes the verdict — which is the only thing that makes the file
// authoritative rather than decorative. Editing it is meant to be a commit of
// its own, made before the run it judges.
func LoadCriteria(path string) (Criteria, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read acceptance criteria: %w", err)
	}

	var criteria Criteria
	seen := map[string]bool{}

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}

		columns := strings.Split(strings.Trim(line, "|"), "|")
		if len(columns) < 3 {
			continue
		}

		label := clean(columns[1])
		threshold := clean(columns[2])
		if label == "" || threshold == "" || strings.EqualFold(label, "criterion") {
			continue
		}

		match := thresholdPattern.FindStringSubmatch(threshold)
		if match == nil {
			continue
		}

		key := ""
		lowered := strings.ToLower(label)
		for _, candidate := range criterionKeys {
			if strings.Contains(lowered, candidate.Contains) {
				key = candidate.Key
				break
			}
		}
		if key == "" || seen[key] {
			continue
		}

		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			continue
		}
		if match[3] == "%" && fractionKeys[key] {
			value /= 100
		}

		comparator := match[1]
		switch comparator {
		case "≥":
			comparator = ">="
		case "≤":
			comparator = "<="
		}

		seen[key] = true
		criteria = append(criteria, Criterion{
			Key: key, Label: label, Comparator: comparator, Threshold: value,
		})
	}

	// Every criterion the file is supposed to carry must be present. A file
	// that parsed to four of seven would produce a confident verdict from an
	// incomplete test, which is the failure mode this returns an error for.
	for _, required := range criterionKeys {
		if !seen[required.Key] {
			return nil, fmt.Errorf(
				"acceptance criteria: no threshold found for %q in %s", required.Key, path)
		}
	}
	return criteria, nil
}

// clean strips markdown emphasis and whitespace from a table cell.
func clean(cell string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(cell), "*", ""))
}

// Judge measures a finished run against the criteria.
func (c Criteria) Judge(stats Statistics, analysis Analysis) Verdict {
	if len(c) == 0 {
		return Verdict{Reason: "criteria file unreadable"}
	}

	verdict := Verdict{Evaluated: true}

	for _, criterion := range c {
		value, ok := measure(criterion.Key, stats, analysis)
		if !ok {
			// The measurement could not be taken — gross profit of zero, for
			// instance. Not a pass.
			verdict.Failed = append(verdict.Failed,
				fmt.Sprintf("%s (not measurable)", criterion.Label))
			continue
		}

		if satisfies(value, criterion.Comparator, criterion.Threshold) {
			verdict.Passed = append(verdict.Passed, criterion.Label)
			continue
		}
		verdict.Failed = append(verdict.Failed,
			fmt.Sprintf("%s (%s, needs %s %s)",
				criterion.Label, formatValue(criterion.Key, value),
				criterion.Comparator, formatValue(criterion.Key, criterion.Threshold)))
	}
	return verdict
}

// measure pulls one value out of a finished run.
func measure(key string, stats Statistics, analysis Analysis) (float64, bool) {
	switch key {
	case "net_return":
		return stats.NetReturn, true
	case "profit_factor":
		if math.IsInf(stats.ProfitFactor, 0) || math.IsNaN(stats.ProfitFactor) {
			return 0, false
		}
		return stats.ProfitFactor, true
	case "max_drawdown":
		// Held as a negative fraction; the criterion is about its size.
		return math.Abs(stats.MaxDrawdown.Percent), true
	case "trade_count":
		return float64(stats.TradeCount), true
	case "cost_share":
		// Gross profit at or below zero means there was no profit for costs to
		// be a share of. That is not a pass — it is a strategy with no edge at
		// all, and reporting 0% would read as the best possible result.
		if !stats.TotalGrossPnL.IsPositive() {
			return 0, false
		}
		costs, _ := stats.TotalCosts.Div(stats.TotalGrossPnL).Float64()
		return costs, true
	case "losing_streak":
		return float64(stats.LongestLosingStreak), true
	case "concentration":
		return analysis.Concentration.Share, true
	}
	return 0, false
}

// satisfies applies one comparator.
func satisfies(value float64, comparator string, threshold float64) bool {
	switch comparator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	}
	return false
}

// formatValue renders a measurement in the units a reader expects.
func formatValue(key string, value float64) string {
	switch key {
	case "trade_count", "losing_streak":
		return strconv.FormatFloat(value, 'f', 0, 64)
	case "profit_factor":
		return strconv.FormatFloat(value, 'f', 2, 64)
	default:
		return strconv.FormatFloat(value*100, 'f', 2, 64) + "%"
	}
}
