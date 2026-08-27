import { useEffect, useMemo, useState } from 'react';
import { Pressable, ScrollView, View, useWindowDimensions } from 'react-native';

import { useApi } from '../../api/provider';
import { useCandles, useOutcomes, useSignals, useStatus } from '../../api/queries';
import { price, utc } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle } from '../../components/Card';
import { Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import { Candles, markersFor } from './Candles';
import { MINUTES, VISIBLE_BARS, covers, windowFor } from './window';
import type { Window } from './window';
import type { Timeframe } from '../../api/types';

const TIMEFRAMES: Timeframe[] = ['1m', '5m', '15m', '1h', '4h', '1d'];

/**
 * B3 — "what did the market do around it".
 *
 * # What is drawn and what is not
 *
 * Candles in the direction colours, the signal markers, and the stop and
 * target as dashed horizontals. Nothing else: no gold gridlines, no jade fill
 * under the price. The chart is data and the theme is the frame around it.
 *
 * # Panning
 *
 * The window is one screen wider than the view in each direction, and moving
 * inside that costs nothing: the fetched window is held in state, so a step
 * that stays inside it never reaches the query key. Stepping past the edge is
 * the only thing that fetches — see window.ts, where the arithmetic lives.
 */
export function ChartScreen() {
  const { baseUrl } = useApi();
  const { width } = useWindowDimensions();
  const [timeframe, setTimeframe] = useState<Timeframe>('4h');

  // The right edge of the view. Stepping it is what pans.
  //
  // Null means "wherever the data ends", which is where a chart should open.
  // Anchoring on wall-clock now is blank whenever the collector is behind, and
  // blank looks identical to a market that stopped trading — so the default
  // follows the series and only becomes a fixed instant once somebody pans.
  const [pannedTo, setPannedTo] = useState<Date | null>(null);

  const status = useStatus();
  const seriesEnd = useMemo(() => {
    const series = status.data?.ingestion.timeframes.find((tf) => tf.timeframe === timeframe);
    return series?.latest_open_time ? new Date(series.latest_open_time) : undefined;
  }, [status.data, timeframe]);

  const end = pannedTo ?? seriesEnd ?? new Date();
  const setEnd = setPannedTo;

  // Wall-clock now is the fallback, not the opening position. Fetching
  // against it before the status reply lands asks for a window nowhere near
  // the data and throws the answer away, so nothing is asked for until the
  // anchor has settled one way or the other.
  const anchored = status.isSuccess || status.isError;

  // The instant rather than the Date, so the memo compares by value: a new
  // Date with the same time is a different object and would rebuild the
  // window — and the window is part of the query key.
  const endAt = end.getTime();
  const visibleFrom = endAt - VISIBLE_BARS * MINUTES[timeframe] * 60_000;

  // The window that was fetched, which is not the window being looked at.
  // Holding it in state is what makes a pan free: `endAt` moves on every
  // step, and if it fed the query key directly every step would be a round
  // trip and the overscan would buy nothing.
  const [loaded, setLoaded] = useState<{ timeframe: Timeframe; window: Window } | null>(null);

  useEffect(() => {
    if (!anchored) return;
    const stale =
      loaded === null ||
      loaded.timeframe !== timeframe ||
      !covers(loaded.window, new Date(visibleFrom), new Date(endAt));
    if (stale) setLoaded({ timeframe, window: windowFor(timeframe, new Date(endAt)) });
  }, [anchored, loaded, timeframe, endAt, visibleFrom]);

  // A window left over from the previous timeframe would draw 4h bars on a
  // 1m axis for one frame, so the query waits rather than showing them.
  const ready = loaded !== null && loaded.timeframe === timeframe;
  const request = ready ? loaded.window : undefined;
  const candles = useCandles(
    { timeframe, from: request?.from, to: request?.to, limit: request?.limit },
    ready,
  );
  const signals = useSignals({ limit: 50 });
  const outcomes = useOutcomes({ limit: 200 });

  // Drawn from `end`, not from the tail of what arrived. The window runs a
  // screen past the right edge on purpose, so the tail is the overscan —
  // slicing it would draw the same bars wherever the chart was panned to.
  const visible = useMemo(() => {
    const all = candles.data?.candles ?? [];
    return all.filter((candle) => Date.parse(candle.open_time) <= endAt).slice(-VISIBLE_BARS);
  }, [candles.data, endAt]);
  const markers = markersFor(signals.data?.signals ?? [], outcomes.data?.outcomes ?? []);
  const forming = visible.filter((candle) => !candle.is_closed).length;

  const chartWidth = width - 2 * layout.screenPadding - 2 * layout.cardPadding;

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: colors.bg.base }}
      contentContainerStyle={{ padding: layout.screenPadding, gap: layout.space.md }}
    >
      <View style={{ flexDirection: 'row', gap: layout.space.xs, flexWrap: 'wrap' }}>
        {TIMEFRAMES.map((value) => (
          <Pressable
            key={value}
            accessibilityRole="button"
            accessibilityState={{ selected: timeframe === value }}
            onPress={() => setTimeframe(value)}
            style={{
              paddingHorizontal: layout.space.md,
              paddingVertical: layout.space.xs,
              borderRadius: layout.radius,
              borderWidth: layout.hairline,
              borderColor: timeframe === value ? colors.border.gold : colors.border.subtle,
              backgroundColor: timeframe === value ? colors.bg.raised : 'transparent',
            }}
          >
            <Text size="detail" tone={timeframe === value ? 'primary' : 'tertiary'}>
              {value}
            </Text>
          </Pressable>
        ))}
      </View>

      {candles.error ? <Failure error={candles.error} baseUrl={baseUrl} /> : null}

      <Card>
        <View style={{ flexDirection: 'row', justifyContent: 'space-between' }}>
          <CardTitle>{timeframe}</CardTitle>
          <Text size="caption" tone="tertiary" tabular>
            {visible.length} bars
          </Text>
        </View>

        {visible.length > 0 ? (
          <Candles candles={visible} markers={markers} width={chartWidth} height={260} />
        ) : (
          <Empty
            timeframe={timeframe}
            loading={candles.isFetching || status.isLoading}
            seriesEnd={seriesEnd}
            onJump={() => setEnd(null)}
          />
        )}

        {/* The forming candle, called what it is. §3.1 permits it on the wire
            and this is where a person finds out they are looking at one. */}
        {forming > 0 ? (
          <Text size="detail" style={{ color: colors.semantic.warn }}>
            The last bar has not closed. It is drawn hollow and its price can still change.
          </Text>
        ) : null}

        {visible.length > 0 ? (
          <Text size="caption" tone="tertiary" tabular>
            {utc(visible[0]!.open_time)} → {utc(visible[visible.length - 1]!.open_time)}
          </Text>
        ) : null}
      </Card>

      <View style={{ flexDirection: 'row', gap: layout.space.sm }}>
        <Step label="← earlier" onPress={() => setEnd(step(timeframe, end, -1))} />
        <Step label="later →" onPress={() => setEnd(step(timeframe, end, 1))} />
        <Step label="newest" onPress={() => setEnd(null)} />
      </View>

      {candles.data?.truncated ? (
        <Text size="detail" tone="secondary">
          The window held more bars than were returned; these are the newest.
        </Text>
      ) : null}

      {visible.length > 0 ? (
        <Card>
          <CardTitle>Newest bar</CardTitle>
          <Text size="detail" tone="secondary" tabular>
            O {price(visible[visible.length - 1]!.open)} · H{' '}
            {price(visible[visible.length - 1]!.high)} · L{' '}
            {price(visible[visible.length - 1]!.low)} · C{' '}
            {price(visible[visible.length - 1]!.close)}
          </Text>
        </Card>
      ) : null}
    </ScrollView>
  );
}

/**
 * What to show when the window holds nothing.
 *
 * A blank rectangle is the worst possible answer here: it is what a stopped
 * collector, a panned-past-the-end chart and a market that never traded all
 * look like. Saying which costs three lines.
 */
function Empty({
  timeframe,
  loading,
  seriesEnd,
  onJump,
}: {
  timeframe: Timeframe;
  loading: boolean;
  seriesEnd: Date | undefined;
  onJump: () => void;
}) {
  if (loading) {
    return (
      <View style={{ height: 260, justifyContent: 'center' }}>
        <Text tone="secondary" size="detail">
          Reading candles…
        </Text>
      </View>
    );
  }

  return (
    <View style={{ height: 260, justifyContent: 'center', gap: layout.space.sm }}>
      <Text size="body" weight="medium">
        No candles in this window
      </Text>
      {seriesEnd ? (
        <>
          <Text size="detail" tone="secondary">
            The {timeframe} series ends at {utc(seriesEnd.toISOString())}. You have panned
            past it, or the collector has not caught up.
          </Text>
          <Pressable
            accessibilityRole="button"
            onPress={onJump}
            style={{
              alignSelf: 'flex-start',
              paddingHorizontal: layout.space.md,
              paddingVertical: layout.space.xs,
              borderRadius: layout.radius,
              borderWidth: layout.hairline,
              borderColor: colors.border.subtle,
            }}
          >
            <Text size="detail" tone="secondary">
              Jump to the newest bar
            </Text>
          </Pressable>
        </>
      ) : (
        <Text size="detail" tone="secondary">
          Nothing has been stored for {timeframe} at all. Check the collector on the status
          screen.
        </Text>
      )}
    </View>
  );
}

/**
 * Moves the right edge by half a screen, which is what a pan step is.
 *
 * Half rather than a whole one so that something stays on screen across the
 * step, and so that two steps in a row still land inside the overscan.
 */
function step(timeframe: Timeframe, from: Date, direction: -1 | 1): Date {
  return new Date(from.getTime() + direction * (VISIBLE_BARS / 2) * MINUTES[timeframe] * 60_000);
}

function Step({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      style={{
        flex: 1,
        alignItems: 'center',
        paddingVertical: layout.space.sm,
        borderRadius: layout.radius,
        backgroundColor: colors.bg.raised,
      }}
    >
      <Text size="detail" tone="secondary">
        {label}
      </Text>
    </Pressable>
  );
}
