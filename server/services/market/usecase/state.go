package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
)

// stateMachine tracks which phase of its lifecycle the collector is in and
// publishes every transition.
//
// The phase is what makes the status endpoint interpretable. "The newest
// candle is three years old" is normal progress while backfilling and a
// silent failure while live; without knowing which, the numbers alone cannot
// tell those apart.
type stateMachine struct {
	mu        sync.RWMutex
	current   constants.CollectorState
	enteredAt time.Time
}

func newStateMachine(now time.Time) *stateMachine {
	return &stateMachine{
		current:   constants.CollectorStarting,
		enteredAt: now,
	}
}

// current state, for the staleness gate and for logging.
func (s *stateMachine) state() constants.CollectorState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// transition moves to a new state and reports the previous one with how long
// was spent in it. It returns false when the state is unchanged, so a caller
// does not log or persist a no-op.
func (s *stateMachine) transition(to constants.CollectorState, now time.Time) (from constants.CollectorState, spent time.Duration, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current == to {
		return s.current, 0, false
	}

	from = s.current
	spent = now.Sub(s.enteredAt)
	s.current = to
	s.enteredAt = now
	return from, spent, true
}

// setState moves the collector to a new lifecycle state, logs the transition
// and publishes it for the api to read.
//
// Persisting is best effort: losing a state write is worth a warning, not
// taking ingestion down. The in-memory state is the authority for this
// process; the row exists so another container can see it.
func (u *marketUsecase) setState(ctx context.Context, to constants.CollectorState) {
	from, spent, changed := u.state.transition(to, u.now())
	if !changed {
		return
	}

	u.log.InfoContext(ctx, "collector state changed",
		"from", from.String(),
		"to", to.String(),
		"spent_in_previous", spent.String(),
	)

	// Detached: a transition into a terminal state happens while the context
	// is already cancelled, and that is exactly when the row most needs to
	// stop claiming the collector is live.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.DefaultConnectTimeout)
	defer cancel()

	if err := u.status.SetState(writeCtx, u.cfg.Symbol, u.cfg.MarketType, to); err != nil {
		u.log.WarnContext(ctx, "could not publish the collector state",
			"state", to.String(), "error", err)
	}
}
