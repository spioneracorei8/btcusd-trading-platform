package usecase

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

type signalUsecase struct {
	signalRepository signal.SignalRepository
}

// NewSignalUsecaseImpl builds the signal usecase on top of a repository.
func NewSignalUsecaseImpl(signalRepository signal.SignalRepository) signal.SignalUsecase {
	return &signalUsecase{
		signalRepository: signalRepository,
	}
}

func (u *signalUsecase) CreateSignal(ctx context.Context, s models.Signal) (models.Signal, error) {
	return u.signalRepository.InsertSignal(ctx, s)
}
