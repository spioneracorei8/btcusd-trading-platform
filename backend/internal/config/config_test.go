package config_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/config"
	"github.com/spioneracorei8/btcusd-trading-platform/backend/internal/domain"
)

// env turns a map into a config.LookupFunc so tests never touch the real
// process environment.
func env(m map[string]string) config.LookupFunc {
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

	if cfg.App.Env != config.EnvDev {
		t.Errorf("App.Env = %q, want %q", cfg.App.Env, config.EnvDev)
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

	if cfg.Market.Symbol != "BTCUSDT" {
		t.Errorf("Market.Symbol = %q, want BTCUSDT", cfg.Market.Symbol)
	}
	if cfg.Market.Type != domain.MarketTypeSpot {
		t.Errorf("Market.Type = %q, want %q", cfg.Market.Type, domain.MarketTypeSpot)
	}

	wantTFs := []domain.Timeframe{
		domain.Timeframe1m, domain.Timeframe5m, domain.Timeframe15m, domain.Timeframe1h,
	}
	if len(cfg.Market.Timeframes) != len(wantTFs) {
		t.Fatalf("Market.Timeframes = %v, want %v", cfg.Market.Timeframes, wantTFs)
	}
	for i, tf := range wantTFs {
		if cfg.Market.Timeframes[i] != tf {
			t.Errorf("Market.Timeframes[%d] = %q, want %q", i, cfg.Market.Timeframes[i], tf)
		}
	}

	if got := cfg.Market.FeeTakerPct.String(); got != "0.05" {
		t.Errorf("Market.FeeTakerPct = %s, want 0.05", got)
	}
	if cfg.Market.SlippageTicks != 1 {
		t.Errorf("Market.SlippageTicks = %d, want 1", cfg.Market.SlippageTicks)
	}
	if cfg.Notify.Enabled {
		t.Error("Notify.Enabled = true, want false by default")
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
	e["NOTIFY_ENABLED"] = "true"
	e["FCM_PROJECT_ID"] = "btc-signals"
	e["FCM_CREDENTIALS_FILE"] = "/run/secrets/fcm.json"

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
	if cfg.Market.Type != domain.MarketTypeFutures {
		t.Errorf("Market.Type = %q, want %q", cfg.Market.Type, domain.MarketTypeFutures)
	}
	if len(cfg.Market.Timeframes) != 3 || cfg.Market.Timeframes[2] != domain.Timeframe4h {
		t.Errorf("Market.Timeframes = %v, want [1m 5m 4h]", cfg.Market.Timeframes)
	}
	if got := cfg.Market.FeeTakerPct.String(); got != "0.04" {
		t.Errorf("Market.FeeTakerPct = %s, want 0.04", got)
	}
	if cfg.Market.SlippageTicks != 3 {
		t.Errorf("Market.SlippageTicks = %d, want 3", cfg.Market.SlippageTicks)
	}
	if !cfg.Notify.Enabled || cfg.Notify.FCMProjectID != "btc-signals" {
		t.Errorf("Notify = %+v, want enabled with project id", cfg.Notify)
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
			if !errors.Is(err, config.ErrMissingEnv) {
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
	if !errors.Is(err, config.ErrMissingEnv) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEnv()
			e[tt.key] = tt.value

			_, err := config.LoadFrom(env(e))
			if err == nil {
				t.Fatalf("LoadFrom() with %s=%q returned no error", tt.key, tt.value)
			}
			if !errors.Is(err, config.ErrInvalidEnv) {
				t.Errorf("error %v does not wrap ErrInvalidEnv", err)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q does not name %s", err, tt.wantKey)
			}
		})
	}
}

func TestLoadFromNotifyEnabledRequiresCredentials(t *testing.T) {
	e := validEnv()
	e["NOTIFY_ENABLED"] = "true"

	_, err := config.LoadFrom(env(e))
	if err == nil {
		t.Fatal("LoadFrom() with notifications enabled but no credentials returned no error")
	}
	if !errors.Is(err, config.ErrMissingEnv) {
		t.Errorf("error %v does not wrap ErrMissingEnv", err)
	}
	for _, key := range []string{"FCM_PROJECT_ID", "FCM_CREDENTIALS_FILE"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not name %s", err, key)
		}
	}
}
