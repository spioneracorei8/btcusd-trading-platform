package usecase

import (
	"context"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/health"
)

type healthUsecase struct {
	healthRepository health.HealthRepository
}

// NewHealthUsecaseImpl builds the health usecase on top of a repository.
func NewHealthUsecaseImpl(healthRepository health.HealthRepository) health.HealthUsecase {
	return &healthUsecase{
		healthRepository: healthRepository,
	}
}

func (u *healthUsecase) Liveness() models.Health {
	return models.Health{Status: constants.StatusOK}
}

func (u *healthUsecase) Readiness(ctx context.Context) (models.Health, error) {
	if err := u.healthRepository.PingDatabase(ctx); err != nil {
		return models.Health{
			Status: constants.StatusUnavailable,
			Error:  constants.MsgDatabaseUnreachable,
		}, err
	}
	return models.Health{Status: constants.StatusReady}, nil
}
