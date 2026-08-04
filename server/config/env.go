// Package config loads and validates the whole application configuration
// from environment variables only (12-factor). There is no config file.
//
// Load fails fast and reports every problem at once, naming the offending
// environment variables, so a misconfigured deployment cannot start half
// broken.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/constants"
	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// App holds process-wide settings.
type App struct {
	// Env is dev or prod; it also selects the log handler.
	Env constants.AppEnv
	// LogLevel is the minimum slog level emitted.
	LogLevel slog.Level
	// HTTPPort is the port the API server listens on.
	HTTPPort int
}

// Database holds PostgreSQL/TimescaleDB connection settings.
type Database struct {
	// URL is the libpq/pgx connection string.
	URL string
	// MaxConns caps the pgx pool size.
	MaxConns int32
	// ConnectTimeout bounds a single connection attempt.
	ConnectTimeout time.Duration
}

// Market describes what the platform observes and what trading it costs.
type Market struct {
	// Symbol is the single instrument this deployment analyses, e.g. BTCUSDT.
	Symbol string
	// Type is spot or futures. Futures behaviour is not implemented yet.
	Type constants.MarketType
	// Timeframes are the candle intervals to collect, in the order given.
	Timeframes []constants.Timeframe
	// FeeTakerPct is the taker fee in percent, e.g. 0.05 means 0.05%.
	// It is configuration, never a constant in code, because every backtest
	// result must be reported net of cost.
	FeeTakerPct decimal.Decimal
	// SlippageTicks is the assumed slippage of a fill, in price ticks.
	SlippageTicks int

	// RESTBaseURL and WSBaseURL are the exchange endpoints. They are
	// configuration rather than constants so the futures endpoints can be
	// swapped in later without touching a single call site.
	RESTBaseURL string
	WSBaseURL   string

	// BackfillFrom is how far back history is fetched when a timeframe has no
	// stored candle at all. Once anything is stored, backfill resumes from the
	// latest open_time instead and this value is not consulted.
	BackfillFrom time.Time

	// GapcheckInterval is how often the collector scans for holes in the
	// candle series.
	GapcheckInterval time.Duration

	// HeartbeatInterval is how often the collector writes its status row. The
	// api reads that row to answer /internal/market/status, because the two
	// run in separate containers and cannot share memory.
	HeartbeatInterval time.Duration
}

// Notify holds push notification settings. Phase 01 only carries the values;
// no notification client is wired up yet.
type Notify struct {
	// Enabled turns push delivery on. When false the other fields are unused.
	Enabled bool
	// FCMProjectId is the Firebase project that owns the device token.
	FCMProjectId string
	// FCMCredentialsFile is the path to the service account JSON file.
	FCMCredentialsFile string
}

// Config is the fully validated configuration of a process.
type Config struct {
	App      App
	Database Database
	Market   Market
	Notify   Notify
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom reads the configuration through lookup. It exists so tests can
// supply an environment without mutating the process one.
func LoadFrom(lookup helper.LookupFunc) (*Config, error) {
	l := &loader{lookup: lookup}

	cfg := &Config{
		App: App{
			Env:      l.appEnv("APP_ENV"),
			LogLevel: l.logLevel("LOG_LEVEL"),
			HTTPPort: l.requiredPort("HTTP_PORT"),
		},
		Database: Database{
			URL:            l.requiredString("DATABASE_URL"),
			MaxConns:       int32(l.optionalInt("DATABASE_MAX_CONNS", constants.DefaultDatabaseMaxConns, 1, 1000)),
			ConnectTimeout: constants.DefaultConnectTimeout,
		},
		Market: Market{
			Symbol:        l.optionalString("MARKET_SYMBOL", constants.DefaultMarketSymbol),
			Type:          l.marketType("MARKET_TYPE"),
			Timeframes:    l.timeframes("MARKET_TIMEFRAMES"),
			FeeTakerPct:   l.feePct("FEE_TAKER_PCT"),
			SlippageTicks: l.optionalInt("SLIPPAGE_TICKS", constants.DefaultSlippageTicks, 0, 1000),

			RESTBaseURL:       l.baseURL("BINANCE_REST_BASE_URL", constants.DefaultBinanceRESTBaseURL, "https"),
			WSBaseURL:         l.baseURL("BINANCE_WS_BASE_URL", constants.DefaultBinanceWSBaseURL, "wss"),
			BackfillFrom:      l.timestamp("MARKET_BACKFILL_FROM", constants.DefaultMarketBackfillFrom),
			GapcheckInterval:  l.duration("MARKET_GAPCHECK_INTERVAL", constants.DefaultGapcheckInterval, time.Minute, 24*time.Hour),
			HeartbeatInterval: l.duration("COLLECTOR_HEARTBEAT_INTERVAL", constants.DefaultHeartbeatInterval, time.Second, time.Minute),
		},
		Notify: Notify{
			Enabled:            l.optionalBool("NOTIFY_ENABLED", false),
			FCMProjectId:       l.optionalString("FCM_PROJECT_ID", ""),
			FCMCredentialsFile: l.optionalString("FCM_CREDENTIALS_FILE", ""),
		},
	}

	if cfg.Notify.Enabled {
		if cfg.Notify.FCMProjectId == "" {
			l.missing = append(l.missing, "FCM_PROJECT_ID")
		}
		if cfg.Notify.FCMCredentialsFile == "" {
			l.missing = append(l.missing, "FCM_CREDENTIALS_FILE")
		}
	}

	if err := l.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsProd reports whether the process runs in the production environment.
func (c *Config) IsProd() bool { return c.App.Env == constants.EnvProd }

// HTTPAddr returns the listen address of the API server.
func (c *Config) HTTPAddr() string { return fmt.Sprintf(":%d", c.App.HTTPPort) }

// loader reads values and accumulates every problem instead of stopping at
// the first one, so an operator sees the whole list in a single run.
type loader struct {
	lookup  helper.LookupFunc
	missing []string
	invalid []error
}

func (l *loader) get(key string) (string, bool) {
	return helper.LookupENV(l.lookup, key)
}

func (l *loader) invalidf(key, format string, args ...any) {
	l.invalid = append(l.invalid, fmt.Errorf("%w: %s: %s", constants.ErrInvalidEnv, key, fmt.Sprintf(format, args...)))
}

func (l *loader) requiredString(key string) string {
	v, ok := l.get(key)
	if !ok {
		l.missing = append(l.missing, key)
		return ""
	}
	return v
}

func (l *loader) optionalString(key, def string) string {
	v, ok := l.get(key)
	if !ok {
		return def
	}
	return v
}

// appEnv is required by the spec: there is deliberately no default, because
// silently assuming "dev" in production would change the log format and the
// safety expectations of the process.
func (l *loader) appEnv(key string) constants.AppEnv {
	v, ok := l.get(key)
	if !ok {
		l.missing = append(l.missing, key)
		return ""
	}
	env := constants.AppEnv(strings.ToLower(v))
	if !env.Valid() {
		l.invalidf(key, "%q is not one of %q, %q", v, constants.EnvDev, constants.EnvProd)
		return ""
	}
	return env
}

func (l *loader) requiredPort(key string) int {
	v, ok := l.get(key)
	if !ok {
		l.missing = append(l.missing, key)
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.invalidf(key, "%q is not a number", v)
		return 0
	}
	if n < 1 || n > 65535 {
		l.invalidf(key, "%d is outside 1-65535", n)
		return 0
	}
	return n
}

func (l *loader) optionalInt(key string, def, min, max int) int {
	v, ok := l.get(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.invalidf(key, "%q is not a number", v)
		return def
	}
	if n < min || n > max {
		l.invalidf(key, "%d is outside %d-%d", n, min, max)
		return def
	}
	return n
}

func (l *loader) optionalBool(key string, def bool) bool {
	v, ok := l.get(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.invalidf(key, "%q is not a boolean", v)
		return def
	}
	return b
}

// logLevel is required by the spec, so an unset value is reported as missing
// rather than silently defaulted.
func (l *loader) logLevel(key string) slog.Level {
	v, ok := l.get(key)
	if !ok {
		l.missing = append(l.missing, key)
		return slog.LevelInfo
	}
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		l.invalidf(key, "%q is not one of debug, info, warn, error", v)
		return slog.LevelInfo
	}
}

func (l *loader) marketType(key string) constants.MarketType {
	v := l.optionalString(key, constants.MarketTypeSpot.String())
	mt, err := constants.ParseMarketType(v)
	if err != nil {
		l.invalidf(key, "%s", err)
		return ""
	}
	return mt
}

func (l *loader) timeframes(key string) []constants.Timeframe {
	raw := l.optionalString(key, constants.DefaultMarketTimeframes)
	invalidBefore := len(l.invalid)

	parts := strings.Split(raw, ",")
	out := make([]constants.Timeframe, 0, len(parts))
	seen := make(map[constants.Timeframe]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tf, err := constants.ParseTimeframe(part)
		if err != nil {
			l.invalidf(key, "%s", err)
			continue
		}
		if _, dup := seen[tf]; dup {
			l.invalidf(key, "timeframe %q is listed twice", tf)
			continue
		}
		seen[tf] = struct{}{}
		out = append(out, tf)
	}

	if len(out) == 0 && len(l.invalid) == invalidBefore {
		l.invalidf(key, "no timeframe given")
	}
	return out
}

// baseURL validates an endpoint and strips any trailing slash, so call sites
// can join paths without producing a double slash.
func (l *loader) baseURL(key, def, wantScheme string) string {
	raw := l.optionalString(key, def)

	parsed, err := url.Parse(raw)
	if err != nil {
		l.invalidf(key, "%q is not a URL: %s", raw, err)
		return ""
	}
	if parsed.Scheme != wantScheme {
		l.invalidf(key, "%q must use the %s scheme", raw, wantScheme)
		return ""
	}
	if parsed.Host == "" {
		l.invalidf(key, "%q has no host", raw)
		return ""
	}
	return strings.TrimRight(raw, "/")
}

// timestamp parses an RFC3339 instant and normalises it to UTC.
func (l *loader) timestamp(key, def string) time.Time {
	raw := l.optionalString(key, def)

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		l.invalidf(key, "%q is not an RFC3339 timestamp", raw)
		return time.Time{}
	}
	if t.After(time.Now()) {
		l.invalidf(key, "%s is in the future", raw)
		return time.Time{}
	}
	return helper.UTC(t)
}

// duration parses a Go duration string and bounds it, so a typo like "15"
// (which parses as 15ns) cannot turn a 15 minute ticker into a busy loop.
func (l *loader) duration(key string, def, min, max time.Duration) time.Duration {
	raw := l.optionalString(key, def.String())

	d, err := time.ParseDuration(raw)
	if err != nil {
		l.invalidf(key, "%q is not a duration (want e.g. 15m, 30s)", raw)
		return def
	}
	if d < min || d > max {
		l.invalidf(key, "%s is outside %s-%s", d, min, max)
		return def
	}
	return d
}

func (l *loader) feePct(key string) decimal.Decimal {
	v := l.optionalString(key, constants.DefaultFeeTakerPct)
	d, err := decimal.NewFromString(v)
	if err != nil {
		l.invalidf(key, "%q is not a decimal number", v)
		return decimal.Zero
	}
	if d.IsNegative() || d.GreaterThanOrEqual(decimal.NewFromInt(100)) {
		l.invalidf(key, "%s is outside 0-100 (percent)", d)
		return decimal.Zero
	}
	return d
}

// err joins every collected problem into a single error. Callers can test it
// with errors.Is against constants.ErrMissingEnv and constants.ErrInvalidEnv.
func (l *loader) err() error {
	if len(l.missing) == 0 && len(l.invalid) == 0 {
		return nil
	}

	errs := make([]error, 0, len(l.missing)+len(l.invalid))
	for _, key := range l.missing {
		errs = append(errs, fmt.Errorf("%w: %s", constants.ErrMissingEnv, key))
	}
	errs = append(errs, l.invalid...)
	return errors.Join(errs...)
}
