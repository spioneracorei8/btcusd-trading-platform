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
	"github.com/spioneracorei8/btcusd-trading-platform/server/services/backtest"
)

// App holds process-wide settings.
type App struct {
	// Env is dev or prod; it also selects the log handler.
	Env constants.AppEnv
	// LogLevel is the minimum slog level emitted.
	LogLevel slog.Level
	// HTTPPort is the port the API server listens on.
	HTTPPort int

	// WebRoot is the directory holding the exported web app, or "" to serve
	// no app at all.
	//
	// The app is served by this process rather than beside it so that it and
	// the API share an origin. That is not tidiness: a page served from one
	// origin and an API on another needs CORS on every endpoint and an origin
	// allowlist on the websocket, and the first thing anybody does when a
	// preflight fails is widen the allowlist. Same-origin has none of those
	// decisions in it.
	WebRoot string

	// StreamOrigins are browser origins the websocket accepts in addition to
	// the one the request was served from.
	//
	// Empty in a real deployment, where the app and the API share an origin.
	// It exists for development, where the app is served by Metro on one port
	// and the API answers on another. Adding a host here lets any page on that
	// host read the signal feed, so it is a development tool and says so.
	StreamOrigins []string
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

	// FeeMakerPct is what a resting order pays instead, charged only on a
	// fill that actually rested.
	FeeMakerPct decimal.Decimal

	// EntryOrderType and ExitOrderType decide how orders reach the book.
	// They are separate because the realistic configuration is asymmetric: a
	// limit entry can wait, and a stop cannot.
	EntryOrderType constants.OrderType
	ExitOrderType  constants.OrderType

	// LimitOrderTimeoutBars is how many bars an unfilled limit order rests
	// before it is cancelled and the trade never happens.
	LimitOrderTimeoutBars int

	// CostModel selects percentage-of-notional or spread-in-points, and the
	// venue parameters the second one needs. They take effect together: a
	// percentage run ignores all of them.
	CostModel        constants.CostModel
	SpreadPoints     int
	PointValue       decimal.Decimal
	ContractSize     decimal.Decimal
	MinLot           decimal.Decimal
	LotStep          decimal.Decimal
	CommissionPerLot decimal.Decimal

	// MaxLeverage is how much notional the account may hold per unit of
	// equity. One is a cash account, which is what every evaluation before
	// the CFD venue assumed; a margin venue is opted into.
	MaxLeverage decimal.Decimal

	// SlippageTicks is the assumed slippage of a fill, in price ticks.
	SlippageTicks int
	// TickSize is what one of those ticks is worth in quote currency. Without
	// it SlippageTicks is a count with no unit and no fill can be priced.
	TickSize decimal.Decimal

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

// Strategy is the live signal path's configuration.
//
// # Why an empty name means no live evaluation
//
// Beginning to evaluate a strategy against the live stream should be a
// decision somebody made, not something that happened because a deploy went
// out. An unset STRATEGY_NAME leaves the collector doing exactly what it did
// before: collecting candles and nothing else.
type Strategy struct {
	// Name is the registered strategy to run, or empty for none.
	Name string

	// Timeframe is the base timeframe it decides on. It must be one of
	// MARKET_TIMEFRAMES, or the candles it needs are not being collected.
	Timeframe constants.Timeframe

	// Params are the overrides, in the same key=value form --param takes, so
	// live and backtest are configured through one mechanism and one set of
	// names.
	Params map[string]string

	// TrendFilter is the filter to gate signals with, or empty for none.
	TrendFilter string
}

// Enabled reports whether a strategy is configured to run live.
func (s Strategy) Enabled() bool { return s.Name != "" }

// Outcome is how live signals are followed to their end.
//
// # Why this runs whether or not a strategy does
//
// The follower reads signals out of the table, not out of the evaluator. A
// process that stops following when no strategy is configured would leave the
// signals of a strategy that has just been switched off unresolved forever —
// and those are exactly the ones worth knowing about.
type Outcome struct {
	// ExpiryBars is how many bars a signal is followed before it is recorded
	// as expired. It is a measurement window, not a trading rule: nothing
	// here opens or closes anything.
	ExpiryBars int

	// Interval is how often open signals are advanced against newly stored
	// candles.
	Interval time.Duration
}

// Notify holds push notification settings. Phase 01 only carries the values;
// no notification client is wired up yet.
type Notify struct {
	// SignalMode decides whether a recorded signal is also delivered.
	//
	// # Why this replaced NOTIFY_ENABLED
	//
	// Two switches for one behaviour is a trap: somebody sets SIGNAL_MODE to
	// notify, gets nothing because the other one is false, and looks for the
	// fault in Firebase. The system can do exactly two things, so it has
	// exactly one switch, and NOTIFY_ENABLED is rejected at start-up rather
	// than quietly ignored.
	SignalMode constants.SignalMode

	// VAPIDPublicKey identifies this application server to the push service,
	// and is what the browser subscribes against. It reaches the app, so it is
	// not a secret and is not treated as one.
	VAPIDPublicKey string

	// VAPIDPrivateKey signs the JWT that proves a push came from here. It is
	// the one credential in this configuration that must never be logged,
	// quoted in an error, or reach the browser.
	VAPIDPrivateKey string

	// VAPIDSubject says who to contact about this application server — a
	// mailto: or https: URL, required by RFC 8292. Push services use it when
	// something is wrong with the traffic, not for delivery.
	VAPIDSubject string
}

// Where the device token went, and then FCM with it
//
// FCM_DEVICE_TOKEN used to be here. It became a row in `devices`, written by
// the phone through POST /api/v1/device (ADR 0026), because a token in this
// file is the previous one from the moment Firebase decides otherwise, and the
// deployment goes on looking configured while every delivery fails.
//
// Phase 09b retired the rest of it. The device is an iPhone and the app is a
// PWA, which cannot use FCM at all; keeping both transports would leave one
// exercised by nothing, and an untested delivery path is a broken one nobody
// has noticed. FCM_PROJECT_ID and FCM_CREDENTIALS_FILE are rejected at
// start-up rather than ignored — the same treatment NOTIFY_ENABLED and
// FCM_DEVICE_TOKEN get, for the same reason. See ADR 0028.

// Delivers reports whether signals are pushed to the owner.
func (n Notify) Delivers() bool { return n.SignalMode.Delivers() }

// Config is the fully validated configuration of a process.
type Config struct {
	App      App
	Database Database
	Market   Market
	Strategy Strategy
	Outcome  Outcome
	Notify   Notify

	// EnvFile is the .env that filled any gaps in the environment, or empty
	// when none was found. Reported so a run can say where its configuration
	// came from — "which .env did this actually read" is otherwise guesswork.
	EnvFile string
}

// Option narrows what a process requires.
//
// The three binaries share this configuration but not all of its obligations:
// the backtest CLI opens no socket, and demanding a listen port from it turned
// a read-only analysis tool into something that refused to start over a
// setting it would never use.
type Option func(*settings)

type settings struct{ servesHTTP bool }

// WithoutHTTPServer relaxes HTTP_PORT for a process that never listens.
func WithoutHTTPServer() Option {
	return func(s *settings) { s.servesHTTP = false }
}

// Load reads the configuration from the process environment, after loading a
// .env file if one is found nearby.
func Load(opts ...Option) (*Config, error) {
	lookup, envFile, err := dotEnvLookup()
	if err != nil {
		return nil, err
	}

	cfg, err := LoadFrom(lookup, opts...)
	if err != nil {
		return nil, err
	}
	cfg.EnvFile = envFile
	return cfg, nil
}

// LoadFrom reads the configuration through lookup. It exists so tests can
// supply an environment without mutating the process one.
func LoadFrom(lookup helper.LookupFunc, opts ...Option) (*Config, error) {
	set := settings{servesHTTP: true}
	for _, opt := range opts {
		opt(&set)
	}

	l := &loader{lookup: lookup}

	cfg := &Config{
		App: App{
			Env:           l.appEnv("APP_ENV"),
			LogLevel:      l.logLevel("LOG_LEVEL"),
			HTTPPort:      l.port("HTTP_PORT", set.servesHTTP),
			WebRoot:       l.directory("WEB_ROOT"),
			StreamOrigins: l.originHosts("STREAM_ALLOWED_ORIGINS"),
		},
		Database: Database{
			URL:            l.requiredString("DATABASE_URL"),
			MaxConns:       int32(l.optionalInt("DATABASE_MAX_CONNS", constants.DefaultDatabaseMaxConns, 1, 1000)),
			ConnectTimeout: constants.DefaultConnectTimeout,
		},
		Market: Market{
			Symbol:      l.optionalString("MARKET_SYMBOL", constants.DefaultMarketSymbol),
			Type:        l.marketType("MARKET_TYPE"),
			Timeframes:  l.timeframes("MARKET_TIMEFRAMES"),
			FeeTakerPct: l.feePct("FEE_TAKER_PCT"),
			FeeMakerPct: l.makerFeePct("FEE_MAKER_PCT"),

			EntryOrderType: l.orderType("ENTRY_ORDER_TYPE", constants.DefaultEntryOrderType),
			ExitOrderType:  l.orderType("EXIT_ORDER_TYPE", constants.DefaultExitOrderType),
			LimitOrderTimeoutBars: l.optionalInt("LIMIT_ORDER_TIMEOUT_BARS",
				constants.DefaultLimitOrderTimeoutBars, 1, 1000),

			CostModel:        l.costModel("COST_MODEL"),
			SpreadPoints:     l.optionalInt("SPREAD_POINTS", constants.DefaultSpreadPoints, 0, 10_000_000),
			PointValue:       l.positiveDecimal("POINT_VALUE", constants.DefaultPointValue),
			ContractSize:     l.positiveDecimal("CONTRACT_SIZE", constants.DefaultContractSize),
			MinLot:           l.positiveDecimal("MIN_LOT", constants.DefaultMinLot),
			LotStep:          l.positiveDecimal("LOT_STEP", constants.DefaultLotStep),
			CommissionPerLot: l.nonNegativeDecimal("COMMISSION_PER_LOT", constants.DefaultCommissionPerLot),
			MaxLeverage:      l.positiveDecimal("MAX_LEVERAGE", constants.DefaultMaxLeverage),
			SlippageTicks:    l.optionalInt("SLIPPAGE_TICKS", constants.DefaultSlippageTicks, 0, 1000),
			TickSize:         l.tickSize("MARKET_TICK_SIZE"),

			RESTBaseURL:       l.baseURL("BINANCE_REST_BASE_URL", constants.DefaultBinanceRESTBaseURL, "https"),
			WSBaseURL:         l.baseURL("BINANCE_WS_BASE_URL", constants.DefaultBinanceWSBaseURL, "wss"),
			BackfillFrom:      l.timestamp("MARKET_BACKFILL_FROM", constants.DefaultMarketBackfillFrom),
			GapcheckInterval:  l.duration("MARKET_GAPCHECK_INTERVAL", constants.DefaultGapcheckInterval, time.Minute, 24*time.Hour),
			HeartbeatInterval: l.duration("COLLECTOR_HEARTBEAT_INTERVAL", constants.DefaultHeartbeatInterval, time.Second, time.Minute),
		},
		Strategy: Strategy{
			Name:        l.optionalString("STRATEGY_NAME", ""),
			Timeframe:   l.optionalTimeframe("STRATEGY_TIMEFRAME", constants.DefaultStrategyTimeframe),
			Params:      l.params("STRATEGY_PARAMS"),
			TrendFilter: l.optionalString("STRATEGY_TREND_FILTER", ""),
		},

		Outcome: Outcome{
			ExpiryBars: l.optionalInt("SIGNAL_EXPIRY_BARS", constants.DefaultSignalExpiryBars, 1, 10_000),
			Interval:   l.duration("OUTCOME_INTERVAL", constants.DefaultOutcomeInterval, time.Second, time.Hour),
		},

		Notify: Notify{
			SignalMode:      l.signalMode("SIGNAL_MODE"),
			VAPIDPublicKey:  l.optionalString("VAPID_PUBLIC_KEY", ""),
			VAPIDPrivateKey: l.optionalString("VAPID_PRIVATE_KEY", ""),
			VAPIDSubject:    l.optionalString("VAPID_SUBJECT", ""),
		},
	}

	// A mode that claims to deliver and cannot is worse than one that says it
	// will not: the first looks like it is working.
	if cfg.Notify.Delivers() {
		if cfg.Notify.VAPIDPublicKey == "" {
			l.missing = append(l.missing, "VAPID_PUBLIC_KEY")
		}
		if cfg.Notify.VAPIDPrivateKey == "" {
			l.missing = append(l.missing, "VAPID_PRIVATE_KEY")
		}
		if cfg.Notify.VAPIDSubject == "" {
			l.missing = append(l.missing, "VAPID_SUBJECT")
		}
	}

	// Deliberately not "and a subscription". The phone registers itself after
	// the app is installed, so a deployment that refused to start without one
	// could never reach the state where one exists.
	l.rejectRetired(retiredDeviceToken,
		"the device is now registered by the app through POST /api/v1/device "+
			"and stored in the devices table; remove it")

	// FCM is gone. A deployment carrying its configuration would start, look
	// configured for delivery, and deliver over something else entirely — or,
	// in silent mode, look ready to deliver when nothing is.
	for _, retired := range retiredFCM {
		l.rejectRetired(retired,
			"FCM was replaced by Web Push in phase 09b (ADR 0028); this does nothing. "+
				"Set VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and VAPID_SUBJECT instead, "+
				"and delete the service account key from this host")
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

// retiredNotifyEnabled is the switch SIGNAL_MODE replaced. It is named here so
// a stale .env fails loudly instead of misleading whoever reads it next.
const retiredNotifyEnabled = "NOTIFY_ENABLED"

// retiredDeviceToken is the variable the devices table replaced. Named here
// for the same reason: a stale .env should fail loudly rather than leave
// somebody believing the token in it is the one being used.
const retiredDeviceToken = "FCM_DEVICE_TOKEN"

// retiredFCM are the variables Web Push replaced. Rejected rather than
// ignored: a deployment holding them looks configured for a transport it no
// longer has, which is exactly the state this system is built to make
// impossible to be in without knowing.
var retiredFCM = []string{"FCM_PROJECT_ID", "FCM_CREDENTIALS_FILE"}

// rejectRetired reports a variable that no longer does anything, with what to
// do instead. Ignoring it silently is what leaves an operator reading a value
// in their .env that nothing has consulted for months.
func (l *loader) rejectRetired(key, advice string) {
	if _, ok := l.get(key); ok {
		l.invalidf(key, "%s", advice)
	}
}

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

// port reads a listen port. When the process serves no HTTP it is optional,
// but a value that is present must still be valid: a typo in a variable this
// process ignores is worth reporting, and silently accepting nonsense here
// would make the same .env behave differently for the api.
func (l *loader) port(key string, required bool) int {
	v, ok := l.get(key)
	if !ok {
		if required {
			l.missing = append(l.missing, key)
		}
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

// signalMode reads SIGNAL_MODE, and refuses the switch it replaced.
//
// # Why a leftover NOTIFY_ENABLED is an error
//
// It used to be the thing that decided whether the owner got alerts. Leaving
// it in a file where it no longer does anything means somebody eventually
// reads it, believes it, and concludes delivery is off when it is on — or
// spends an evening looking for the fault in Firebase when the answer is one
// line in .env. Failing at start-up costs one edit and says which one.
func (l *loader) signalMode(key string) constants.SignalMode {
	l.rejectRetired(retiredNotifyEnabled, fmt.Sprintf(
		"this has been replaced by %s (%s or %s); remove it",
		key, constants.SignalModeSilent, constants.SignalModeNotify))

	mode, err := constants.ParseSignalMode(l.optionalString(key, constants.DefaultSignalMode))
	if err != nil {
		l.invalidf(key, "%v", err)
		return ""
	}
	return mode
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

// directory reads an optional path and checks that it is one.
//
// Checked at start-up rather than on the first request, because the failure it
// prevents is a deployment that serves 404s from a typo and looks like an app
// that will not load.
func (l *loader) directory(key string) string {
	raw := strings.TrimSpace(l.optionalString(key, ""))
	if raw == "" {
		return ""
	}

	info, err := os.Stat(raw)
	if err != nil {
		l.invalidf(key, "%q cannot be read: %s", raw, err)
		return ""
	}
	if !info.IsDir() {
		l.invalidf(key, "%q is not a directory", raw)
		return ""
	}
	return strings.TrimRight(raw, "/")
}

// originHosts reads a comma-separated list of browser origins and returns the
// host of each.
//
// The websocket library matches on host rather than on the whole origin, so a
// value is accepted in either form and reduced to one. Taking full origins is
// what a person writes, and it keeps the scheme visible in the environment
// file even though the match ignores it.
func (l *loader) originHosts(key string) []string {
	raw := l.optionalString(key, "")
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	out := make([]string, 0, 2)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// A bare host is what the matcher wants; a full origin is what people
		// write. Accept both and keep the host.
		host := part
		if strings.Contains(part, "//") {
			parsed, err := url.Parse(part)
			if err != nil || parsed.Host == "" {
				l.invalidf(key, "%q is not an origin like https://host:port", part)
				continue
			}
			if parsed.Path != "" && parsed.Path != "/" {
				l.invalidf(key, "%q has a path; an origin is scheme, host and port only", part)
				continue
			}
			host = parsed.Host
		} else if strings.ContainsAny(part, "/?#") {
			l.invalidf(key, "%q is not a host or an origin", part)
			continue
		}

		// "*" would accept every origin, which is the check switched off
		// while looking like it is on.
		if host == "*" {
			l.invalidf(key, "%q accepts every origin, which is the same as no check; "+
				"list the hosts instead", part)
			continue
		}
		out = append(out, host)
	}
	return out
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

// makerFeePct parses the resting-order fee.
//
// It is read through the same bounds as the taker fee but kept separate,
// because the two are not interchangeable: charging maker rates on a fill that
// crossed the spread would report a discount that was never received.
func (l *loader) makerFeePct(key string) decimal.Decimal {
	v := l.optionalString(key, constants.DefaultFeeMakerPct)
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

// optionalTimeframe parses a timeframe, falling back to a default.
func (l *loader) optionalTimeframe(key, def string) constants.Timeframe {
	v := l.optionalString(key, def)
	parsed, err := constants.ParseTimeframe(v)
	if err != nil {
		l.invalidf(key, "%v", err)
		return ""
	}
	return parsed
}

// params reads a comma-separated key=value list.
//
// The same names --param takes, so a live run and the backtest that predicted
// it are configured through one mechanism. A malformed entry is an error
// rather than a skipped pair: a typo that silently ran the default is exactly
// what phase 06 made impossible on the command line, and it must not become
// possible again through the environment.
func (l *loader) params(key string) map[string]string {
	raw := l.optionalString(key, "")
	if raw == "" {
		return nil
	}

	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		name, value, found := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			l.invalidf(key, "%q is not name=value", pair)
			return nil
		}
		if _, dup := out[name]; dup {
			l.invalidf(key, "%s is given twice", name)
			return nil
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

// costModel parses percentage or spread.
func (l *loader) costModel(key string) constants.CostModel {
	v := l.optionalString(key, constants.DefaultCostModel)
	parsed, err := constants.ParseCostModel(v)
	if err != nil {
		l.invalidf(key, "%v", err)
		return ""
	}
	return parsed
}

// positiveDecimal reads a value that would be meaningless at zero.
//
// A zero contract size or lot step would make the sizing arithmetic divide by
// nothing; a zero point value would make the spread free, which is the
// flattering-by-default failure CLAUDE.md §3.4 rules out.
func (l *loader) positiveDecimal(key, def string) decimal.Decimal {
	v := l.optionalString(key, def)
	d, err := decimal.NewFromString(v)
	if err != nil {
		l.invalidf(key, "%q is not a decimal number", v)
		return decimal.Zero
	}
	if !d.IsPositive() {
		l.invalidf(key, "%s is not positive", d)
		return decimal.Zero
	}
	return d
}

// nonNegativeDecimal reads a value that may legitimately be zero.
func (l *loader) nonNegativeDecimal(key, def string) decimal.Decimal {
	v := l.optionalString(key, def)
	d, err := decimal.NewFromString(v)
	if err != nil {
		l.invalidf(key, "%q is not a decimal number", v)
		return decimal.Zero
	}
	if d.IsNegative() {
		l.invalidf(key, "%s is negative", d)
		return decimal.Zero
	}
	return d
}

// orderType parses market or limit.
func (l *loader) orderType(key, def string) constants.OrderType {
	v := l.optionalString(key, def)
	parsed, err := constants.ParseOrderType(v)
	if err != nil {
		l.invalidf(key, "%v", err)
		return ""
	}
	return parsed
}

// tickSize parses the instrument's price increment.
//
// Zero is rejected rather than treated as "no slippage": a zero tick makes
// SLIPPAGE_TICKS silently free, which is precisely the flattering-by-default
// behaviour CLAUDE.md §3.4 exists to prevent. Slippage is disabled by setting
// SLIPPAGE_TICKS to 0, visibly, not by nulling the unit it is measured in.
func (l *loader) tickSize(key string) decimal.Decimal {
	v := l.optionalString(key, constants.DefaultMarketTickSize)

	d, err := decimal.NewFromString(v)
	if err != nil {
		l.invalidf(key, "%q is not a decimal number", v)
		return decimal.Zero
	}
	if !d.IsPositive() {
		l.invalidf(key, "%s is not a positive price increment", d)
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

// BacktestCosts is the venue as configured, in the engine's own type.
//
// # Why every process builds its costs here and nowhere else
//
// There used to be four constructors: the backtest CLI set all eleven fields
// and the collector, the API and the reconcile CLI each set three. An empty
// Model reads as percentage, so a spread-configured venue was priced as
// percentage-with-taker everywhere except the CLI — and the reconciliation
// then reported a cost-model difference as a verdict on the strategy.
//
// One constructor makes that particular mistake impossible to make again, and
// a field added to Costs is now missed in one place rather than three. The
// test in server/architecture_test.go walks the returned struct and fails on
// any field left at its zero value, so adding a field without wiring it here
// fails immediately.
func (c *Config) BacktestCosts() backtest.Costs {
	return backtest.Costs{
		FeeTakerPct:   c.Market.FeeTakerPct,
		FeeMakerPct:   c.Market.FeeMakerPct,
		SlippageTicks: c.Market.SlippageTicks,
		TickSize:      c.Market.TickSize,

		Model:            c.Market.CostModel,
		SpreadPoints:     c.Market.SpreadPoints,
		PointValue:       c.Market.PointValue,
		ContractSize:     c.Market.ContractSize,
		MinLot:           c.Market.MinLot,
		LotStep:          c.Market.LotStep,
		CommissionPerLot: c.Market.CommissionPerLot,
	}
}
