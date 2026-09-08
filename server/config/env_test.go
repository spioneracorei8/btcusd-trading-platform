package config_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// env turns a map into a helper.LookupFunc so tests never touch the real
// process environment.
func env(m map[string]string) helper.LookupFunc {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// validEnv is the minimum environment that must load cleanly.
func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      "dev",
		"LOG_LEVEL":    "info",
		"HTTP_PORT":    "8080",
		"DATABASE_URL": "postgres://user:pass@localhost:5432/trading?sslmode=disable",
	}
}

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(env(validEnv()))
	if err != nil {
		t.Fatalf("LoadFrom() returned error: %v", err)
	}

	if cfg.App.Env != constants.EnvDev {
		t.Errorf("App.Env = %q, want %q", cfg.App.Env, constants.EnvDev)
	}
	if cfg.App.LogLevel != slog.LevelInfo {
		t.Errorf("App.LogLevel = %v, want %v", cfg.App.LogLevel, slog.LevelInfo)
	}
	if cfg.App.HTTPPort != 8080 {
		t.Errorf("App.HTTPPort = %d, want 8080", cfg.App.HTTPPort)
	}
	if got, want := cfg.HTTPAddr(), ":8080"; got != want {
		t.Errorf("HTTPAddr() = %q, want %q", got, want)
	}
	if cfg.IsProd() {
		t.Error("IsProd() = true, want false for dev")
	}

	if cfg.Market.Symbol != constants.DefaultMarketSymbol {
		t.Errorf("Market.Symbol = %q, want %q", cfg.Market.Symbol, constants.DefaultMarketSymbol)
	}
	if cfg.Market.Type != constants.MarketTypeSpot {
		t.Errorf("Market.Type = %q, want %q", cfg.Market.Type, constants.MarketTypeSpot)
	}

	wantTFs := []constants.Timeframe{
		constants.Timeframe1m, constants.Timeframe5m, constants.Timeframe15m, constants.Timeframe1h,
	}
	if len(cfg.Market.Timeframes) != len(wantTFs) {
		t.Fatalf("Market.Timeframes = %v, want %v", cfg.Market.Timeframes, wantTFs)
	}
	for i, tf := range wantTFs {
		if cfg.Market.Timeframes[i] != tf {
			t.Errorf("Market.Timeframes[%d] = %q, want %q", i, cfg.Market.Timeframes[i], tf)
		}
	}

	if got := cfg.Market.FeeTakerPct.String(); got != constants.DefaultFeeTakerPct {
		t.Errorf("Market.FeeTakerPct = %s, want %s", got, constants.DefaultFeeTakerPct)
	}
	if cfg.Market.SlippageTicks != constants.DefaultSlippageTicks {
		t.Errorf("Market.SlippageTicks = %d, want %d", cfg.Market.SlippageTicks, constants.DefaultSlippageTicks)
	}
	if cfg.Notify.SignalMode != constants.SignalModeSilent {
		t.Errorf("Notify.SignalMode = %q, want silent by default", cfg.Notify.SignalMode)
	}
	if cfg.Notify.Delivers() {
		t.Error("a bare environment delivers push notifications")
	}

	if cfg.Market.RESTBaseURL != constants.DefaultBinanceRESTBaseURL {
		t.Errorf("Market.RESTBaseURL = %q, want %q", cfg.Market.RESTBaseURL, constants.DefaultBinanceRESTBaseURL)
	}
	if cfg.Market.WSBaseURL != constants.DefaultBinanceWSBaseURL {
		t.Errorf("Market.WSBaseURL = %q, want %q", cfg.Market.WSBaseURL, constants.DefaultBinanceWSBaseURL)
	}
	wantFrom := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	if !cfg.Market.BackfillFrom.Equal(wantFrom) {
		t.Errorf("Market.BackfillFrom = %s, want %s", cfg.Market.BackfillFrom, wantFrom)
	}
	if cfg.Market.BackfillFrom.Location() != time.UTC {
		t.Errorf("Market.BackfillFrom location = %v, want UTC", cfg.Market.BackfillFrom.Location())
	}
	if cfg.Market.GapcheckInterval != constants.DefaultGapcheckInterval {
		t.Errorf("Market.GapcheckInterval = %s, want %s", cfg.Market.GapcheckInterval, constants.DefaultGapcheckInterval)
	}
	if cfg.Market.HeartbeatInterval != constants.DefaultHeartbeatInterval {
		t.Errorf("Market.HeartbeatInterval = %s, want %s", cfg.Market.HeartbeatInterval, constants.DefaultHeartbeatInterval)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	e := validEnv()
	e["APP_ENV"] = "prod"
	e["LOG_LEVEL"] = "warn"
	e["HTTP_PORT"] = "9000"
	e["MARKET_SYMBOL"] = "ETHUSDT"
	e["MARKET_TYPE"] = "futures"
	e["MARKET_TIMEFRAMES"] = "1m, 5m ,4h"
	e["FEE_TAKER_PCT"] = "0.04"
	e["SLIPPAGE_TICKS"] = "3"
	e["SIGNAL_MODE"] = "notify"
	e["VAPID_PUBLIC_KEY"] = "a-public-key"
	e["VAPID_PRIVATE_KEY"] = "a-private-key"
	e["VAPID_SUBJECT"] = "mailto:owner@example.com"
	e["MARKET_GAPCHECK_INTERVAL"] = "30m"
	e["BINANCE_REST_BASE_URL"] = "https://testnet.binance.vision/"

	cfg, err := config.LoadFrom(env(e))
	if err != nil {
		t.Fatalf("LoadFrom() returned error: %v", err)
	}

	if !cfg.IsProd() {
		t.Error("IsProd() = false, want true")
	}
	if cfg.App.LogLevel != slog.LevelWarn {
		t.Errorf("App.LogLevel = %v, want %v", cfg.App.LogLevel, slog.LevelWarn)
	}
	if cfg.Market.Type != constants.MarketTypeFutures {
		t.Errorf("Market.Type = %q, want %q", cfg.Market.Type, constants.MarketTypeFutures)
	}
	if len(cfg.Market.Timeframes) != 3 || cfg.Market.Timeframes[2] != constants.Timeframe4h {
		t.Errorf("Market.Timeframes = %v, want [1m 5m 4h]", cfg.Market.Timeframes)
	}
	if got := cfg.Market.FeeTakerPct.String(); got != "0.04" {
		t.Errorf("Market.FeeTakerPct = %s, want 0.04", got)
	}
	if cfg.Market.SlippageTicks != 3 {
		t.Errorf("Market.SlippageTicks = %d, want 3", cfg.Market.SlippageTicks)
	}
	if !cfg.Notify.Delivers() || cfg.Notify.VAPIDPublicKey != "a-public-key" {
		t.Errorf("Notify = %+v, want delivering with a VAPID key", cfg.Notify)
	}
	if cfg.Market.GapcheckInterval != 30*time.Minute {
		t.Errorf("Market.GapcheckInterval = %s, want 30m", cfg.Market.GapcheckInterval)
	}
	// A trailing slash must not survive, or joined paths grow a double slash.
	if cfg.Market.RESTBaseURL != "https://testnet.binance.vision" {
		t.Errorf("Market.RESTBaseURL = %q", cfg.Market.RESTBaseURL)
	}
}

func TestLoadFromMissingRequired(t *testing.T) {
	for _, key := range []string{"APP_ENV", "LOG_LEVEL", "HTTP_PORT", "DATABASE_URL"} {
		t.Run(key, func(t *testing.T) {
			e := validEnv()
			delete(e, key)

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatalf("LoadFrom() without %s returned no error", key)
			}
			if !errors.Is(err, constants.ErrMissingEnv) {
				t.Errorf("error %v does not wrap ErrMissingEnv", err)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name the missing variable %s", err, key)
			}
		})
	}
}

func TestLoadFromEmptyValueCountsAsMissing(t *testing.T) {
	e := validEnv()
	e["DATABASE_URL"] = "   "

	_, err := config.LoadFrom(env(e))
	if err == nil {
		t.Fatal("LoadFrom() with blank DATABASE_URL returned no error")
	}
	if !errors.Is(err, constants.ErrMissingEnv) {
		t.Errorf("error %v does not wrap ErrMissingEnv", err)
	}
}

func TestLoadFromReportsEveryMissingVariableAtOnce(t *testing.T) {
	_, err := config.LoadFrom(env(map[string]string{}))
	if err == nil {
		t.Fatal("LoadFrom() with an empty environment returned no error")
	}
	for _, key := range []string{"APP_ENV", "LOG_LEVEL", "HTTP_PORT", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}

func TestLoadFromInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantKey string
	}{
		{name: "app env enum", key: "APP_ENV", value: "staging", wantKey: "APP_ENV"},
		{name: "log level enum", key: "LOG_LEVEL", value: "verbose", wantKey: "LOG_LEVEL"},
		{name: "port not a number", key: "HTTP_PORT", value: "http", wantKey: "HTTP_PORT"},
		{name: "port out of range", key: "HTTP_PORT", value: "70000", wantKey: "HTTP_PORT"},
		{name: "market type enum", key: "MARKET_TYPE", value: "margin", wantKey: "MARKET_TYPE"},
		{name: "timeframe unsupported", key: "MARKET_TIMEFRAMES", value: "1m,7m", wantKey: "MARKET_TIMEFRAMES"},
		{name: "timeframe duplicated", key: "MARKET_TIMEFRAMES", value: "1m,1m", wantKey: "MARKET_TIMEFRAMES"},
		{name: "timeframe empty", key: "MARKET_TIMEFRAMES", value: ",", wantKey: "MARKET_TIMEFRAMES"},
		{name: "fee not a number", key: "FEE_TAKER_PCT", value: "cheap", wantKey: "FEE_TAKER_PCT"},
		{name: "fee negative", key: "FEE_TAKER_PCT", value: "-0.01", wantKey: "FEE_TAKER_PCT"},
		{name: "fee too large", key: "FEE_TAKER_PCT", value: "100", wantKey: "FEE_TAKER_PCT"},
		{name: "slippage negative", key: "SLIPPAGE_TICKS", value: "-1", wantKey: "SLIPPAGE_TICKS"},
		{name: "notify enabled not a bool", key: "NOTIFY_ENABLED", value: "yes please", wantKey: "NOTIFY_ENABLED"},
		{name: "rest url wrong scheme", key: "BINANCE_REST_BASE_URL", value: "ws://api.binance.com", wantKey: "BINANCE_REST_BASE_URL"},
		{name: "rest url no host", key: "BINANCE_REST_BASE_URL", value: "https://", wantKey: "BINANCE_REST_BASE_URL"},
		{name: "ws url wrong scheme", key: "BINANCE_WS_BASE_URL", value: "https://stream.binance.com", wantKey: "BINANCE_WS_BASE_URL"},
		{name: "backfill not rfc3339", key: "MARKET_BACKFILL_FROM", value: "2023-01-01", wantKey: "MARKET_BACKFILL_FROM"},
		{name: "backfill in the future", key: "MARKET_BACKFILL_FROM", value: "2999-01-01T00:00:00Z", wantKey: "MARKET_BACKFILL_FROM"},
		{name: "gapcheck not a duration", key: "MARKET_GAPCHECK_INTERVAL", value: "15", wantKey: "MARKET_GAPCHECK_INTERVAL"},
		{name: "gapcheck too short", key: "MARKET_GAPCHECK_INTERVAL", value: "1s", wantKey: "MARKET_GAPCHECK_INTERVAL"},
		{name: "heartbeat too long", key: "COLLECTOR_HEARTBEAT_INTERVAL", value: "10m", wantKey: "COLLECTOR_HEARTBEAT_INTERVAL"},
		{name: "strategy timeframe unsupported", key: "STRATEGY_TIMEFRAME", value: "7m", wantKey: "STRATEGY_TIMEFRAME"},
		{name: "strategy params not a pair", key: "STRATEGY_PARAMS", value: "fast", wantKey: "STRATEGY_PARAMS"},
		{name: "strategy params unnamed", key: "STRATEGY_PARAMS", value: "=9", wantKey: "STRATEGY_PARAMS"},
		{name: "strategy params repeated", key: "STRATEGY_PARAMS", value: "fast=9,fast=21", wantKey: "STRATEGY_PARAMS"},
		{name: "signal mode unknown", key: "SIGNAL_MODE", value: "uat", wantKey: "SIGNAL_MODE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEnv()
			e[tt.key] = tt.value

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatalf("LoadFrom() with %s=%q returned no error", tt.key, tt.value)
			}
			if !errors.Is(err, constants.ErrInvalidEnv) {
				t.Errorf("error %v does not wrap ErrInvalidEnv", err)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q does not name %s", err, tt.wantKey)
			}
		})
	}
}

// TestNotifyModeRequiresSomewhereToSend.
//
// A mode that claims to deliver and has nowhere to send is worse than one
// that says it will not: the first looks like it is working, and the missing
// alert is only noticed when it matters.
func TestNotifyModeRequiresSomewhereToSend(t *testing.T) {
	e := validEnv()
	e["SIGNAL_MODE"] = "notify"

	_, err := config.LoadFrom(env(e))
	if err == nil {
		t.Fatal("LoadFrom() in notify mode with no credentials returned no error")
	}
	if !errors.Is(err, constants.ErrMissingEnv) {
		t.Errorf("error %v does not wrap ErrMissingEnv", err)
	}
	for _, key := range []string{"VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}

// TestSilentModeNeedsNoCredentials, because the common case is a system that
// records and says nothing, and demanding a VAPID key pair to do that would
// make silence the harder configuration to reach.
func TestSilentModeNeedsNoCredentials(t *testing.T) {
	e := validEnv()
	e["SIGNAL_MODE"] = "silent"

	cfg, err := config.LoadFrom(env(e))
	if err != nil {
		t.Fatalf("LoadFrom() in silent mode returned error: %v", err)
	}
	if cfg.Notify.Delivers() {
		t.Error("silent mode delivers")
	}
}

// TestTheRetiredSwitchIsRefusedRatherThanIgnored.
//
// NOTIFY_ENABLED used to decide whether the owner got alerts. Left in a file
// where it no longer does anything, somebody reads it, believes it, and
// concludes delivery is off when it is on. Failing at start-up costs one edit
// and says which one.
func TestTheRetiredSwitchIsRefusedRatherThanIgnored(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			e := validEnv()
			e["NOTIFY_ENABLED"] = value

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatalf("LoadFrom() with NOTIFY_ENABLED=%s returned no error", value)
			}
			if !strings.Contains(err.Error(), "NOTIFY_ENABLED") {
				t.Errorf("error %q does not name the variable to remove", err)
			}
			if !strings.Contains(err.Error(), "SIGNAL_MODE") {
				t.Errorf("error %q does not name what replaced it", err)
			}
		})
	}
}

// TestNotifyModeDoesNotRequireADeviceToken.
//
// # Why this is a test rather than an absence
//
// It used to. FCM_DEVICE_TOKEN was required in notify mode, and that was the
// wrong shape once the phone became the thing that registers: the app posts
// its token to the api after being installed, so a process that refused to
// start without one could never reach the state where one exists.
//
// The check that replaced it is not at start-up at all — /api/v1/status
// reports whether a device is registered, and says in words that signals are
// being recorded and not delivered while none is. See ADR 0026.
func TestNotifyModeDoesNotRequireADeviceToken(t *testing.T) {
	e := validEnv()
	e["SIGNAL_MODE"] = "notify"
	e["VAPID_PUBLIC_KEY"] = "a-public-key"
	e["VAPID_PRIVATE_KEY"] = "a-private-key"
	e["VAPID_SUBJECT"] = "mailto:owner@example.com"

	cfg, err := config.LoadFrom(env(e))
	if err != nil {
		t.Fatalf("notify mode was refused with no device token: %v", err)
	}
	if !cfg.Notify.Delivers() {
		t.Error("the configuration does not report that it delivers")
	}
}

// TestTheRetiredDeviceTokenIsRefusedRatherThanIgnored.
//
// FCM_DEVICE_TOKEN used to be where alerts were sent. Left in a file where it
// no longer does anything, somebody reads it and believes that is the token in
// use — and the symptom they are usually investigating is alerts having
// stopped, which is exactly what a stale token looks like. The variable that
// would mislead them is the one that must fail.
func TestTheRetiredDeviceTokenIsRefusedRatherThanIgnored(t *testing.T) {
	for _, mode := range []string{"silent", "notify"} {
		t.Run(mode, func(t *testing.T) {
			e := validEnv()
			e["SIGNAL_MODE"] = mode
			e["VAPID_PUBLIC_KEY"] = "a-public-key"
			e["VAPID_PRIVATE_KEY"] = "a-private-key"
			e["VAPID_SUBJECT"] = "mailto:owner@example.com"
			e["FCM_DEVICE_TOKEN"] = "a-token-nothing-reads"

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatal("LoadFrom() with FCM_DEVICE_TOKEN returned no error")
			}
			if !strings.Contains(err.Error(), "FCM_DEVICE_TOKEN") {
				t.Errorf("error %q does not name the variable to remove", err)
			}
			if !strings.Contains(err.Error(), "/api/v1/device") {
				t.Errorf("error %q does not say where the token comes from now", err)
			}
		})
	}
}

// TestNoStrategyIsConfiguredByDefault.
//
// Evaluating a strategy against the live stream has to be a decision somebody
// made. A default that started doing it would mean a deploy could begin
// producing signals nobody chose to produce.
func TestNoStrategyIsConfiguredByDefault(t *testing.T) {
	cfg, err := config.LoadFrom(env(validEnv()))
	if err != nil {
		t.Fatalf("LoadFrom() returned error: %v", err)
	}

	if cfg.Strategy.Enabled() {
		t.Errorf("a bare environment configures %q to run live", cfg.Strategy.Name)
	}
	if cfg.Strategy.TrendFilter != "" {
		t.Errorf("Strategy.TrendFilter = %q, want none", cfg.Strategy.TrendFilter)
	}
	if len(cfg.Strategy.Params) != 0 {
		t.Errorf("Strategy.Params = %v, want none", cfg.Strategy.Params)
	}
	if cfg.Strategy.Timeframe != constants.Timeframe(constants.DefaultStrategyTimeframe) {
		t.Errorf("Strategy.Timeframe = %q, want %q",
			cfg.Strategy.Timeframe, constants.DefaultStrategyTimeframe)
	}
}

// TestStrategyParametersAreReadInTheSameFormTheCLITakes.
//
// The reconciliation this phase exists to produce compares a live run against
// the backtest that predicted it. Two ways of spelling a parameter set is two
// ways for them to differ without anybody meaning them to.
func TestStrategyParametersAreReadInTheSameFormTheCLITakes(t *testing.T) {
	e := validEnv()
	e["STRATEGY_NAME"] = "ema_crossover"
	e["STRATEGY_TIMEFRAME"] = "1h"
	e["STRATEGY_PARAMS"] = "fast=9, slow = 21 ,stop_atr_mult=1.5"

	cfg, err := config.LoadFrom(env(e))
	if err != nil {
		t.Fatalf("LoadFrom() returned error: %v", err)
	}

	if !cfg.Strategy.Enabled() {
		t.Fatal("a named strategy is not enabled")
	}
	if cfg.Strategy.Timeframe != constants.Timeframe1h {
		t.Errorf("Strategy.Timeframe = %q, want 1h", cfg.Strategy.Timeframe)
	}

	want := map[string]string{"fast": "9", "slow": "21", "stop_atr_mult": "1.5"}
	if len(cfg.Strategy.Params) != len(want) {
		t.Fatalf("Strategy.Params = %v, want %v", cfg.Strategy.Params, want)
	}
	for name, value := range want {
		if got := cfg.Strategy.Params[name]; got != value {
			t.Errorf("Strategy.Params[%q] = %q, want %q", name, got, value)
		}
	}
}

/*
TestTheWebRootIsCheckedAtStartUp.

# What this prevents

A WEB_ROOT with a typo in it produces a process that starts, answers the API,
and 404s every page. The symptom is an app that will not load, which sends
whoever is looking at the export, the service worker and the browser cache
before the environment file.

Checking at start-up turns an afternoon into a refusal to boot that names the
variable.
*/
func TestTheWebRootIsCheckedAtStartUp(t *testing.T) {
	t.Run("a directory loads", func(t *testing.T) {
		e := validEnv()
		e["WEB_ROOT"] = t.TempDir()

		cfg, err := config.LoadFrom(env(e))
		if err != nil {
			t.Fatalf("LoadFrom() returned error: %v", err)
		}
		if cfg.App.WebRoot != e["WEB_ROOT"] {
			t.Fatalf("WebRoot = %q; want %q", cfg.App.WebRoot, e["WEB_ROOT"])
		}
	})

	t.Run("a path that is not there is refused, by name", func(t *testing.T) {
		e := validEnv()
		e["WEB_ROOT"] = filepath.Join(t.TempDir(), "not-exported")

		_, err := config.LoadFrom(env(e))
		if err == nil {
			t.Fatal("a WEB_ROOT that does not exist was accepted")
		}
		if !strings.Contains(err.Error(), "WEB_ROOT") {
			t.Fatalf("the error does not name the variable: %v", err)
		}
	})

	t.Run("a file is refused", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "index.html")
		if err := os.WriteFile(file, []byte("<!doctype html>"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		e := validEnv()
		e["WEB_ROOT"] = file

		if _, err := config.LoadFrom(env(e)); err == nil {
			t.Fatal("a WEB_ROOT pointing at a file was accepted")
		}
	})

	t.Run("unset serves no app", func(t *testing.T) {
		cfg, err := config.LoadFrom(env(validEnv()))
		if err != nil {
			t.Fatalf("LoadFrom() returned error: %v", err)
		}
		if cfg.App.WebRoot != "" {
			t.Fatalf("WebRoot = %q with nothing set; want empty", cfg.App.WebRoot)
		}
	})
}

/*
TestStreamOriginsAreHostsAndNeverAWildcard.

# What this prevents

STREAM_ALLOWED_ORIGINS widens who may open the signal feed from a browser. It
exists for development and is empty in a real deployment, so the two failures
worth refusing at start-up are a value that does not do what it looks like, and
a value that switches the check off while looking like it is on.

"*" is the second one. It reads as configuration and means "any page on any
host may read every signal, its entry, stop, target and reason".
*/
func TestStreamOriginsAreHostsAndNeverAWildcard(t *testing.T) {
	t.Run("an origin is reduced to its host", func(t *testing.T) {
		e := validEnv()
		e["STREAM_ALLOWED_ORIGINS"] = "http://localhost:8081, https://phone.tail1234.ts.net"

		cfg, err := config.LoadFrom(env(e))
		if err != nil {
			t.Fatalf("LoadFrom() returned error: %v", err)
		}

		want := []string{"localhost:8081", "phone.tail1234.ts.net"}
		if len(cfg.App.StreamOrigins) != len(want) {
			t.Fatalf("StreamOrigins = %v; want %v", cfg.App.StreamOrigins, want)
		}
		for i, host := range want {
			if cfg.App.StreamOrigins[i] != host {
				t.Fatalf("StreamOrigins = %v; want %v", cfg.App.StreamOrigins, want)
			}
		}
	})

	t.Run("a bare host is taken as written", func(t *testing.T) {
		e := validEnv()
		e["STREAM_ALLOWED_ORIGINS"] = "localhost:8081"

		cfg, err := config.LoadFrom(env(e))
		if err != nil {
			t.Fatalf("LoadFrom() returned error: %v", err)
		}
		if len(cfg.App.StreamOrigins) != 1 || cfg.App.StreamOrigins[0] != "localhost:8081" {
			t.Fatalf("StreamOrigins = %v", cfg.App.StreamOrigins)
		}
	})

	t.Run("a wildcard is refused", func(t *testing.T) {
		e := validEnv()
		e["STREAM_ALLOWED_ORIGINS"] = "*"

		_, err := config.LoadFrom(env(e))
		if err == nil {
			t.Fatal("a wildcard origin was accepted; the check is off and looks on")
		}
		if !strings.Contains(err.Error(), "STREAM_ALLOWED_ORIGINS") {
			t.Fatalf("the error does not name the variable: %v", err)
		}
	})

	t.Run("a url with a path is refused", func(t *testing.T) {
		e := validEnv()
		// An origin is scheme, host and port. A path here means somebody
		// expects it to be matched, and it never is.
		e["STREAM_ALLOWED_ORIGINS"] = "https://phone.tail1234.ts.net/app"

		if _, err := config.LoadFrom(env(e)); err == nil {
			t.Fatal("an origin with a path was accepted")
		}
	})

	t.Run("unset allows nothing beyond same-origin", func(t *testing.T) {
		cfg, err := config.LoadFrom(env(validEnv()))
		if err != nil {
			t.Fatalf("LoadFrom() returned error: %v", err)
		}
		if len(cfg.App.StreamOrigins) != 0 {
			t.Fatalf("StreamOrigins = %v with nothing set; want none", cfg.App.StreamOrigins)
		}
	})
}

/*
TestTheRetiredFCMVariablesAreRefusedRatherThanIgnored.

# What this prevents

Phase 09b replaced FCM with Web Push. The device is an iPhone running a PWA,
which cannot use FCM at all, so keeping both transports would have left one
exercised by nothing — and an untested delivery path is a broken one nobody has
noticed.

A .env still carrying FCM_PROJECT_ID and a service account path is the specific
danger: the deployment starts, the file reads as configured for delivery, and
the credentials it names are for a transport that no longer exists. Somebody
debugging silent alerts would go and check Firebase.

The same treatment NOTIFY_ENABLED and FCM_DEVICE_TOKEN already get, for the
same reason: a variable that no longer does anything must not sit in a file
looking like it does.
*/
func TestTheRetiredFCMVariablesAreRefusedRatherThanIgnored(t *testing.T) {
	for _, key := range []string{"FCM_PROJECT_ID", "FCM_CREDENTIALS_FILE"} {
		t.Run(key, func(t *testing.T) {
			e := validEnv()
			e[key] = "something-nothing-reads"

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatalf("LoadFrom() with %s returned no error", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name %s", err, key)
			}
			// Naming the replacement is the difference between a refusal that
			// costs one edit and one that costs an afternoon.
			if !strings.Contains(err.Error(), "VAPID_PUBLIC_KEY") {
				t.Errorf("error %q does not say what to set instead", err)
			}
		})
	}
}

/*
TestTheVAPIDPrivateKeyIsNeverInAnErrorMessage.

It is the one credential in this configuration that can push to the owner's
phone. Config errors are printed at start-up, land in journald, and get pasted
into issues — a loader that quoted the offending value would put it in all
three.
*/
func TestTheVAPIDPrivateKeyIsNeverInAnErrorMessage(t *testing.T) {
	const secret = "a-private-key-that-must-not-be-quoted"

	e := validEnv()
	e["SIGNAL_MODE"] = "notify"
	e["VAPID_PRIVATE_KEY"] = secret
	// Missing public key and subject, so the loader definitely errors.

	_, err := config.LoadFrom(env(e))
	if err == nil {
		t.Fatal("LoadFrom() with an incomplete VAPID configuration returned no error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the private key appears in the error: %v", err)
	}
}

/*
TestABareAddressIsRefusedAsAVAPIDSubject.

# What this prevents

RFC 8292 wants a URL — `mailto:someone@example.com` — and the natural way to
get it wrong is to write the address on its own, because "set this to a real
address" reads as "replace the address" and the mailto: goes with it.

The sender catches that too, but the sender only exists in the collector. So a
bare address started the api happily and crash-looped the collector, which
presents as two unrelated problems rather than one line in one file. Checking
it here makes every process refuse together, and the message carries the value
with the prefix already added.
*/
func TestABareAddressIsRefusedAsAVAPIDSubject(t *testing.T) {
	e := validEnv()
	e["SIGNAL_MODE"] = "notify"
	e["VAPID_PUBLIC_KEY"] = "a-public-key"
	e["VAPID_PRIVATE_KEY"] = "a-private-key"
	e["VAPID_SUBJECT"] = "owner@example.com"

	_, err := config.LoadFrom(env(e))
	if err == nil {
		t.Fatal("a bare address was accepted as a VAPID subject")
	}
	if !strings.Contains(err.Error(), "VAPID_SUBJECT") {
		t.Errorf("the error does not name the variable: %v", err)
	}
	// The fix, spelled out, because the difference is one word.
	if !strings.Contains(err.Error(), "mailto:owner@example.com") {
		t.Errorf("the error does not show what it should have been: %v", err)
	}
}

// TestAMailtoOrHttpsSubjectIsAccepted, which are the two RFC 8292 allows.
func TestAMailtoOrHttpsSubjectIsAccepted(t *testing.T) {
	for _, subject := range []string{
		"mailto:owner@example.com",
		"https://example.com/contact",
	} {
		e := validEnv()
		e["SIGNAL_MODE"] = "notify"
		e["VAPID_PUBLIC_KEY"] = "a-public-key"
		e["VAPID_PRIVATE_KEY"] = "a-private-key"
		e["VAPID_SUBJECT"] = subject

		if _, err := config.LoadFrom(env(e)); err != nil {
			t.Errorf("%q was refused: %v", subject, err)
		}
	}
}

/*
TestEachProcessRequiresOnlyWhatItIsGiven.

# What this prevents, and did not

SIGNAL_MODE=notify is a fact about the deployment. Whether a given binary sends
is a fact about that binary — only the collector does. The api holds the public
key because it serves it to the app, which cannot subscribe without it, and is
deliberately never given the private one: a process on the network boundary
that cannot push is one whose compromise cannot either.

Requiring all three of every process made the api unable to start at all in
notify mode while the collector ran fine. It reached the VPS, where it looked
like two unrelated faults, and the api had been crash-looping for some minutes
before anybody connected it to a setting changed in one file.

It was invisible in development because a shell that sources the whole .env
gives every process everything. Only compose withholds, and only in production.
*/
func TestEachProcessRequiresOnlyWhatItIsGiven(t *testing.T) {
	// Exactly what deploy/docker-compose.yml passes to the api.
	apiEnv := func() map[string]string {
		e := validEnv()
		e["SIGNAL_MODE"] = "notify"
		e["VAPID_PUBLIC_KEY"] = "a-public-key"
		return e
	}

	t.Run("the api starts on the public key alone", func(t *testing.T) {
		if _, err := config.LoadFrom(env(apiEnv()), config.WithoutDelivery()); err != nil {
			t.Fatalf("the api cannot start on what compose gives it: %v", err)
		}
	})

	t.Run("the api still needs the public key, which it serves", func(t *testing.T) {
		e := apiEnv()
		delete(e, "VAPID_PUBLIC_KEY")

		_, err := config.LoadFrom(env(e), config.WithoutDelivery())
		if err == nil {
			t.Fatal("the api started with nothing to hand the app to subscribe with")
		}
		if !strings.Contains(err.Error(), "VAPID_PUBLIC_KEY") {
			t.Errorf("the error does not name it: %v", err)
		}
	})

	t.Run("the collector needs the sending half too", func(t *testing.T) {
		_, err := config.LoadFrom(env(apiEnv()))
		if err == nil {
			t.Fatal("the collector started with no private key to sign with")
		}
		for _, key := range []string{"VAPID_PRIVATE_KEY", "VAPID_SUBJECT"} {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("the error does not name %s: %v", key, err)
			}
		}
	})

	t.Run("the CLIs need none of it", func(t *testing.T) {
		e := validEnv()
		e["SIGNAL_MODE"] = "notify"

		if _, err := config.LoadFrom(env(e),
			config.WithoutHTTPServer(), config.WithoutDelivery()); err != nil {
			t.Fatalf("a read-only tool was refused over a key it never uses: %v", err)
		}
	})
}

/*
TestComposeGivesTheApiTheHalfItNeedsAndNotTheOther.

The other side of the same coupling. The test above says what each process
requires; this says what the compose file actually supplies, because the bug
lived in the gap between the two and neither file alone was wrong.

It reads the compose file as text rather than parsing YAML — coarse, and enough
to catch a key being added to the wrong service.
*/
func TestComposeGivesTheApiTheHalfItNeedsAndNotTheOther(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker-compose.yml"))
	if err != nil {
		t.Skipf("no compose file alongside the module: %v", err)
	}

	// The api service block: from "  api:" to the next top-level service.
	compose := string(raw)
	start := strings.Index(compose, "\n  api:\n")
	if start < 0 {
		t.Fatal("no api service in the compose file")
	}
	rest := compose[start+len("\n  api:\n"):]
	end := strings.Index(rest, "\n  collector:")
	if end < 0 {
		t.Fatal("no collector service after the api")
	}
	api := rest[:end]

	if !strings.Contains(api, "VAPID_PUBLIC_KEY") {
		t.Error("the api is not given VAPID_PUBLIC_KEY; it has nothing to hand the app")
	}
	if strings.Contains(api, "VAPID_PRIVATE_KEY") {
		t.Error("the api is given VAPID_PRIVATE_KEY; only the collector should be able to push")
	}

	collector := rest[end:]
	for _, key := range []string{"VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT"} {
		if !strings.Contains(collector, key) {
			t.Errorf("the collector is not given %s; it cannot send without it", key)
		}
	}
}

/*
TestEachBinaryDeclaresWhatItIs.

# What this prevents

The check above knows what a process needs once it says what it is. Nothing
knew whether the binaries say so. Removing `config.WithoutDelivery()` from
server/main.go puts the api back to demanding a private key it is never given,
and every test still passed — which is how the bug got out in the first place:
the two halves were in different files and nothing read both.

Coarse on purpose. It reads the call as text rather than resolving it, which is
enough to catch an option being dropped or added to the wrong binary.
*/
func TestEachBinaryDeclaresWhatItIs(t *testing.T) {
	for _, binary := range []struct {
		file    string
		wants   []string
		refuses []string
	}{
		// Serves the app and the API; never pushes.
		{file: "main.go", wants: []string{"WithoutDelivery"}, refuses: []string{"WithoutHTTPServer"}},

		// The only one that sends, and the only one given the private key.
		{file: "collector/main.go", refuses: []string{"WithoutDelivery", "WithoutHTTPServer"}},

		// Read-only tools: no socket, no push.
		{file: "backtest/main.go", wants: []string{"WithoutHTTPServer", "WithoutDelivery"}},
		{file: "reconcile/main.go", wants: []string{"WithoutHTTPServer", "WithoutDelivery"}},
	} {
		t.Run(binary.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(binary.file)))
			if err != nil {
				t.Skipf("cannot read %s: %v", binary.file, err)
			}

			// To the end of the line rather than to the first ")", which is the
			// closing paren of the first option rather than of the call.
			call := regexp.MustCompile(`config\.Load\(.*`).FindString(string(raw))
			if call == "" {
				t.Fatalf("%s does not call config.Load", binary.file)
			}

			for _, want := range binary.wants {
				if !strings.Contains(call, want) {
					t.Errorf("%s calls %s, which does not declare %s", binary.file, call, want)
				}
			}
			for _, refuse := range binary.refuses {
				if strings.Contains(call, refuse) {
					t.Errorf("%s calls %s, which should not declare %s", binary.file, call, refuse)
				}
			}
		})
	}
}
