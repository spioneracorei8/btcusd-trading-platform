import React from 'react';
import { View } from 'react-native';
import Svg, { Line, Rect } from 'react-native-svg';

import { colors } from '../../theme';
import type { Candle, Direction, Outcome, Signal } from '../../api/types';

/**
 * The candles, drawn.
 *
 * # Why the chart uses the direction colours and nothing else
 *
 * "Charts use the direction colours for candles and nothing else. No gold
 * gridlines, no jade fills under the price line. The chart is data; the theme
 * is the frame around it."
 *
 * A gold gridline would be the largest gold area in the app by an order of
 * magnitude, and it would be behind the one part of the screen that is
 * supposed to be read rather than admired.
 *
 * # Why the numbers are parsed here
 *
 * Prices are strings everywhere else in this app, on purpose: they are
 * numeric(20,8) and a float64 cannot hold every value. Drawing requires
 * pixels, and pixels are floats — so this is the one place a price becomes a
 * number, and it does so to compute a y coordinate and for nothing else. No
 * value that reaches the user through this file is derived from the parse.
 */

export type Marker = {
  at: number;
  price: number;
  direction: Direction;
  kind: 'entry' | 'exit';
};

export function Candles({
  candles,
  markers = [],
  levels,
  width,
  height,
}: {
  candles: Candle[];
  markers?: Marker[];
  /** Stop and target of the signal being looked at, drawn as horizontals. */
  levels?: { stop?: number; target?: number };
  width: number;
  height: number;
}) {
  if (candles.length === 0 || width <= 0 || height <= 0) {
    return <View style={{ width, height }} />;
  }

  const lows = candles.map((c) => Number(c.low));
  const highs = candles.map((c) => Number(c.high));
  const extra = [levels?.stop, levels?.target].filter((v): v is number => v !== undefined);

  const min = Math.min(...lows, ...extra);
  const max = Math.max(...highs, ...extra);
  const span = max - min || 1;

  const y = (price: number) => height - ((price - min) / span) * height;
  const slot = width / candles.length;
  const body = Math.max(1, slot * 0.6);

  return (
    <Svg width={width} height={height}>
      {levels?.stop !== undefined ? (
        <Line
          x1={0}
          x2={width}
          y1={y(levels.stop)}
          y2={y(levels.stop)}
          stroke={colors.direction.short}
          strokeWidth={1}
          strokeDasharray="4 4"
        />
      ) : null}
      {levels?.target !== undefined ? (
        <Line
          x1={0}
          x2={width}
          y1={y(levels.target)}
          y2={y(levels.target)}
          stroke={colors.direction.long}
          strokeWidth={1}
          strokeDasharray="4 4"
        />
      ) : null}

      {candles.map((candle, index) => {
        const open = Number(candle.open);
        const close = Number(candle.close);
        const rising = close >= open;
        const tint = rising ? colors.direction.long : colors.direction.short;
        const x = index * slot + slot / 2;

        const top = y(Math.max(open, close));
        const bottom = y(Math.min(open, close));

        return (
          <React.Fragment key={candle.open_time}>
            <Line
              x1={x}
              x2={x}
              y1={y(Number(candle.high))}
              y2={y(Number(candle.low))}
              stroke={tint}
              strokeWidth={1}
              // A forming bar is drawn hollow. It is the only candle on this
              // chart that can still change, and the flag on the wire has to
              // survive into something the eye can see.
              opacity={candle.is_closed ? 1 : 0.5}
            />
            <Rect
              x={x - body / 2}
              y={top}
              width={body}
              height={Math.max(1, bottom - top)}
              fill={candle.is_closed ? tint : 'none'}
              stroke={tint}
              strokeWidth={1}
              opacity={candle.is_closed ? 1 : 0.5}
            />
          </React.Fragment>
        );
      })}

      {markers.map((marker) => {
        // Only markers inside the drawn range.
        //
        // The two ends need different guards. Past the end, findIndex finds
        // nothing and returns -1. Before the start it returns 0 — so every
        // signal older than the window would land on the first bar, and with
        // thirty of them that is a column of marks stacked on the left edge at
        // a time and a price the chart is not showing.
        if (marker.at < Date.parse(candles[0]!.open_time)) return null;

        const index = candles.findIndex((c) => Date.parse(c.open_time) >= marker.at);
        if (index < 0) return null;

        const x = index * slot + slot / 2;
        const tint =
          marker.direction === 'long' ? colors.direction.long : colors.direction.short;

        return (
          <Rect
            key={`${marker.kind}:${marker.at}`}
            x={x - 3}
            y={y(marker.price) - 3}
            width={6}
            height={6}
            fill={marker.kind === 'entry' ? tint : 'none'}
            stroke={tint}
            strokeWidth={1.5}
          />
        );
      })}
    </Svg>
  );
}

/** Entry and exit markers for a set of signals and their outcomes. */
export function markersFor(signals: Signal[], outcomes: Outcome[]): Marker[] {
  const byId = new Map(outcomes.map((o) => [o.signal_id, o]));
  const markers: Marker[] = [];

  for (const signal of signals) {
    const price = signal.entry_price ?? signal.signal_price;
    if (price !== null) {
      markers.push({
        at: Date.parse(signal.signal_time),
        price: Number(price),
        direction: signal.direction,
        kind: 'entry',
      });
    }

    const outcome = byId.get(signal.id);
    if (outcome?.resolved_at && outcome.resolved_price !== null) {
      markers.push({
        at: Date.parse(outcome.resolved_at),
        price: Number(outcome.resolved_price),
        direction: signal.direction,
        kind: 'exit',
      });
    }
  }
  return markers;
}
