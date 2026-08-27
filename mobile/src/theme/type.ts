import { Platform, TextStyle } from 'react-native';

/**
 * Typography.
 *
 * One typeface, three weights. A five-screen instrument does not ship a
 * display face.
 */

/**
 * Tabular figures, on every number in the app.
 *
 * Non-negotiable. Proportional digits change width as they change value, so a
 * live price jitters horizontally while it updates and a column of returns
 * fails to line up. `fontVariant` is the portable way to ask for it; on
 * Android it needs the font feature settings too, which React Native exposes
 * through the same property on recent versions.
 */
export const tabular: TextStyle = {
  fontVariant: ['tabular-nums'],
};

/**
 * The scale. 28 / 20 / 16 / 14 / 12, and nothing between.
 *
 * `sampleSize` is 14 rather than 12 and that is the single most important
 * typographic rule here. A sample size is a qualifier on the number beside it
 * — "win rate 62%" and "over 8 trades" are one statement — and a qualifier set
 * in caption size loses the argument to the figure it qualifies. Phase 08
 * found the same thing about the insufficient-sample banner: put beside a
 * number it loses, put above it it wins.
 */
export const size = {
  price: 28,
  heading: 20,
  body: 16,
  detail: 14,
  /** Not `caption`. See above. */
  sampleSize: 14,
  caption: 12,
} as const;

export const weight = {
  regular: '400',
  medium: '500',
  semibold: '600',
} as const satisfies Record<string, TextStyle['fontWeight']>;

/** The system font, named explicitly so the two platforms do not diverge. */
export const family = Platform.select({
  android: 'sans-serif',
  ios: 'System',
  default: 'system-ui',
});

export const type = { tabular, size, weight, family } as const;
