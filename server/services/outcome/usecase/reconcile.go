package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
)

// ReconcileConfig is what building a comparison needs.
type ReconcileConfig struct {
	// MinResolved is how many resolved signals a group needs before its
	// numbers are treated as saying anything.
	MinResolved int

	// Backtest re-runs the same strategy and parameters over the same period.
	// Optional: without it the report carries the live side and says the
	// comparison was not run, which is different from finding no divergence.
	Backtest BacktestComparer

	Now func() time.Time
}

// BacktestComparer produces the backtest side of one group.
//
// It is an interface so the expensive half — building a strategy and replaying
// history — is separable from the arithmetic, and so a test can exercise the
// divergence rules without a database or an engine.
type BacktestComparer interface {
	Compare(ctx context.Context, params outcome.ReconcileParams, group outcome.ReconciledGroup) (outcome.Side, error)
}

type reconcileUsecase struct {
	repo outcome.ReconcileRepository
	log  *slog.Logger
	cfg  ReconcileConfig
}

// NewReconcileUsecaseImpl builds the comparison.
func NewReconcileUsecaseImpl(
	repo outcome.ReconcileRepository, log *slog.Logger, cfg ReconcileConfig,
) (outcome.ReconcileUsecase, error) {
	if repo == nil {
		return nil, errors.New("reconcile: no repository")
	}
	if log == nil {
		return nil, errors.New("reconcile: no logger")
	}
	if cfg.MinResolved <= 0 {
		cfg.MinResolved = constants.ReconcileMinResolved
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &reconcileUsecase{repo: repo, log: log, cfg: cfg}, nil
}

// Reconcile builds the report.
func (u *reconcileUsecase) Reconcile(
	ctx context.Context, params outcome.ReconcileParams,
) (outcome.Reconciliation, error) {
	if params.To.Before(params.From) {
		return outcome.Reconciliation{}, fmt.Errorf(
			"reconcile: the window ends (%s) before it starts (%s)",
			params.To.Format(time.RFC3339), params.From.Format(time.RFC3339))
	}

	required := params.MinResolved
	if required <= 0 {
		required = u.cfg.MinResolved
	}

	groups, err := u.repo.LiveGroups(ctx, params)
	if err != nil {
		return outcome.Reconciliation{}, err
	}

	for i := range groups {
		groups[i].Sample = adequacy(groups[i].Live, required)
		u.compare(ctx, params, &groups[i])
		groups[i].Divergences = divergences(groups[i])
	}

	return outcome.Reconciliation{
		Symbol:      params.Symbol,
		MarketType:  params.MarketType,
		From:        params.From,
		To:          params.To,
		GeneratedAt: u.cfg.Now(),
		Groups:      groups,
	}, nil
}

// compare fills in the backtest side, or says why it is missing.
//
// A failure here is recorded on the group rather than returned. The live side
// is worth reporting on its own, and a report that failed outright because
// one strategy could no longer be built would hide the groups that were fine.
func (u *reconcileUsecase) compare(
	ctx context.Context, params outcome.ReconcileParams, group *outcome.ReconciledGroup,
) {
	switch {
	case params.SkipBacktest:
		group.Unavailable = "the comparison was not run"
		return
	case u.cfg.Backtest == nil:
		group.Unavailable = "this binary cannot run the engine, so only the live side is reported"
		return
	}

	side, err := u.cfg.Backtest.Compare(ctx, params, *group)
	if err != nil {
		group.Unavailable = err.Error()
		u.log.WarnContext(ctx, "the backtest side of a comparison could not be produced",
			"error", err, "strategy", group.Strategy, "version", group.Version)
		return
	}
	group.Backtest = &side
}

// adequacy states how far the sample is from saying anything.
//
// # Why the wait is computed and printed
//
// At the 4h strategy's rate of about a tenth of a trade a day, a hundred
// signals is nearly three years. It is better to know that up front than to
// draw conclusions from twenty trades — and if the wait is unacceptable the
// answer is a higher-frequency strategy, not a smaller sample.
func adequacy(side outcome.Side, required int) outcome.SampleAdequacy {
	sample := outcome.SampleAdequacy{
		Resolved:   side.Resolved,
		Required:   required,
		Sufficient: side.Resolved >= required,
	}
	if sample.Sufficient || side.Resolved == 0 {
		return sample
	}

	// The rate is measured from the first signal to now-ish rather than
	// between the first and last: a strategy that has produced nothing for a
	// month is slower than its own signals suggest, and using only the span
	// between them would hide that.
	span := side.Last.Sub(side.First)
	if span <= 0 {
		return sample
	}

	days := span.Hours() / 24
	if days <= 0 {
		return sample
	}

	sample.PerDay = float64(side.Resolved) / days
	if sample.PerDay <= 0 {
		return sample
	}

	remaining := float64(required - side.Resolved)
	sample.Wait = time.Duration(remaining / sample.PerDay * float64(24*time.Hour))
	sample.Known = true
	return sample
}

// divergences fires the rows of the table in docs/ that a group's numbers
// cross the threshold for.
//
// # Why the last row matters as much as the others
//
// A faithful pipeline delivering a thin edge is a different problem from a
// broken pipeline, and they demand opposite responses. Only the "outcomes
// match closely" row distinguishes them, so it is reported rather than left
// as the absence of the others.
func divergences(group outcome.ReconciledGroup) []outcome.Divergence {
	// Nothing can be said about a sample too small to say anything about.
	if !group.Sample.Sufficient {
		return nil
	}
	if group.Backtest == nil {
		return nil
	}

	live, back := group.Live, *group.Backtest
	var found []outcome.Divergence

	if !math.IsNaN(live.WinRate) && !math.IsNaN(back.WinRate) {
		gap := back.WinRate - live.WinRate

		entriesMatch := entryGapPct(live, back).Abs().
			LessThanOrEqual(decimal.NewFromFloat(constants.ReconcileEntryTolerancePct))

		switch {
		case gap >= constants.ReconcileWinRateTolerance && entriesMatch:
			found = append(found, outcome.Divergence{
				Symptom:     "live win rate much lower, entries match",
				LikelyCause: "the strategy has no real edge; the backtest was fitted",
				Detail: fmt.Sprintf("live %.2f%% against backtest %.2f%% over %d resolved signals, "+
					"with entry prices within %.2f%%",
					live.WinRate*100, back.WinRate*100, live.Resolved,
					constants.ReconcileEntryTolerancePct),
			})
		case gap >= constants.ReconcileWinRateTolerance:
			found = append(found, outcome.Divergence{
				Symptom:     "live win rate much lower, entries do not match",
				LikelyCause: "the entries are not the ones the backtest scored; look at warm-up, gaps and the filter before blaming the edge",
				Detail: fmt.Sprintf("live %.2f%% against backtest %.2f%%, entries differ by %s%%",
					live.WinRate*100, back.WinRate*100, entryGapPct(live, back).StringFixed(3)),
			})
		}
	}

	if gap := entryGapPct(live, back); gap.Abs().
		GreaterThan(decimal.NewFromFloat(constants.ReconcileEntryTolerancePct)) {
		found = append(found, outcome.Divergence{
			Symptom:     "live entry prices consistently worse",
			LikelyCause: "slippage exceeds the model",
			Detail: fmt.Sprintf("live average entry %s against backtest %s, a difference of %s%%",
				live.AverageEntryPrice.StringFixed(2), back.AverageEntryPrice.StringFixed(2),
				gap.StringFixed(3)),
		})
	}

	if live.AverageWinPct.IsPositive() && back.AverageWinPct.IsPositive() &&
		live.AverageWinPct.LessThan(back.AverageWinPct.Mul(decimal.NewFromFloat(constants.ReconcileWinSizeTolerance))) {
		found = append(found, outcome.Divergence{
			Symptom:     "live wins smaller than backtest",
			LikelyCause: "fill assumptions too optimistic",
			Detail: fmt.Sprintf("average win %s%% against %s%%",
				live.AverageWinPct.StringFixed(3), back.AverageWinPct.StringFixed(3)),
		})
	}

	if back.Signals > 0 &&
		float64(live.Signals) < float64(back.Signals)*constants.ReconcileSignalCountTolerance {
		found = append(found, outcome.Divergence{
			Symptom:     "live signals fewer than expected",
			LikelyCause: "warm-up, gaps, or the filter behaving differently live",
			Detail: fmt.Sprintf("%d live against %d from the engine over the same period",
				live.Signals, back.Signals),
		})
	}

	if len(found) == 0 {
		found = append(found, outcome.Divergence{
			Symptom:     "live outcomes match closely",
			LikelyCause: "the pipeline is sound; any disappointment is the edge itself",
			Detail: fmt.Sprintf("live win rate %.2f%% against backtest %.2f%% over %d resolved signals",
				live.WinRate*100, back.WinRate*100, live.Resolved),
		})
	}
	return found
}

// entryGapPct is how far the live average entry sits from the backtest's, as
// a percentage of the backtest's.
func entryGapPct(live, back outcome.Side) decimal.Decimal {
	if !back.AverageEntryPrice.IsPositive() {
		return decimal.Zero
	}
	return live.AverageEntryPrice.Sub(back.AverageEntryPrice).
		Div(back.AverageEntryPrice).Mul(decimal.NewFromInt(100))
}
