package datagap

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// DataGapUsecase holds the rules around recording a gap.
type DataGapUsecase interface {
	RecordGap(ctx context.Context, gap models.DataGap) (models.DataGap, error)
}
