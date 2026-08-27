/**
 * The wire shapes, from docs/api.md.
 *
 * # Whose shape this is
 *
 * The API's. If the app and the server disagree on a field name, that is the
 * app's bug — phase 08 moved every response through one renderer per object
 * precisely so there would be a single answer to "what is this called", and
 * an app that quietly accepts both spellings puts the ambiguity back.
 *
 * Prices are strings here and everywhere. numeric(20,8) does not fit a
 * float64, and a phone parsing 0.1 + 0.2 is the same hazard as a server doing
 * it. They are formatted for display and never arithmetic'd.
 *
 * Absent is null, never zero or an empty string: a price that is not yet known
 * and a price of nothing are different facts.
 */

export type Direction = 'long' | 'short';

export type OutcomeStatus = 'open' | 'target' | 'stop' | 'expired' | 'invalidated';

export type Timeframe = '1m' | '5m' | '15m' | '1h' | '4h' | '1d';

export type SignalMode = 'silent' | 'notify';

/** The stable half of an error. `message` is for a person and may change. */
export type ApiErrorCode =
  | 'invalid_parameter'
  | 'limit_exceeded'
  | 'not_found'
  | 'unavailable'
  | 'internal';

export type ApiErrorBody = {
  error: { code: ApiErrorCode; message: string };
};

// ---------------------------------------------------------------------------
// Candles
// ---------------------------------------------------------------------------

export type Candle = {
  open_time: string;
  close_time: string;
  open: string;
  high: string;
  low: string;
  close: string;
  volume: string;

  /**
   * Always true on REST — only closed candles are stored. May be false on the
   * websocket, which is the one place in this system permitted to send a bar
   * that has not closed.
   *
   * A client that ignores this is charting a price that can still change.
   * That is legitimate for display and for nothing else.
   */
  is_closed: boolean;
};

export type CandlesResponse = {
  symbol: string;
  market_type: string;
  timeframe: Timeframe;
  from: string;
  to: string;
  count: number;
  limit: number;
  /** The window held more than `limit`; these are the newest. */
  truncated: boolean;
  candles: Candle[];
};

// ---------------------------------------------------------------------------
// Indicators
// ---------------------------------------------------------------------------

/** Numbers, not strings: an indicator is a derived statistic and is never
 * added to a balance. CLAUDE.md §4 reserves decimal for money. */
export type IndicatorPoint = {
  open_time: string;
  ema: number;
  rsi: number;
  atr: number;
  vwap: number;
};

export type IndicatorsResponse = {
  symbol: string;
  market_type: string;
  timeframe: Timeframe;
  from: string;
  to: string;
  periods: { ema: number; rsi: number; atr: number };
  /** How far before `from` the computation had to start, and how many bars
   * were read in total. A short `count` on a wide window means the series
   * does not reach back far enough to converge. */
  warmup_bars: number;
  bars_read: number;
  count: number;
  values: IndicatorPoint[];
};

// ---------------------------------------------------------------------------
// Signals
// ---------------------------------------------------------------------------

export type Signal = {
  id: string;
  symbol: string;
  market_type: string;
  timeframe: Timeframe;
  signal_time: string;
  direction: Direction;

  /**
   * The close the strategy decided on. NOT the entry: a decision taken on a
   * bar's close cannot also fill on it.
   */
  signal_price: string | null;
  /** What a position would have opened at — the next bar's open plus
   * slippage. Null until that bar closes. */
  entry_price: string | null;
  stop_loss: string | null;
  take_profit: string | null;

  strategy_name: string;
  strategy_version: string;
  created_at: string;

  /** Present on the by-id endpoint only. Large, and the whole point of the
   * detail view: a signal without its reasoning is unreviewable six weeks
   * later. */
  reason?: SignalReason;
};

/** The stored reason. Its shape is the strategy's, so the parts this app
 * relies on are optional and everything else is passed through for display. */
export type SignalReason = {
  trigger?: string;
  strategy?: {
    name?: string;
    version?: string;
    params?: { name: string; value: string }[];
  };
  indicators?: Record<string, number | string>;
  trend?: Record<string, unknown>;
  [key: string]: unknown;
};

export type SignalsResponse = {
  symbol: string;
  market_type: string;
  count: number;
  /** The size of the collection this page came from, so a short page is
   * distinguishable from the last page without a second request. */
  total: number;
  limit: number;
  offset: number;
  signals: Signal[];
};

// ---------------------------------------------------------------------------
// Outcomes
// ---------------------------------------------------------------------------

export type Outcome = {
  signal_id: string;
  signal_time: string;
  direction: Direction;
  timeframe: Timeframe;
  strategy_name: string;
  strategy_version: string;

  status: OutcomeStatus;
  bars_held: number;

  /**
   * False for an invalidated outcome: its window had missing data, so whether
   * it would have won is not knowable and it is excluded from every statistic.
   * Carried explicitly rather than inferred from the status string.
   */
  measurable: boolean;

  resolved_at: string | null;
  signal_price: string | null;
  entry_price: string | null;
  resolved_price: string | null;

  /** Distances in price from the entry. An MAE routinely close to the stop on
   * trades that eventually win means the stop is barely surviving, which is
   * invisible in a win rate. */
  mae: string | null;
  mfe: string | null;

  /** Already net of modelled costs. Null for an open or invalidated outcome —
   * no return was computed, as against a return of nothing. */
  net_return_pct: string | null;

  divergence_note?: string;
};

export type OutcomesResponse = {
  symbol: string;
  market_type: string;
  from: string;
  to: string;
  count: number;
  total: number;
  limit: number;
  offset: number;
  outcomes: Outcome[];
};

// ---------------------------------------------------------------------------
// Performance
// ---------------------------------------------------------------------------

/** The report's statement about its own reliability. Rendered before the
 * numbers, never beside them. */
export type Sample = {
  resolved: number;
  required: number;
  /** The only thing that should decide whether the figures are acted on. */
  sufficient: boolean;
  banner?: string;
  resolved_per_day: number | null;
  expected_wait?: string;
};

export type PerformanceGroup = {
  strategy: string;
  version: string;
  params: { name: string; value: string }[];

  sample: Sample;

  signals: number;
  resolved: number;
  still_open: number;
  invalidated_excluded: number;
  targets: number;
  stops: number;
  expired: number;
  wins: number;
  losses: number;

  /** Null when nothing has resolved. A zero would read as a strategy that
   * never wins, which is a different statement. */
  win_rate: number | null;

  average_win_pct: string;
  average_loss_pct: string;
  average_cost_pct: string;

  /** win rate x average win + loss rate x average loss, after costs. The
   * number that decides whether a strategy is worth running, and not
   * derivable from a win rate alone. */
  expectancy_pct: string | null;

  /** Resolutions that came from an assumption rather than from the data. */
  rested_on_assumption: number;
};

export type PerformanceResponse = {
  symbol: string;
  market_type: string;
  from: string;
  to: string;
  generated_at: string;
  /** One per strategy, version and parameter set. There is deliberately no
   * total across them: averaging across a parameter change produces a number
   * describing nothing. */
  groups: PerformanceGroup[];
  note: string;
};

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

export type Concern = { component: string; detail: string };

export type Status = {
  symbol: string;
  market_type: string;
  observed_at: string;

  collector: {
    reachable: boolean;
    state: string;
    ws_connected: boolean;
    started_at: string | null;
    updated_at: string | null;
    heartbeat_age_seconds: number | null;
    reconnect_count: number;
    last_disconnect_note: string;
  };

  evaluator: {
    /** False means no strategy is running. A configuration, not a fault —
     * and the distinction is the point: switched off and stuck warming up
     * both produce no signals. */
    configured: boolean;
    strategy: string;
    timeframe: string;
    ready: boolean;
    /** Why it is not deciding, when it is not. */
    reason: string;
    last_signal_at: string | null;
    last_signal_age_seconds: number | null;
    signals_total: number;
  };

  ingestion: {
    unfilled_gaps: number;
    timeframes: { timeframe: string; unfilled_gaps: number }[];
  };

  outcomes: {
    open: number;
    oldest_open_at: string | null;
    oldest_open_age_seconds: number | null;
    missing_outcome_rows: number;
  };

  delivery: {
    mode: SignalMode;
    pending: number;
    sent: number;
    failed: number;
    last_sent_at: string | null;
    /** Zero or one. Zero while the mode is notify means signals are being
     * recorded and queued and nothing is delivered. */
    devices_registered: number;
  };

  /** Empty when there is nothing wrong — an empty list, not a missing field,
   * because a missing field reads as a check that did not run. */
  concerns: Concern[];
  note: string;
};

// ---------------------------------------------------------------------------
// Device
// ---------------------------------------------------------------------------

export type DeviceResponse = {
  registered: boolean;
  /** Masked to a prefix. The API never returns the token in full. */
  token?: string;
  platform?: string;
  label?: string;
  registered_at?: string;
  refreshed_at?: string;
  /** Whether this deployment sends anything at all. Registering against a
   * silent deployment succeeds and delivers nothing. */
  delivery_mode: SignalMode;
  note: string;
};
