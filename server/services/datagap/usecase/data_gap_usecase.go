package usecase

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
)

type dataGapUsecase struct {
	dataGapRepository datagap.DataGapRepository
}

// NewDataGapUsecaseImpl builds the data gap usecase on top of a repository.
func NewDataGapUsecaseImpl(dataGapRepository datagap.DataGapRepository) datagap.DataGapUsecase {
	return &dataGapUsecase{
		dataGapRepository: dataGapRepository,
	}
}

func (u *dataGapUsecase) RecordGap(ctx context.Context, gap models.DataGap) (models.DataGap, error) {
	return u.dataGapRepository.InsertGap(ctx, gap)
}

func (u *dataGapUsecase) MarkFilled(ctx context.Context, id int64) error {
	return u.dataGapRepository.MarkFilled(ctx, id)
}

func (u *dataGapUsecase) RecordFillAttempt(ctx context.Context, id int64, note string) (models.DataGap, error) {
	return u.dataGapRepository.RecordFillAttempt(ctx, id, note)
}

func (u *dataGapUsecase) CountUnfilled(ctx context.Context, symbol string, marketType constants.MarketType, timeframe constants.Timeframe) (int64, error) {
	return u.dataGapRepository.CountUnfilled(ctx, symbol, marketType, timeframe)
}

// ListUnfilledInRange returns every unfilled gap overlapping a window.
func (u *dataGapUsecase) ListUnfilledInRange(ctx context.Context, params datagap.GapRangeParams) ([]models.DataGap, error) {
	return u.dataGapRepository.ListUnfilledInRange(ctx, params)
}
