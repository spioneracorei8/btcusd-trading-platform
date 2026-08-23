// Package constants holds the enumerations, fixed values and sentinel errors
// shared across every layer. It imports nothing from this project, so any
// layer may depend on it without creating a cycle.
package constants

import (
	"fmt"
	"time"
)

// MarketType selects which Binance market a symbol is traded on.
//
// Futures logic (funding rate, leverage) is intentionally not implemented yet,
// but the type exists from day one so no endpoint or symbol format ends up
// hardcoded in calculation code.
type MarketType string

// Supported market types.
const (
	MarketTypeSpot    MarketType = "spot"
	MarketTypeFutures MarketType = "futures"
)

// Valid reports whether m is a known market type.
func (m MarketType) Valid() bool {
	switch m {
	case MarketTypeSpot, MarketTypeFutures:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the market type.
func (m MarketType) String() string { return string(m) }

// ParseMarketType converts s into a MarketType, rejecting unknown values.
func ParseMarketType(s string) (MarketType, error) {
	m := MarketType(s)
	if !m.Valid() {
		return "", fmt.Errorf("unknown market type %q (want %q or %q)", s, MarketTypeSpot, MarketTypeFutures)
	}
	return m, nil
}

// Timeframe is a candle interval expressed with Binance's notation.
type Timeframe string

// Timeframes the platform is allowed to work with. 1m and 5m drive the
// scalping strategies; 15m and 1h are used as trend filters.
const (
	Timeframe1m  Timeframe = "1m"
	Timeframe3m  Timeframe = "3m"
	Timeframe5m  Timeframe = "5m"
	Timeframe15m Timeframe = "15m"
	Timeframe30m Timeframe = "30m"
	Timeframe1h  Timeframe = "1h"
	Timeframe2h  Timeframe = "2h"
	Timeframe4h  Timeframe = "4h"
	Timeframe6h  Timeframe = "6h"
	Timeframe12h Timeframe = "12h"
	Timeframe1d  Timeframe = "1d"
)

// timeframeDurations is the single source of truth for valid timeframes.
var timeframeDurations = map[Timeframe]time.Duration{
	Timeframe1m:  time.Minute,
	Timeframe3m:  3 * time.Minute,
	Timeframe5m:  5 * time.Minute,
	Timeframe15m: 15 * time.Minute,
	Timeframe30m: 30 * time.Minute,
	Timeframe1h:  time.Hour,
	Timeframe2h:  2 * time.Hour,
	Timeframe4h:  4 * time.Hour,
	Timeframe6h:  6 * time.Hour,
	Timeframe12h: 12 * time.Hour,
	Timeframe1d:  24 * time.Hour,
}

// Valid reports whether t is a supported timeframe.
func (t Timeframe) Valid() bool {
	_, ok := timeframeDurations[t]
	return ok
}

// Duration returns the wall-clock length of one candle of this timeframe.
// It returns 0 for an unknown timeframe.
func (t Timeframe) Duration() time.Duration { return timeframeDurations[t] }

// String returns the wire/database representation of the timeframe.
func (t Timeframe) String() string { return string(t) }

// ParseTimeframe converts s into a Timeframe, rejecting unknown values.
func ParseTimeframe(s string) (Timeframe, error) {
	tf := Timeframe(s)
	if !tf.Valid() {
		return "", fmt.Errorf("unsupported timeframe %q", s)
	}
	return tf, nil
}

// Bias is which way the trend filter says the market is leaning.
//
// It is deliberately not Direction. "The trend is up" and "I am long" are
// different claims, and a single type for both invites the trend filter's
// output to be mistaken for a position — which is exactly the confusion a
// filter that vetoes rather than signals exists to avoid.
type Bias string

// Supported trend biases.
const (
	BiasBullish Bias = "bullish"
	BiasBearish Bias = "bearish"

	// BiasNeutral permits nothing. It is the value reported inside the dead
	// zone and whenever the filter is not ready, and phase 06 must read it as
	// "no entries", never as "no opinion, proceed freely".
	BiasNeutral Bias = "neutral"
)

// Valid reports whether b is a known bias.
func (b Bias) Valid() bool {
	switch b {
	case BiasBullish, BiasBearish, BiasNeutral:
		return true
	default:
		return false
	}
}

// String returns the wire representation of the bias.
func (b Bias) String() string { return string(b) }

// ParseBias converts s into a Bias, rejecting unknown values.
func ParseBias(s string) (Bias, error) {
	b := Bias(s)
	if !b.Valid() {
		return "", fmt.Errorf("unknown trend bias %q", s)
	}
	return b, nil
}

// Direction is the side a signal points to. The system never places orders;
// a direction is advice for the owner, nothing more.
type Direction string

// Supported signal directions.
const (
	DirectionLong  Direction = "long"
	DirectionShort Direction = "short"
	DirectionFlat  Direction = "flat"
)

// Valid reports whether d is a known direction.
func (d Direction) Valid() bool {
	switch d {
	case DirectionLong, DirectionShort, DirectionFlat:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the direction.
func (d Direction) String() string { return string(d) }

// ParseDirection converts s into a Direction, rejecting unknown values.
func ParseDirection(s string) (Direction, error) {
	d := Direction(s)
	if !d.Valid() {
		return "", fmt.Errorf("unknown direction %q", s)
	}
	return d, nil
}

// APIErrorCode is a stable identifier the mobile app may branch on.
//
// Stable is the whole point: the message is for a person and may be reworded,
// the code is for the app and may not. Phase 09 is written against these.
type APIErrorCode string

// The API error codes.
const (
	// APIErrInvalidParameter is a malformed or out-of-range query parameter.
	APIErrInvalidParameter APIErrorCode = "invalid_parameter"

	// APIErrLimitExceeded is a request for more rows than the endpoint will
	// serve. Distinct from invalid_parameter because the app's correct
	// response is different: page, rather than fix the request.
	APIErrLimitExceeded APIErrorCode = "limit_exceeded"

	// APIErrNotFound is a resource that does not exist.
	APIErrNotFound APIErrorCode = "not_found"

	// APIErrUnavailable is a dependency the server needs and cannot reach.
	APIErrUnavailable APIErrorCode = "unavailable"

	// APIErrInternal is anything else. The message is deliberately vague; the
	// detail is in the server log against the request id.
	APIErrInternal APIErrorCode = "internal"
)

// String returns the wire representation of the code.
func (c APIErrorCode) String() string { return string(c) }

// OutcomeStatus is what became of a signal.
type OutcomeStatus string

// Supported outcome statuses.
const (
	// OutcomeOpen is a signal still being followed.
	OutcomeOpen OutcomeStatus = "open"

	// OutcomeTarget and OutcomeStop are level hits. A bar reaching both is
	// recorded as a stop, matching the backtest's pessimistic rule — it must
	// be the same assumption in both places or the comparison between them
	// compares assumptions rather than outcomes.
	OutcomeTarget OutcomeStatus = "target"
	OutcomeStop   OutcomeStatus = "stop"

	// OutcomeExpired is a signal that reached neither level within its
	// window. It is a real outcome and counts: a strategy whose signals
	// mostly expire is a strategy that mostly does nothing.
	OutcomeExpired OutcomeStatus = "expired"

	// OutcomeInvalidated is a signal whose window has missing data. It is
	// excluded from statistics rather than counted as anything: what happened
	// is not knowable, and guessing would put a number where there is none.
	OutcomeInvalidated OutcomeStatus = "invalidated"
)

// Valid reports whether s is a known outcome status.
func (s OutcomeStatus) Valid() bool {
	switch s {
	case OutcomeOpen, OutcomeTarget, OutcomeStop, OutcomeExpired, OutcomeInvalidated:
		return true
	default:
		return false
	}
}

// Resolved reports whether the signal is finished with.
func (s OutcomeStatus) Resolved() bool { return s.Valid() && s != OutcomeOpen }

// Measurable reports whether this outcome may be counted in a statistic.
//
// An invalidated signal is excluded. Its window has a hole in it, so whether
// it would have won is not knowable — and a win rate that quietly counted
// guesses would be worse than one with a smaller sample.
func (s OutcomeStatus) Measurable() bool {
	switch s {
	case OutcomeTarget, OutcomeStop, OutcomeExpired:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the status.
func (s OutcomeStatus) String() string { return string(s) }

// ParseOutcomeStatus converts s into an OutcomeStatus, rejecting unknown
// values.
func ParseOutcomeStatus(s string) (OutcomeStatus, error) {
	st := OutcomeStatus(s)
	if !st.Valid() {
		return "", fmt.Errorf("unknown outcome status %q", s)
	}
	return st, nil
}

// SignalMode decides whether a recorded signal is also delivered.
//
// There are exactly two, and there will not be a third. The system can send or
// not send; a name like "uat" or "test" would imply a behaviour that does not
// exist. Whether the owner acts on an alert with demo money or real money is
// not something this system knows or should model.
type SignalMode string

// Supported signal modes.
const (
	// SignalModeSilent evaluates and records, and delivers nothing.
	SignalModeSilent SignalMode = "silent"

	// SignalModeNotify also queues each recorded signal for delivery.
	SignalModeNotify SignalMode = "notify"
)

// Valid reports whether m is a known signal mode.
func (m SignalMode) Valid() bool {
	switch m {
	case SignalModeSilent, SignalModeNotify:
		return true
	default:
		return false
	}
}

// Delivers reports whether this mode sends anything to the owner.
func (m SignalMode) Delivers() bool { return m == SignalModeNotify }

// String returns the wire/database representation of the mode.
func (m SignalMode) String() string { return string(m) }

// ParseSignalMode converts s into a SignalMode, rejecting unknown values.
func ParseSignalMode(s string) (SignalMode, error) {
	m := SignalMode(s)
	if !m.Valid() {
		return "", fmt.Errorf("unknown signal mode %q, want %s or %s",
			s, SignalModeSilent, SignalModeNotify)
	}
	return m, nil
}

// NotificationChannel is a delivery target for a signal.
type NotificationChannel string

// Supported notification channels.
const (
	NotificationChannelFCM NotificationChannel = "fcm"
)

// String returns the wire/database representation of the channel.
func (c NotificationChannel) String() string { return string(c) }

// NotificationStatus is the delivery state of a notification attempt.
type NotificationStatus string

// Supported notification statuses.
const (
	NotificationStatusPending NotificationStatus = "pending"
	NotificationStatusSent    NotificationStatus = "sent"
	NotificationStatusFailed  NotificationStatus = "failed"
)

// Valid reports whether s is a known notification status.
func (s NotificationStatus) Valid() bool {
	switch s {
	case NotificationStatusPending, NotificationStatusSent, NotificationStatusFailed:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the status.
func (s NotificationStatus) String() string { return string(s) }

// ParseNotificationStatus converts s into a NotificationStatus, rejecting
// unknown values.
func ParseNotificationStatus(s string) (NotificationStatus, error) {
	st := NotificationStatus(s)
	if !st.Valid() {
		return "", fmt.Errorf("unknown notification status %q", s)
	}
	return st, nil
}

// AppEnv is the deployment environment the process runs in.
type AppEnv string

// Supported application environments.
const (
	EnvDev  AppEnv = "dev"
	EnvProd AppEnv = "prod"
)

// Valid reports whether e is a known environment.
func (e AppEnv) Valid() bool {
	switch e {
	case EnvDev, EnvProd:
		return true
	default:
		return false
	}
}

// String returns the environment name.
func (e AppEnv) String() string { return string(e) }

// CollectorState is which phase of its lifecycle the collector is in.
//
// It exists because "the newest candle is three years old" means completely
// different things depending on the phase: during a backfill it is normal
// progress, while live it means ingestion has silently stopped. Without the
// state, the status endpoint cannot tell those apart and reports the same
// numbers for both.
type CollectorState string

// Collector lifecycle states.
const (
	// CollectorNeverStarted is reported when no collector has ever registered.
	// It is a valid state, not an error: a dead collector is the single most
	// important thing the status endpoint has to be able to say.
	CollectorNeverStarted CollectorState = "never_started"

	// CollectorStarting is the process up but not yet backfilling.
	CollectorStarting CollectorState = "starting"

	// CollectorBackfilling is historical backfill in progress.
	CollectorBackfilling CollectorState = "backfilling"

	// CollectorLive is backfill complete and the stream being consumed. This
	// is the only state in which staleness is a meaningful question.
	CollectorLive CollectorState = "live"

	// CollectorReconnecting is the stream dropped, with backoff or reconnect
	// backfill in progress.
	CollectorReconnecting CollectorState = "reconnecting"
)

// Valid reports whether s is a known collector state.
func (s CollectorState) Valid() bool {
	switch s {
	case CollectorNeverStarted, CollectorStarting, CollectorBackfilling,
		CollectorLive, CollectorReconnecting:
		return true
	default:
		return false
	}
}

// String returns the wire/database representation of the state.
func (s CollectorState) String() string { return string(s) }

// ParseCollectorState converts s into a CollectorState, rejecting unknown values.
func ParseCollectorState(s string) (CollectorState, error) {
	state := CollectorState(s)
	if !state.Valid() {
		return "", fmt.Errorf("unknown collector state %q", s)
	}
	return state, nil
}

// OrderType is how an order reaches the book.
//
// # Why this exists as a type rather than a boolean
//
// The two differ in more than their fee. A market order always fills and pays
// taker plus slippage; a limit order pays maker and no slippage but only fills
// if price comes to it, and otherwise does not trade at all. Modelling the
// cheaper fee without the missed fills would produce a report that is
// straightforwardly false — cheaper trades that always happen — so the two
// halves travel together under one name.
type OrderType string

// Supported order types.
const (
	// OrderTypeMarket crosses the spread: it always fills, pays the taker fee
	// and suffers slippage.
	OrderTypeMarket OrderType = "market"

	// OrderTypeLimit rests on the book: it pays the maker fee and no
	// slippage, and fills only if price reaches it before the order times out.
	OrderTypeLimit OrderType = "limit"
)

// Valid reports whether o is a known order type.
func (o OrderType) Valid() bool {
	switch o {
	case OrderTypeMarket, OrderTypeLimit:
		return true
	default:
		return false
	}
}

// String returns the configuration representation of the order type.
func (o OrderType) String() string { return string(o) }

// ParseOrderType converts s into an OrderType, rejecting unknown values.
func ParseOrderType(s string) (OrderType, error) {
	o := OrderType(s)
	if !o.Valid() {
		return "", fmt.Errorf("unknown order type %q (want %q or %q)",
			s, OrderTypeMarket, OrderTypeLimit)
	}
	return o, nil
}

// CostModel selects how the cost of trading is computed.
//
// # Why this is a choice rather than one formula
//
// A percentage of notional and a fixed spread in price points are not two
// parameterisations of the same thing. On an exchange charging 0.05% the cost
// of a round trip scales with price; on a CFD venue quoting a 25 USD spread it
// does not. At 1m, where price moves and the spread are the same order of
// magnitude, using the wrong one misprices every trade — and in opposite
// directions depending on where price happens to be.
type CostModel string

// The cost models.
const (
	// CostModelPercentage charges a fee as a share of notional, per side. It
	// is what every evaluation before this used, and stays the default so
	// those results remain comparable.
	CostModelPercentage CostModel = "percentage"

	// CostModelSpread charges the bid/ask spread in price points, plus an
	// optional commission per lot. The cost of a round trip is then
	// independent of the price level.
	CostModelSpread CostModel = "spread"
)

// Valid reports whether m is a known cost model.
func (m CostModel) Valid() bool {
	switch m {
	case CostModelPercentage, CostModelSpread:
		return true
	default:
		return false
	}
}

// String returns the configuration representation of the cost model.
func (m CostModel) String() string { return string(m) }

// ParseCostModel converts s into a CostModel, rejecting unknown values.
func ParseCostModel(s string) (CostModel, error) {
	m := CostModel(s)
	if !m.Valid() {
		return "", fmt.Errorf("unknown cost model %q (want %q or %q)",
			s, CostModelPercentage, CostModelSpread)
	}
	return m, nil
}
