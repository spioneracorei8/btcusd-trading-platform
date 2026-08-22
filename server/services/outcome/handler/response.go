package handler

import (
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

// The JSON shape of GET /internal/signals/reconciliation.
//
// A response type separate from the model keeps the wire format stable while
// the internals move, and lets a rate that was never computed be rendered as
// null rather than as a zero somebody would read as a real answer.

type reconciliationResponse struct {
	Symbol     string `json:"symbol"`
	MarketType string `json:"market_type"`

	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GeneratedAt time.Time `json:"generated_at"`

	// Groups are one per strategy, version and resolved parameter set. There
	// is deliberately no total across them: averaging across a parameter
	// change produces a number describing nothing.
	Groups []groupResponse `json:"groups"`

	// Note says so, so the absence of a total reads as a decision rather than
	// an omission.
	Note string `json:"note"`
}

type groupResponse struct {
	Strategy string          `json:"strategy"`
	Version  string          `json:"version"`
	Params   []paramResponse `json:"params"`

	Live     sideResponse  `json:"live"`
	Backtest *sideResponse `json:"backtest"`

	// Unavailable says why there is no backtest side. A report that quietly
	// dropped the comparison would look like one that found no divergence.
	Unavailable string `json:"backtest_unavailable,omitempty"`

	Sample      sampleResponse       `json:"sample"`
	Divergences []divergenceResponse `json:"divergences"`
}

type paramResponse struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type sideResponse struct {
	Signals     int `json:"signals"`
	StillOpen   int `json:"still_open"`
	Invalidated int `json:"invalidated"`
	Resolved    int `json:"resolved"`

	Targets int `json:"targets"`
	Stops   int `json:"stops"`
	Expired int `json:"expired"`

	// Noted counts resolutions that rested on an assumption rather than on
	// the data.
	Noted int `json:"rested_on_assumption"`

	Wins   int `json:"wins"`
	Losses int `json:"losses"`

	// WinRate is null when nothing has resolved.
	WinRate *float64 `json:"win_rate"`

	AverageWinPct  string `json:"average_win_pct"`
	AverageLossPct string `json:"average_loss_pct"`
	AverageCostPct string `json:"average_cost_pct"`

	AverageEntryPrice string `json:"average_entry_price"`
	AverageBarsHeld   string `json:"average_bars_held"`

	First *time.Time `json:"first_signal"`
	Last  *time.Time `json:"last_signal"`
}

type sampleResponse struct {
	Resolved   int  `json:"resolved"`
	Required   int  `json:"required"`
	Sufficient bool `json:"sufficient"`

	// Banner is the sentence to print when the sample is too small, including
	// the expected wait. It is built here rather than left to the caller so
	// the API and the CLI cannot word it differently.
	Banner string `json:"banner,omitempty"`

	PerDay       *float64 `json:"resolved_per_day"`
	ExpectedWait string   `json:"expected_wait,omitempty"`
}

type divergenceResponse struct {
	Symptom     string `json:"symptom"`
	LikelyCause string `json:"likely_cause"`
	Detail      string `json:"detail"`
}

// toResponse renders the report.
func toResponse(report outcome.Reconciliation) reconciliationResponse {
	groups := make([]groupResponse, 0, len(report.Groups))
	for _, g := range report.Groups {
		groups = append(groups, toGroupResponse(g))
	}

	return reconciliationResponse{
		Symbol:      report.Symbol,
		MarketType:  report.MarketType.String(),
		From:        report.From,
		To:          report.To,
		GeneratedAt: report.GeneratedAt,
		Groups:      groups,
		Note: "There is no total across groups. Only like is compared with like: " +
			"averaging across a strategy version or a parameter change produces a " +
			"number describing nothing.",
	}
}

func toGroupResponse(g outcome.ReconciledGroup) groupResponse {
	params := make([]paramResponse, 0, len(g.Params))
	for _, p := range g.Params {
		params = append(params, paramResponse{Name: p.Name, Value: p.Value})
	}

	divergences := make([]divergenceResponse, 0, len(g.Divergences))
	for _, d := range g.Divergences {
		divergences = append(divergences, divergenceResponse{
			Symptom: d.Symptom, LikelyCause: d.LikelyCause, Detail: d.Detail,
		})
	}

	out := groupResponse{
		Strategy:    g.Strategy,
		Version:     g.Version,
		Params:      params,
		Live:        toSideResponse(g.Live),
		Unavailable: g.Unavailable,
		Sample:      toSampleResponse(g.Sample),
		Divergences: divergences,
	}
	if g.Backtest != nil {
		side := toSideResponse(*g.Backtest)
		out.Backtest = &side
	}
	return out
}

func toSideResponse(s outcome.Side) sideResponse {
	out := sideResponse{
		Signals:           s.Signals,
		StillOpen:         s.StillOpen,
		Invalidated:       s.Invalidated,
		Resolved:          s.Resolved,
		Targets:           s.Targets,
		Stops:             s.Stops,
		Expired:           s.Expired,
		Noted:             s.Noted,
		Wins:              s.Wins,
		Losses:            s.Losses,
		WinRate:           nullableRate(s.WinRate),
		AverageWinPct:     s.AverageWinPct.StringFixed(4),
		AverageLossPct:    s.AverageLossPct.StringFixed(4),
		AverageCostPct:    s.AverageCostPct.StringFixed(4),
		AverageEntryPrice: s.AverageEntryPrice.StringFixed(2),
		AverageBarsHeld:   s.AverageBarsHeld.StringFixed(2),
	}
	if !s.First.IsZero() {
		first := s.First.UTC()
		out.First = &first
	}
	if !s.Last.IsZero() {
		last := s.Last.UTC()
		out.Last = &last
	}
	return out
}

func toSampleResponse(s outcome.SampleAdequacy) sampleResponse {
	out := sampleResponse{
		Resolved:   s.Resolved,
		Required:   s.Required,
		Sufficient: s.Sufficient,
		Banner:     outcome.SampleBanner(s),
	}
	if s.Known {
		perDay := s.PerDay
		out.PerDay = &perDay
		out.ExpectedWait = outcome.HumanDuration(s.Wait)
	}
	return out
}
