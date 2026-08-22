package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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

// LiveGroups aggregates resolved outcomes by strategy, version and parameters.
func (r *reconcileRepository) LiveGroups(
	ctx context.Context, params outcome.ReconcileParams,
) ([]outcome.ReconciledGroup, error) {
	rows, err := r.queries.ReconcileLiveGroups(ctx, db.ReconcileLiveGroupsParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType.String(),
		FromTime:   database.TimestamptzFromTime(params.From),
		ToTime:     database.TimestamptzFromTime(params.To),
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile live groups: %w", err)
	}

	groups := make([]outcome.ReconciledGroup, 0, len(rows))
	for _, row := range rows {
		group, err := toReconciledGroup(row)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

// toReconciledGroup converts one aggregate row.
func toReconciledGroup(row db.ReconcileLiveGroupsRow) (outcome.ReconciledGroup, error) {
	values, err := decodeParams(row.Params)
	if err != nil {
		return outcome.ReconciledGroup{}, fmt.Errorf(
			"reconcile %s %s: %w", row.StrategyName, row.StrategyVersion, err)
	}

	side := outcome.Side{
		Signals:     int(row.Signals),
		StillOpen:   int(row.StillOpen),
		Invalidated: int(row.Invalidated),
		Resolved:    int(row.Resolved),
		Targets:     int(row.Targets),
		Stops:       int(row.Stops),
		Expired:     int(row.Expired),
		Noted:       int(row.Noted),
		Wins:        int(row.Wins),
		Losses:      int(row.Losses),
		First:       database.TimeFromTimestamptz(row.FirstSignal),
		Last:        database.TimeFromTimestamptz(row.LastSignal),
	}

	// NaN rather than zero when nothing has resolved. A zero would read as a
	// strategy that never wins, which is a different claim from "nothing has
	// finished yet".
	side.WinRate = math.NaN()
	if side.Resolved > 0 {
		side.WinRate = float64(side.Wins) / float64(side.Resolved)
	}

	for _, field := range []struct {
		into  *decimal.Decimal
		from  pgtype.Numeric
		label string
	}{
		{&side.AverageWinPct, row.AverageWinPct, "average_win_pct"},
		{&side.AverageLossPct, row.AverageLossPct, "average_loss_pct"},
		{&side.AverageCostPct, row.AverageCostPct, "average_cost_pct"},
		{&side.AverageEntryPrice, row.AverageEntryPrice, "average_entry_price"},
		{&side.AverageBarsHeld, row.AverageBarsHeld, "average_bars_held"},
	} {
		// An average over no rows is SQL NULL, which is zero here and is the
		// right reading: there were none to average.
		value, err := database.NullDecimalFromNumeric(field.from)
		if err != nil {
			return outcome.ReconciledGroup{}, fmt.Errorf(
				"reconcile %s: %s: %w", row.StrategyName, field.label, err)
		}
		*field.into = value.Decimal
	}

	return outcome.ReconciledGroup{
		Strategy: row.StrategyName,
		Version:  row.StrategyVersion,
		Params:   values,
		Live:     side,
	}, nil
}

// decodeParams reads the resolved parameter set recorded on the signals.
//
// It is the grouping key, so a set that will not decode is an error rather
// than an empty list: silently grouping two different parameter sets under
// "no parameters" is the exact mistake this key exists to prevent.
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
