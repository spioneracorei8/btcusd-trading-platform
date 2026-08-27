package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/market"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/pipeline"
)

var observedAt = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// fakeMarket returns a prepared collector status.
type fakeMarket struct {
	status models.MarketStatus
	err    error

	askedAt time.Time
}

func (f *fakeMarket) Status(_ context.Context, now time.Time) (models.MarketStatus, error) {
	f.askedAt = now
	return f.status, f.err
}

func (f *fakeMarket) Run(context.Context) error      { return nil }
func (f *fakeMarket) Backfill(context.Context) error { return nil }
func (f *fakeMarket) LatestOpenCandle(constants.Timeframe) (models.Candle, bool) {
	return models.Candle{}, false
}

// fakeActivity returns prepared counts.
type fakeActivity struct {
	signals     pipeline.SignalActivity
	signalsErr  error
	delivery    pipeline.DeliveryActivity
	deliveryErr error
}

func (f *fakeActivity) SignalActivity(context.Context, string, constants.MarketType) (pipeline.SignalActivity, error) {
	return f.signals, f.signalsErr
}

func (f *fakeActivity) DeliveryActivity(context.Context, string, constants.MarketType) (pipeline.DeliveryActivity, error) {
	return f.delivery, f.deliveryErr
}

// healthy is a collector that started ten minutes ago and last beat a second
// ago, with a warm evaluator. Tests break the one thing they are about.
func healthy() models.MarketStatus {
	return models.MarketStatus{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframes: []models.TimeframeStatus{
			{Timeframe: constants.Timeframe1m},
			{Timeframe: constants.Timeframe4h},
		},
		Collector: models.CollectorStatus{
			Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
			State:       constants.CollectorLive,
			WSConnected: true,
			StartedAt:   observedAt.Add(-10 * time.Minute),
			UpdatedAt:   observedAt.Add(-time.Second),
			Evaluator: models.EvaluatorState{
				Strategy: "ema_crossover", Timeframe: constants.Timeframe4h, Ready: true,
			},
		},
	}
}

func statusOf(t *testing.T, marketStatus *fakeMarket, activity *fakeActivity, cfg Config) pipeline.Status {
	t.Helper()

	if cfg.Symbol == "" {
		cfg.Symbol = "BTCUSDT"
	}
	if !cfg.MarketType.Valid() {
		cfg.MarketType = constants.MarketTypeSpot
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	cfg.Now = func() time.Time { return observedAt }

	usecase, err := NewPipelineUsecaseImpl(activity, marketStatus, cfg)
	if err != nil {
		t.Fatalf("build the usecase: %v", err)
	}

	status, err := usecase.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return status
}

// concernsFor returns the details recorded against a component.
func concernsFor(status pipeline.Status, component string) []string {
	var out []string
	for _, c := range status.Concerns {
		if c.Component == component {
			out = append(out, c.Detail)
		}
	}
	return out
}

// TestAHealthyPipelineReportsNoConcerns, and reports them as an empty list
// rather than a null — a missing field reads as a check that did not run.
func TestAHealthyPipelineReportsNoConcerns(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		signals: pipeline.SignalActivity{
			LastSignalAt: observedAt.Add(-2 * time.Hour), SignalsTotal: 12,
		},
	}, Config{})

	if len(status.Concerns) != 0 {
		t.Fatalf("concerns = %v, want none", status.Concerns)
	}
	if status.Concerns == nil {
		t.Error("concerns is nil; it must be an empty list")
	}
}

// TestQuietIsNotAFault.
//
// # What this prevents
//
// The commonest misreading of this endpoint is treating silence as failure.
// A strategy at a tenth of a signal a day is quiet for weeks by design, so an
// old last_signal_at must not raise anything on its own. If it did, the
// concerns list would always be non-empty on a working deployment and would
// stop being read.
func TestQuietIsNotAFault(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		signals: pipeline.SignalActivity{
			LastSignalAt: observedAt.Add(-90 * 24 * time.Hour), SignalsTotal: 3,
		},
	}, Config{})

	if len(status.Concerns) != 0 {
		t.Fatalf("a signal 90 days old raised %v; quiet is normal here", status.Concerns)
	}
	if status.Evaluator.LastSignalAge == nil {
		t.Fatal("no last signal age; the age is what a person judges quiet by")
	}
	if got := *status.Evaluator.LastSignalAge; got != 90*24*time.Hour {
		t.Errorf("last signal age = %s, want 2160h", got)
	}
}

// TestAnEvaluatorThatIsNotReadySaysWhy.
//
// This is the gap the phase 07 audit found: warm-up state lived in the
// collector's memory, so "switched off", "still warming" and "stuck" all
// looked identical from outside. Configured and Reason are what separate them.
func TestAnEvaluatorThatIsNotReadySaysWhy(t *testing.T) {
	cold := healthy()
	cold.Collector.Evaluator = models.EvaluatorState{
		Strategy: "ema_crossover", Timeframe: constants.Timeframe4h,
		Ready:  false,
		Reason: "the strategy has seen 40 4h bars and needs 200 before it may decide",
	}

	status := statusOf(t, &fakeMarket{status: cold}, &fakeActivity{}, Config{})

	if !status.Evaluator.Configured {
		t.Fatal("configured is false while a strategy is named")
	}
	details := concernsFor(status, "evaluator")
	if len(details) != 1 {
		t.Fatalf("evaluator concerns = %v, want exactly one", details)
	}
	if !strings.Contains(details[0], "needs 200") {
		t.Errorf("the concern does not carry the reason: %q", details[0])
	}
}

// TestNoStrategyConfiguredIsNotAConcern. Running the collector without a
// strategy is a supported configuration — it collects and evaluates nothing —
// and must not look like a fault.
func TestNoStrategyConfiguredIsNotAConcern(t *testing.T) {
	off := healthy()
	off.Collector.Evaluator = models.EvaluatorState{}

	status := statusOf(t, &fakeMarket{status: off}, &fakeActivity{}, Config{})

	if status.Evaluator.Configured {
		t.Fatal("configured is true while no strategy is named")
	}
	if details := concernsFor(status, "evaluator"); len(details) != 0 {
		t.Fatalf("a switched-off evaluator raised %v", details)
	}
}

// TestAStaleHeartbeatIsReportedOnlyAfterThreeIntervals.
//
// One missed tick is a scheduling hiccup, not a dead process. Three is the
// threshold, and the message carries both the age and the interval so a reader
// does not have to know the configuration to judge it.
func TestAStaleHeartbeatIsReportedOnlyAfterThreeIntervals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		age       time.Duration
		wantStale bool
	}{
		{"one interval", 5 * time.Second, false},
		{"exactly three", 15 * time.Second, false},
		{"just past three", 15*time.Second + time.Millisecond, true},
		{"long gone", 10 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			late := healthy()
			late.Collector.UpdatedAt = observedAt.Add(-tc.age)

			status := statusOf(t, &fakeMarket{status: late}, &fakeActivity{},
				Config{HeartbeatInterval: 5 * time.Second})

			var stale bool
			for _, detail := range concernsFor(status, "collector") {
				if strings.Contains(detail, "heartbeat") {
					stale = true
				}
			}
			if stale != tc.wantStale {
				t.Fatalf("stale heartbeat reported = %v, want %v (age %s, interval 5s)",
					stale, tc.wantStale, tc.age)
			}
		})
	}
}

// TestACollectorThatHasNeverRunIsDistinctFromOneThatStopped.
//
// Both produce no candles. The first is a deployment that was never started,
// the second is a process that died, and they are looked into in different
// places.
func TestACollectorThatHasNeverRunIsDistinctFromOneThatStopped(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: models.MarketStatus{}}, &fakeActivity{}, Config{})

	if status.Collector.Reachable {
		t.Fatal("reachable is true with no status row")
	}
	details := concernsFor(status, "collector")
	if len(details) != 1 {
		t.Fatalf("collector concerns = %v, want exactly one", details)
	}
	if !strings.Contains(details[0], "has ever registered") {
		t.Errorf("concern = %q, want it to say no collector has ever registered", details[0])
	}
}

// TestAnUnreadableCollectorStatusIsReportedRatherThanFailingTheEndpoint.
//
// The collector's status row and the signal counts come from different places.
// If one is unreachable the rest is still worth having — and the fact that it
// is unreachable is itself the most useful thing on the page.
func TestAnUnreadableCollectorStatusIsReportedRatherThanFailingTheEndpoint(t *testing.T) {
	status := statusOf(t,
		&fakeMarket{err: errors.New("connection refused")},
		&fakeActivity{signals: pipeline.SignalActivity{SignalsTotal: 7}},
		Config{})

	if status.Evaluator.SignalsTotal != 7 {
		t.Errorf("signals total = %d, want 7: the rest of the page still assembles",
			status.Evaluator.SignalsTotal)
	}
	details := concernsFor(status, "collector")
	if len(details) != 1 || !strings.Contains(details[0], "connection refused") {
		t.Fatalf("collector concerns = %v, want one naming the read failure", details)
	}
}

// TestSignalsWithNoOutcomeRowAreReported. A follower that has stopped opening
// rows has no other symptom: the signals keep arriving and nothing follows
// them.
func TestSignalsWithNoOutcomeRowAreReported(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		signals: pipeline.SignalActivity{OutcomesMissing: 4, OutcomesOpen: 2,
			OldestOpenSignalAt: observedAt.Add(-6 * time.Hour)},
	}, Config{})

	if status.Outcomes.Missing != 4 {
		t.Errorf("missing = %d, want 4", status.Outcomes.Missing)
	}
	if status.Outcomes.OldestOpenAge == nil || *status.Outcomes.OldestOpenAge != 6*time.Hour {
		t.Errorf("oldest open age = %v, want 6h", status.Outcomes.OldestOpenAge)
	}
	details := concernsFor(status, "outcomes")
	if len(details) != 1 || !strings.Contains(details[0], "4 signals") {
		t.Fatalf("outcome concerns = %v, want one naming the four", details)
	}
}

// TestNotifyModeWithNoRegisteredDeviceSaysSoInWords.
//
// # What this prevents
//
// The device token is registered by the phone, so every deployment passes
// through "notify mode, nothing registered" between switching the mode on and
// opening the app. In that state everything looks configured: the mode says
// notify, the credentials validated at start-up, signals are being recorded
// and queued.
//
// A bare `devices_registered: 0` beside `mode: notify` leaves the reader to
// join those two facts themselves — and the person reading this page is
// usually reading it because something is already confusing. So it is a
// sentence, and it says what will happen to the signals rather than only what
// is missing.
func TestNotifyModeWithNoRegisteredDeviceSaysSoInWords(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		delivery: pipeline.DeliveryActivity{DevicesRegistered: 0},
	}, Config{SignalMode: constants.SignalModeNotify})

	if status.Delivery.DevicesRegistered != 0 {
		t.Fatalf("devices registered = %d, want 0", status.Delivery.DevicesRegistered)
	}

	details := concernsFor(status, "delivery")
	if len(details) != 1 {
		t.Fatalf("delivery concerns = %v, want exactly one", details)
	}
	for _, want := range []string{"no device is registered", "recorded", "not delivered"} {
		if !strings.Contains(details[0], want) {
			t.Errorf("the concern does not say %q: %q", want, details[0])
		}
	}
}

// TestARegisteredDeviceInNotifyModeIsNotAConcern.
func TestARegisteredDeviceInNotifyModeIsNotAConcern(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		delivery: pipeline.DeliveryActivity{DevicesRegistered: 1},
	}, Config{SignalMode: constants.SignalModeNotify})

	if status.Delivery.DevicesRegistered != 1 {
		t.Errorf("devices registered = %d, want 1", status.Delivery.DevicesRegistered)
	}
	if details := concernsFor(status, "delivery"); len(details) != 0 {
		t.Fatalf("a registered device raised %v", details)
	}
}

// TestNoRegisteredDeviceInSilentModeIsNotAConcern.
//
// Silent mode sends nothing to anywhere, so there is nothing for a
// registration to be missing from. Reporting it would put a permanent entry on
// the page of every deployment that has not switched alerts on — which is the
// default — and a concerns list that is never empty is one nobody reads.
func TestNoRegisteredDeviceInSilentModeIsNotAConcern(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		delivery: pipeline.DeliveryActivity{DevicesRegistered: 0},
	}, Config{SignalMode: constants.SignalModeSilent})

	if details := concernsFor(status, "delivery"); len(details) != 0 {
		t.Fatalf("silent mode with no device raised %v", details)
	}
}

// TestUnfilledGapsAreReportedWithTheTimeframesHoldingThem.
//
// A gap is the collector noticing a hole and queueing a backfill, which is the
// mechanism working. A count that stays put is the finding: every signal whose
// window overlaps an unfilled gap resolves as invalidated and leaves the
// statistics, so a performance screen quietly narrows without saying why.
//
// The breakdown names only the series actually holding gaps. A list reading
// "1m: 3, 5m: 0, 15m: 0, 1h: 0, 4h: 0, 1d: 0" buries the one fact it contains.
func TestUnfilledGapsAreReportedWithTheTimeframesHoldingThem(t *testing.T) {
	holed := healthy()
	holed.Timeframes = []models.TimeframeStatus{
		{Timeframe: constants.Timeframe1m, UnfilledGaps: 3},
		{Timeframe: constants.Timeframe5m, UnfilledGaps: 0},
		{Timeframe: constants.Timeframe4h, UnfilledGaps: 1},
	}

	status := statusOf(t, &fakeMarket{status: holed}, &fakeActivity{}, Config{})

	if status.Ingestion.UnfilledGaps != 4 {
		t.Errorf("total unfilled gaps = %d, want 4", status.Ingestion.UnfilledGaps)
	}
	if len(status.Ingestion.Timeframes) != 3 {
		t.Fatalf("breakdown covers %d timeframes, want all 3 collected",
			len(status.Ingestion.Timeframes))
	}

	details := concernsFor(status, "ingestion")
	if len(details) != 1 {
		t.Fatalf("ingestion concerns = %v, want exactly one", details)
	}
	if !strings.Contains(details[0], "1m: 3") || !strings.Contains(details[0], "4h: 1") {
		t.Errorf("the concern does not name the timeframes holding gaps: %q", details[0])
	}
	if strings.Contains(details[0], "5m") {
		t.Errorf("the concern lists a timeframe with no gaps: %q", details[0])
	}
	if !strings.Contains(details[0], "invalidated") {
		t.Errorf("the concern does not say what an unfilled gap costs: %q", details[0])
	}
}

// TestTheStatusSaysWhereTheDataEnds.
//
// # Why a client needs this and not only an operator
//
// A chart opening on wall-clock now is blank whenever the collector is behind
// — and blank looks exactly like a market that stopped trading. Nothing else
// on this page answers "where does the series end", so the app has no way to
// anchor on the newest bar rather than on the current time.
func TestTheStatusSaysWhereTheDataEnds(t *testing.T) {
	latest := observedAt.Add(-8 * time.Hour)
	age := int64(8 * 60 * 60)

	current := healthy()
	current.Timeframes = []models.TimeframeStatus{
		{Timeframe: constants.Timeframe4h, LatestOpenTime: &latest, LatestAgeSeconds: &age},
		{Timeframe: constants.Timeframe1m},
	}

	status := statusOf(t, &fakeMarket{status: current}, &fakeActivity{}, Config{})

	if len(status.Ingestion.Timeframes) != 2 {
		t.Fatalf("breakdown covers %d timeframes, want 2", len(status.Ingestion.Timeframes))
	}

	four := status.Ingestion.Timeframes[0]
	if four.LatestOpenTime == nil || !four.LatestOpenTime.Equal(latest) {
		t.Errorf("4h latest open time = %v, want %s", four.LatestOpenTime, latest)
	}
	if four.LatestAge == nil || *four.LatestAge != 8*time.Hour {
		t.Errorf("4h latest age = %v, want 8h", four.LatestAge)
	}

	// An empty series says so with a nil rather than the zero instant, which
	// would render as 0001-01-01 and read as a very old candle.
	if status.Ingestion.Timeframes[1].LatestOpenTime != nil {
		t.Errorf("an empty series reported %v, want nil",
			status.Ingestion.Timeframes[1].LatestOpenTime)
	}
}

// TestAWholeSeriesRaisesNothing, so the concerns list stays empty on a healthy
// deployment and means something when it is not.
func TestAWholeSeriesRaisesNothing(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{}, Config{})

	if status.Ingestion.UnfilledGaps != 0 {
		t.Errorf("unfilled gaps = %d, want 0", status.Ingestion.UnfilledGaps)
	}
	if len(status.Ingestion.Timeframes) != 2 {
		t.Errorf("breakdown covers %d timeframes, want the 2 collected",
			len(status.Ingestion.Timeframes))
	}
	if details := concernsFor(status, "ingestion"); len(details) != 0 {
		t.Fatalf("a whole series raised %v", details)
	}
}

// TestAPendingQueueMeansDifferentThingsInEachMode.
//
// In notify mode a queue that does not drain is a broken worker. In silent
// mode nothing should be queued at all, so anything there was queued before
// delivery was switched off and will never be sent. The same number, two
// findings — which is why the mode is on the page.
func TestAPendingQueueMeansDifferentThingsInEachMode(t *testing.T) {
	for _, tc := range []struct {
		mode     constants.SignalMode
		contains string
	}{
		{constants.SignalModeNotify, "the worker should be draining them"},
		{constants.SignalModeSilent, "nothing will send them"},
	} {
		t.Run(tc.mode.String(), func(t *testing.T) {
			// A registered device, so the pending queue is the only thing
			// this test is asking about.
			status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
				delivery: pipeline.DeliveryActivity{Pending: 3, DevicesRegistered: 1},
			}, Config{SignalMode: tc.mode})

			details := concernsFor(status, "delivery")
			if len(details) != 1 {
				t.Fatalf("delivery concerns = %v, want exactly one", details)
			}
			if !strings.Contains(details[0], tc.contains) {
				t.Errorf("concern = %q, want it to contain %q", details[0], tc.contains)
			}
			if status.Delivery.Mode != tc.mode.String() {
				t.Errorf("mode = %q, want %q", status.Delivery.Mode, tc.mode)
			}
		})
	}
}

// TestFailedNotificationsAreReportedBecauseNothingRetriesThem.
func TestFailedNotificationsAreReportedBecauseNothingRetriesThem(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{
		delivery: pipeline.DeliveryActivity{Failed: 2, Sent: 9, DevicesRegistered: 1},
	}, Config{SignalMode: constants.SignalModeNotify})

	details := concernsFor(status, "delivery")
	if len(details) != 1 || !strings.Contains(details[0], "2 notifications were given up on") {
		t.Fatalf("delivery concerns = %v, want one naming the two failures", details)
	}
}

// TestEveryAgeIsMeasuredFromOneInstant.
//
// Four ages computed against four calls to time.Now() are inconsistent with
// each other by however long the queries took, and a reader comparing two of
// them is comparing two clocks. ObservedAt is the single reference, and the
// collector is asked for its view as of the same instant.
func TestEveryAgeIsMeasuredFromOneInstant(t *testing.T) {
	marketStatus := &fakeMarket{status: healthy()}

	status := statusOf(t, marketStatus, &fakeActivity{
		signals: pipeline.SignalActivity{
			LastSignalAt:       observedAt.Add(-time.Hour),
			OldestOpenSignalAt: observedAt.Add(-30 * time.Minute),
		},
		delivery: pipeline.DeliveryActivity{LastSentAt: observedAt.Add(-time.Minute)},
	}, Config{})

	if !status.ObservedAt.Equal(observedAt) {
		t.Fatalf("observed at %s, want %s", status.ObservedAt, observedAt)
	}
	if !marketStatus.askedAt.Equal(observedAt) {
		t.Errorf("the collector was asked as of %s, want the same instant %s",
			marketStatus.askedAt, observedAt)
	}
	if got := *status.Evaluator.LastSignalAge; got != time.Hour {
		t.Errorf("last signal age = %s, want 1h", got)
	}
	if got := *status.Outcomes.OldestOpenAge; got != 30*time.Minute {
		t.Errorf("oldest open age = %s, want 30m", got)
	}
	if got := *status.Collector.HeartbeatAge; got != time.Second {
		t.Errorf("heartbeat age = %s, want 1s", got)
	}
}

// TestAnAbsentInstantIsNullRatherThanTheZeroTime.
//
// 0001-01-01 read as a real timestamp is worse than nothing: it sorts and
// formats like one, and an age computed from it is two thousand years.
func TestAnAbsentInstantIsNullRatherThanTheZeroTime(t *testing.T) {
	status := statusOf(t, &fakeMarket{status: healthy()}, &fakeActivity{}, Config{})

	if status.Evaluator.LastSignalAt != nil {
		t.Errorf("last signal at = %v, want nil when nothing has been produced",
			status.Evaluator.LastSignalAt)
	}
	if status.Evaluator.LastSignalAge != nil {
		t.Errorf("last signal age = %v, want nil", status.Evaluator.LastSignalAge)
	}
	if status.Outcomes.OldestOpenAt != nil {
		t.Errorf("oldest open at = %v, want nil", status.Outcomes.OldestOpenAt)
	}
	if status.Delivery.LastSentAt != nil {
		t.Errorf("last sent at = %v, want nil", status.Delivery.LastSentAt)
	}
}

// TestAFailedCountQueryFailsTheEndpoint.
//
// Unlike the collector's status, these counts are the substance of the page.
// Reporting zeros because a query failed would say the pipeline is idle when
// nothing is known about it, which is the one answer worse than an error.
func TestAFailedCountQueryFailsTheEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		activity *fakeActivity
	}{
		{"signals", &fakeActivity{signalsErr: errors.New("read signals")}},
		{"delivery", &fakeActivity{deliveryErr: errors.New("read delivery")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usecase, err := NewPipelineUsecaseImpl(tc.activity, &fakeMarket{status: healthy()},
				Config{Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
					Now: func() time.Time { return observedAt }})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if _, err := usecase.Status(context.Background()); err == nil {
				t.Fatal("status succeeded while the counts could not be read")
			}
		})
	}
}

// TestTheUsecaseRefusesToBuildWithoutWhatItNeeds, rather than reporting an
// empty status that looks like a healthy idle pipeline.
func TestTheUsecaseRefusesToBuildWithoutWhatItNeeds(t *testing.T) {
	good := Config{Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot}

	for _, tc := range []struct {
		name   string
		repo   pipeline.PipelineRepository
		market market.MarketUsecase
		cfg    Config
	}{
		{"no repository", nil, &fakeMarket{}, good},
		{"no market usecase", &fakeActivity{}, nil, good},
		{"no symbol", &fakeActivity{}, &fakeMarket{}, Config{MarketType: constants.MarketTypeSpot}},
		{"bad market type", &fakeActivity{}, &fakeMarket{}, Config{Symbol: "BTCUSDT", MarketType: "futures-ish"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPipelineUsecaseImpl(tc.repo, tc.market, tc.cfg); err == nil {
				t.Fatal("built successfully")
			}
		})
	}
}
