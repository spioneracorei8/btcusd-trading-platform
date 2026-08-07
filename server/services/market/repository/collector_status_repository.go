package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database"
	"github.com/spioneracorei8/btcusd-trading-platform/server/database/db"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
)

type collectorStatusRepository struct {
	queries *db.Queries
}

// NewCollectorStatusRepoImpl builds the collector status repository on a pgx pool.
func NewCollectorStatusRepoImpl(pool *pgxpool.Pool) market.CollectorStatusRepository {
	return &collectorStatusRepository{
		queries: db.New(pool),
	}
}

func (r *collectorStatusRepository) RegisterStart(ctx context.Context, symbol string, marketType constants.MarketType) (models.CollectorStatus, error) {
	row, err := r.queries.RegisterCollectorStart(ctx, db.RegisterCollectorStartParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
	})
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("register collector start: %w", err)
	}

	status, err := toCollectorStatusModel(row)
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("register collector start: %w", err)
	}
	return status, nil
}

func (r *collectorStatusRepository) Heartbeat(ctx context.Context, symbol string, marketType constants.MarketType, wsConnected bool) error {
	err := r.queries.HeartbeatCollector(ctx, db.HeartbeatCollectorParams{
		Symbol:      symbol,
		MarketType:  marketType.String(),
		WsConnected: wsConnected,
	})
	if err != nil {
		return fmt.Errorf("heartbeat collector: %w", err)
	}
	return nil
}

func (r *collectorStatusRepository) MarkConnected(ctx context.Context, symbol string, marketType constants.MarketType, reconnect bool) error {
	// The first connection is not a reconnect, so it must not inflate the
	// count an operator reads as "how unstable has this been".
	increment := int32(0)
	if reconnect {
		increment = 1
	}

	err := r.queries.MarkCollectorConnected(ctx, db.MarkCollectorConnectedParams{
		Symbol:             symbol,
		MarketType:         marketType.String(),
		ReconnectIncrement: increment,
	})
	if err != nil {
		return fmt.Errorf("mark collector connected: %w", err)
	}
	return nil
}

func (r *collectorStatusRepository) MarkDisconnected(ctx context.Context, symbol string, marketType constants.MarketType, note string) error {
	err := r.queries.MarkCollectorDisconnected(ctx, db.MarkCollectorDisconnectedParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		Note:       note,
	})
	if err != nil {
		return fmt.Errorf("mark collector disconnected: %w", err)
	}
	return nil
}

func (r *collectorStatusRepository) FetchStatus(ctx context.Context, symbol string, marketType constants.MarketType) (models.CollectorStatus, error) {
	row, err := r.queries.GetCollectorStatus(ctx, db.GetCollectorStatusParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CollectorStatus{}, constants.ErrNotFound
	}
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("fetch collector status: %w", err)
	}

	status, err := toCollectorStatusModel(row)
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("fetch collector status: %w", err)
	}
	return status, nil
}

// toCollectorStatusModel maps a generated row onto the model.
func toCollectorStatusModel(row db.CollectorStatus) (models.CollectorStatus, error) {
	marketType, err := constants.ParseMarketType(row.MarketType)
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("collector status %s: %w", row.Symbol, err)
	}

	state, err := constants.ParseCollectorState(row.State)
	if err != nil {
		return models.CollectorStatus{}, fmt.Errorf("collector status %s: %w", row.Symbol, err)
	}

	return models.CollectorStatus{
		Symbol:             row.Symbol,
		MarketType:         marketType,
		State:              state,
		StateChangedAt:     database.TimeFromTimestamptz(row.StateChangedAt),
		WSConnected:        row.WsConnected,
		LastConnectedAt:    database.TimePtrFromTimestamptz(row.LastConnectedAt),
		LastDisconnectedAt: database.TimePtrFromTimestamptz(row.LastDisconnectedAt),
		LastDisconnectNote: row.LastDisconnectNote,
		ReconnectCount:     row.ReconnectCount,
		StartedAt:          database.TimeFromTimestamptz(row.StartedAt),
		UpdatedAt:          database.TimeFromTimestamptz(row.UpdatedAt),
	}, nil
}

// SetState records a lifecycle transition.
func (r *collectorStatusRepository) SetState(ctx context.Context, symbol string, marketType constants.MarketType, state constants.CollectorState) error {
	err := r.queries.SetCollectorState(ctx, db.SetCollectorStateParams{
		Symbol:     symbol,
		MarketType: marketType.String(),
		State:      state.String(),
	})
	if err != nil {
		return fmt.Errorf("set collector state to %s: %w", state, err)
	}
	return nil
}
