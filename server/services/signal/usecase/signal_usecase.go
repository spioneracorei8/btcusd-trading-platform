package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"

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

// FetchSignalById returns one signal.
//
// There is no rule to apply here — it is a read of a row by its primary key —
// so the usecase passes it straight through. It exists at this layer because
// the delivery queue holds signal ids and must not reach into the signal
// service's repository to turn one back into a signal.
func (u *signalUsecase) FetchSignalById(
	ctx context.Context, id uuid.UUID,
) (models.Signal, error) {
	return u.signalRepository.FetchSignalById(ctx, id)
}

// SetEntryPrice fills in what a position would have opened at.
//
// A non-positive price is refused rather than stored: the entry is the
// denominator of every return computed from this signal, and a zero would
// turn a comparison into an infinity.
func (u *signalUsecase) SetEntryPrice(
	ctx context.Context, id uuid.UUID, entry decimal.Decimal,
) (models.Signal, error) {
	if !entry.IsPositive() {
		return models.Signal{}, fmt.Errorf("signal: entry price %s is not positive", entry)
	}
	return u.signalRepository.SetEntryPrice(ctx, id, entry)
}

// ListSignals returns a page of the signal history.
//
// The limit is bounded here rather than trusted from the caller: a handler
// that forgot to cap it would turn one request into a full table scan, and the
// rule belongs where every caller inherits it.
func (u *signalUsecase) ListSignals(
	ctx context.Context, params signal.ListParams,
) ([]models.Signal, int64, error) {
	if params.Limit <= 0 || params.Limit > constants.APIPageLimit {
		params.Limit = constants.APIPageLimitDefault
	}
	if params.Offset < 0 {
		params.Offset = 0
	}
	if params.Direction != "" && !params.Direction.Valid() {
		return nil, 0, fmt.Errorf("signal: %q is not a direction", params.Direction)
	}
	return u.signalRepository.ListSignals(ctx, params)
}
