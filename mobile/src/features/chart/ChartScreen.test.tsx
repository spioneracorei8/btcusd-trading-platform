import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { ChartScreen } from './ChartScreen';
import { MINUTES, VISIBLE_BARS } from './window';
import type { Candle, Status } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';
const BAR = MINUTES['4h'] * 60_000;

/** Where the 4h series ends, which is where the chart should open. */
const SERIES_END = Date.parse('2024-03-06T00:00:00Z');

/** Enough history either side that no assertion is measuring the edge. */
const HISTORY_BARS = 400;

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

function universe(): Candle[] {
  return Array.from({ length: HISTORY_BARS }, (_, i) => {
    const openTime = SERIES_END - (HISTORY_BARS - 1 - i) * BAR;
    const price = (30000 + i).toString();
    return {
      open_time: new Date(openTime).toISOString(),
      close_time: new Date(openTime + BAR - 1).toISOString(),
      open: price,
      high: price,
      low: price,
      close: price,
      volume: '1',
      is_closed: true,
    };
  });
}

function statusFixture(): Status {
  return {
    symbol: 'BTCUSDT',
    market_type: 'spot',
    observed_at: '2024-03-06T00:00:00Z',
    collector: {
      reachable: true,
      state: 'live',
      ws_connected: true,
      started_at: '2024-03-05T00:00:00Z',
      updated_at: '2024-03-06T00:00:00Z',
      heartbeat_age_seconds: 1,
      reconnect_count: 0,
      last_disconnect_note: '',
    },
    evaluator: {
      configured: true,
      strategy: 'ema_crossover',
      timeframe: '4h',
      ready: true,
      reason: '',
      last_signal_at: null,
      last_signal_age_seconds: null,
      signals_total: 0,
    },
    ingestion: {
      unfilled_gaps: 0,
      timeframes: [
        {
          timeframe: '4h',
          unfilled_gaps: 0,
          latest_open_time: new Date(SERIES_END).toISOString(),
          latest_age_seconds: 0,
        },
      ],
    },
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
    note: '',
  };
}

function json(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

/**
 * Renders the chart against a server that answers /candles honestly: only the
 * bars inside the window asked for, newest-first-truncated at the limit.
 *
 * A fake that returns the same page whatever the query would hide exactly the
 * bug this file exists to catch.
 */
function renderChart() {
  const candleWindows: { from: number; to: number }[] = [];
  const all = universe();

  const fetchImpl = (async (input: RequestInfo | URL) => {
    const url = new URL(String(input));

    if (url.pathname.endsWith('/status')) return json(statusFixture());
    if (url.pathname.endsWith('/signals')) {
      return json({ signals: [], count: 0, total: 0, limit: 50, offset: 0 });
    }
    if (url.pathname.endsWith('/outcomes')) {
      return json({ outcomes: [], count: 0, total: 0, limit: 200, offset: 0 });
    }

    const from = Date.parse(url.searchParams.get('from') ?? '');
    const to = Date.parse(url.searchParams.get('to') ?? '');
    const limit = Number(url.searchParams.get('limit') ?? '500');
    candleWindows.push({ from, to });

    const inWindow = all.filter((candle) => {
      const at = Date.parse(candle.open_time);
      return at >= from && at <= to;
    });
    const page = inWindow.slice(-limit);

    return json({
      symbol: 'BTCUSDT',
      market_type: 'spot',
      timeframe: '4h',
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
      count: page.length,
      limit,
      truncated: page.length < inWindow.length,
      candles: page,
    });
  }) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  const view = render(
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={new ApiClient({ baseUrl: BASE, fetchImpl })}>
        <ChartScreen />
      </ApiProvider>
    </QueryClientProvider>,
  );
  return { ...view, candleWindows };
}

/** The right edge of what is drawn, read off the range the screen prints. */
async function rightEdge(): Promise<number> {
  const label = await screen.findByText(/Z → .*Z/);
  const text = label.props.children as unknown[];
  const printed = text.flat().join('');
  const to = printed.split('→')[1]!.trim();
  return Date.parse(to.replace(' ', 'T'));
}

async function settled(candleWindows: unknown[]): Promise<number> {
  await waitFor(() => expect(candleWindows.length).toBeGreaterThan(0));
  await waitFor(() => expect(screen.getByText(`${VISIBLE_BARS} bars`)).toBeTruthy());
  return candleWindows.length;
}

/**
 * TestPanningInsideTheLoadedWindowDoesNotRefetch.
 *
 * # What this prevents
 *
 * window.ts computes an overscan and `covers` says whether a view still fits
 * inside it, but neither does anything unless the screen holds the fetched
 * window still while the view moves. If the visible edge feeds the query key
 * directly, every step is a round trip, the overscan buys nothing, and the
 * phase's "must not refetch per gesture" is satisfied only on paper.
 *
 * The arithmetic: the window reaches 120 bars behind the opening edge and a
 * step is half a screen, 30 bars. So two steps stay inside it and the third
 * does not.
 */
describe('panning the chart', () => {
  it('moves what is drawn without asking the server again', async () => {
    const { candleWindows } = renderChart();
    const before = await settled(candleWindows);
    const openedAt = await rightEdge();

    fireEvent.press(screen.getByText('← earlier'));

    await waitFor(async () => {
      expect(await rightEdge()).toBe(openedAt - (VISIBLE_BARS / 2) * BAR);
    });
    expect(candleWindows).toHaveLength(before);
  });

  it('asks again once the view leaves the window that was loaded', async () => {
    const { candleWindows } = renderChart();
    const before = await settled(candleWindows);

    fireEvent.press(screen.getByText('← earlier'));
    fireEvent.press(screen.getByText('← earlier'));
    await waitFor(() => expect(candleWindows).toHaveLength(before));

    fireEvent.press(screen.getByText('← earlier'));
    await waitFor(() => expect(candleWindows.length).toBeGreaterThan(before));

    // And the new window is behind the old one, not a repeat of it.
    const first = candleWindows[0]!;
    const latest = candleWindows[candleWindows.length - 1]!;
    expect(latest.from).toBeLessThan(first.from);
  });

  /**
   * TestAFullWindowIsNotReportedAsOverflowing.
   *
   * # What this prevents
   *
   * `from` and `to` are both inclusive, so a window spanning 180 bars holds
   * 181. A limit of 180 comes back one bar short with `truncated` set, and
   * the chart says the window held more bars than were returned — which is
   * alarming, permanent once there is history on both sides, and false.
   *
   * It does not show at the opening position, because nothing exists past
   * the end of the series. It appears on the first pan that has bars on both
   * sides of the view, which is why only a panned assertion catches it.
   */
  it('does not report a full window as having overflowed', async () => {
    const { candleWindows } = renderChart();
    const before = await settled(candleWindows);

    for (let i = 0; i < 3; i++) fireEvent.press(screen.getByText('← earlier'));
    await waitFor(() => expect(candleWindows.length).toBeGreaterThan(before));
    await waitFor(() => expect(screen.getByText(`${VISIBLE_BARS} bars`)).toBeTruthy());

    expect(screen.queryByText(/held more bars than were returned/)).toBeNull();
  });
});

/**
 * TestTheChartOpensOnTheEndOfTheSeries.
 *
 * # What this prevents
 *
 * The window runs a screen past the right edge on purpose, so the newest bars
 * in a reply are the overscan rather than the bars being looked at. Drawing
 * the tail of the reply would put the same bars on screen wherever the chart
 * had been panned to — panning would refetch, redraw, and appear not to move.
 */
describe('the drawn range', () => {
  it('ends at the end of the series, not at the end of the window fetched', async () => {
    const { candleWindows } = renderChart();
    await settled(candleWindows);

    expect(await rightEdge()).toBe(SERIES_END);
    // The window really did ask for bars past it; there just were not any.
    expect(candleWindows[candleWindows.length - 1]!.to).toBeGreaterThan(SERIES_END);
  });
});
