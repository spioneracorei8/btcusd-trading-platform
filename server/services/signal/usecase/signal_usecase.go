package usecase

import (
	"context"
	"errors"
	"fmt"

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

// CreateSignal stores a decision after checking it may be acted on.
//
// The bar is passed alongside the signal so the closed-candle rule can be
// checked here rather than trusted. An alert cannot be recalled: a signal
// emitted from a bar that is still forming would tell the owner about a price
// that can still change, and by the time it changed the notification would
// already be on their phone.
func (u *signalUsecase) CreateSignal(ctx context.Context, s models.Signal, bar models.Candle) (models.Signal, error) {
	if err := signal.ValidateForBar(bar, s.SignalTime); err != nil {
		return models.Signal{}, err
	}
	if !s.Direction.Valid() {
		return models.Signal{}, fmt.Errorf("signal: %q is not a direction", s.Direction)
	}
	if len(s.Reason) == 0 {
		return models.Signal{}, errors.New(
			"signal: no reason recorded. The indicator values behind a decision cannot " +
				"be reconstructed later, so a signal without them cannot be audited")
	}
	return u.signalRepository.InsertSignal(ctx, s)
}
