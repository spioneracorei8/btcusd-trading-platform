package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/datagap"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market/repository/binance"
)

// Config is what the usecase needs to know about the deployment.
type Config struct {
	Symbol            string
	MarketType        constants.MarketType
	Timeframes        []constants.Timeframe
	BackfillFrom      time.Time
	GapcheckInterval  time.Duration
	HeartbeatInterval time.Duration

	// OnClosedCandle is called after a closed candle has been durably stored,
	// and is optional.
	//
	// # Why a callback rather than a dependency
	//
	// Ingestion's job is to keep the candle series complete, and it must not
	// acquire an opinion about what else the system does with a bar. A
	// signal evaluator passed in here would make the collector unable to
	// collect if signalling were misconfigured.
	//
	// It is called *after* the write, never instead of it, and whatever it
	// returns is its own business: the candle is the durable artefact and a
	// failure downstream of it must not cost one.
	OnClosedCandle func(ctx context.Context, bar models.Candle)
}

type marketUsecase struct {
	cfg Config
	log *slog.Logger

	marketData market.MarketDataRepository
	status     market.CollectorStatusRepository
	candles    candle.CandleUsecase
	gaps       datagap.DataGapUsecase

	cache   *LatestCandleCache
	backoff *binance.Backoff
	state   *stateMachine

	// historyWarned remembers which timeframes have already reported that the
	// exchange has no candles as far back as MARKET_BACKFILL_FROM.
	historyWarned *seenSet

	// now is injectable so tests can control the clock.
	now func() time.Time
}

// NewMarketUsecaseImpl builds the ingestion usecase.
func NewMarketUsecaseImpl(
	cfg Config,
	log *slog.Logger,
	marketData market.MarketDataRepository,
	status market.CollectorStatusRepository,
	candles candle.CandleUsecase,
	gaps datagap.DataGapUsecase,
) market.MarketUsecase {
	return &marketUsecase{
		cfg:           cfg,
		log:           log,
		marketData:    marketData,
		status:        status,
		candles:       candles,
		gaps:          gaps,
		cache:         NewLatestCandleCache(),
		backoff:       binance.NewBackoff(),
		state:         newStateMachine(time.Now().UTC()),
		historyWarned: &seenSet{seen: make(map[constants.Timeframe]bool)},
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// seenSet records which timeframes something has already been said about.
//
// It exists for log-noise control only: the backward fill is re-attempted on
// every reconnect and decides what to fetch from the stored series, never from
// this. Guarded because status reads arrive on another goroutine.
type seenSet struct {
	mu   sync.Mutex
	seen map[constants.Timeframe]bool
}

// mark records a timeframe and reports whether it had been seen before.
func (s *seenSet) mark(timeframe constants.Timeframe) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	already := s.seen[timeframe]
	s.seen[timeframe] = true
	return already
}

// LatestOpenCandle returns the forming bar held in memory.
func (u *marketUsecase) LatestOpenCandle(timeframe constants.Timeframe) (models.Candle, bool) {
	return u.cache.Get(timeframe)
}

// Run drives ingestion until the context is cancelled.
//
// The shape is: register, backfill, then stream. Every reconnect backfills
// again before resuming the live feed, so the stored sequence stays ordered
// and the bars missed while disconnected are filled before newer ones arrive.
func (u *marketUsecase) Run(ctx context.Context) error {
	if _, err := u.status.RegisterStart(ctx, u.cfg.Symbol, u.cfg.MarketType); err != nil {
		return err // the repository already names the operation
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error { return u.heartbeatLoop(groupCtx) })
	group.Go(func() error { return u.gapcheckLoop(groupCtx) })
	group.Go(func() error { return u.ingestLoop(groupCtx) })

	err := group.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// ingestLoop backfills and streams, reconnecting for as long as ctx is live.
func (u *marketUsecase) ingestLoop(ctx context.Context) error {
	firstConnection := true

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		// Backfill before every connection, not only the first: the bars
		// missed while disconnected must land before the live feed resumes,
		// or the series would gain a hole that only the next scan notices.
		u.setState(ctx, constants.CollectorBackfilling)
		if err := u.Backfill(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			u.log.ErrorContext(ctx, "backfill failed, retrying after backoff", "error", err)
			u.setState(ctx, constants.CollectorReconnecting)
			if waitErr := u.waitBackoff(ctx); waitErr != nil {
				return nil
			}
			continue
		}

		streamErr := u.stream(ctx, firstConnection)
		if ctx.Err() != nil {
			return nil
		}
		firstConnection = false

		u.logDisconnect(ctx, streamErr)
		u.setState(ctx, constants.CollectorReconnecting)
		if err := u.waitBackoff(ctx); err != nil {
			return nil
		}
	}
}

// stream opens the live feed and consumes it until it ends.
func (u *marketUsecase) stream(ctx context.Context, firstConnection bool) error {
	connectedAt := u.now()

	if err := u.status.MarkConnected(ctx, u.cfg.Symbol, u.cfg.MarketType, !firstConnection); err != nil {
		u.log.WarnContext(ctx, "could not record the connection", "error", err)
	}
	u.backoff.Reset()
	u.setState(ctx, constants.CollectorLive)
	u.log.InfoContext(ctx, "market data stream connected",
		"symbol", u.cfg.Symbol,
		"timeframes", timeframeNames(u.cfg.Timeframes),
		"reconnect", !firstConnection,
	)

	// The writer owns every database write, so the stream callback never
	// blocks on I/O and the reader can keep pace with the exchange.
	closed := make(chan models.Candle, constants.ClosedCandleBufferSize)
	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error { return u.writeLoop(groupCtx, closed) })

	group.Go(func() error {
		defer close(closed)

		return u.marketData.StreamKlines(groupCtx, market.StreamParams{
			Symbol:     u.cfg.Symbol,
			MarketType: u.cfg.MarketType,
			Timeframes: u.cfg.Timeframes,
		}, func(kline market.StreamedKline) {
			u.route(groupCtx, kline.Candle, closed)
		})
	})

	err := group.Wait()

	note := "stream ended"
	if err != nil {
		note = err.Error()
	}
	if markErr := u.status.MarkDisconnected(context.WithoutCancel(ctx), u.cfg.Symbol, u.cfg.MarketType, truncateNote(note)); markErr != nil {
		u.log.WarnContext(ctx, "could not record the disconnection", "error", markErr)
	}
	u.log.InfoContext(ctx, "market data stream ended", "uptime", u.now().Sub(connectedAt).String())

	return err
}

// route sends a closed candle to the writer and keeps a forming one in memory.
//
// This is the single most consequential branch in the collector: k.x decides
// whether a bar is a fact or a work in progress.
func (u *marketUsecase) route(ctx context.Context, c models.Candle, closed chan<- models.Candle) {
	if !c.IsClosed {
		u.cache.Put(c)
		return
	}

	// The bar is final, so the cached forming version is obsolete.
	u.cache.Drop(c.Timeframe)

	// Sending is always preferred, including during shutdown: a select that
	// also watched ctx.Done() here would pick at random and could drop a
	// confirmed candle while buffer space was available.
	select {
	case closed <- c:
		return
	default:
	}

	// Blocking is the correct behaviour: dropping a candle silently is the
	// worst failure this system has, and back pressure on the reader is
	// recoverable where a missing bar is not.
	u.log.ErrorContext(ctx, "closed candle buffer is full, blocking the reader",
		"capacity", constants.ClosedCandleBufferSize,
		"timeframe", c.Timeframe.String(),
	)
	select {
	case closed <- c:
	case <-ctx.Done():
	}
}

// writeLoop persists closed candles one at a time, in arrival order.
//
// It ranges to completion rather than selecting on ctx.Done(), and that is the
// whole point. errgroup cancels the context the instant StreamKlines returns,
// which happens on every ordinary disconnect, while the buffer may still hold
// up to ClosedCandleBufferSize candles the exchange already confirmed. Bailing
// out on cancellation would discard them silently — a hole in the series that
// nothing would report until a gap scan noticed it much later.
//
// The producer closes the channel, so the loop always terminates.
func (u *marketUsecase) writeLoop(ctx context.Context, closed <-chan models.Candle) error {
	for c := range closed {
		if ctx.Err() != nil {
			return u.drainRemaining(ctx, c, closed)
		}
		if err := u.candles.SaveCandle(ctx, c); err != nil {
			return storeCandleError(c, err)
		}
		u.observeClosed(ctx, c)
	}
	return nil
}

// observeClosed hands a stored candle to whatever is watching.
//
// Guarded rather than assumed, because most processes configure nothing here,
// and a panic in an observer must not take ingestion down with it — the
// collector host has one job and it is not this one.
func (u *marketUsecase) observeClosed(ctx context.Context, c models.Candle) {
	if u.cfg.OnClosedCandle == nil {
		return
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			u.log.ErrorContext(ctx, "the closed-candle observer panicked; ingestion continues",
				"panic", recovered,
				"timeframe", c.Timeframe.String(),
				"open_time", c.OpenTime.UTC().Format(time.RFC3339))
		}
	}()

	u.cfg.OnClosedCandle(ctx, c)
}

// drainRemaining finishes candles that were already accepted when the context
// was cancelled.
//
// They are written on a detached context bounded by the shutdown budget,
// which is the "finish in-flight writes" half of a clean SIGTERM. The window
// starts here, at cancellation, rather than when the writer began: a
// connection alive for a day must not inherit a deadline that expired long
// ago.
func (u *marketUsecase) drainRemaining(ctx context.Context, current models.Candle, closed <-chan models.Candle) error {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.ShutdownTimeout)
	defer cancel()

	if err := u.candles.SaveCandle(drainCtx, current); err != nil {
		return storeCandleError(current, err)
	}
	for c := range closed {
		if err := u.candles.SaveCandle(drainCtx, c); err != nil {
			return storeCandleError(c, err)
		}
	}
	return nil
}

func storeCandleError(c models.Candle, err error) error {
	return fmt.Errorf("store closed candle %s %s: %w",
		c.Timeframe, c.OpenTime.Format(time.RFC3339), err)
}

// heartbeatLoop publishes liveness for the api to read.
func (u *marketUsecase) heartbeatLoop(ctx context.Context) error {
	ticker := time.NewTicker(u.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current, err := u.status.FetchStatus(ctx, u.cfg.Symbol, u.cfg.MarketType)
			connected := err == nil && current.WSConnected

			if err := u.status.Heartbeat(ctx, u.cfg.Symbol, u.cfg.MarketType, connected); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				u.log.WarnContext(ctx, "heartbeat failed", "error", err)
			}
			u.checkStaleness(ctx, connected)
		}
	}
}

// checkStaleness reports the combination no other check catches: the stream
// says it is connected while the newest candle has gone cold.
func (u *marketUsecase) checkStaleness(ctx context.Context, connected bool) {
	// Only meaningful once live. A years-old candle during a backfill is
	// progress, and logging it as an error would train the reader to ignore
	// the one case that matters.
	if !connected || u.state.state() != constants.CollectorLive {
		return
	}

	latest, err := u.candles.FetchLatestCandle(ctx, u.cfg.Symbol, u.cfg.MarketType, constants.Timeframe1m)
	if err != nil {
		return
	}

	if age := u.now().Sub(latest.OpenTime); age > constants.StaleCandleThreshold {
		u.log.ErrorContext(ctx, "candles are stale while the stream reports connected",
			"age", age.String(),
			"threshold", constants.StaleCandleThreshold.String(),
			"latest_open_time", latest.OpenTime.Format(time.RFC3339),
		)
	}
}

// waitBackoff sleeps for the next reconnect delay.
func (u *marketUsecase) waitBackoff(ctx context.Context) error {
	delay := u.backoff.Next()
	u.log.InfoContext(ctx, "reconnecting after backoff",
		"delay", delay.String(), "attempt", u.backoff.Attempt())

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// logDisconnect records why the stream ended, at the level the cause deserves.
//
// An ordinary close is routine — Binance cycles every connection after 24
// hours — and logging it as an error would train the reader to ignore errors.
func (u *marketUsecase) logDisconnect(ctx context.Context, err error) {
	switch {
	case err == nil, errors.Is(err, constants.ErrStreamClosed):
		u.log.InfoContext(ctx, "market data stream disconnected", "error", err)
	case errors.Is(err, constants.ErrStreamStalled):
		u.log.WarnContext(ctx, "market data stream stalled, reconnecting", "error", err)
	default:
		u.log.ErrorContext(ctx, "market data stream failed", "error", err)
	}
}

func timeframeNames(timeframes []constants.Timeframe) []string {
	names := make([]string, 0, len(timeframes))
	for _, tf := range timeframes {
		names = append(names, tf.String())
	}
	return names
}

// truncateNote keeps a disconnect reason to a sensible length for the status
// row.
func truncateNote(note string) string {
	const limit = 500
	if len(note) <= limit {
		return note
	}
	return note[:limit]
}
