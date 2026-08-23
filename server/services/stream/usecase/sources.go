package usecase

import (
	"context"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/candle"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/outcome"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/stream"
)

// CandleFeed publishes every kline the exchange sends, forming bars included.
//
// # Why the api opens its own stream
//
// The forming bar exists only in memory, and the collector's memory is a
// different process. The api cannot read it, and storing it is forbidden —
// CLAUDE.md §3.1 — so the only way to show a live price is to watch the same
// public feed.
//
// This connection is read-only in the strongest available sense: the type
// holds no repository, so there is nothing here that could write a candle
// even by mistake. It forwards and forgets.
type CandleFeed struct {
	Source     stream.CandleSource
	Symbol     string
	MarketType constants.MarketType
	Timeframes []constants.Timeframe
}

// Run watches the exchange until ctx is cancelled.
func (f CandleFeed) Run(ctx context.Context, publish func(stream.Event)) error {
	return f.Source.Watch(ctx, f.Symbol, f.MarketType, f.Timeframes,
		func(c models.Candle) {
			publish(stream.Event{
				Topic: stream.TopicCandles,
				// The bar's own instant, not now: a client's cursor has to
				// mean something about the market rather than about delivery.
				At: c.OpenTime.UTC(),
				// Rendered with the same function GET /api/v1/candles uses,
				// so is_closed is one field in one place rather than two
				// spellings a client has to know about.
				Payload: candle.ToCandleResponse(c),
			})
		})
}

// SignalPoller publishes signals as they are recorded.
//
// # Why polling
//
// The collector writes signals and the api serves them; they share a database
// and nothing else. A poll on a short interval is the whole mechanism. It is
// not elegant, and it is honest about the latency it adds — which a client
// can see, because the event carries the signal's own time.
type SignalPoller struct {
	Signals    signal.SignalUsecase
	Symbol     string
	MarketType constants.MarketType
	Interval   time.Duration

	// seen is the newest signal already published, so a poll sends only what
	// arrived since.
	seen time.Time
}

// Run polls until ctx is cancelled.
func (p *SignalPoller) Run(ctx context.Context, publish func(stream.Event)) error {
	interval := p.Interval
	if interval <= 0 {
		interval = constants.StreamPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// The first poll establishes the watermark without publishing: a client
	// connecting should not receive the whole history as though it were new.
	if latest, err := p.newest(ctx); err == nil {
		p.seen = latest
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		signals, _, err := p.Signals.ListSignals(ctx, signal.ListParams{
			Symbol: p.Symbol, MarketType: p.MarketType,
			Limit: constants.StreamPollBatch,
		})
		if err != nil {
			continue
		}

		// Oldest first, so a client receiving several sees them in the order
		// they happened.
		for i := len(signals) - 1; i >= 0; i-- {
			s := signals[i]
			if !s.SignalTime.After(p.seen) {
				continue
			}
			p.seen = s.SignalTime
			publish(stream.Event{
				Topic: stream.TopicSignals, At: s.SignalTime.UTC(),
				// Without the reason, as on the list endpoint: it is large,
				// and a client that wants it fetches the signal by id.
				Payload: signal.ToSignalResponse(s, false),
			})
		}
	}
}

func (p *SignalPoller) newest(ctx context.Context) (time.Time, error) {
	signals, _, err := p.Signals.ListSignals(ctx, signal.ListParams{
		Symbol: p.Symbol, MarketType: p.MarketType, Limit: 1,
	})
	if err != nil || len(signals) == 0 {
		return time.Time{}, err
	}
	return signals[0].SignalTime, nil
}

// OutcomePoller publishes outcomes as they resolve.
type OutcomePoller struct {
	Outcomes   outcome.OutcomeUsecase
	Symbol     string
	MarketType constants.MarketType
	Interval   time.Duration

	// published is the outcomes already sent, by signal id and status, so a
	// row that changes from open to stop is sent again and one that has not
	// changed is not.
	published map[string]string
}

// Run polls until ctx is cancelled.
func (p *OutcomePoller) Run(ctx context.Context, publish func(stream.Event)) error {
	interval := p.Interval
	if interval <= 0 {
		interval = constants.StreamPollInterval
	}
	if p.published == nil {
		p.published = map[string]string{}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	first := true
	for {
		resolved, _, err := p.Outcomes.ListOutcomes(ctx, outcome.ListParams{
			Symbol: p.Symbol, MarketType: p.MarketType,
			From: time.Time{}, To: time.Now().UTC().Add(time.Hour),
			Limit: constants.StreamPollBatch,
		})
		if err == nil {
			for i := len(resolved) - 1; i >= 0; i-- {
				row := resolved[i]
				key := row.Outcome.SignalId.String()
				if p.published[key] == row.Outcome.Status.String() {
					continue
				}
				p.published[key] = row.Outcome.Status.String()

				// The first pass establishes what is already there. Sending
				// it would hand a connecting client the whole history as
				// though it had just happened.
				if first {
					continue
				}
				publish(stream.Event{
					Topic: stream.TopicOutcomes, At: row.Signal.SignalTime.UTC(),
					Payload: outcome.ToOutcomeResponse(row),
				})
			}
		}
		first = false

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// StatusTicker publishes the pipeline status on a slow tick.
//
// Slow because it is a health view, not a feed: a phone does not need the
// oldest unresolved signal recomputed every second, and the query behind it
// touches three tables.
type StatusTicker struct {
	Pipeline pipeline.PipelineUsecase
	Interval time.Duration
}

// Run ticks until ctx is cancelled.
func (t StatusTicker) Run(ctx context.Context, publish func(stream.Event)) error {
	interval := t.Interval
	if interval <= 0 {
		interval = constants.StreamStatusInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if status, err := t.Pipeline.Status(ctx); err == nil {
			publish(stream.Event{
				Topic: stream.TopicStatus, At: status.ObservedAt,
				Payload: pipeline.ToStatusResponse(status),
			})
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
