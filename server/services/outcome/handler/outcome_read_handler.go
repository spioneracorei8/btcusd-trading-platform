package handler

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

// Outcomes answers GET /api/v1/outcomes.
func (h *outcomeHandler) Outcomes(w http.ResponseWriter, r *http.Request) {
	if h.reader == nil {
		helper.WriteAPIError(w, h.logger, http.StatusServiceUnavailable,
			constants.APIErrUnavailable, "outcomes are not available on this process")
		return
	}

	limit, err := helper.QueryLimit(r, "limit", constants.APIPageLimitDefault, constants.APIPageLimit)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest, limitCode(err), err.Error())
		return
	}
	offset, err := helper.QueryOffset(r, "offset")
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}

	now := h.now()
	from, err := helper.QueryTime(r, "from", now.AddDate(-1, 0, 0))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	to, err := helper.QueryTime(r, "to", now)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}

	var status constants.OutcomeStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		parsed, err := constants.ParseOutcomeStatus(raw)
		if err != nil {
			helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
				constants.APIErrInvalidParameter, err.Error())
			return
		}
		status = parsed
	}

	resolved, total, err := h.reader.ListOutcomes(r.Context(), outcome.ListParams{
		Symbol: h.symbol, MarketType: h.marketType, Status: status,
		From: from, To: to, Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not list outcomes", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "the outcomes could not be read")
		return
	}

	items := make([]outcome.OutcomeResponse, 0, len(resolved))
	for _, row := range resolved {
		items = append(items, outcome.ToOutcomeResponse(row))
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, outcomesResponse{
		Symbol: h.symbol, MarketType: h.marketType.String(),
		From: from.UTC(), To: to.UTC(),
		Count: len(items), Total: total, Limit: limit, Offset: offset,
		Outcomes: items,
	})
}

// Performance answers GET /api/v1/performance.
//
// Live outcomes only: what the signals this system produced actually did, not
// what a backtest predicted. The comparison between the two is the
// reconciliation endpoint, which is a different question and a much more
// expensive one.
//
// Every figure carries the sample it was drawn from, and a sample below the
// threshold carries the banner. A win rate over nine trades and one over nine
// hundred must not be able to look alike.
func (h *outcomeHandler) Performance(w http.ResponseWriter, r *http.Request) {
	now := h.now()

	from, err := helper.QueryTime(r, "from", now.AddDate(-1, 0, 0))
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	to, err := helper.QueryTime(r, "to", now)
	if err != nil {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, err.Error())
		return
	}
	if to.Before(from) {
		helper.WriteAPIError(w, h.logger, http.StatusBadRequest,
			constants.APIErrInvalidParameter, "to is before from")
		return
	}

	// The reconciliation with its expensive half switched off: the live side
	// is exactly what performance is, and computing it twice in two places
	// would be two definitions of a win rate.
	report, err := h.usecase.Reconcile(r.Context(), outcome.ReconcileParams{
		Symbol: h.symbol, MarketType: h.marketType,
		From: from, To: to, SkipBacktest: true,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not compute performance", "error", err)
		helper.WriteAPIError(w, h.logger, http.StatusInternalServerError,
			constants.APIErrInternal, "performance could not be computed")
		return
	}

	groups := make([]performanceGroup, 0, len(report.Groups))
	for _, g := range report.Groups {
		groups = append(groups, toPerformanceGroup(g))
	}

	helper.WriteAPIJSON(w, h.logger, http.StatusOK, performanceResponse{
		Symbol: h.symbol, MarketType: h.marketType.String(),
		From: from.UTC(), To: to.UTC(), GeneratedAt: report.GeneratedAt.UTC(),
		Groups: groups,
		Note: "Grouped by strategy, version and resolved parameter set, with no total " +
			"across groups: averaging across a parameter change produces a number " +
			"describing nothing. Every figure is after modelled costs.",
	})
}

type outcomesResponse struct {
	Symbol     string    `json:"symbol"`
	MarketType string    `json:"market_type"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`

	Count  int   `json:"count"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`

	Outcomes []outcome.OutcomeResponse `json:"outcomes"`
}

type performanceResponse struct {
	Symbol      string    `json:"symbol"`
	MarketType  string    `json:"market_type"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GeneratedAt time.Time `json:"generated_at"`

	Groups []performanceGroup `json:"groups"`
	Note   string             `json:"note"`
}

type performanceGroup struct {
	Strategy string          `json:"strategy"`
	Version  string          `json:"version"`
	Params   []paramResponse `json:"params"`

	// Sample comes first because it decides whether anything below it means
	// anything.
	Sample sampleResponse `json:"sample"`

	Signals     int `json:"signals"`
	Resolved    int `json:"resolved"`
	StillOpen   int `json:"still_open"`
	Invalidated int `json:"invalidated_excluded"`

	Targets int `json:"targets"`
	Stops   int `json:"stops"`
	Expired int `json:"expired"`

	Wins   int `json:"wins"`
	Losses int `json:"losses"`

	// WinRate is null when nothing has resolved. Zero would read as a
	// strategy that never wins, which is a different claim.
	WinRate *float64 `json:"win_rate"`

	AverageWinPct  string `json:"average_win_pct"`
	AverageLossPct string `json:"average_loss_pct"`
	AverageCostPct string `json:"average_cost_pct"`

	// Expectancy is what one signal is worth on average, after costs. Null
	// when nothing has resolved.
	ExpectancyPct *string `json:"expectancy_pct"`

	// RestedOnAssumption counts resolutions that came from an assumption
	// rather than from the data.
	RestedOnAssumption int `json:"rested_on_assumption"`
}

func toPerformanceGroup(g outcome.ReconciledGroup) performanceGroup {
	params := make([]paramResponse, 0, len(g.Params))
	for _, p := range g.Params {
		params = append(params, paramResponse{Name: p.Name, Value: p.Value})
	}

	out := performanceGroup{
		Strategy: g.Strategy, Version: g.Version, Params: params,
		Sample:      toSampleResponse(g.Sample),
		Signals:     g.Live.Signals,
		Resolved:    g.Live.Resolved,
		StillOpen:   g.Live.StillOpen,
		Invalidated: g.Live.Invalidated,
		Targets:     g.Live.Targets,
		Stops:       g.Live.Stops,
		Expired:     g.Live.Expired,
		Wins:        g.Live.Wins,
		Losses:      g.Live.Losses,
		WinRate:     nullableRate(g.Live.WinRate),

		AverageWinPct:      g.Live.AverageWinPct.StringFixed(4),
		AverageLossPct:     g.Live.AverageLossPct.StringFixed(4),
		AverageCostPct:     g.Live.AverageCostPct.StringFixed(4),
		RestedOnAssumption: g.Live.Noted,
	}
	out.ExpectancyPct = expectancy(g.Live)
	return out
}

// expectancy is what one signal is worth on average, after costs.
//
// win rate x average win + loss rate x average loss. It is the number that
// decides whether a strategy is worth running, and it is not derivable from a
// win rate alone — a 30% win rate with a 3:1 payoff beats a 60% one with 1:2.
func expectancy(side outcome.Side) *string {
	if side.Resolved == 0 || math.IsNaN(side.WinRate) {
		return nil
	}

	winRate := decimal.NewFromFloat(side.WinRate)
	lossRate := decimal.NewFromInt(1).Sub(winRate)

	value := winRate.Mul(side.AverageWinPct).
		Add(lossRate.Mul(side.AverageLossPct)).StringFixed(4)
	return &value
}

// limitCode separates "too many" from "not a number".
func limitCode(err error) constants.APIErrorCode {
	if err != nil && strings.Contains(err.Error(), "above the maximum") {
		return constants.APIErrLimitExceeded
	}
	return constants.APIErrInvalidParameter
}
