import { render, screen, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { SignalDetailScreen } from './SignalDetailScreen';
import type { Outcome, Signal } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';
const ID = 'e6d0e070-39a3-4fa2-907c-1dc470c83d3d';

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

function aSignal(overrides: Partial<Signal> = {}): Signal {
  return {
    id: ID,
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

function renderWith(signal: Signal, outcome?: Outcome) {
  const fetchImpl = (async (input: RequestInfo | URL) => {
    const url = String(input);
    const body = url.includes('/outcomes')
      ? { outcomes: outcome ? [outcome] : [], count: 0, total: 0, limit: 200, offset: 0 }
      : signal;
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
        <SignalDetailScreen id={ID} />
      </ApiProvider>
    </QueryClientProvider>,
  );
}

/**
 * TestTheReasonIsRenderedInFull.
 *
 * # Why this screen is the point of the signals tab
 *
 * A signal without its reasoning is unreviewable six weeks later. Indicators
 * are never stored, so the values behind a decision cannot be recomputed
 * against the warm-up state the live process actually had — the reason blob is
 * the only record there will ever be.
 *
 * That includes the parts this build has never heard of. A strategy added
 * later will record fields this app does not know, and dropping them would
 * quietly make the older app worse at the one job this screen has.
 */
describe('the reason', () => {
  it('shows the trigger, the parameters and the indicator values', async () => {
    renderWith(
      aSignal({
        reason: {
          trigger: 'fast crossed slow',
          strategy: {
            name: 'ema_crossover',
            version: 'v1',
            params: [
              { name: 'fast', value: '9' },
              { name: 'slow', value: '21' },
            ],
          },
          indicators: { ema: 64000.5, rsi: 58.2, atr: 412.75 },
        },
      }),
    );

    await waitFor(() => expect(screen.getByText('fast crossed slow')).toBeTruthy());
    expect(screen.getByText('fast')).toBeTruthy();
    expect(screen.getByText('9')).toBeTruthy();
    expect(screen.getByText('slow')).toBeTruthy();
    expect(screen.getByText('21')).toBeTruthy();
    expect(screen.getByText('ema')).toBeTruthy();
    expect(screen.getByText('64000.5')).toBeTruthy();
    expect(screen.getByText('rsi')).toBeTruthy();
  });

  it('shows fields this build has never heard of rather than dropping them', async () => {
    // The forward-compatibility case. A strategy shipped after this app will
    // record things it does not know about, and this screen exists precisely
    // so those survive to be read.
    renderWith(
      aSignal({
        reason: {
          trigger: 'something new',
          regime: 'trending',
          confluence: { htf: 'up', ltf: 'up' },
        },
      }),
    );

    await waitFor(() => expect(screen.getByText('regime')).toBeTruthy());
    expect(screen.getByText('trending')).toBeTruthy();
    expect(screen.getByText('confluence')).toBeTruthy();
    expect(screen.getByText(/"htf":"up"/)).toBeTruthy();
  });

  it('says so when no reason was recorded', async () => {
    renderWith(aSignal({ reason: undefined }));

    await waitFor(() =>
      expect(screen.getByText(/no reason was recorded/i)).toBeTruthy(),
    );
  });
});

describe('the prices', () => {
  it('keeps the decided price and the fill apart, with labels that say which', async () => {
    // Phase 07 made the distinction deliberately: a decision taken on a bar's
    // close cannot also fill on it. Two numbers, two labels.
    renderWith(aSignal());

    await waitFor(() =>
      expect(screen.getByText('reference price (decided on)')).toBeTruthy(),
    );
    expect(screen.getByText("entry (next bar's open + slippage)")).toBeTruthy();
    expect(screen.getByText('64,000')).toBeTruthy();
    expect(screen.getByText('64,010.01')).toBeTruthy();
  });

  it('shows an unfilled entry as a dash rather than a zero', async () => {
    // entry_price is null until the next bar closes. A zero would be charted
    // and compared like a real fill.
    renderWith(aSignal({ entry_price: null }));

    await waitFor(() => expect(screen.getByText('64,000')).toBeTruthy());
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });
});

describe('the outcome', () => {
  it('says an invalidated window is not knowable rather than showing a figure', async () => {
    renderWith(aSignal(), {
      signal_id: ID,
      signal_time: '2026-08-27T08:00:00Z',
      direction: 'long',
      timeframe: '4h',
      strategy_name: 'ema_crossover',
      strategy_version: 'v1',
      status: 'invalidated',
      bars_held: 4,
      measurable: false,
      resolved_at: '2026-08-27T20:00:00Z',
      signal_price: '64000',
      entry_price: '64010.01',
      resolved_price: null,
      mae: null,
      mfe: null,
      net_return_pct: null,
    });

    await waitFor(() => expect(screen.getByText(/not\s+knowable/i)).toBeTruthy());
    expect(screen.getByText(/excluded from every figure/i)).toBeTruthy();
  });
});

describe('what the screen will not do', () => {
  it('says out loud that nothing here places an order', async () => {
    renderWith(aSignal());

    await waitFor(() => expect(screen.getByText(/places an order/i)).toBeTruthy());
  });
});
