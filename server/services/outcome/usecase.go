package outcome

import "context"

// OutcomeUsecase follows signals to their end.
type OutcomeUsecase interface {
	// FollowOpen advances every open signal against the candles stored since
	// it was last looked at, and reports what moved.
	//
	// It returns an error only for a failure that stops the pass. One signal
	// that cannot be followed is recorded against its own row, not a reason
	// to abandon the ones behind it.
	FollowOpen(ctx context.Context) (FollowReport, error)

	// Run follows open signals until ctx is cancelled. It returns nil on
	// cancellation, which is the ordinary way it ends.
	Run(ctx context.Context) error

	// ListOutcomes returns a page of resolved and open outcomes.
	ListOutcomes(ctx context.Context, params ListParams) ([]Resolved, int64, error)
}

// FollowReport is what one pass did.
type FollowReport struct {
	// Opened counts signals that had no outcome row and now have one.
	Opened int

	// Followed counts open signals advanced against new candles.
	Followed int

	// Resolved counts signals that reached an end this pass, broken out by
	// how they ended.
	Resolved    int
	Target      int
	Stop        int
	Expired     int
	Invalidated int

	// Ambiguous counts resolutions where one bar reached both levels and the
	// stop was assumed. A win rate resting largely on these rests on an
	// assumption rather than on evidence.
	Ambiguous int

	// Contended counts signals another process resolved while this pass was
	// working on them. The database refuses the second write, so no data is
	// lost — but the only way this is reachable is two followers running at
	// once, which means two collectors, which means a deploy went wrong.
	//
	// It is counted rather than only logged because that is a fault whose
	// other symptoms are subtle: duplicated work, doubled exchange
	// connections, and two processes racing on every row. A non-zero count
	// here names it directly.
	Contended int
}

// Quiet reports whether the pass did nothing worth saying, so a follower
// waking every minute does not narrate an empty sweep forever.
func (r FollowReport) Quiet() bool {
	return r.Opened == 0 && r.Followed == 0 && r.Resolved == 0 && r.Contended == 0
}
