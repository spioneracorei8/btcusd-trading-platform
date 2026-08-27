/**
 * The palette. Every colour in this app is defined here and nowhere else,
 * which the lint rule in eslint.config.js enforces by failing on a hex literal
 * outside this directory.
 *
 * # Why the restraint is functional
 *
 * This is a screen someone checks at night, half awake, to see whether
 * anything happened. It is also a screen that must not make a thin edge look
 * like a strong one — the system's honest output is usually silence and
 * occasionally a marginal signal, and there is nothing here to celebrate.
 *
 * Gold is the trap. At full saturation it reads as celebration, so it is used
 * as accent and structure and never as fill: a hairline, a small marker, an
 * active tab indicator. See `budget` at the foot of this file, which the
 * screenshot audit checks against.
 */

/** Surfaces. Deep desaturated teal-black, never pure black — pure black makes
 * gold vibrate against it and is harsh in a dark room. */
export const bg = {
  /** The page. Near-black with a green cast. */
  base: '#0D1512',
  /** Cards and sheets. */
  raised: '#131E1A',
  /** Modals and menus. */
  overlay: '#18251F',
} as const;

export const border = {
  /** Hairlines between sections. Used only where a real edge exists —
   * separation is by background step everywhere else. */
  subtle: '#1F2E27',
  /** The gold hairline. Dim, not metallic. */
  gold: '#4A4032',
} as const;

/** Jade. The primary, closer to celadon than emerald. */
export const jade = {
  dim: '#3E6B5A',
  /** Primary actions and active states. */
  base: '#5A9179',
  /** Reserved for the single most important element on a screen. */
  bright: '#7DB89C',
} as const;

/** Gold. Accent only, aged and dusty, never yellow. */
export const gold = {
  dim: '#8A7A52',
  /** Accents, markers, active indicators. */
  base: '#B39B63',
  /** At most once per screen, if at all. */
  bright: '#D4BC85',
} as const;

/** Text. Warm off-white, never #FFFFFF — pure white on dark is the commonest
 * cause of eye strain in night use. */
export const text = {
  primary: '#E8E4D9',
  secondary: '#A8A294',
  /**
   * Captions and timestamps. Note that sample sizes are NOT tertiary text:
   * see `type.sampleSize` for why.
   *
   * Lightened from the #6E6B61 the phase brief specified. That value renders
   * at contrast 3.48/3.21/2.98 against the three surfaces, and tertiary text
   * is 12pt — below WCAG's "large" threshold, so the bar is 4.5 and it missed
   * on all three. A timestamp is not decoration: "how long ago" is the whole
   * content of that line. Same hue, one step lighter, 4.54 at worst.
   */
  tertiary: '#8D897C',
} as const;

/**
 * Direction.
 *
 * Not red and green. Green is the interface colour throughout, so a green
 * long would be indistinguishable from ordinary chrome — the eye would read
 * it as a UI element rather than as a fact about the trade.
 *
 * Terracotta rather than red for short: it belongs to the same warm-earth
 * family as the gold, and it does not read as an error state. A losing trade
 * is not a fault.
 *
 * The two differ in warmth as well as hue, which is what keeps them apart
 * under the common forms of colour blindness. That is checked rather than
 * assumed — see colorblind.test.ts.
 */
export const direction = {
  long: '#5A9179',
  short: '#C67B5C',
} as const;

/**
 * Semantic state. Deliberately distinct from `direction`, so a losing trade
 * never looks like a system fault and a broken collector never looks like a
 * bad trade.
 */
export const semantic = {
  ok: '#5A9179',
  warn: '#B39B63',

  /**
   * Lightened from the #B85C4A the phase brief specified.
   *
   * That value and `warn` are both warm and both mid-lightness, and under
   * deuteranopia they converge: ΔE 11.8, against the 15 this palette holds
   * itself to. The status screen when unhealthy is exactly where those two
   * must not look alike — a warning to read later and a fault to act on now.
   *
   * Hue cannot fix it, because red-green deficiency is what flattens the hue
   * axis these two differ on. Lightness can: this sits at L* 75 against warn's
   * L* 65, which survives every dichromacy, and clears 17 against both `warn`
   * and `ok` under all three.
   *
   * It is lighter and higher-contrast than the rest of the palette, and that
   * is the one place §E5 sanctions leaving the muted register: "the status
   * screen when unhealthy drops the aesthetic entirely — error, high contrast,
   * no ornament. Someone reading it is troubleshooting."
   *
   * Severity is never carried by colour alone regardless. Every status row is
   * labelled in words, because a palette check is a floor and not a licence.
   */
  error: '#ECA89A',
} as const;

/**
 * The gold budget, in points of area, checked by the screenshot audit.
 *
 * Gold is an accent. Anything larger than roughly a 24pt square is a fill,
 * and a fill is what turns an instrument into a celebration.
 */
export const budget = {
  maxGoldAreaPt: 24 * 24,
} as const;

export const colors = {
  bg,
  border,
  jade,
  gold,
  text,
  direction,
  semantic,
} as const;
