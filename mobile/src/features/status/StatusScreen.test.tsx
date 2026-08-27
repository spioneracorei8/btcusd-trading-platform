import { render, screen, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { StatusScreen } from './StatusScreen';
import type { Status } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';

function statusFixture(overrides: Partial<Status> = {}): Status {
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
    ingestion: {
      unfilled_gaps: 0,
      timeframes: [{ timeframe: '1m', unfilled_gaps: 0 }],
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
    note: 'Silence is the normal output of this pipeline.',
    ...overrides,
  };
}

/**
 * The clients a test created, torn down afterwards.
 *
 * useStatus polls, so an un-cleared client leaves a timer running past the end
 * of the test file and jest sits waiting on it. Unmounting stops the observer;
 * clearing the cache stops everything else.
 */
const clients: QueryClient[] = [];

afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

function renderWith(responder: () => Response | Promise<Response>) {
  const fetchImpl = (async () => responder()) as unknown as typeof fetch;
  const client = new ApiClient({ baseUrl: BASE, fetchImpl });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(queryClient);

  return render(
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={client}>
        <StatusScreen />
      </ApiProvider>
    </QueryClientProvider>,
  );
}

function json(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('the status screen', () => {
  it('shows the raw values rather than a summary', async () => {
    // Optimised for the day something is wrong: readable aloud down a phone,
    // legible in a screenshot, nothing interpreted away.
    renderWith(() => json(statusFixture()));

    await waitFor(() => expect(screen.getByText('BTCUSDT')).toBeTruthy());
    expect(screen.getByText('live')).toBeTruthy();
    expect(screen.getByText('ema_crossover on 4h')).toBeTruthy();
    expect(screen.getByText('2026-08-27 11:59:58Z')).toBeTruthy();
  });

  it('says there are no concerns rather than showing an empty space', async () => {
    // A blank where a problem list would be is indistinguishable from a check
    // that did not run.
    renderWith(() => json(statusFixture()));

    await waitFor(() =>
      expect(screen.getByText(/nothing it reported looks wrong/i)).toBeTruthy(),
    );
  });

  it('puts the concerns above everything else, in the server’s own words', async () => {
    const detail =
      'the last heartbeat was 4m52s ago, more than three intervals of 5s — ' +
      'the collector process may be gone';
    renderWith(() =>
      json(statusFixture({ concerns: [{ component: 'collector', detail }] })),
    );

    await waitFor(() => expect(screen.getByText(detail)).toBeTruthy());
    expect(screen.getByText('1 concern')).toBeTruthy();
  });

  it('labels severity in words, not only in colour', async () => {
    // The palette clears the colour-blindness threshold, but a floor is not a
    // licence. This is the screen somebody reads while something is broken.
    renderWith(() =>
      json(
        statusFixture({
          concerns: [{ component: 'delivery', detail: 'no device is registered' }],
        }),
      ),
    );

    await waitFor(() => expect(screen.getByText('DELIVERY')).toBeTruthy());
  });

  it('shows the registered device count and the gap breakdown', async () => {
    // Both are here because B5 asks for them and neither was on /status
    // before phase 09.
    renderWith(() =>
      json(
        statusFixture({
          delivery: {
            mode: 'notify',
            pending: 3,
            sent: 0,
            failed: 0,
            last_sent_at: null,
            devices_registered: 0,
          },
          ingestion: {
            unfilled_gaps: 4,
            timeframes: [
              { timeframe: '1m', unfilled_gaps: 3 },
              { timeframe: '4h', unfilled_gaps: 1 },
            ],
          },
        }),
      ),
    );

    await waitFor(() => expect(screen.getByText('devices registered')).toBeTruthy());
    expect(screen.getByText('unfilled candle gaps')).toBeTruthy();
    expect(screen.getByText('  1m')).toBeTruthy();
    expect(screen.getByText('  4h')).toBeTruthy();
  });

  it('names Tailscale when the server cannot be reached', async () => {
    // The commonest failure this app will see, and a spinner is the wrong
    // answer to it.
    renderWith(() => {
      throw new TypeError('Network request failed');
    });

    await waitFor(() => expect(screen.getByText(/cannot reach the server/i)).toBeTruthy());
    expect(screen.getByText(new RegExp(BASE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))).toBeTruthy();
    expect(screen.getByText(/open tailscale/i)).toBeTruthy();
  });
});
