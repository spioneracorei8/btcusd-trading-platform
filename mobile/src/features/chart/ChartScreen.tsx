import { useMemo, useState } from 'react';
import { Pressable, ScrollView, View, useWindowDimensions } from 'react-native';

import { useApi } from '../../api/provider';
import { useCandles, useOutcomes, useSignals, useStatus } from '../../api/queries';
import { price, utc } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle } from '../../components/Card';
import { Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import { Candles, markersFor } from './Candles';
import { VISIBLE_BARS, windowFor } from './window';
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
 * inside that costs nothing. Stepping past the edge fetches the next window —
 * see window.ts, where the arithmetic lives and is tested.
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

  // The instant rather than the Date, so the memo compares by value: a new
  // Date with the same time is a different object and would rebuild the
  // window — and the window is part of the query key.
  const endAt = end.getTime();
  const window = useMemo(() => windowFor(timeframe, new Date(endAt)), [timeframe, endAt]);
  const candles = useCandles({ timeframe, ...window });
  const signals = useSignals({ limit: 50 });
  const outcomes = useOutcomes({ limit: 200 });

  const visible = (candles.data?.candles ?? []).slice(-VISIBLE_BARS);
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
            loading={candles.isLoading || status.isLoading}
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

/** Moves the right edge by half a screen, which is what a pan step is. */
function step(timeframe: Timeframe, from: Date, direction: -1 | 1): Date {
  const minutes: Record<Timeframe, number> = {
    '1m': 1,
    '5m': 5,
    '15m': 15,
    '1h': 60,
    '4h': 240,
    '1d': 1440,
  };
  return new Date(from.getTime() + direction * (VISIBLE_BARS / 2) * minutes[timeframe] * 60_000);
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
