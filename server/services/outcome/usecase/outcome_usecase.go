package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
)

// Config is what following a signal to its end needs.
type Config struct {
	Symbol     string
	MarketType constants.MarketType

	// Costs are the venue's, and they are the backtest's own type on purpose:
	// the entry a live signal is measured against is filled by the same rule
	// the engine uses, with the same slippage.
	Costs backtest.Costs

	// ExpiryBars is how many bars a signal is followed before it is recorded
	// as expired.
	ExpiryBars int

	// Interval is how often Run advances open signals.
	Interval time.Duration

	// Now is the clock. A test supplies its own.
	Now func() time.Time
}

type outcomeUsecase struct {
	repo    outcome.OutcomeRepository
	log     *slog.Logger
	cfg     Config
	signals signal.SignalUsecase
	candles candle.CandleUsecase
	gaps    datagap.DataGapUsecase
}

// NewOutcomeUsecaseImpl builds the follower.
func NewOutcomeUsecaseImpl(
	repo outcome.OutcomeRepository,
	log *slog.Logger,
	signals signal.SignalUsecase,
	candles candle.CandleUsecase,
	gaps datagap.DataGapUsecase,
	cfg Config,
) (outcome.OutcomeUsecase, error) {
	switch {
	case repo == nil:
		return nil, errors.New("outcome: no repository")
	case log == nil:
		return nil, errors.New("outcome: no logger")
	case signals == nil:
		return nil, errors.New("outcome: no way to read a signal")
	case candles == nil:
		return nil, errors.New("outcome: no way to read candles")
	case cfg.Symbol == "":
		return nil, errors.New("outcome: no symbol")
	case !cfg.MarketType.Valid():
		return nil, fmt.Errorf("outcome: %q is not a market type", cfg.MarketType)
	}

	if cfg.ExpiryBars <= 0 {
		cfg.ExpiryBars = constants.DefaultSignalExpiryBars
	}
	if cfg.Interval <= 0 {
		cfg.Interval = constants.DefaultOutcomeInterval
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}

	return &outcomeUsecase{
		repo: repo, log: log, cfg: cfg,
		signals: signals, candles: candles, gaps: gaps,
	}, nil
}

// Run follows open signals on a ticker until ctx is cancelled.
func (u *outcomeUsecase) Run(ctx context.Context) error {
	u.log.InfoContext(ctx, "outcome follower started",
		"interval", u.cfg.Interval.String(),
		"expiry_bars", u.cfg.ExpiryBars)

	ticker := time.NewTicker(u.cfg.Interval)
	defer ticker.Stop()

	for {
		// A pass first, so signals that resolved while the process was down
		// are picked up at start-up rather than after one interval.
		report, err := u.FollowOpen(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return nil
		case err != nil:
			// Worth saying and not worth stopping for: the database may be
			// briefly unavailable, and giving up would mean every later
			// signal goes unfollowed.
			u.log.ErrorContext(ctx, "could not follow open signals", "error", err)
		case !report.Quiet():
			u.log.InfoContext(ctx, "outcome pass",
				"opened", report.Opened, "followed", report.Followed,
				"resolved", report.Resolved, "target", report.Target,
				"stop", report.Stop, "expired", report.Expired,
				"invalidated", report.Invalidated, "ambiguous", report.Ambiguous)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// FollowOpen advances every open signal against the candles stored since it
// was last looked at.
func (u *outcomeUsecase) FollowOpen(ctx context.Context) (outcome.FollowReport, error) {
	var report outcome.FollowReport

	opened, err := u.repo.EnsureOutcomes(
		ctx, u.cfg.Symbol, u.cfg.MarketType, constants.OutcomeBatchSize)
	if err != nil {
		return report, err
	}
	report.Opened = len(opened)

	open, err := u.repo.FetchOpen(ctx, u.cfg.Symbol, u.cfg.MarketType, constants.OutcomeBatchSize)
	if err != nil {
		return report, err
	}

	for _, row := range open {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}

		resolved, err := u.follow(ctx, row)
		if err != nil {
			// One signal that cannot be followed is not a reason to abandon
			// the ones behind it.
			u.log.ErrorContext(ctx, "could not follow a signal",
				"error", err, "signal_id", row.SignalId.String())
			continue
		}
		report.Followed++
		tally(&report, resolved)
	}
	return report, nil
}

// tally counts one followed signal into the pass's report.
func tally(report *outcome.FollowReport, resolved models.SignalOutcome) {
	if !resolved.Status.Resolved() {
		return
	}

	report.Resolved++
	if resolved.DivergenceNote != "" {
		report.Ambiguous++
	}

	switch resolved.Status {
	case constants.OutcomeTarget:
		report.Target++
	case constants.OutcomeStop:
		report.Stop++
	case constants.OutcomeExpired:
		report.Expired++
	case constants.OutcomeInvalidated:
		report.Invalidated++
	}
}

// follow advances one signal and records where it got to.
func (u *outcomeUsecase) follow(
	ctx context.Context, stored models.SignalOutcome,
) (models.SignalOutcome, error) {
	signalRow, err := u.signals.FetchSignalById(ctx, stored.SignalId)
	if err != nil {
		return models.SignalOutcome{}, fmt.Errorf("read the signal: %w", err)
	}

	// Every bar from the signal's close onward. The first of them is the one
	// the entry fills on, and it can also be the one that resolves the trade:
	// the engine checks levels against the same bar it filled the entry at,
	// and pretending otherwise would hide the worst case.
	bars, err := u.barsSince(ctx, signalRow)
	if err != nil {
		return models.SignalOutcome{}, err
	}
	if len(bars) == 0 {
		// Nothing has closed since the signal. Not an error: the very next
		// pass is the ordinary case for a signal made a moment ago.
		return stored, nil
	}

	// A hole in the window means what happened is not knowable. That is a
	// different thing from a loss and must not be counted as one, so it is
	// checked before the bars are read for anything else: the candles either
	// side of a gap look continuous, and walking them would produce a
	// confident answer drawn across missing data.
	holed, err := u.windowIsHoled(ctx, signalRow, bars)
	if err != nil {
		return models.SignalOutcome{}, err
	}
	if holed {
		return u.invalidate(ctx, stored, signalRow, bars)
	}

	walked := u.walk(signalRow, bars)

	// The entry is knowable the moment the first bar after the signal opens,
	// and it is written once. It is the denominator of every return computed
	// from this signal, so recording it is not optional bookkeeping.
	u.recordEntry(ctx, signalRow, walked.entry)

	if !walked.status.Resolved() {
		// Still running. Progress is saved so the excursions and the bar
		// count survive a restart, and so a reconciliation looking at open
		// signals sees how far each has got.
		stored.MAE, stored.MFE = walked.mae(), walked.mfe()
		stored.BarsHeld = int32(walked.bars)
		return u.repo.SaveOutcome(ctx, stored)
	}

	stored.Status = walked.status
	stored.ResolvedAt = &walked.resolvedAt
	stored.ResolvedPrice = decimal.NullDecimal{Decimal: walked.resolvedPrice, Valid: true}
	stored.MAE, stored.MFE = walked.mae(), walked.mfe()
	stored.BarsHeld = int32(walked.bars)

	// An invalidated window has no price to resolve at, and the check
	// constraint says so: what happened is not knowable, and putting a number
	// there would make a guess look like a measurement.
	if walked.status == constants.OutcomeInvalidated {
		stored.ResolvedPrice = decimal.NullDecimal{}
	}

	accounting, note, err := u.accountFor(signalRow, walked)
	if err != nil {
		return models.SignalOutcome{}, err
	}
	stored.BacktestWouldHave = accounting
	stored.DivergenceNote = note

	saved, err := u.repo.SaveOutcome(ctx, stored)
	if err != nil {
		return models.SignalOutcome{}, err
	}

	u.log.InfoContext(ctx, "signal resolved",
		"signal_id", saved.SignalId.String(),
		"status", saved.Status.String(),
		"bars_held", saved.BarsHeld,
		"ambiguous", walked.ambiguous,
		"strategy", signalRow.StrategyName)
	return saved, nil
}

// recordEntry writes the entry price, once.
//
// A failure is logged rather than returned. The outcome is still worth
// following without it — the levels do not depend on the entry — and a signal
// whose entry could not be written is better recorded with a note than not
// followed at all.
func (u *outcomeUsecase) recordEntry(
	ctx context.Context, signalRow models.Signal, entry decimal.Decimal,
) {
	if signalRow.EntryPrice.Valid || !entry.IsPositive() {
		return
	}

	if _, err := u.signals.SetEntryPrice(ctx, signalRow.Id, entry); err != nil {
		if errors.Is(err, constants.ErrNotFound) {
			// Another pass got there first, which is the write-once rule
			// working rather than a failure.
			return
		}
		u.log.ErrorContext(ctx, "could not record the entry price",
			"error", err, "signal_id", signalRow.Id.String())
		return
	}

	u.log.DebugContext(ctx, "entry price recorded",
		"signal_id", signalRow.Id.String(), "entry_price", entry.String())
}

// barsSince reads the closed candles from the signal's bar onward.
//
// The window is bounded by the expiry: a signal that never resolves would
// otherwise have its whole subsequent history re-read on every pass, forever.
func (u *outcomeUsecase) barsSince(
	ctx context.Context, signalRow models.Signal,
) ([]models.Candle, error) {
	span := signalRow.Timeframe.Duration()
	if span <= 0 {
		return nil, fmt.Errorf("signal %s: %q has no duration", signalRow.Id, signalRow.Timeframe)
	}

	// signal_time is the bar's close, which is the next bar's open.
	from := signalRow.SignalTime.UTC()
	to := from.Add(time.Duration(u.cfg.ExpiryBars) * span)

	bars, err := u.candles.FetchCandles(ctx, candle.FetchCandlesParams{
		Symbol:     signalRow.Symbol,
		MarketType: signalRow.MarketType,
		Timeframe:  signalRow.Timeframe,
		From:       from,
		To:         to,
	})
	if err != nil {
		return nil, fmt.Errorf("read the candles after signal %s: %w", signalRow.Id, err)
	}
	return bars, nil
}

// windowIsHoled reports whether data is missing inside the window a signal is
// followed over.
//
// Two things are asked, because they catch different failures. A recorded gap
// is one the collector noticed and has not filled; a break in the bars handed
// to us is one nobody recorded — the series simply skips, which is what a gap
// looks like from here whether or not anything wrote it down.
func (u *outcomeUsecase) windowIsHoled(
	ctx context.Context, signalRow models.Signal, bars []models.Candle,
) (bool, error) {
	span := signalRow.Timeframe.Duration()

	// A break between consecutive bars. Checked without the database because
	// it is visible in what was already read.
	for i := 1; i < len(bars); i++ {
		if bars[i].OpenTime.Sub(bars[i-1].OpenTime) > span {
			return true, nil
		}
	}

	if u.gaps == nil {
		return false, nil
	}

	// Only as far as the bars actually go. Asking about the whole expiry
	// window would flag a signal made an hour ago for the data that has not
	// happened yet.
	last := bars[len(bars)-1].OpenTime
	unfilled, err := u.gaps.ListUnfilledInRange(ctx, datagap.GapRangeParams{
		Symbol:     signalRow.Symbol,
		MarketType: signalRow.MarketType,
		Timeframe:  signalRow.Timeframe,
		From:       signalRow.SignalTime.UTC(),
		To:         last,
	})
	if err != nil {
		return false, fmt.Errorf("check for gaps around signal %s: %w", signalRow.Id, err)
	}
	return len(unfilled) > 0, nil
}

// invalidate records a signal whose window has missing data.
//
// It carries no resolved price and no accounting. What happened is not
// knowable, and a number there would make a guess look like a measurement —
// the status exists precisely so the reconciliation can leave these out
// rather than average them in.
func (u *outcomeUsecase) invalidate(
	ctx context.Context, stored models.SignalOutcome,
	signalRow models.Signal, bars []models.Candle,
) (models.SignalOutcome, error) {
	at := u.cfg.Now()
	stored.Status = constants.OutcomeInvalidated
	stored.ResolvedAt = &at
	stored.ResolvedPrice = decimal.NullDecimal{}
	stored.MAE, stored.MFE = decimal.NullDecimal{}, decimal.NullDecimal{}
	stored.BarsHeld = int32(len(bars))
	stored.BacktestWouldHave = nil
	stored.DivergenceNote = "the window this signal was followed over has missing data, " +
		"so what happened is not knowable; it is excluded from statistics"

	saved, err := u.repo.SaveOutcome(ctx, stored)
	if err != nil {
		return models.SignalOutcome{}, err
	}

	u.log.WarnContext(ctx, "a signal was invalidated by missing data",
		"signal_id", saved.SignalId.String(),
		"timeframe", signalRow.Timeframe.String(),
		"from", signalRow.SignalTime.UTC().Format(time.RFC3339))
	return saved, nil
}
