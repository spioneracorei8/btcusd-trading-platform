import { render, screen, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { SignalsScreen } from './SignalsScreen';
import type { Outcome, Signal } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

/** A signal from long enough ago that a one-year default window misses it. */
function oldSignal(id: string, direction: 'long' | 'short' = 'long'): Signal {
  return {
    id,
    symbol: 'BTCUSDT',
    market_type: 'spot',
    timeframe: '4h',
    signal_time: '2024-03-06T00:00:00Z',
    direction,
    signal_price: '30800',
    entry_price: '30200.01',
    stop_loss: '29900',
    take_profit: '30500',
    strategy_name: 'ema_crossover',
    strategy_version: 'v1',
    created_at: '2024-03-06T00:00:01Z',
  };
}

function resolved(id: string): Outcome {
  return {
    signal_id: id,
    signal_time: '2024-03-06T00:00:00Z',
    direction: 'long',
    timeframe: '4h',
    strategy_name: 'ema_crossover',
    strategy_version: 'v1',
    status: 'stop',
    bars_held: 2,
    measurable: true,
    resolved_at: '2024-03-06T08:00:00Z',
    signal_price: '30800',
    entry_price: '30200.01',
    resolved_price: '29899.99',
    mae: '625.01',
    mfe: '24.99',
    net_return_pct: '-1.0934',
  };
}

function renderWith(signals: Signal[], outcomes: Outcome[]) {
  const urls: string[] = [];

  const fetchImpl = (async (input: RequestInfo | URL) => {
    const url = String(input);
    urls.push(url);

    const body = url.includes('/outcomes')
      ? { outcomes, count: outcomes.length, total: outcomes.length, limit: 200, offset: 0 }
      : { signals, count: signals.length, total: signals.length, limit: 50, offset: 0 };

    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  const view = render(
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={new ApiClient({ baseUrl: BASE, fetchImpl })}>
        <SignalsScreen onOpen={() => {}} />
      </ApiProvider>
    </QueryClientProvider>,
  );
  return { ...view, urls };
}

/**
 * TestTheOutcomeWindowFollowsTheSignalsOnScreen.
 *
 * # What this prevents
 *
 * /signals is not windowed and /outcomes is, defaulting to the last year. A
 * list annotated from the default window shows every signal older than that as
 * "not followed yet" — including ones that resolved two years ago and are
 * sitting in the table.
 *
 * It is a silent wrong answer: the row renders, it looks like a signal nobody
 * has followed up, and the outcome it does have is invisible.
 */
describe('the outcomes shown beside each signal', () => {
  it('are fetched over a window that covers the oldest signal on screen', async () => {
    const { urls } = renderWith([oldSignal('a')], [resolved('a')]);

    await waitFor(() => expect(screen.getByText(/stop · -1\.0934%/)).toBeTruthy());

    const outcomeCall = urls.findLast((url) => url.includes('/outcomes'));
    expect(outcomeCall).toBeDefined();

    const from = new URL(outcomeCall!).searchParams.get('from');
    expect(from).toBeTruthy();
    // Before the signal, not a year before now.
    expect(Date.parse(from!)).toBeLessThan(Date.parse('2024-03-06T00:00:00Z'));
  });

  it('shows the outcome rather than "not followed yet"', async () => {
    renderWith([oldSignal('a')], [resolved('a')]);

    await waitFor(() => expect(screen.getByText(/stop · -1.0934%/)).toBeTruthy());
    expect(screen.queryByText('not followed yet')).toBeNull();
  });

  it('says "not followed yet" only when there really is no outcome', async () => {
    renderWith([oldSignal('a')], []);

    await waitFor(() => expect(screen.getByText('not followed yet')).toBeTruthy());
  });
});

/**
 * TestTheQueryKeyIsStable.
 *
 * # What this prevents
 *
 * The outcome window is part of the react-query key. A Date built in the
 * render body is a different key every render, so the query refetches, which
 * re-renders, which builds another Date — a loop that never settles, and whose
 * only symptom is that the data never appears.
 *
 * Counting requests is the way to see it: a settled screen makes one call per
 * endpoint, and a looping one makes them without end.
 */
describe('the queries', () => {
  it('settle rather than refetching themselves', async () => {
    const { urls } = renderWith([oldSignal('a')], [resolved('a')]);

    await waitFor(() => expect(screen.getByText(/stop · -1\.0934%/)).toBeTruthy());

    // Let anything still in flight land, then take the count.
    await new Promise((resolve) => setTimeout(resolve, 200));
    const settled = urls.length;

    await new Promise((resolve) => setTimeout(resolve, 400));

    expect(urls.length).toBe(settled);
    // One per endpoint: the outcome query waits for the window rather than
    // firing once against the default and again against the real one.
    expect(urls.filter((u) => u.includes('/outcomes'))).toHaveLength(1);
    expect(urls.filter((u) => u.includes('/signals'))).toHaveLength(1);
  });
});

describe('the filters', () => {
  it('offers direction and outcome status', async () => {
    renderWith([oldSignal('a', 'long'), oldSignal('b', 'short')], []);

    await waitFor(() => expect(screen.getByText('all')).toBeTruthy());
    expect(screen.getByText('long')).toBeTruthy();
    expect(screen.getByText('short')).toBeTruthy();
    expect(screen.getByText('any outcome')).toBeTruthy();
    expect(screen.getByText('invalidated')).toBeTruthy();
  });
});
