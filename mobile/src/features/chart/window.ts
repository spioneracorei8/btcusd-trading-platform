import type { Timeframe } from '../../api/types';

/**
 * Which candles to hold, and when to ask for more.
 *
 * # The rule this exists to keep
 *
 * "Pan and zoom must not refetch per gesture." A chart that issues a request
 * on every frame of a drag is unusable over a tailnet and rude to a server
 * that answers by scanning an index.
 *
 * So the app holds a window wider than the screen and extends it at the edges.
 * The arithmetic is here rather than in the component because it is the part
 * worth testing: the component draws whatever this decides.
 */

export const MINUTES: Record<Timeframe, number> = {
  '1m': 1,
  '5m': 5,
  '15m': 15,
  '1h': 60,
  '4h': 240,
  '1d': 1440,
};

/** How many bars are on screen at once. */
export const VISIBLE_BARS = 60;

/**
 * How much either side of the visible range is held in memory.
 *
 * One screen's worth each way. Enough that an ordinary drag lands inside what
 * is already loaded; small enough that the first paint is not waiting on
 * several thousand bars.
 */
export const OVERSCAN_BARS = VISIBLE_BARS;

/**
 * The API's cap, mirrored here.
 *
 * The server refuses more than this and says so politely. The app refuses
 * first, because on a chart an oversized range is a gesture rather than a
 * typo — it happens while somebody is pinching — and a round trip that is
 * known to fail is worse than an axis that stops zooming out.
 */
export const MAX_BARS = 5000;

export type Window = { from: Date; to: Date; limit: number };

/**
 * The window to fetch for a view whose right edge is `end`.
 *
 * `end` is the newest bar on screen, so the window runs one screen of overscan
 * past it in each direction: back far enough to drag left without refetching,
 * forward far enough that bars arriving while the chart is open are already
 * inside the range that was asked for.
 */
export function windowFor(timeframe: Timeframe, end: Date, barsVisible = VISIBLE_BARS): Window {
  const bar = MINUTES[timeframe] * 60_000;
  const total = Math.min(barsVisible + 2 * OVERSCAN_BARS, MAX_BARS);

  // Whatever the cap left, spent on the past first: dragging back is the
  // gesture, and bars in the future do not exist yet.
  const ahead = Math.min(OVERSCAN_BARS, Math.floor(total / 3));
  const behind = total - ahead;

  return {
    from: new Date(end.getTime() - behind * bar),
    to: new Date(end.getTime() + ahead * bar),
    limit: total,
  };
}

/**
 * Whether the loaded window still covers what is being looked at.
 *
 * False is what triggers a refetch. It is deliberately generous: a drag that
 * stays inside the overscan must not refetch, or the overscan buys nothing.
 */
export function covers(loaded: Window | undefined, visibleFrom: Date, visibleTo: Date): boolean {
  if (!loaded) return false;
  return loaded.from.getTime() <= visibleFrom.getTime() && loaded.to.getTime() >= visibleTo.getTime();
}

/**
 * The largest range this timeframe may be asked for, in whole days.
 *
 * Three years of 1m candles is 1.5 million bars. The chart must refuse that
 * itself: the API caps the response, so the request would come back truncated
 * and the chart would silently draw the wrong range.
 */
export function maxSpanMs(timeframe: Timeframe): number {
  return MAX_BARS * MINUTES[timeframe] * 60_000;
}

/** True when the range asked for is more than this timeframe can answer. */
export function tooWide(timeframe: Timeframe, from: Date, to: Date): boolean {
  return to.getTime() - from.getTime() > maxSpanMs(timeframe);
}

/** What to tell somebody who asked for more than the API returns. */
export function tooWideMessage(timeframe: Timeframe, from: Date, to: Date): string {
  const bars = Math.round((to.getTime() - from.getTime()) / (MINUTES[timeframe] * 60_000));
  return (
    `${bars.toLocaleString('en-GB')} bars of ${timeframe} is more than the ` +
    `${MAX_BARS.toLocaleString('en-GB')} this API returns. Zoom in, or choose a longer timeframe.`
  );
}
