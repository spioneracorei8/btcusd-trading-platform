package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

type reconcileRepository struct {
	queries *db.Queries
}

// NewReconcileRepoImpl builds the reconciliation reader on a pgx pool.
func NewReconcileRepoImpl(pool *pgxpool.Pool) outcome.ReconcileRepository {
	return &reconcileRepository{queries: db.New(pool)}
}

// LiveSignals returns every live signal in the window, one row each.
func (r *reconcileRepository) LiveSignals(
	ctx context.Context, params outcome.ReconcileParams,
) ([]outcome.LiveSignal, error) {
	rows, err := r.queries.ReconcileLiveSignals(ctx, db.ReconcileLiveSignalsParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		FromTime:   database.TimestamptzFromTime(params.From),
		ToTime:     database.TimestamptzFromTime(params.To),
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile live signals: %w", err)
	}

	out := make([]outcome.LiveSignal, 0, len(rows))
	for _, row := range rows {
		signal, err := toLiveSignal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, signal)
	}
	return out, nil
}

// toLiveSignal converts one row.
func toLiveSignal(row db.ReconcileLiveSignalsRow) (outcome.LiveSignal, error) {
	at := database.TimeFromTimestamptz(row.SignalTime)

	status, err := constants.ParseOutcomeStatus(row.Status)
	if err != nil {
		return outcome.LiveSignal{}, fmt.Errorf("live signal at %s: %w", at, err)
	}
	values, err := decodeParams(row.Params)
	if err != nil {
		return outcome.LiveSignal{}, fmt.Errorf("live signal at %s: %w", at, err)
	}

	signal := outcome.LiveSignal{
		Strategy:           row.StrategyName,
		Version:            row.StrategyVersion,
		Params:             values,
		At:                 at,
		Status:             status,
		BarsHeld:           row.BarsHeld,
		RestedOnAssumption: row.RestedOnAssumption,
	}

	for _, field := range []struct {
		into  *decimal.NullDecimal
		from  pgtype.Numeric
		label string
	}{
		{&signal.EntryPrice, row.EntryPrice, "entry_price"},
		{&signal.NetReturnPct, row.NetReturnPct, "net_return_pct"},
		{&signal.CostPct, row.CostPct, "cost_pct"},
	} {
		value, err := database.NullDecimalFromNumeric(field.from)
		if err != nil {
			return outcome.LiveSignal{}, fmt.Errorf("live signal at %s: %s: %w", at, field.label, err)
		}
		*field.into = value
	}
	return signal, nil
}

// decodeParams reads the resolved parameter set recorded on the signal.
//
// It is part of the grouping key, so a set that will not decode is an error
// rather than an empty list: silently grouping two different parameter sets
// under "no parameters" is the exact mistake this key exists to prevent.
func decodeParams(raw []byte) ([]outcome.ParamValue, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var recorded []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		return nil, fmt.Errorf("decode the recorded parameter set: %w", err)
	}

	values := make([]outcome.ParamValue, 0, len(recorded))
	for _, p := range recorded {
		values = append(values, outcome.ParamValue{Name: p.Name, Value: p.Value})
	}
	return values, nil
}
