import { render, screen, waitFor } from '@testing-library/react-native';
import { StyleSheet, Text as RNText } from 'react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { ApiClient } from '../../api/client';
import { ApiProvider } from '../../api/provider';
import { PerformanceScreen } from './PerformanceScreen';
import { type as typography } from '../../theme';
import type { PerformanceGroup, PerformanceResponse } from '../../api/types';

const BASE = 'http://100.72.14.3:8080';

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
});

function group(overrides: Partial<PerformanceGroup> = {}): PerformanceGroup {
  return {
    strategy: 'ema_crossover',
    version: 'v1',
    params: [{ name: 'fast', value: '9' }],
    sample: {
      resolved: 30,
      required: 100,
      sufficient: false,
      banner:
        'signals resolved: 30\n' +
        'NOT ENOUGH DATA — differences below are within normal variation.\n' +
        'A meaningful comparison needs at least 100 resolved signals.\n' +
        'At the observed 6.21 resolved signals a day, the remaining 70 would take about 11 days.',
      resolved_per_day: 6.21,
      expected_wait: '11 days',
    },
    signals: 30,
    resolved: 30,
    still_open: 0,
    invalidated_excluded: 0,
    targets: 4,
    stops: 26,
    expired: 0,
    wins: 4,
    losses: 26,
    win_rate: 0.13333333333333333,
    average_win_pct: '1.0049',
    average_loss_pct: '-1.1106',
    average_cost_pct: '0.1000',
    expectancy_pct: '-0.8285',
    rested_on_assumption: 0,
    ...overrides,
  };
}

function response(groups: PerformanceGroup[]): PerformanceResponse {
  return {
    symbol: 'BTCUSDT',
    market_type: 'spot',
    from: '2025-08-27T00:00:00Z',
    to: '2026-08-27T00:00:00Z',
    generated_at: '2026-08-27T12:00:00Z',
    groups,
    note:
      'Grouped by strategy, version and resolved parameter set, with no total across ' +
      'groups: averaging across a parameter change produces a number describing nothing.',
  };
}

function renderWith(body: PerformanceResponse) {
  const fetchImpl = (async () =>
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  return render(
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={new ApiClient({ baseUrl: BASE, fetchImpl })}>
        <PerformanceScreen />
      </ApiProvider>
    </QueryClientProvider>,
  );
}

/**
 * TestTheBannerComesBeforeTheNumbers.
 *
 * # What this prevents
 *
 * Phase 08 found that a banner placed beside a figure loses to the figure: the
 * eye goes to the number, reads it, and forms a view before it reaches the
 * caveat. This screen invites exactly that — it is glanced at on a phone — and
 * the figure it would form a view about is one drawn from thirty trades.
 *
 * Reading order is the mechanism, so the test asserts on order rather than on
 * presence. A banner rendered anywhere on the page would pass a presence check
 * and fail the reader.
 */
describe('the insufficient-sample banner', () => {
  it('renders above every figure it qualifies', async () => {
    renderWith(response([group()]));

    await waitFor(() => expect(screen.getByText(/NOT ENOUGH DATA/)).toBeTruthy());

    const order = screen
      .UNSAFE_getAllByType(RNText)
      .map((node) => String(node.props.children ?? ''));

    const banner = order.findIndex((text) => text.includes('NOT ENOUGH DATA'));
    const expectancy = order.findIndex((text) => text.includes('-0.8285'));
    const winRate = order.findIndex((text) => text.includes('13.3%'));

    expect(banner).toBeGreaterThanOrEqual(0);
    expect(expectancy).toBeGreaterThan(banner);
    expect(winRate).toBeGreaterThan(banner);
  });

  it('carries the whole of the server’s wording, including the wait', async () => {
    // One wording for the CLI, the API and here, so nobody reads a softer
    // version of "this number does not mean anything yet".
    renderWith(response([group()]));

    await waitFor(() =>
      expect(screen.getByText(/needs at least 100 resolved signals/)).toBeTruthy(),
    );
    expect(screen.getByText(/would take about 11 days/)).toBeTruthy();
  });

  it('is absent once the sample is sufficient, so it means something when it is there', async () => {
    renderWith(
      response([
        group({
          sample: { resolved: 140, required: 100, sufficient: true, resolved_per_day: 6.2 },
        }),
      ]),
    );

    await waitFor(() => expect(screen.getByText('win rate')).toBeTruthy());
    expect(screen.queryByText(/NOT ENOUGH DATA/)).toBeNull();
    // And the wait card goes with it: there is nothing left to wait for.
    expect(screen.queryByText('expected wait')).toBeNull();
  });
});

/**
 * TestEveryFigureCarriesItsSample.
 *
 * A win rate over nine trades and one over nine hundred must not be able to
 * look alike.
 */
describe('sample sizes', () => {
  it('sit beside the expectancy and the win rate', async () => {
    renderWith(response([group()]));

    await waitFor(() => expect(screen.getAllByText(/over 30 resolved/)).toHaveLength(2));
  });

  it('render at 14, not caption size', async () => {
    // The single most important typographic rule in the app. A sample size is
    // a qualifier on the number beside it, and a qualifier set smaller than
    // the figure loses the argument.
    renderWith(response([group()]));

    await waitFor(() => expect(screen.getAllByText(/over 30 resolved/)).toHaveLength(2));

    for (const node of screen.getAllByText(/over 30 resolved/)) {
      const style = StyleSheet.flatten(node.props.style) as { fontSize?: number };
      expect(style.fontSize).toBe(14);
      expect(style.fontSize).toBeGreaterThan(typography.size.caption);
    }
  });
});

describe('the figures themselves', () => {
  it('shows expectancy after costs as the headline', async () => {
    // The number that decides whether a strategy is worth running.
    renderWith(response([group()]));

    await waitFor(() => expect(screen.getByText('-0.8285%')).toBeTruthy());
    expect(screen.getByText(/after modelled costs/i)).toBeTruthy();
  });

  it('renders a null win rate as a dash rather than zero', async () => {
    // A zero would read as a strategy that never wins. Nothing having
    // resolved yet is a different statement.
    renderWith(
      response([
        group({
          win_rate: null,
          expectancy_pct: null,
          resolved: 0,
          sample: { resolved: 0, required: 100, sufficient: false, resolved_per_day: null },
        }),
      ]),
    );

    await waitFor(() => expect(screen.getByText('win rate')).toBeTruthy());
    expect(screen.queryByText('0.0%')).toBeNull();
  });

  it('states the expected wait rather than leaving it to be worked out', async () => {
    // A performance screen that looks nearly ready to tell you something, for
    // years, is worse than one that says how long it will be.
    renderWith(response([group()]));

    await waitFor(() => expect(screen.getByText('expected wait')).toBeTruthy());
    expect(screen.getByText('11 days')).toBeTruthy();
  });

  it('marks invalidated outcomes as excluded', async () => {
    renderWith(response([group({ invalidated_excluded: 3 })]));

    await waitFor(() => expect(screen.getByText('invalidated (excluded)')).toBeTruthy());
  });

  it('explains a resolution that rested on an assumption', async () => {
    renderWith(response([group({ rested_on_assumption: 7 })]));

    await waitFor(() =>
      expect(screen.getByText(/rests on an assumption rather than on evidence/)).toBeTruthy(),
    );
  });
});

/**
 * TestThereIsNoTotalAcrossGroups.
 *
 * Averaging across a parameter change produces a number describing nothing,
 * and a screen offering one would be offering the most tempting number on it.
 */
describe('two parameter sets', () => {
  it('are shown separately, with no combined figure', async () => {
    renderWith(
      response([
        group({ params: [{ name: 'fast', value: '9' }] }),
        group({ params: [{ name: 'fast', value: '21' }], expectancy_pct: '0.4210' }),
      ]),
    );

    await waitFor(() => expect(screen.getByText('fast=9')).toBeTruthy());
    expect(screen.getByText('fast=21')).toBeTruthy();
    expect(screen.getByText('-0.8285%')).toBeTruthy();
    expect(screen.getByText('+0.421%')).toBeTruthy();

    // Nothing that reads as a roll-up.
    expect(screen.queryByText(/^total/i)).toBeNull();
    expect(screen.queryByText(/overall/i)).toBeNull();
    expect(screen.queryByText(/combined/i)).toBeNull();
  });

  it('carries the note explaining why there is no total', async () => {
    renderWith(response([group()]));

    await waitFor(() =>
      expect(screen.getByText(/no total across groups/)).toBeTruthy(),
    );
  });
});
