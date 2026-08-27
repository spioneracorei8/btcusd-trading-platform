import { useQuery } from '@tanstack/react-query';

import { useApi } from './provider';
import type { CandlesQuery, OutcomesQuery, SignalsQuery } from './client';
import type { Timeframe } from './types';

/**
 * Server state, and how often each thing is worth asking for again.
 *
 * # Why the intervals differ so much
 *
 * The status is what somebody stares at while troubleshooting, so it refreshes
 * often. Signals arrive about once every ten days on a 4h strategy, so polling
 * them hard would be asking a question whose answer almost never changes.
 * Performance aggregates the whole outcome history and takes seconds on the
 * server; it is a screen somebody opens, not a feed.
 *
 * Nothing here recomputes anything. Every figure arrives already computed —
 * a win rate derived on the phone would be a second implementation of one that
 * already exists, and the two would drift.
 */

const SECOND = 1000;
const MINUTE = 60 * SECOND;

export function useStatus() {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['status', client.baseUrl],
    queryFn: ({ signal }) => client.status(signal),
    enabled: ready,
    // The screen somebody watches while something is wrong.
    refetchInterval: 15 * SECOND,
    staleTime: 5 * SECOND,
  });
}

export function useSignals(query: SignalsQuery = {}) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['signals', client.baseUrl, query],
    queryFn: ({ signal }) => client.signals(query, signal),
    enabled: ready,
    refetchInterval: MINUTE,
  });
}

export function useSignal(id: string | undefined) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['signal', client.baseUrl, id],
    queryFn: ({ signal }) => client.signal(id!, signal),
    enabled: ready && Boolean(id),
    // A recorded signal never changes except for entry_price, which is filled
    // in one bar later. Worth one refetch, not a poll.
    staleTime: 5 * MINUTE,
  });
}

export function useOutcomes(query: OutcomesQuery = {}) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['outcomes', client.baseUrl, query],
    queryFn: ({ signal }) => client.outcomes(query, signal),
    enabled: ready,
    refetchInterval: MINUTE,
  });
}

export function usePerformance(query: { from?: Date; to?: Date } = {}) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['performance', client.baseUrl, query.from?.toISOString(), query.to?.toISOString()],
    queryFn: ({ signal }) => client.performance(query, signal),
    enabled: ready,
    // Aggregates every outcome in the window. A screen somebody opens.
    staleTime: 5 * MINUTE,
  });
}

export function useCandles(query: CandlesQuery, enabled = true) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: [
      'candles',
      client.baseUrl,
      query.timeframe,
      query.from?.toISOString(),
      query.to?.toISOString(),
      query.limit,
    ],
    queryFn: ({ signal }) => client.candles(query, signal),
    enabled: ready && enabled,
    // The chart caches windows and extends at the edges; see ChartScreen.
    staleTime: MINUTE,
  });
}

export function useIndicators(query: { timeframe: Timeframe; from?: Date; to?: Date }) {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: [
      'indicators',
      client.baseUrl,
      query.timeframe,
      query.from?.toISOString(),
      query.to?.toISOString(),
    ],
    queryFn: ({ signal }) => client.indicators(query, signal),
    enabled: ready,
    staleTime: MINUTE,
  });
}

export function useDevice() {
  const { client, ready } = useApi();
  return useQuery({
    queryKey: ['device', client.baseUrl],
    queryFn: ({ signal }) => client.device(signal),
    enabled: ready,
    staleTime: MINUTE,
  });
}
