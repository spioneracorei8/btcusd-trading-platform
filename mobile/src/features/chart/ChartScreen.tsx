import { useMemo, useState } from 'react';
import { Pressable, ScrollView, View, useWindowDimensions } from 'react-native';

import { useApi } from '../../api/provider';
import { useCandles, useOutcomes, useSignals } from '../../api/queries';
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
  const [end, setEnd] = useState(() => new Date());

  const window = useMemo(() => windowFor(timeframe, end), [timeframe, end]);
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

        <Candles candles={visible} markers={markers} width={chartWidth} height={260} />

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
        <Step label="now" onPress={() => setEnd(new Date())} />
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
