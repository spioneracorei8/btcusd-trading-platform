/**
 * Spacing, radius and motion.
 *
 * A 4pt scale, one radius, and short ease-out transitions. This is an
 * instrument rather than a lifestyle app: no large rounds, no spring, no
 * bounce.
 */

/** The 4pt scale. Nothing off it. */
export const space = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 32,
} as const;

export const screenPadding = space.lg;
export const cardPadding = space.md;

/** One radius, everywhere. */
export const radius = 8;

/** Hairline weight. Borders only where a real edge exists — separation is by
 * background step everywhere else. */
export const hairline = 1;

/**
 * Motion.
 *
 * `numbers` is zero deliberately. A price sliding between two values is
 * unreadable while it moves, and the animation implies a continuum the data
 * does not have: the price did not pass through the values in between, it was
 * one number and then another.
 */
export const motion = {
  fade: 150,
  transition: 200,
  numbers: 0,
  easing: 'ease-out',
} as const;

/**
 * The one permitted gradient: a very subtle radial behind the dashboard
 * header. Under 4% so it reads as depth rather than as decoration.
 *
 * No other surface gets one, and nothing gets a glow, a neon edge or a shadow
 * spread. The reference art is lit softly; a UI imitating it with bloom looks
 * like a game menu.
 */
export const headerWash = { maxOpacity: 0.04 } as const;

export const layout = {
  space,
  screenPadding,
  cardPadding,
  radius,
  hairline,
  motion,
  headerWash,
} as const;
