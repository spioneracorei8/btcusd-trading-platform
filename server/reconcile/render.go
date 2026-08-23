package main

import (
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

// render prints the report for a person.
//
// # Why the banner comes first in every group
//
// It is the only line that decides whether the rest of them mean anything.
// Printed underneath the numbers it would be read after them, which is one
// paragraph too late — by then the reader has already formed a view about a
// win rate drawn from twenty trades.
func render(w io.Writer, report outcome.Reconciliation) {
	fmt.Fprintf(w, "RECONCILIATION  %s %s\n", report.Symbol, report.MarketType)
	fmt.Fprintf(w, "window          %s to %s\n",
		report.From.Format("2006-01-02"), report.To.Format("2006-01-02"))
	fmt.Fprintf(w, "generated       %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05Z"))

	if len(report.Groups) == 0 {
		fmt.Fprintf(w, "\nNo signals in this window. Nothing to compare.\n")
		return
	}

	for _, group := range report.Groups {
		renderGroup(w, group)
	}

	fmt.Fprintf(w, "\n%s\n", wrap(
		"There is no total across groups. Only like is compared with like: averaging "+
			"across a strategy version or a parameter change produces a number describing "+
			"nothing.", 78))
}

func renderGroup(w io.Writer, group outcome.ReconciledGroup) {
	fmt.Fprintf(w, "\n%s\n", strings.Repeat("=", 78))
	fmt.Fprintf(w, "%s %s\n", group.Strategy, group.Version)
	if len(group.Params) > 0 {
		fmt.Fprintf(w, "%s\n", paramLine(group.Params))
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("=", 78))

	if banner := outcome.SampleBanner(group.Sample); banner != "" {
		fmt.Fprintf(w, "\n%s\n", banner)
	}

	fmt.Fprintln(w)
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// The matched subset is what sits beside the engine, because it is the
	// only column comparable with it.
	compared := group.Live
	if group.Matched != nil {
		compared = *group.Matched
	}
	back := group.Backtest

	fmt.Fprintf(table, "\tLIVE (matched)\tBACKTEST\n")
	row(table, "signals", fmt.Sprint(compared.Signals), sideInt(back, func(s outcome.Side) int { return s.Signals }))
	row(table, "resolved", fmt.Sprint(compared.Resolved), sideInt(back, func(s outcome.Side) int { return s.Resolved }))
	row(table, "still open", fmt.Sprint(compared.StillOpen), "—")
	row(table, "invalidated (excluded)", fmt.Sprint(compared.Invalidated), "—")
	row(table, "win rate", rate(compared.WinRate), sideRate(back))
	row(table, "wins / losses",
		fmt.Sprintf("%d / %d", compared.Wins, compared.Losses),
		sideStr(back, func(s outcome.Side) string { return fmt.Sprintf("%d / %d", s.Wins, s.Losses) }))
	row(table, "average win %", compared.AverageWinPct.StringFixed(3),
		sideStr(back, func(s outcome.Side) string { return s.AverageWinPct.StringFixed(3) }))
	row(table, "average loss %", compared.AverageLossPct.StringFixed(3),
		sideStr(back, func(s outcome.Side) string { return s.AverageLossPct.StringFixed(3) }))
	row(table, "average entry", compared.AverageEntryPrice.StringFixed(2),
		sideStr(back, func(s outcome.Side) string { return s.AverageEntryPrice.StringFixed(2) }))
	row(table, "round-trip cost %", compared.AverageCostPct.StringFixed(4),
		sideStr(back, func(s outcome.Side) string { return s.AverageCostPct.StringFixed(4) }))
	row(table, "average bars held", compared.AverageBarsHeld.StringFixed(1),
		sideStr(back, func(s outcome.Side) string { return s.AverageBarsHeld.StringFixed(1) }))
	row(table, "rested on assumption", fmt.Sprint(compared.Noted),
		sideInt(back, func(s outcome.Side) int { return s.Noted }))
	table.Flush()

	renderSurplus(w, group)

	if group.Unavailable != "" {
		fmt.Fprintf(w, "\nNo backtest side: %s\n", wrap(group.Unavailable, 74))
	}

	// The realised cost is the modelled one, and saying so is the point.
	fmt.Fprintf(w, "\n%s\n", wrap(
		"Round-trip cost is modelled, not realised: this system places no orders, so "+
			"there is no executed cost to compare against. That row becomes a real "+
			"comparison only if execution is ever added.", 78))

	if len(group.Divergences) == 0 {
		return
	}
	fmt.Fprintf(w, "\nREADING\n")
	for _, d := range group.Divergences {
		fmt.Fprintf(w, "  %s\n", d.Symptom)
		fmt.Fprintf(w, "      likely cause: %s\n", wrapIndent(d.LikelyCause, 60, "                    "))
		fmt.Fprintf(w, "      %s\n", wrapIndent(d.Detail, 60, "      "))
	}
}

func row(w io.Writer, label, live, back string) {
	fmt.Fprintf(w, "%s\t%s\t%s\n", label, live, back)
}

func rate(value float64) string {
	if math.IsNaN(value) {
		// Not zero: a strategy that never wins and a strategy that has not
		// finished a trade are different claims.
		return "—"
	}
	return fmt.Sprintf("%.2f%%", value*100)
}

func sideRate(side *outcome.Side) string {
	if side == nil {
		return "—"
	}
	return rate(side.WinRate)
}

func sideInt(side *outcome.Side, pick func(outcome.Side) int) string {
	if side == nil {
		return "—"
	}
	return fmt.Sprint(pick(*side))
}

func sideStr(side *outcome.Side, pick func(outcome.Side) string) string {
	if side == nil {
		return "—"
	}
	return pick(*side)
}

func paramLine(params []outcome.ParamValue) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, p.Name+"="+p.Value)
	}
	return strings.Join(parts, "  ")
}

// wrap breaks a sentence at a width, so a terminal does not decide where the
// reasoning ends.
func wrap(text string, width int) string { return wrapIndent(text, width, "") }

func wrapIndent(text string, width int, indent string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var (
		out  strings.Builder
		line = words[0]
	)
	for _, word := range words[1:] {
		if len(line)+1+len(word) > width {
			out.WriteString(line + "\n" + indent)
			line = word
			continue
		}
		line += " " + word
	}
	out.WriteString(line)
	return out.String()
}

// renderSurplus reports the live signals the engine did not emit.
//
// Below the comparison and clearly apart from it: they have no counterpart,
// so putting them in the same column would invite exactly the arithmetic the
// split exists to prevent.
func renderSurplus(w io.Writer, group outcome.ReconciledGroup) {
	if group.Surplus == nil || group.Surplus.Signals == 0 {
		return
	}

	surplus := *group.Surplus
	fmt.Fprintf(w, "\nLIVE-ONLY (not compared)\n")

	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row(table, "  signals", fmt.Sprint(surplus.Signals), "")
	row(table, "  resolved", fmt.Sprint(surplus.Resolved), "")
	row(table, "  win rate", rate(surplus.WinRate), "")
	table.Flush()

	fmt.Fprintf(w, "  %s\n", wrapIndent(outcome.SurplusNote, 74, "  "))
}
