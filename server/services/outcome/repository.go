// Package outcome declares the contracts for following signals to their end.
//
// # Why this is its own service
//
// Producing a signal and finding out what became of it are different
// responsibilities with different lifetimes. A signal is written once, in the
// moment; an outcome is followed for hours or days afterwards, against
// candles that had not arrived when the signal was made. It has its own
// table, its own worker and its own reconciliation, and folding it into the
// signal service would make that service about two things.
//
// Nothing here places or closes anything. It reads stored candles and records
// what would have happened.
package outcome

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
)

// OutcomeRepository stores what became of each signal.
type OutcomeRepository interface {
	// EnsureOutcomes opens a row for every signal that has none, and returns
	// the ones it opened.
	//
	// Idempotent: a signal already being followed keeps its row and its
	// progress. That is what lets a follower start against a table of signals
	// it has never seen — a first deploy, or a restart after an outage — and
	// pick all of them up without double-counting any.
	EnsureOutcomes(ctx context.Context, symbol string, marketType constants.MarketType, limit int32) ([]models.SignalOutcome, error)

	// FetchOpen returns the outcomes still being followed, oldest signal
	// first, so a backlog is worked through in the order the signals
	// happened.
	//
	// Outcomes only. The signal behind one is read through the signal
	// service's usecase rather than joined in here — reaching past a usecase
	// into another service's rows is how the rules that live in between get
	// bypassed.
	FetchOpen(ctx context.Context, symbol string, marketType constants.MarketType, limit int32) ([]models.SignalOutcome, error)

	// SaveOutcome records progress or a resolution.
	SaveOutcome(ctx context.Context, o models.SignalOutcome) (models.SignalOutcome, error)

	// FetchOutcome returns one outcome, or constants.ErrNotFound.
	FetchOutcome(ctx context.Context, signalId uuid.UUID) (models.SignalOutcome, error)

	// ListOutcomes returns a page of outcomes, newest signal first, and the
	// size of the collection they came from.
	//
	// Outcomes only. The signal each one describes is read through the signal
	// service's usecase, for the same reason FetchOpen returns outcomes:
	// reaching past a usecase into another service's rows bypasses the rules
	// that live in between.
	ListOutcomes(ctx context.Context, params ListParams) ([]models.SignalOutcome, int64, error)
}

// ListParams bounds a page of outcomes.
type ListParams struct {
	Symbol     string
	MarketType constants.MarketType

	// Status filters to one outcome status. Empty means all of them.
	Status constants.OutcomeStatus

	From, To time.Time

	Limit  int32
	Offset int32
}

// Resolved is one outcome and the signal it describes.
type Resolved struct {
	Signal  models.Signal
	Outcome models.SignalOutcome
}
