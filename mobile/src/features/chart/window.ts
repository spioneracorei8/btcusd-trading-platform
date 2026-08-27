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
 * The API's cap, mirrored here so `windowFor` cannot exceed it.
 *
 * The refusal itself lives in `ApiClient.candles`, which is the one place
 * every request passes through. Repeating it here would be a second
 * implementation of the same rule, and the two would drift.
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

  // How far the window reaches either side of `end`, in bars. One short of
  // the cap, because the bar at `end` itself has to fit under it too.
  const span = Math.min(barsVisible + 2 * OVERSCAN_BARS, MAX_BARS - 1);

  // Whatever the cap left, spent on the past first: dragging back is the
  // gesture, and bars in the future do not exist yet.
  const ahead = Math.min(OVERSCAN_BARS, Math.floor(span / 3));
  const behind = span - ahead;

  return {
    from: new Date(end.getTime() - behind * bar),
    to: new Date(end.getTime() + ahead * bar),
    // Both ends of the range are inclusive, so a window spanning `span` bars
    // holds one more than that. Asking for `span` comes back truncated by
    // exactly one, and the chart then reports an overflow that did not happen.
    limit: span + 1,
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
