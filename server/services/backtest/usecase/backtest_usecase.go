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
	_indicator_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/indicator/usecase"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/strategy"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/trend"
)

type backtestUsecase struct {
	candles   candle.CandleUsecase
	gaps      datagap.DataGapUsecase
	log       *slog.Logger
	indicator _indicator_us.SetConfig

	// guard refuses to replay a strategy instance that has already run. See
	// reuse.go for what that would silently do to a comparison.
	guard *strategyGuard
}

// NewBacktestUsecaseImpl builds the engine over the stored candle series.
//
// It takes the candle usecase rather than a repository because the rule that
// only closed candles are stored is a usecase rule, and the engine must not
// be able to reach around it.
func NewBacktestUsecaseImpl(
	log *slog.Logger,
	candles candle.CandleUsecase,
	gaps datagap.DataGapUsecase,
	indicatorConfig _indicator_us.SetConfig,
) backtest.BacktestUsecase {
	return &backtestUsecase{
		candles:   candles,
		gaps:      gaps,
		log:       log,
		indicator: indicatorConfig,
		guard:     newStrategyGuard(),
	}
}

// errStopScan ends the candle scan early without it looking like a failure.
var errStopScan = errors.New("backtest scan finished")

// Run replays the requested range through the strategy.
//
// The loop is the whole engine and is deliberately small:
//
//	stream candle → feed indicators → decide tradeability → fill queued
//	intents at this open → check levels against this bar → mark equity →
//	call OnBar → queue what it wants for the next open
//
// The order matters. Intents are filled at the open of the bar *after* the
// one that produced them, which is the single rule most responsible for
// backtests that cannot be reproduced live: the close of bar t is only
// knowable once t is over, so a decision made on it cannot also fill on it.
func (u *backtestUsecase) Run(ctx context.Context, params backtest.RunParams) (backtest.Result, error) {
	if err := validateParams(params); err != nil {
		return backtest.Result{}, err
	}
	if err := u.guard.claim(params.Strategy); err != nil {
		return backtest.Result{}, err
	}

	result := backtest.Result{
		Params:          params,
		StrategyName:    params.Strategy.Name(),
		StrategyVersion: params.Strategy.Version(),
		UntradeableWindows: backtest.OutagesFor(
			params.Symbol, params.MarketType, params.From, params.To),
	}

	gaps, err := u.gaps.ListUnfilledInRange(ctx, datagap.GapRangeParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType,
		Timeframe:  params.Timeframe,
		From:       params.From,
		To:         params.To,
	})
	if err != nil {
		return backtest.Result{}, fmt.Errorf("check data gaps: %w", err)
	}
	result.UnfilledGaps = gaps

	switch {
	case len(gaps) == 0:
	case params.GapPolicy == backtest.GapHalt:
		// The result is returned alongside the refusal so the caller can print
		// which ranges are missing. Refusing without saying why would send the
		// reader to psql, which is the workflow this gate replaces.
		return result, fmt.Errorf("%w: %d unfilled gap(s) in %s..%s",
			constants.ErrDataIncomplete, len(gaps),
			params.From.Format(time.RFC3339), params.To.Format(time.RFC3339))
	case params.GapPolicy == backtest.GapIgnore:
		// Running over holes is allowed; pretending the numbers are clean is
		// not. Everything downstream reads this stamp.
		result.DataIncomplete = true
	}

	run, err := u.newRunner(params, gaps, result.UntradeableWindows)
	if err != nil {
		return backtest.Result{}, err
	}
	run.ctx = ctx

	if params.Filtered() {
		if params.TrendAligner == nil {
			return backtest.Result{}, errors.New(
				"backtest: a trend filter was given with no aligner; it would have nothing to read")
		}
		result.TrendFilterName = params.TrendFilter.Name()
		result.TrendFilterVersion = params.TrendFilter.Version()
		result.TrendFilterConfig = params.TrendConfig.Describe()
	}

	// Warm-up is loaded from before the requested range so the range itself
	// can be scored from its first bar. When that history does not exist the
	// warm-up is consumed inside the range instead and reported as skipped,
	// rather than silently shortening what was asked for.
	streamFrom := params.From.Add(-time.Duration(run.warmupBars) * params.Timeframe.Duration())

	err = u.candles.StreamCandles(ctx, candle.FetchCandlesParams{
		Symbol:     params.Symbol,
		MarketType: params.MarketType,
		Timeframe:  params.Timeframe,
		From:       streamFrom,
		To:         params.To,
	}, run.onCandle)

	if err != nil && !errors.Is(err, errStopScan) {
		return backtest.Result{}, err
	}

	run.finish()
	run.fill(&result)

	if result.BarsEvaluated == 0 {
		return result, fmt.Errorf("%w: %s %s %s..%s",
			constants.ErrNoCandles, params.Symbol, params.Timeframe,
			params.From.Format(time.RFC3339), params.To.Format(time.RFC3339))
	}
	return result, nil
}

// validateParams rejects a run that could not produce a meaningful number.
func validateParams(params backtest.RunParams) error {
	if params.Strategy == nil {
		return errors.New("backtest: no strategy given")
	}
	if !params.MarketType.Valid() {
		return fmt.Errorf("backtest: %q is not a market type", params.MarketType)
	}
	if !params.Timeframe.Valid() {
		return fmt.Errorf("backtest: %q is not a timeframe", params.Timeframe)
	}
	if !params.GapPolicy.Valid() {
		return fmt.Errorf("backtest: %q is not a gap policy", params.GapPolicy)
	}
	if !params.To.After(params.From) {
		return fmt.Errorf("backtest: range %s..%s ends before it starts",
			params.From.Format(time.RFC3339), params.To.Format(time.RFC3339))
	}
	if !params.InitialEquity.IsPositive() {
		return fmt.Errorf("backtest: initial equity %s is not positive", params.InitialEquity)
	}
	if !params.Costs.TickSize.IsPositive() {
		return fmt.Errorf("backtest: tick size %s is not positive", params.Costs.TickSize)
	}
	if params.Costs.FeeTakerPct.IsNegative() {
		return fmt.Errorf("backtest: taker fee %s is negative", params.Costs.FeeTakerPct)
	}
	if params.Costs.FeeMakerPct.IsNegative() {
		return fmt.Errorf("backtest: maker fee %s is negative", params.Costs.FeeMakerPct)
	}
	if err := params.Execution.Validate(); err != nil {
		return err
	}
	if err := params.Sizing.Validate(); err != nil {
		return err
	}
	return nil
}

// runner is the mutable state of one run. It exists so the loop body can stay
// a method rather than a closure over a dozen variables.
type runner struct {
	params   backtest.RunParams
	log      *slog.Logger
	set      *_indicator_us.Set
	excluded excludedRegions

	// ctx belongs to the run. onCandle is called from StreamCandles, which
	// does not pass one through, and the aligner reads the database.
	ctx context.Context

	warmupBars int

	// pending are the intents produced by the previous bar, waiting for this
	// bar's open. There is never more than one bar of delay.
	pending []strategy.Intent

	position *openPosition
	equity   decimal.Decimal

	trades []backtest.Trade
	curve  []backtest.EquityPoint

	barsEvaluated      int64
	barsSkippedWarmup  int64
	barsSkippedGap     int64
	ambiguousBars      int64
	barsVetoed         int64
	barsFilterNotReady int64
	entriesSizeCapped  int64

	firstBar time.Time
	lastBar  time.Time

	// lastTradeableClose is the most recent close at which a position could
	// still have been liquidated. A forced exit uses it rather than a price
	// from inside a period nobody could trade.
	lastTradeableClose decimal.Decimal
	lastTradeableTime  time.Time

	// pendingATR is the ATR at the close that produced r.pending. The fill
	// happens on the next bar's open, so the volatility the decision was made
	// under has to be carried across with the intents.
	pendingATR float64

	// pendingClose is the close of the bar that produced r.pending. A limit
	// entry rests at that price, because that is what an order placed at the
	// moment of the signal would have been priced at.
	pendingClose decimal.Decimal

	// resting is the limit entry waiting for price to come to it, if any.
	resting *restingOrder

	makerEntries, takerEntries int64
	makerExits, takerExits     int64

	// entriesRequested counts entry intents the engine acted on, filled or
	// not. It is the denominator for the cancellation rate: the share of a
	// strategy's intent that never became a trade.
	entriesRequested   int64
	limitOrdersExpired int64
}

// restingOrder is a limit entry sitting on the book.
//
// # Why the whole intent is carried
//
// The order was placed on the strength of a decision made at one bar's close,
// and it fills — if it fills — some bars later. The stop, the target and the
// ATR all belong to the moment of the decision, not to the moment of the fill:
// re-deriving them at fill time would size the position against volatility the
// strategy never saw.
type restingOrder struct {
	direction constants.Direction
	limit     decimal.Decimal
	intent    strategy.Intent
	entryATR  float64

	placedAt time.Time
	// barsRested counts how many bars the order has been offered to. It is
	// compared against Execution.Timeout().
	barsRested int
}

// fillableBy reports whether the bar traded through the resting price.
//
// A buy fills when the bar's low reaches down to the limit, a sell when the
// high reaches up to it. Touch is treated as a fill, which is optimistic and
// reported as such: queue position is real, and being at the front of the book
// is not automatic.
func (o *restingOrder) fillableBy(bar models.Candle) bool {
	if o.direction == constants.DirectionLong {
		return bar.Low.LessThanOrEqual(o.limit)
	}
	return bar.High.GreaterThanOrEqual(o.limit)
}

// newRunner prepares the per-run state.
func (u *backtestUsecase) newRunner(
	params backtest.RunParams,
	gaps []models.DataGap,
	outages []backtest.UntradeableWindow,
) (*runner, error) {
	set, err := _indicator_us.NewSet(u.indicator)
	if err != nil {
		return nil, fmt.Errorf("backtest: %w", err)
	}

	// The engine waits for whichever warm-up is longer. An indicator that is
	// not converged and a strategy that has not seen enough history are the
	// same failure — a decision made on values that do not mean yet what they
	// will mean later.
	warmup := max(set.WarmupPeriod(), params.Strategy.WarmupPeriod())

	return &runner{
		params:     params,
		log:        u.log,
		set:        set,
		excluded:   newExcludedRegions(params, gaps, outages),
		warmupBars: warmup,
		equity:     params.InitialEquity,
	}, nil
}

// onCandle is the loop body, called once per stored candle in order.
func (r *runner) onCandle(bar models.Candle) error {
	ctx := r.ctx
	// Indicators are fed every bar without exception, including bars that are
	// not scored. They are stateful: skipping one would leave them
	// permanently out of step with the series, and the same code feeds them
	// live.
	snapshot, ready := r.set.Update(bar)

	// Warm-up loaded from before the requested range is not part of the run
	// and is not counted as anything.
	if bar.OpenTime.Before(r.params.From) {
		return nil
	}
	if bar.OpenTime.After(r.params.To) {
		return errStopScan
	}

	if !ready {
		r.barsSkippedWarmup++
		return nil
	}

	tradeable := r.excluded.tradeableAt(bar.OpenTime)
	if !tradeable {
		// A position cannot be carried across a period whose data is missing
		// or during which no order could have been placed. It is closed at the
		// last price at which closing was possible.
		r.forceClose(bar)
		r.pending = nil
		r.barsSkippedGap++
		return nil
	}

	// 1. A gap has no candles in it — that is what makes it a gap — so the
	//    stream jumps over the hole rather than offering a bar inside it.
	//    Detecting the jump is the only way a real gap can force a position
	//    out; waiting to be handed an excluded bar works for an outage, where
	//    the candles exist, and never fires for missing data.
	if r.position.isOpen() && !r.lastTradeableTime.IsZero() &&
		r.excluded.crossedBetween(r.lastTradeableTime, bar.OpenTime) {
		r.forceClose(bar)
		r.pending = nil
	}

	// 2a. A limit entry placed earlier fills if this bar comes to its price,
	//     before anything else happens on the bar: it was on the book first.
	r.resolveResting(bar)

	// 2b. Fill what the previous bar asked for, at this bar's open.
	if err := r.applyPending(bar); err != nil {
		return err
	}

	// 3. Levels are checked against this bar's range, after any entry that
	//    just filled on its open — an entry and its stop can resolve within
	//    the same bar, and pretending otherwise would hide the worst case.
	r.checkLevels(bar)

	// 4. Mark the account to this bar's close.
	r.markEquity(bar)

	// 5. Only now does the strategy see the bar, with the position state as
	//    it stands after everything above.
	r.pending = r.params.Strategy.OnBar(strategy.BarContext{
		Candle:     bar,
		Indicators: snapshot,
		Position:   r.positionView(),
	})
	r.pendingATR = snapshot.ATR
	r.pendingClose = bar.Close

	// 6. The trend filter vetoes entries the higher timeframes do not permit.
	//    It runs on the same bar the strategy decided on, using the same
	//    information the strategy had — vetoing at fill time instead would
	//    judge a decision against data that arrived after it was made.
	if err := r.applyTrendVeto(ctx, bar, snapshot); err != nil {
		return err
	}
	r.applyLevelIntents()

	r.barsEvaluated++
	if r.firstBar.IsZero() {
		r.firstBar = bar.OpenTime
	}
	r.lastBar = bar.OpenTime
	r.lastTradeableClose = bar.Close
	r.lastTradeableTime = bar.OpenTime

	if r.position.isOpen() {
		r.position.barsHeld++
	}
	return nil
}

// positionView is the read-only copy the strategy is given.
func (r *runner) positionView() strategy.Position {
	if !r.position.isOpen() {
		return strategy.Position{Direction: constants.DirectionFlat}
	}
	return strategy.Position{
		Direction:  r.position.direction,
		EntryPrice: r.position.entryPrice,
		EntryTime:  r.position.entryTime,
		Size:       r.position.size,
		Stop:       r.position.stop,
		Target:     r.position.target,
		BarsHeld:   r.position.barsHeld,
	}
}

// applyPending fills the previous bar's intents at this bar's open.
//
// Entries and exits are resolved first, then any levels in the same batch are
// attached to whatever position now exists. The order is what makes
// "enter with a stop" work: a strategy returning EnterLong and SetStop
// together is asking for a protected position, and arming the stop a bar
// later would leave it exposed through exactly the move it was placed for.
func (r *runner) applyPending(bar models.Candle) error {
	intents := r.pending
	entryATR := r.pendingATR
	r.pending = nil

	for _, intent := range intents {
		switch intent.Kind {
		case strategy.IntentEnterLong:
			r.enter(bar, constants.DirectionLong, intent, entryATR)

		case strategy.IntentEnterShort:
			// A spot backtest that shorts is fiction, so this ends the run
			// rather than warning. Silently dropping the intent would be
			// worse than either: the strategy would appear to have been
			// measured while half its decisions were discarded.
			if r.params.MarketType == constants.MarketTypeSpot {
				return fmt.Errorf("%w: %s wanted a short on %s at %s",
					constants.ErrShortOnSpot, r.params.Strategy.Name(),
					r.params.Symbol, bar.OpenTime.Format(time.RFC3339))
			}
			r.enter(bar, constants.DirectionShort, intent, entryATR)

		case strategy.IntentExit:
			if r.position.isOpen() {
				r.closeAt(bar.OpenTime, bar.Open, backtest.ExitStrategy, intent.Reason, false)
			}

		case strategy.IntentSetStop, strategy.IntentSetTarget:
			// Attached below, once the entries in this batch have filled.

		default:
			return fmt.Errorf("backtest: %s returned an unknown intent %q",
				r.params.Strategy.Name(), intent.Kind)
		}
	}

	r.attachLevels(intents)
	return nil
}

// attachLevels sets the stop and target carried by a batch of intents.
//
// Silently dropping them when no position is open would be the worst
// available behaviour: the strategy would appear to be running protected
// while it was not, and the backtest would report the unprotected result as
// though it were the strategy's own.
func (r *runner) attachLevels(intents []strategy.Intent) {
	if !r.position.isOpen() {
		return
	}

	for _, intent := range intents {
		switch intent.Kind {
		case strategy.IntentSetStop:
			r.position.stop = intent.Price
		case strategy.IntentSetTarget:
			r.position.target = intent.Price
		}
	}
}

// applyLevelIntents attaches stops and targets requested by this bar.
//
// They take effect immediately rather than at the next open because a level
// is a threshold, not a fill: waiting a bar to arm a stop would let a position
// sit unprotected through exactly the move the stop was placed for.
//
// When no position is open the level intents are deliberately left in pending.
// They belong to an entry in the same batch that has not filled yet, and
// applyPending attaches them the moment it does.
func (r *runner) applyLevelIntents() {
	if !r.position.isOpen() {
		return
	}
	r.attachLevels(r.pending)
}

// openAt opens a position at this bar's open, if none is held.
//
// A second entry while a position is open is dropped rather than pyramided:
// the position model is one at a time, and silently averaging in would make
// the reported entry price something no single order ever paid.
// reference is the price the fill is measured from and maker says whether it
// rested on the book: a resting order fills at its own price and pays no
// slippage, which is the entire reason to use one.
func (r *runner) openAt(
	bar models.Candle,
	direction constants.Direction,
	intent strategy.Intent,
	entryATR float64,
	reference decimal.Decimal,
	maker bool,
) {
	if r.position.isOpen() {
		return
	}

	buying := direction == constants.DirectionLong
	price := reference
	if !maker {
		price = fillPrice(reference, buying, r.params.Costs)
	}
	if !price.IsPositive() {
		return
	}

	size, capped := r.sizeFor(price, intent.Stop)
	if !size.IsPositive() {
		return
	}
	if capped {
		r.entriesSizeCapped++
	}

	notional := size.Mul(price)
	r.position = &openPosition{
		direction:      direction,
		entryTime:      bar.OpenTime,
		entryPrice:     price,
		entryReference: reference,
		size:           size,
		equityAtEntry:  r.equity,
		entryFee:       feeOn(notional, r.params.Costs, maker),
		entryMaker:     maker,
		entryNote:      intent.Reason,
		entryATR:       entryATR,
		stop:           intent.Stop,
		target:         intent.Target,
	}

	if maker {
		r.makerEntries++
	} else {
		r.takerEntries++
	}
}

// sizeFor decides how much the position commits, and reports whether the
// answer had to be capped.
//
// # The cap is not a detail
//
// Fixed-fractional sizing solves for a size whose loss at the stop equals the
// risk budget. With a tight stop that size implies more notional than the
// account holds — a 0.1% stop distance at 1% risk is ten times the equity,
// which on a spot account is not a smaller position, it is an impossible one.
//
// Capping at all-in is the only honest answer, but a capped entry is no longer
// risking what the configuration says. Runs count how often it happens, because
// a strategy whose sizing is constantly capped will show a drawdown far worse
// than its risk setting implies and nothing else would explain why.
func (r *runner) sizeFor(price, stop decimal.Decimal) (size decimal.Decimal, capped bool) {
	// All-in: commit the whole account, fee included. Dividing by (1 + fee) is
	// what makes the entry affordable rather than one fee over budget.
	feeRate := r.params.Costs.FeeTakerPct.Div(hundred)
	affordable := r.equity.Div(price.Mul(decimal.NewFromInt(1).Add(feeRate)))

	if r.params.Sizing.Mode == backtest.SizingAllIn {
		return affordable, false
	}

	// Without a stop there is no distance to size against. Falling back to
	// all-in silently would be the worst option: the run would report
	// fixed-fractional sizing while committing everything.
	distance := price.Sub(stop).Abs()
	if !stop.IsPositive() || !distance.IsPositive() {
		return decimal.Zero, false
	}

	risked := r.equity.Mul(r.params.Sizing.RiskPct).Div(hundred)
	wanted := risked.Div(distance)

	if wanted.GreaterThan(affordable) {
		return affordable, true
	}
	return wanted, false
}

// checkLevels resolves a stop or target reached inside this bar.
func (r *runner) checkLevels(bar models.Candle) {
	if !r.position.isOpen() {
		return
	}

	reason, level, ambiguous, hit := r.position.levelHitBy(bar)
	if !hit {
		return
	}
	if ambiguous {
		r.ambiguousBars++
	}

	// The level is the reference; slippage is applied to it exactly as to any
	// other fill, because a stop is a market order once it triggers.
	r.closeAt(bar.OpenTime, level, reason, string(reason), ambiguous)
}

// exitIsMaker decides whether an exit rested on the book.
//
// # A stop is never a maker fill, under any configuration
//
// This is a switch on the reason rather than a check of the configuration,
// because the rule has to hold structurally. A stop that only filled at its
// limit price is a stop that does not fill when the market gaps through it —
// which is exactly the situation stops exist for. Modelling one as a resting
// order would delete the worst losses from the record and produce a strategy
// that looks robust because its tail was quietly removed.
//
// Only a target can rest: reaching a target means price came to a price the
// position was already willing to sell at, which is what a resting order is.
// Everything else — a gap forcing the position out, the end of the run — is a
// market order because nothing was resting at that price.
func (r *runner) exitIsMaker(reason backtest.ExitReason) bool {
	if r.params.Execution.Exit() != constants.OrderTypeLimit {
		return false
	}
	return reason == backtest.ExitTarget
}

// closeAt closes the open position and records the trade.
//
// reference is the price the exit was triggered at and fill is what it
// actually got after slippage. Both are needed: the account follows the fill,
// while gross PnL is measured from the reference so that slippage is
// reported as a cost instead of vanishing into the price.
func (r *runner) closeAt(at time.Time, reference decimal.Decimal, reason backtest.ExitReason, note string, ambiguous bool) {
	p := r.position
	if !p.isOpen() {
		return
	}

	maker := r.exitIsMaker(reason)

	fill := reference
	if !maker {
		fill = fillPrice(reference, p.direction == constants.DirectionShort, r.params.Costs)
	}
	exitFee := feeOn(fill.Mul(p.size), r.params.Costs, maker)

	fees := p.entryFee.Add(exitFee)
	slippage := p.slippageCost(r.params.Costs, maker)
	costs := fees.Add(slippage)
	gross := p.grossPnL(reference)

	// The account follows the fills; net is gross less every cost. The two
	// agree by construction — realised = gross - slippage — and the
	// guaranteed-loss fixture exists to keep them agreeing.
	r.equity = p.equityAtEntry.Sub(fees).Add(p.realisedPnL(fill))

	if maker {
		r.makerExits++
	} else {
		r.takerExits++
	}

	r.trades = append(r.trades, backtest.Trade{
		Direction:                  p.direction,
		EntryTime:                  p.entryTime,
		EntryPrice:                 p.entryPrice,
		ExitTime:                   at,
		ExitPrice:                  fill,
		Size:                       p.size,
		GrossPnL:                   gross,
		Costs:                      costs,
		Fees:                       fees,
		Slippage:                   slippage,
		NetPnL:                     gross.Sub(costs),
		ExitReason:                 reason,
		EntryNote:                  p.entryNote,
		ExitNote:                   note,
		EntryATR:                   p.entryATR,
		EntryMaker:                 p.entryMaker,
		ExitMaker:                  maker,
		StopAndTargetBothReachable: ambiguous,
		ForcedByGap:                reason == backtest.ExitGapForced,
	})
	r.position = nil
}

// forceClose liquidates at the last price at which liquidation was possible.
//
// Using the current bar would be wrong twice over: inside an outage no order
// could have been filled at all, and inside a gap there is no price to speak
// of. The last tradeable close is the last thing that was actually true.
func (r *runner) forceClose(bar models.Candle) {
	if !r.position.isOpen() {
		return
	}
	if r.lastTradeableClose.IsZero() {
		return
	}

	r.closeAt(r.lastTradeableTime, r.lastTradeableClose, backtest.ExitGapForced,
		"data untrustworthy or market halted", false)
	r.log.Debug("position force-closed",
		"at", r.lastTradeableTime.Format(time.RFC3339),
		"resumes_after", bar.OpenTime.Format(time.RFC3339))
}

// markEquity appends this bar's account value to the curve.
//
// The close is the reference, not the fill: equityAt applies the slippage an
// exit would suffer, so the curve and a trade closed at that same price agree.
func (r *runner) markEquity(bar models.Candle) {
	equity := r.equity
	if r.position.isOpen() {
		equity = r.position.equityAt(bar.Close, r.params.Costs)
	}
	r.curve = append(r.curve, backtest.EquityPoint{OpenTime: bar.OpenTime, Equity: equity})
}

// finish closes anything still open at the end of the range.
//
// It fills at the final bar's close rather than a following open, because
// there is no following bar. This is not the look-ahead §4 forbids: no
// decision is being made on that price, the engine is liquidating so the
// reported return is realised rather than half on paper.
func (r *runner) finish() {
	if !r.position.isOpen() || r.lastTradeableClose.IsZero() {
		return
	}

	r.closeAt(r.lastTradeableTime, r.lastTradeableClose, backtest.ExitEndOfRun,
		"range ended with a position open", false)

	// The final curve point predates the liquidation cost, so it is corrected
	// rather than appended to: the curve has one point per evaluated bar and
	// that invariant is what drawdown is computed against.
	if len(r.curve) > 0 {
		r.curve[len(r.curve)-1].Equity = r.equity
	}
}

// fill copies the run's state onto the result.
func (r *runner) fill(result *backtest.Result) {
	result.BarsEvaluated = r.barsEvaluated
	result.BarsVetoed = r.barsVetoed
	result.EntriesSizeCapped = r.entriesSizeCapped
	result.BarsFilterNotReady = r.barsFilterNotReady
	result.BarsSkippedWarmup = r.barsSkippedWarmup
	result.BarsSkippedGap = r.barsSkippedGap
	result.AmbiguousBars = r.ambiguousBars
	result.FirstBar = r.firstBar
	result.LastBar = r.lastBar
	result.TrendUnavailable = r.params.TrendUnavailable
	result.MakerEntries = r.makerEntries
	result.TakerEntries = r.takerEntries
	result.MakerExits = r.makerExits
	result.TakerExits = r.takerExits
	result.EntriesRequested = r.entriesRequested
	result.LimitOrdersExpired = r.limitOrdersExpired
	result.Trades = r.trades
	result.Equity = r.curve
}

// applyTrendVeto removes entry intents the trend filter does not permit.
//
// # Why the veto lands here rather than at fill time
//
// The strategy decided on this bar's close using this bar's information. The
// filter judges that decision with the same information: the higher-timeframe
// readings that had closed by the same instant. Vetoing at fill time would
// judge it against a bar that arrived after the decision was made, which is
// the cross-timeframe version of the mistake phase 04 §4 exists to prevent.
//
// Exits are never vetoed. A filter that could trap a position in the market
// would be making a trading decision, and this one is only allowed to refuse
// entries.
func (r *runner) applyTrendVeto(ctx context.Context, bar models.Candle, snapshot models.IndicatorSnapshot) error {
	if !r.params.Filtered() {
		return nil
	}

	views, err := r.params.TrendAligner.Advance(ctx, bar.CloseTime)
	if err != nil {
		return fmt.Errorf("advance the trend filter: %w", err)
	}

	state := r.params.TrendFilter.OnBar(trend.BarContext{
		Candle:     bar,
		Indicators: snapshot,
		Higher:     views,
	})
	if !state.Ready {
		r.barsFilterNotReady++
	}

	kept := r.pending[:0]
	vetoed := false

	for _, intent := range r.pending {
		direction := entryDirection(intent.Kind)
		if direction == constants.DirectionFlat || state.Permits(direction) {
			kept = append(kept, intent)
			continue
		}
		vetoed = true
	}

	r.pending = kept
	if vetoed {
		r.barsVetoed++
	}
	return nil
}

// entryDirection maps an intent to the side it would open, or flat when it
// opens nothing.
func entryDirection(kind strategy.IntentKind) constants.Direction {
	switch kind {
	case strategy.IntentEnterLong:
		return constants.DirectionLong
	case strategy.IntentEnterShort:
		return constants.DirectionShort
	default:
		return constants.DirectionFlat
	}
}

// enter acts on an entry intent, either crossing the spread now or resting on
// the book until price comes to it.
//
// The limit price is the close of the bar the strategy decided on, not this
// bar's open. That is what a resting order placed at the moment of the signal
// would have been priced at, and using the later open would credit the model
// with information the order did not have.
func (r *runner) enter(
	bar models.Candle,
	direction constants.Direction,
	intent strategy.Intent,
	entryATR float64,
) {
	if r.position.isOpen() || r.resting != nil {
		return
	}
	r.entriesRequested++

	if r.params.Execution.Entry() != constants.OrderTypeLimit {
		r.openAt(bar, direction, intent, entryATR, bar.Open, false)
		return
	}

	// The signal bar's close, which this bar's open follows.
	limit := r.pendingClose
	if !limit.IsPositive() {
		limit = bar.Open
	}
	r.resting = &restingOrder{
		direction: direction,
		limit:     limit,
		intent:    intent,
		entryATR:  entryATR,
		placedAt:  bar.OpenTime,
	}
	// The bar the order is placed into is itself an opportunity to fill.
	r.resolveResting(bar)
}

// resolveResting fills or ages the order waiting on the book.
//
// An order that times out is cancelled and no trade happens. Those are counted
// rather than discarded: a strategy whose limit entries fill only when price
// moves against it has adverse selection — it gets filled on the trades it
// should have skipped and misses the ones it wanted — and that is invisible in
// the headline number.
func (r *runner) resolveResting(bar models.Candle) {
	order := r.resting
	if order == nil {
		return
	}
	if r.position.isOpen() {
		// Something else opened first; the order has nothing to fill into.
		r.resting = nil
		r.limitOrdersExpired++
		return
	}

	if order.fillableBy(bar) {
		r.resting = nil
		r.openAt(bar, order.direction, order.intent, order.entryATR, order.limit, true)
		return
	}

	order.barsRested++
	if order.barsRested >= r.params.Execution.Timeout() {
		r.resting = nil
		r.limitOrdersExpired++
	}
}
