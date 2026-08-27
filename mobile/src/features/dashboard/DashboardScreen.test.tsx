import { render, screen, waitFor } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { colors } from '../../theme';
import { DashboardScreen } from './DashboardScreen';
import type { Outcome, Signal, Status } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

function status(overrides: Partial<Status> = {}): Status {
  return {
    symbol: 'BTCUSDT',
    market_type: 'spot',
    observed_at: '2026-08-27T12:00:00Z',
    collector: {
      reachable: true,
      state: 'live',
      ws_connected: true,
      started_at: '2026-08-27T09:00:00Z',
      updated_at: '2026-08-27T11:59:58Z',
      heartbeat_age_seconds: 2,
      reconnect_count: 0,
      last_disconnect_note: '',
    },
    evaluator: {
      configured: true,
      strategy: 'ema_crossover',
      timeframe: '4h',
      ready: true,
      reason: '',
      last_signal_at: '2026-08-17T12:00:00Z',
      last_signal_age_seconds: 864000,
      signals_total: 12,
    },
    ingestion: { unfilled_gaps: 0, timeframes: [] },
    outcomes: {
      open: 0,
      oldest_open_at: null,
      oldest_open_age_seconds: null,
      missing_outcome_rows: 0,
    },
    delivery: {
      mode: 'silent',
      pending: 0,
      sent: 0,
      failed: 0,
      last_sent_at: null,
      devices_registered: 0,
    },
    concerns: [],
    note: 'Silence is the normal output of this pipeline.',
    ...overrides,
  };
}

function aSignal(overrides: Partial<Signal> = {}): Signal {
  return {
    id: 'e6d0e070-39a3-4fa2-907c-1dc470c83d3d',
    symbol: 'BTCUSDT',
    market_type: 'spot',
    timeframe: '4h',
    signal_time: '2026-08-27T08:00:00Z',
    direction: 'long',
    signal_price: '64000',
    entry_price: '64010.01',
    stop_loss: '63500',
    take_profit: '65000',
    strategy_name: 'ema_crossover',
    strategy_version: 'v1',
    created_at: '2026-08-27T08:00:01Z',
    ...overrides,
  };
}

function anOutcome(overrides: Partial<Outcome> = {}): Outcome {
  return {
    signal_id: 'e6d0e070-39a3-4fa2-907c-1dc470c83d3d',
    signal_time: '2026-08-27T08:00:00Z',
    direction: 'long',
    timeframe: '4h',
    strategy_name: 'ema_crossover',
    strategy_version: 'v1',
    status: 'open',
    bars_held: 2,
    measurable: true,
    resolved_at: null,
    signal_price: '64000',
    entry_price: '64010.01',
    resolved_price: null,
    mae: '120.5',
    mfe: '340.25',
    net_return_pct: null,
    ...overrides,
  };
}

/** Routes each endpoint to a fixture. */
function renderWith(fixtures: {
  status?: Status;
  signals?: Signal[];
  outcomes?: Outcome[];
  fail?: boolean;
}) {
  const fetchImpl = (async (input: RequestInfo | URL) => {
    if (fixtures.fail) throw new TypeError('Network request failed');

    const url = String(input);
    const body = url.includes('/status')
      ? fixtures.status ?? status()
      : url.includes('/signals')
        ? { signals: fixtures.signals ?? [], count: 0, total: 0, limit: 1, offset: 0 }
        : { outcomes: fixtures.outcomes ?? [], count: 0, total: 0, limit: 1, offset: 0 };

    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  return render(
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={new ApiClient({ baseUrl: BASE, fetchImpl })}>
        <DashboardScreen />
      </ApiProvider>
    </QueryClientProvider>,
  );
}

/**
 * TestSilenceIsLegibleOnTheDashboard.
 *
 * Normal output on 4h is roughly one signal every ten days, so this screen is
 * blank most of the time. Blank has four causes demanding different responses,
 * and a screen that renders the same emptiness for all four teaches its reader
 * that blank means nothing happened.
 */
describe('when there is no signal', () => {
  it('says a warm strategy has found no setup, and that this is ordinary', async () => {
    renderWith({});

    await waitFor(() => expect(screen.getByText('No setup found')).toBeTruthy());
    expect(screen.getByText(/ordinary state/i)).toBeTruthy();
  });

  it('says a strategy is warming up, with the server’s own reason', async () => {
    const cold = status();
    cold.evaluator = {
      ...cold.evaluator,
      ready: false,
      reason: 'the strategy has seen 40 4h bars and needs 200 before it may decide',
    };
    renderWith({ status: cold });

    await waitFor(() =>
      expect(screen.getByText('The strategy is not deciding yet')).toBeTruthy(),
    );
    expect(screen.getByText(/needs 200/)).toBeTruthy();
  });

  it('says no strategy is running when none is configured', async () => {
    const off = status();
    off.evaluator = { ...off.evaluator, configured: false, ready: false, strategy: '' };
    renderWith({ status: off });

    await waitFor(() => expect(screen.getByText('No strategy is running')).toBeTruthy());
    expect(screen.getByText(/STRATEGY_NAME/)).toBeTruthy();
  });

  it('says the collector has stopped rather than that the market is quiet', async () => {
    const dead = status({
      concerns: [
        { component: 'collector', detail: 'the last heartbeat was 4h12m ago' },
      ],
    });
    renderWith({ status: dead });

    await waitFor(() => expect(screen.getByText('The collector has stopped')).toBeTruthy());
    // And it must not also claim the market was quiet.
    expect(screen.queryByText('No setup found')).toBeNull();
  });

  it('colours a fault differently from ordinary quiet', async () => {
    // The wording carries the difference and the colour has to agree with it.
    // A stopped collector rendered in the same tone as "no setup found" reads
    // as normality at a glance, which is the glance this screen exists for.
    renderWith({});
    await waitFor(() => expect(screen.getByText('No setup found')).toBeTruthy());
    const ordinary = colourOf(screen.getByText('No setup found'));

    screen.unmount();

    renderWith({
      status: status({
        concerns: [{ component: 'collector', detail: 'the last heartbeat was 4h12m ago' }],
      }),
    });
    await waitFor(() => expect(screen.getByText('The collector has stopped')).toBeTruthy());
    const fault = colourOf(screen.getByText('The collector has stopped'));

    expect(fault).not.toBe(ordinary);
    expect(fault).toBe(colors.semantic.warn);
  });
});

/** The colour a rendered Text actually resolved to. */
function colourOf(node: { props: { style?: unknown } }): string | undefined {
  const flat = StyleSheet.flatten(node.props.style as never) as { color?: string } | undefined;
  return flat?.color;
}

describe('when there is a signal', () => {
  it('labels the decided price "reference" and never "entry"', async () => {
    // Phase 07 made that distinction deliberately: the decision was taken on a
    // bar's close and nothing could have filled there. Relabelling it here
    // would put a systematic difference into every comparison made by eye.
    renderWith({ signals: [aSignal()] });

    await waitFor(() => expect(screen.getByText('reference price')).toBeTruthy());
    expect(screen.getByText('entry')).toBeTruthy();
    expect(screen.getByText('64,000')).toBeTruthy();
    expect(screen.getByText('64,010.01')).toBeTruthy();
  });

  it('shows the direction, the levels and how long ago', async () => {
    renderWith({ signals: [aSignal()] });

    await waitFor(() => expect(screen.getByText('LONG')).toBeTruthy());
    expect(screen.getByText('63,500')).toBeTruthy();
    expect(screen.getByText('65,000')).toBeTruthy();
  });

  it('shows the excursions while it is still open', async () => {
    // An MAE routinely close to the stop on trades that eventually win means
    // the stop is barely surviving, which is invisible in a win rate.
    renderWith({ signals: [aSignal()], outcomes: [anOutcome()] });

    await waitFor(() => expect(screen.getByText('still open')).toBeTruthy());
    expect(screen.getByText('worst excursion (MAE)')).toBeTruthy();
    expect(screen.getByText('120.5')).toBeTruthy();
    expect(screen.getByText('340.25')).toBeTruthy();
  });

  it('says an invalidated outcome is not knowable rather than showing a number', async () => {
    renderWith({
      signals: [aSignal()],
      outcomes: [
        anOutcome({ status: 'invalidated', measurable: false, net_return_pct: null }),
      ],
    });

    await waitFor(() => expect(screen.getByText(/not\s+knowable/i)).toBeTruthy());
  });

  it('says out loud that nothing here places an order', async () => {
    // The gap between showing a signal and placing an order has to stay wide
    // enough that nobody crosses it by muscle memory, and on a phone this is
    // where it is narrowest.
    renderWith({ signals: [aSignal()] });

    await waitFor(() => expect(screen.getByText(/places an order/i)).toBeTruthy());
  });
});

describe('pipeline health', () => {
  it('is one quiet line when nothing is wrong', async () => {
    // A permanent dashboard of green ticks trains its reader to skip the area
    // where the red would appear.
    renderWith({});

    await waitFor(() => expect(screen.getByText(/pipeline healthy/i)).toBeTruthy());
  });

  it('is prominent when something is', async () => {
    renderWith({
      status: status({
        concerns: [{ component: 'delivery', detail: 'no device is registered' }],
      }),
    });

    await waitFor(() => expect(screen.getByText('1 concern')).toBeTruthy());
    expect(screen.getByText(/no device is registered/)).toBeTruthy();
  });
});

describe('when the server cannot be reached', () => {
  it('names Tailscale instead of spinning', async () => {
    renderWith({ fail: true });

    await waitFor(() => expect(screen.getByText(/cannot reach the server/i)).toBeTruthy());
    expect(screen.getByText(/open tailscale/i)).toBeTruthy();
  });
});
