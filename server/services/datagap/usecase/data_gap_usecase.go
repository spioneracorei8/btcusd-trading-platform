package usecase

import (
	"context"

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
