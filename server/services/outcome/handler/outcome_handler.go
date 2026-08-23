package handler

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	_health_handler "github.com/spioneracorei8/btcusd-trading-platform/server/services/health/handler"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

type outcomeHandler struct {
	usecase outcome.ReconcileUsecase

	// reader serves the outcome list. Nil on a process that has no follower
	// wired up, which the endpoint reports rather than pretending an empty
	// list is an answer.
	reader outcome.OutcomeUsecase

	logger     *slog.Logger
	symbol     string
	marketType constants.MarketType
	now        func() time.Time
}

// NewOutcomeHandlerImpl builds the reconciliation handler.
func NewOutcomeHandlerImpl(
	usecase outcome.ReconcileUsecase,
	reader outcome.OutcomeUsecase,
	logger *slog.Logger,
	symbol string,
	marketType constants.MarketType,
) outcome.OutcomeHandler {
	return &outcomeHandler{
		usecase:    usecase,
		reader:     reader,
		logger:     logger,
		symbol:     symbol,
		marketType: marketType,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Reconciliation answers GET /internal/signals/reconciliation.
//
// Query parameters, all optional: from and to (RFC3339), days (a window
// ending now), min_resolved, and skip_backtest.
func (h *outcomeHandler) Reconciliation(w http.ResponseWriter, r *http.Request) {
	params, err := h.parse(r)
	if err != nil {
		_health_handler.WriteJSON(w, h.logger, http.StatusBadRequest,
			map[string]string{"error": err.Error()})
		return
	}

	report, err := h.usecase.Reconcile(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "could not build the reconciliation", "error", err)
		_health_handler.WriteJSON(w, h.logger, http.StatusInternalServerError,
			map[string]string{"error": "could not build the reconciliation"})
		return
	}

	_health_handler.WriteJSON(w, h.logger, http.StatusOK, toResponse(report))
}

// parse reads the window and the thresholds from the query string.
func (h *outcomeHandler) parse(r *http.Request) (outcome.ReconcileParams, error) {
	query := r.URL.Query()

	params := outcome.ReconcileParams{
		Symbol:     h.symbol,
		MarketType: h.marketType,
		To:         h.now(),
	}
	params.From = params.To.AddDate(-1, 0, 0)

	if raw := query.Get("days"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days <= 0 {
			return outcome.ReconcileParams{}, invalid("days", raw, "a positive number of days")
		}
		params.From = params.To.AddDate(0, 0, -days)
	}

	for name, into := range map[string]*time.Time{"from": &params.From, "to": &params.To} {
		raw := query.Get(name)
		if raw == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return outcome.ReconcileParams{}, invalid(name, raw, "an RFC3339 timestamp")
		}
		*into = at.UTC()
	}

	if params.To.Before(params.From) {
		return outcome.ReconcileParams{}, invalid("to", params.To.Format(time.RFC3339),
			"a time at or after from")
	}

	if raw := query.Get("min_resolved"); raw != "" {
		minimum, err := strconv.Atoi(raw)
		if err != nil || minimum <= 0 {
			return outcome.ReconcileParams{}, invalid("min_resolved", raw, "a positive count")
		}
		params.MinResolved = minimum
	}

	if raw := query.Get("skip_backtest"); raw != "" {
		skip, err := strconv.ParseBool(raw)
		if err != nil {
			return outcome.ReconcileParams{}, invalid("skip_backtest", raw, "a boolean")
		}
		params.SkipBacktest = skip
	}

	return params, nil
}

func invalid(name, value, want string) error {
	return fmt.Errorf("%s=%q is not %s", name, value, want)
}

// nullableRate renders a win rate, with null for one that was not computed.
//
// NaN cannot be encoded as JSON and zero would read as a strategy that never
// wins, which is a different claim from "nothing has resolved yet".
func nullableRate(rate float64) *float64 {
	if math.IsNaN(rate) {
		return nil
	}
	return &rate
}
