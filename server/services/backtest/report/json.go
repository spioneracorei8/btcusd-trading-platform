package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// # Determinism
//
// Two runs over the same inputs must produce byte-identical JSON, which is
// what makes "did my change alter the result" answerable with diff instead of
// with judgement.
//
// Three things would break it and are therefore absent by construction:
//
//   - No map anywhere in these types. Go randomises map iteration, so a map
//     would reorder keys between runs.
//   - No timestamp of when the run happened. A generated_at field would
//     differ on every run by definition; the run's own date range is what
//     identifies it, and the strategy name and version say which code
//     produced it.
//   - Money is emitted as a string, not a float. A JSON number would go
//     through float64 and could round differently once the numbers grow.
//
// Floats that remain are genuine statistics, and NaN and infinity are
// rendered as null rather than as the invalid tokens encoding/json would
// otherwise refuse.

// Document is the JSON form of a finished run.
type Document struct {
	// DataIncomplete is first so the stamp cannot be missed by anything
	// reading the head of the file.
	DataIncomplete bool   `json:"data_incomplete"`
	Stamp          string `json:"stamp,omitempty"`

	Strategy    strategyDoc    `json:"strategy"`
	TrendFilter trendFilterDoc `json:"trend_filter"`
	Run         runDoc         `json:"run"`
	Costs       costsDoc       `json:"costs"`
	Bars        barsDoc        `json:"bars"`
	Performance performanceDoc `json:"performance"`
	Risk        riskDoc        `json:"risk"`
	TradeStats  tradeStatsDoc  `json:"trade_stats"`

	Trades             []tradeDoc  `json:"trades"`
	Equity             []equityDoc `json:"equity_curve"`
	UnfilledGaps       []gapDoc    `json:"unfilled_gaps"`
	UntradeableWindows []outageDoc `json:"untradeable_windows"`
}

type strategyDoc struct {
	Name    string `json:"name"`
	Version string `json:"version"`

	// Parameters is empty when the run used documented defaults. It carries
	// only what differs, because listing every parameter would bury the one
	// fact a reader is looking for.
	Parameters []paramDoc `json:"parameters"`
}

// paramDoc is one parameter that differed from its default.
type paramDoc struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Default string `json:"default"`
}

// paramDocs converts the changes for the document.
func paramDocs(changes []helper.ParamChange) []paramDoc {
	out := make([]paramDoc, 0, len(changes))
	for _, change := range changes {
		out = append(out, paramDoc{Name: change.Name, Value: change.To, Default: change.From})
	}
	return out
}

// trendFilterDoc records what vetoed the run. Enabled is explicit rather than
// implied by an empty name: "no filter" and "a filter that reported nothing"
// are different findings and must not read the same.
type trendFilterDoc struct {
	Enabled       bool       `json:"enabled"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Configuration string     `json:"configuration"`
	Parameters    []paramDoc `json:"parameters"`
}

type runDoc struct {
	Symbol     string `json:"symbol"`
	MarketType string `json:"market_type"`
	Timeframe  string `json:"timeframe"`

	RequestedFrom string `json:"requested_from"`
	RequestedTo   string `json:"requested_to"`
	EvaluatedFrom string `json:"evaluated_from"`
	EvaluatedTo   string `json:"evaluated_to"`

	GapPolicy     string `json:"gap_policy"`
	InitialEquity string `json:"initial_equity"`

	// Sizing, recorded because the notional limit decides position size just
	// as surely as the risk setting does.
	SizingMode  string `json:"sizing_mode"`
	RiskPct     string `json:"risk_pct"`
	MaxLeverage string `json:"max_leverage"`

	// The engine-enforced exits. Zero is the fixed stop and fixed target every
	// evaluation before phase 06 ran with.
	TrailingATRMult     float64 `json:"trailing_atr_mult"`
	TrailingActivateATR float64 `json:"trailing_activate_atr"`
}

type costsDoc struct {
	FeeTakerPct string `json:"fee_taker_pct"`
	FeeMakerPct string `json:"fee_maker_pct"`

	// The venue model. Recorded because neither fee rate says what a fill
	// cost once the charge can be a spread in price points instead.
	CostModel        string `json:"cost_model"`
	SpreadPoints     int    `json:"spread_points"`
	SpreadPrice      string `json:"spread_price"`
	CommissionPerLot string `json:"commission_per_lot"`

	// EntryOrderType and ExitOrderType are recorded because the fee rates
	// alone no longer say what a fill cost.
	EntryOrderType   string  `json:"entry_order_type"`
	ExitOrderType    string  `json:"exit_order_type"`
	LimitTimeoutBars int     `json:"limit_timeout_bars"`
	SlippageTicks    int     `json:"slippage_ticks"`
	TickSize         string  `json:"tick_size"`
	TotalPaid        string  `json:"total_paid"`
	PerRoundTripBps  float64 `json:"per_round_trip_bps"`
}

type barsDoc struct {
	Evaluated     int64 `json:"evaluated"`
	SkippedWarmup int64 `json:"skipped_warmup"`
	SkippedGap    int64 `json:"skipped_gap"`

	// StopAndTargetBothReachable is the count of bars resolved by the
	// stop-first assumption rather than by evidence.
	StopAndTargetBothReachable int64 `json:"stop_and_target_both_reachable"`

	// Vetoed is how many bars the trend filter blocked an entry on, and
	// FilterNotReady how many it had no answer for. Kept apart: "blocked on
	// purpose" and "could not say" are different findings.
	Vetoed         int64 `json:"vetoed"`
	FilterNotReady int64 `json:"filter_not_ready"`

	// TrailAmbiguous counts bars where the trailing stop would have both
	// extended and triggered, and the trigger was assumed.
	TrailAmbiguous int64 `json:"trail_ambiguous"`
}

// exitReasonDoc is how one kind of exit performed.
type exitReasonDoc struct {
	Reason     string `json:"reason"`
	Count      int    `json:"count"`
	Wins       int    `json:"wins"`
	AverageNet string `json:"average_net"`
	AverageWin string `json:"average_win"`
	TotalNet   string `json:"total_net"`
}

// exitReasonDocs converts the breakdown for the document.
func exitReasonDocs(groups []ExitBreakdown) []exitReasonDoc {
	out := make([]exitReasonDoc, 0, len(groups))
	for _, group := range groups {
		out = append(out, exitReasonDoc{
			Reason:     string(group.Reason),
			Count:      group.Count,
			Wins:       group.Wins,
			AverageNet: group.AverageNet.String(),
			AverageWin: group.AverageWin.String(),
			TotalNet:   group.TotalNet.String(),
		})
	}
	return out
}

type performanceDoc struct {
	// NetReturn is first: it is the only figure that survived costs.
	NetReturn   float64 `json:"net_return"`
	NetPnL      string  `json:"net_pnl"`
	TotalCosts  string  `json:"total_costs"`
	GrossReturn float64 `json:"gross_return"`
	GrossPnL    string  `json:"gross_pnl"`

	// CostShareOfGross is null when the run made no gross profit, for the same
	// reason Sharpe is: a share of nothing is undefined, and zero would be
	// read as "costs were negligible".
	CostShareOfGross *float64 `json:"cost_share_of_gross_profit"`

	FinalEquity string `json:"final_equity"`
}

type riskDoc struct {
	MaxDrawdownPercent  float64  `json:"max_drawdown_percent"`
	MaxDrawdownAbsolute string   `json:"max_drawdown_absolute"`
	MaxDrawdownPeakAt   string   `json:"max_drawdown_peak_at"`
	MaxDrawdownTroughAt string   `json:"max_drawdown_trough_at"`
	Sharpe              *float64 `json:"sharpe"`
	RiskFreeRate        float64  `json:"risk_free_rate"`
	AnnualisationBars   float64  `json:"annualisation_bars_per_year"`
}

type tradeStatsDoc struct {
	Count               int      `json:"count"`
	Wins                int      `json:"wins"`
	Losses              int      `json:"losses"`
	WinRate             float64  `json:"win_rate"`
	ProfitFactor        *float64 `json:"profit_factor"`
	AverageWin          string   `json:"average_win"`
	AverageLoss         string   `json:"average_loss"`
	LargestWin          string   `json:"largest_win"`
	LargestLoss         string   `json:"largest_loss"`
	AverageHoldSeconds  int64    `json:"average_hold_seconds"`
	LongestLosingStreak int      `json:"longest_losing_streak"`
	TradesPerDay        float64  `json:"trades_per_day"`

	// Fills by how they reached the book, and the signals that never became
	// trades at all. The last one is the number to read first under a limit
	// entry model: a large share means the statistics above describe a subset
	// of the strategy's intent rather than the strategy.
	MakerEntries           int64   `json:"maker_entries"`
	TakerEntries           int64   `json:"taker_entries"`
	MakerExits             int64   `json:"maker_exits"`
	TakerExits             int64   `json:"taker_exits"`
	EntriesRequested       int64   `json:"entries_requested"`
	EntriesBelowMinLot     int64   `json:"entries_below_min_lot"`
	EntriesRefusedAfterCap int64   `json:"entries_refused_after_notional_cap"`
	AverageCostPerTrade    string  `json:"average_cost_per_trade"`
	AverageRisk            string  `json:"average_risk"`
	WorstRisk              string  `json:"worst_risk"`
	AverageRiskPct         float64 `json:"average_risk_pct_of_balance"`
	WorstRiskPct           float64 `json:"worst_risk_pct_of_balance"`
	LimitOrdersExpired     int64   `json:"limit_orders_expired"`
	CancelledPercent       float64 `json:"limit_orders_cancelled_percent"`

	// ByExitReason is how each kind of exit performed. It is the only way to
	// tell whether an exit mechanism is doing anything or merely adding a code
	// path.
	ByExitReason []exitReasonDoc `json:"by_exit_reason"`
}

type tradeDoc struct {
	Direction string `json:"direction"`

	EntryTime  string `json:"entry_time"`
	EntryPrice string `json:"entry_price"`
	ExitTime   string `json:"exit_time"`
	ExitPrice  string `json:"exit_price"`
	Size       string `json:"size"`

	GrossPnL string `json:"gross_pnl"`
	Costs    string `json:"costs"`
	NetPnL   string `json:"net_pnl"`

	ExitReason string `json:"exit_reason"`
	EntryNote  string `json:"entry_note"`
	ExitNote   string `json:"exit_note"`

	StopPrice     string `json:"stop_price"`
	EquityAtEntry string `json:"equity_at_entry"`
	EntryMaker    bool   `json:"entry_maker"`
	ExitMaker     bool   `json:"exit_maker"`

	StopAndTargetBothReachable bool `json:"stop_and_target_both_reachable"`
	ForcedByGap                bool `json:"forced_by_gap"`
}

type equityDoc struct {
	OpenTime string `json:"open_time"`
	Equity   string `json:"equity"`
}

type gapDoc struct {
	GapStart     string `json:"gap_start"`
	GapEnd       string `json:"gap_end"`
	FillAttempts int32  `json:"fill_attempts"`
	Note         string `json:"note"`
}

type outageDoc struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Reason string `json:"reason"`
}

// BuildDocument converts a run and its statistics into the JSON form.
func BuildDocument(result backtest.Result, stats Statistics) Document {
	doc := Document{
		DataIncomplete: result.DataIncomplete,
		Strategy: strategyDoc{
			Name:       result.StrategyName,
			Version:    result.StrategyVersion,
			Parameters: paramDocs(result.Params.StrategyParams),
		},
		TrendFilter: trendFilterDoc{
			Enabled:       result.TrendFilterName != "",
			Name:          result.TrendFilterName,
			Version:       result.TrendFilterVersion,
			Configuration: result.TrendFilterConfig,
			Parameters:    paramDocs(result.Params.FilterParams),
		},
		Run: runDoc{
			Symbol:        result.Params.Symbol,
			MarketType:    result.Params.MarketType.String(),
			Timeframe:     result.Params.Timeframe.String(),
			RequestedFrom: rfc3339(result.Params.From),
			RequestedTo:   rfc3339(result.Params.To),
			EvaluatedFrom: rfc3339(result.FirstBar),
			EvaluatedTo:   rfc3339(result.LastBar),
			GapPolicy:     result.Params.GapPolicy.String(),
			InitialEquity: stats.InitialEquity.String(),
			SizingMode:    result.Params.Sizing.Mode.String(),
			RiskPct:       result.Params.Sizing.RiskPct.String(),
			MaxLeverage:   result.Params.Sizing.Leverage().String(),

			TrailingATRMult:     result.Params.Exits.TrailingATRMult,
			TrailingActivateATR: result.Params.Exits.TrailingActivateATR,
		},
		Costs: costsDoc{
			FeeTakerPct:      result.Params.Costs.FeeTakerPct.String(),
			FeeMakerPct:      result.Params.Costs.MakerFeePct().String(),
			CostModel:        result.Params.Costs.CostModel().String(),
			SpreadPoints:     result.Params.Costs.SpreadPoints,
			SpreadPrice:      result.Params.Costs.SpreadPrice().String(),
			CommissionPerLot: result.Params.Costs.CommissionPerLot.String(),
			EntryOrderType:   result.Params.Execution.Entry().String(),
			ExitOrderType:    result.Params.Execution.Exit().String(),
			LimitTimeoutBars: result.Params.Execution.Timeout(),
			SlippageTicks:    result.Params.Costs.SlippageTicks,
			PerRoundTripBps:  stats.CostPerTripBps,
			TickSize:         result.Params.Costs.TickSize.String(),
			TotalPaid:        stats.TotalCosts.String(),
		},
		Bars: barsDoc{
			Evaluated:                  result.BarsEvaluated,
			SkippedWarmup:              result.BarsSkippedWarmup,
			SkippedGap:                 result.BarsSkippedGap,
			StopAndTargetBothReachable: result.AmbiguousBars,
			Vetoed:                     result.BarsVetoed,
			FilterNotReady:             result.BarsFilterNotReady,
			TrailAmbiguous:             result.TrailAmbiguousBars,
		},
		Performance: performanceDoc{
			NetReturn:   stats.NetReturn,
			NetPnL:      stats.TotalNetPnL.String(),
			TotalCosts:  stats.TotalCosts.String(),
			GrossReturn: stats.GrossReturn,
			GrossPnL:    stats.TotalGrossPnL.String(),

			CostShareOfGross: finite(stats.CostShareOfGross),

			FinalEquity: stats.FinalEquity.String(),
		},
		Risk: riskDoc{
			MaxDrawdownPercent:  stats.MaxDrawdown.Percent,
			MaxDrawdownAbsolute: stats.MaxDrawdown.Absolute.String(),
			MaxDrawdownPeakAt:   rfc3339(stats.MaxDrawdown.PeakAt),
			MaxDrawdownTroughAt: rfc3339(stats.MaxDrawdown.TroughAt),
			Sharpe:              finite(stats.Sharpe),
			RiskFreeRate:        stats.RiskFreeRate,
			AnnualisationBars:   stats.AnnualisationBars,
		},
		TradeStats: tradeStatsDoc{
			Count:                  stats.TradeCount,
			Wins:                   stats.WinCount,
			Losses:                 stats.LossCount,
			WinRate:                stats.WinRate,
			ProfitFactor:           finite(stats.ProfitFactor),
			AverageWin:             stats.AverageWin.String(),
			AverageLoss:            stats.AverageLoss.String(),
			LargestWin:             stats.LargestWin.String(),
			LargestLoss:            stats.LargestLoss.String(),
			AverageHoldSeconds:     int64(stats.AverageHoldingTime / time.Second),
			LongestLosingStreak:    stats.LongestLosingStreak,
			TradesPerDay:           stats.TradesPerDay,
			MakerEntries:           result.MakerEntries,
			TakerEntries:           result.TakerEntries,
			MakerExits:             result.MakerExits,
			TakerExits:             result.TakerExits,
			EntriesRequested:       result.EntriesRequested,
			EntriesBelowMinLot:     result.EntriesBelowMinLot,
			EntriesRefusedAfterCap: result.EntriesRefusedAfterCap,
			AverageCostPerTrade:    stats.AverageCostPerTrade.String(),
			AverageRisk:            stats.AverageRisk.String(),
			WorstRisk:              stats.WorstRisk.String(),
			AverageRiskPct:         stats.AverageRiskPct,
			WorstRiskPct:           stats.WorstRiskPct,
			LimitOrdersExpired:     result.LimitOrdersExpired,
			CancelledPercent:       percent(result.LimitOrdersExpired, result.EntriesRequested),
			ByExitReason:           exitReasonDocs(stats.ByExitReason),
		},
		// Never nil: an empty run emits [] rather than null, so a consumer
		// can iterate without a special case.
		Trades:             make([]tradeDoc, 0, len(result.Trades)),
		Equity:             make([]equityDoc, 0, len(result.Equity)),
		UnfilledGaps:       make([]gapDoc, 0, len(result.UnfilledGaps)),
		UntradeableWindows: make([]outageDoc, 0, len(result.UntradeableWindows)),
	}

	if result.DataIncomplete {
		doc.Stamp = DataIncompleteStamp
	}

	for _, trade := range result.Trades {
		doc.Trades = append(doc.Trades, tradeDoc{
			Direction:                  string(trade.Direction),
			EntryTime:                  rfc3339(trade.EntryTime),
			EntryPrice:                 trade.EntryPrice.String(),
			ExitTime:                   rfc3339(trade.ExitTime),
			ExitPrice:                  trade.ExitPrice.String(),
			Size:                       trade.Size.String(),
			GrossPnL:                   trade.GrossPnL.String(),
			Costs:                      trade.Costs.String(),
			NetPnL:                     trade.NetPnL.String(),
			ExitReason:                 trade.ExitReason.String(),
			EntryNote:                  trade.EntryNote,
			ExitNote:                   trade.ExitNote,
			StopAndTargetBothReachable: trade.StopAndTargetBothReachable,
			StopPrice:                  trade.StopPrice.String(),
			EquityAtEntry:              trade.EquityAtEntry.String(),
			EntryMaker:                 trade.EntryMaker,
			ExitMaker:                  trade.ExitMaker,
			ForcedByGap:                trade.ForcedByGap,
		})
	}
	for _, point := range result.Equity {
		doc.Equity = append(doc.Equity, equityDoc{
			OpenTime: rfc3339(point.OpenTime),
			Equity:   point.Equity.String(),
		})
	}
	for _, gap := range result.UnfilledGaps {
		doc.UnfilledGaps = append(doc.UnfilledGaps, gapDoc{
			GapStart:     rfc3339(gap.GapStart),
			GapEnd:       rfc3339(gap.GapEnd),
			FillAttempts: gap.FillAttempts,
			Note:         gap.Note,
		})
	}
	for _, window := range result.UntradeableWindows {
		doc.UntradeableWindows = append(doc.UntradeableWindows, outageDoc{
			Start:  rfc3339(window.Start),
			End:    rfc3339(window.End),
			Reason: window.Reason,
		})
	}
	return doc
}

// WriteJSON renders the document.
func WriteJSON(w io.Writer, doc Document) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	// Escaping is off so that a note containing < or & is not rewritten into
	// <, which would make the file harder to read for no benefit here:
	// nothing serves this JSON as HTML.
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	return nil
}

// rfc3339 renders a timestamp in UTC, or the empty string when unset.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// finite maps NaN and infinity to null.
//
// encoding/json cannot represent either, and substituting zero would turn "no
// answer" into a neutral-looking answer. Null says the statistic did not
// apply, which is what NaN meant.
func finite(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
