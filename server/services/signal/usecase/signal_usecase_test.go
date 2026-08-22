package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/models"
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/signal"
	_signal_us "github.com/spioneracorei8/btcusd-trading-platform/server/services/signal/usecase"
)

var barOpen = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// spyRepo records what reached storage.
type spyRepo struct {
	inserted []models.Signal
	err      error
}

func (s *spyRepo) InsertSignal(_ context.Context, sig models.Signal) (models.Signal, error) {
	if s.err != nil {
		return models.Signal{}, s.err
	}
	s.inserted = append(s.inserted, sig)
	return sig, nil
}

func closedBar() models.Candle {
	return models.Candle{
		Symbol: "BTCUSDT", MarketType: constants.MarketTypeSpot,
		Timeframe: constants.Timeframe1m,
		OpenTime:  barOpen, CloseTime: barOpen.Add(time.Minute),
		Open:     decimal.RequireFromString("27000"),
		High:     decimal.RequireFromString("27050"),
		Low:      decimal.RequireFromString("26980"),
		Close:    decimal.RequireFromString("27020"),
		IsClosed: true,
	}
}

func snapshot() models.IndicatorSnapshot {
	return models.IndicatorSnapshot{OpenTime: barOpen, EMA: 26990, RSI: 55.5, ATR: 40, VWAP: 27005}
}

func validSignal(t *testing.T, bar models.Candle) models.Signal {
	t.Helper()

	reason, err := signal.BuildReason("test trigger", bar, snapshot(),
		"27020", "26980", "27100",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"}).Encode()
	if err != nil {
		t.Fatalf("encode reason: %v", err)
	}

	return models.Signal{
		Symbol: bar.Symbol, MarketType: bar.MarketType, Timeframe: bar.Timeframe,
		SignalTime:      bar.CloseTime,
		Direction:       constants.DirectionLong,
		Strength:        decimal.RequireFromString("72.50"),
		StrategyName:    "ema_crossover",
		StrategyVersion: "v1",
		Reason:          reason,
	}
}

// TestSignalsAreWrittenOnlyForClosedBars is CLAUDE.md §3.1 reaching the last
// place it applies.
//
// An alert cannot be recalled. A signal from a forming bar tells the owner
// about a price that can still change, and by the time it changes the
// notification is already on their phone.
func TestSignalsAreWrittenOnlyForClosedBars(t *testing.T) {
	repo := &spyRepo{}
	us := _signal_us.NewSignalUsecaseImpl(repo)

	forming := closedBar()
	forming.IsClosed = false

	_, err := us.CreateSignal(context.Background(), validSignal(t, forming), forming)
	if !errors.Is(err, constants.ErrUnclosedCandle) {
		t.Fatalf("CreateSignal() returned %v, want ErrUnclosedCandle", err)
	}
	if len(repo.inserted) != 0 {
		t.Errorf("%d signals reached storage from a forming bar", len(repo.inserted))
	}
}

// TestSignalTimeMustBeTheBarsClose. signal_time is what makes a live signal
// comparable with a backtested one; if it were the insert time the two could
// never be lined up.
func TestSignalTimeMustBeTheBarsClose(t *testing.T) {
	repo := &spyRepo{}
	us := _signal_us.NewSignalUsecaseImpl(repo)

	bar := closedBar()
	sig := validSignal(t, bar)
	sig.SignalTime = time.Now().UTC() // the insert time, which is the mistake

	if _, err := us.CreateSignal(context.Background(), sig, bar); err == nil {
		t.Fatal("a signal timed at the insert moment was accepted")
	}
	if len(repo.inserted) != 0 {
		t.Errorf("%d signals reached storage with the wrong time", len(repo.inserted))
	}
}

// TestASignalWithoutAReasonIsRefused. The indicator values behind a decision
// are never persisted anywhere else, so a signal without them cannot be
// audited later — and later is when a surprising alert needs auditing.
func TestASignalWithoutAReasonIsRefused(t *testing.T) {
	repo := &spyRepo{}
	us := _signal_us.NewSignalUsecaseImpl(repo)

	bar := closedBar()
	sig := validSignal(t, bar)
	sig.Reason = nil

	if _, err := us.CreateSignal(context.Background(), sig, bar); err == nil {
		t.Fatal("a signal with no reason was accepted")
	}
}

// TestAValidSignalIsStored, so the tests above are refusing something that
// would otherwise have gone through.
func TestAValidSignalIsStored(t *testing.T) {
	repo := &spyRepo{}
	us := _signal_us.NewSignalUsecaseImpl(repo)

	bar := closedBar()
	if _, err := us.CreateSignal(context.Background(), validSignal(t, bar), bar); err != nil {
		t.Fatalf("CreateSignal() returned error: %v", err)
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("%d signals reached storage, want 1", len(repo.inserted))
	}
	if !repo.inserted[0].SignalTime.Equal(bar.CloseTime) {
		t.Errorf("stored signal_time is %s, want the bar's close %s",
			repo.inserted[0].SignalTime, bar.CloseTime)
	}
}

// TestTheReasonCarriesTheWholeSnapshot. §A3 requires the indicator values and
// trend state to be captured, because they cannot be reconstructed after the
// fact: indicators are never persisted, and recomputing them would need the
// exact warm-up state the live process had.
func TestTheReasonCarriesTheWholeSnapshot(t *testing.T) {
	bar := closedBar()
	reason := signal.BuildReason("ema(9) crossed above ema(21)", bar, snapshot(),
		"27020", "26980", "27100",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"})
	reason.Trend = &signal.TrendReason{
		Filter: "ema_rsi_mtf", Version: "v1", Bias: "bullish",
		Confidence: 0.62, Ready: true,
		PerTF: []signal.TimeframeReason{
			{Timeframe: "1h", Score: 0.8, Weight: 0.5,
				CloseTime: barOpen.Format(time.RFC3339), Ready: true},
		},
	}

	encoded, err := reason.Encode()
	if err != nil {
		t.Fatalf("Encode() returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("the reason is not valid JSON: %v", err)
	}

	for _, key := range []string{"trigger", "bar", "indicators", "trend", "levels"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("the reason has no %q; that cannot be recovered later", key)
		}
	}

	indicators, _ := decoded["indicators"].(map[string]any)
	for _, key := range []string{"ema", "rsi", "atr", "vwap"} {
		if _, ok := indicators[key]; !ok {
			t.Errorf("the reason omits the %s value", key)
		}
	}

	// The higher-timeframe close time is the evidence there was no
	// cross-timeframe look-ahead. Without it a stored signal cannot be checked.
	trend, _ := decoded["trend"].(map[string]any)
	perTF, _ := trend["per_timeframe"].([]any)
	if len(perTF) != 1 {
		t.Fatalf("per_timeframe has %d entries, want 1", len(perTF))
	}
	if _, ok := perTF[0].(map[string]any)["close_time"]; !ok {
		t.Error("a timeframe reading has no close_time, so look-ahead cannot be ruled out later")
	}
}

// TestTheReasonIsDeterministic. Two identical decisions must serialise
// identically, or a duplicate is not recognisable as one.
func TestTheReasonIsDeterministic(t *testing.T) {
	bar := closedBar()

	first, err := signal.BuildReason("t", bar, snapshot(), "1", "2", "3",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"}).Encode()
	if err != nil {
		t.Fatalf("Encode() returned error: %v", err)
	}
	for i := range 20 {
		next, err := signal.BuildReason("t", bar, snapshot(), "1", "2", "3",
			signal.StrategyReason{Name: "ema_crossover", Version: "v1"}).Encode()
		if err != nil {
			t.Fatalf("Encode() returned error on render %d: %v", i, err)
		}
		if string(first) != string(next) {
			t.Fatalf("render %d differs:\n %s\n %s", i, first, next)
		}
	}
}

// TestNaNIndicatorsAreRefused. Substituting zero would store a plausible value
// that never existed, and a stored signal is the only record there will be.
func TestNaNIndicatorsAreRefused(t *testing.T) {
	bar := closedBar()
	broken := snapshot()
	broken.RSI = math.NaN()

	if _, err := signal.BuildReason("t", bar, broken, "1", "2", "3",
		signal.StrategyReason{Name: "ema_crossover", Version: "v1"}).Encode(); err == nil {
		t.Fatal("a reason with a NaN indicator encoded successfully")
	}
}

// TestDuplicateIsPassedThrough. The unique constraint lives in the database,
// where a restart or a replay cannot get around it; the usecase must surface
// its refusal rather than swallow it.
func TestDuplicateIsPassedThrough(t *testing.T) {
	repo := &spyRepo{err: constants.ErrDuplicateSignal}
	us := _signal_us.NewSignalUsecaseImpl(repo)

	bar := closedBar()
	_, err := us.CreateSignal(context.Background(), validSignal(t, bar), bar)
	if !errors.Is(err, constants.ErrDuplicateSignal) {
		t.Fatalf("CreateSignal() returned %v, want ErrDuplicateSignal", err)
	}
}
