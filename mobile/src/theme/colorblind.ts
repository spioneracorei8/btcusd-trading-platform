/**
 * Colour-vision simulation and perceptual distance.
 *
 * # Why this is code rather than a screenshot through a simulator
 *
 * Long and short must stay distinguishable to a reader with any of the common
 * colour vision deficiencies. Checking that by eye through a simulator is a
 * one-off: it passes on the day somebody looks, and says nothing on the day
 * the palette is adjusted. Expressed as a transform and a distance threshold
 * it is a test, and it keeps holding.
 *
 * The maths is the Viénot–Brettel–Mollon dichromat simulation (1999), which is
 * the standard linear approximation, and CIE76 ΔE for the distance. ΔE is
 * approximate — it overstates differences in some regions — but it is the
 * measure the threshold below was chosen against, and being consistent matters
 * more here than being exact.
 */

/** sRGB, 0-255 per channel. */
export type RGB = { r: number; g: number; b: number };

/** The three dichromacies. Together they cover roughly 8% of men. */
export type Deficiency = 'protanopia' | 'deuteranopia' | 'tritanopia';

export function parseHex(hex: string): RGB {
  const value = hex.replace('#', '');
  const full =
    value.length === 3
      ? value
          .split('')
          .map((c) => c + c)
          .join('')
      : value;

  if (!/^[0-9a-fA-F]{6}$/.test(full)) {
    throw new Error(`not a colour: ${hex}`);
  }
  return {
    r: parseInt(full.slice(0, 2), 16),
    g: parseInt(full.slice(2, 4), 16),
    b: parseInt(full.slice(4, 6), 16),
  };
}

/** sRGB companding, 0-255 to linear 0-1. */
function toLinear(channel: number): number {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

function fromLinear(channel: number): number {
  const c = Math.max(0, Math.min(1, channel));
  const companded = c <= 0.0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - 0.055;
  return Math.round(companded * 255);
}

/**
 * Dichromat simulation matrices, applied in linear RGB.
 *
 * These are the widely used Viénot–Brettel–Mollon coefficients. They are an
 * approximation of a perception nobody with normal vision can check directly,
 * which is the point: the alternative is guessing.
 */
type Matrix = readonly [
  readonly [number, number, number],
  readonly [number, number, number],
  readonly [number, number, number],
];

const MATRICES: Record<Deficiency, Matrix> = {
  protanopia: [
    [0.170556992, 0.829443014, 0],
    [0.170556991, 0.829443008, 0],
    [-0.004517144, 0.004517144, 1],
  ],
  deuteranopia: [
    [0.33066007, 0.66933993, 0],
    [0.33066007, 0.66933993, 0],
    [-0.02785538, 0.02785538, 1],
  ],
  tritanopia: [
    [1, 0.1273989, -0.1273989],
    [0, 0.8739093, 0.1260907],
    [0, 0.8739093, 0.1260907],
  ],
};

/** Simulates how a colour appears to a dichromat. */
export function simulate(hex: string, deficiency: Deficiency): RGB {
  const { r, g, b } = parseHex(hex);
  const [lr, lg, lb] = [toLinear(r), toLinear(g), toLinear(b)];
  const m = MATRICES[deficiency];

  return {
    r: fromLinear(m[0][0] * lr + m[0][1] * lg + m[0][2] * lb),
    g: fromLinear(m[1][0] * lr + m[1][1] * lg + m[1][2] * lb),
    b: fromLinear(m[2][0] * lr + m[2][1] * lg + m[2][2] * lb),
  };
}

/** CIE XYZ, D65. */
function toXYZ({ r, g, b }: RGB): [number, number, number] {
  const [lr, lg, lb] = [toLinear(r), toLinear(g), toLinear(b)];
  return [
    lr * 0.4124564 + lg * 0.3575761 + lb * 0.1804375,
    lr * 0.2126729 + lg * 0.7151522 + lb * 0.072175,
    lr * 0.0193339 + lg * 0.119192 + lb * 0.9503041,
  ];
}

/** CIE L*a*b*, D65 white point. */
export function toLab(color: RGB): [number, number, number] {
  const white: [number, number, number] = [0.95047, 1.0, 1.08883];
  const f = (t: number) => (t > 216 / 24389 ? Math.cbrt(t) : ((24389 / 27) * t) / 116 + 16 / 116);

  const [X, Y, Z] = toXYZ(color);
  const [x, y, z] = [f(X / white[0]), f(Y / white[1]), f(Z / white[2])];
  return [116 * y - 16, 500 * (x - y), 200 * (y - z)];
}

/**
 * CIE76 ΔE between two colours.
 *
 * Rough rule of thumb: under 1 is imperceptible, 2-3 is noticeable on close
 * inspection, over 10 is unmistakably a different colour.
 */
export function deltaE(a: string, b: string): number {
  const [l1, a1, b1] = toLab(parseHex(a));
  const [l2, a2, b2] = toLab(parseHex(b));
  return Math.sqrt((l1 - l2) ** 2 + (a1 - a2) ** 2 + (b1 - b2) ** 2);
}

/** ΔE between two colours as a given dichromat sees them. */
export function deltaEAs(a: string, b: string, deficiency: Deficiency): number {
  return deltaE(toHex(simulate(a, deficiency)), toHex(simulate(b, deficiency)));
}

export function toHex({ r, g, b }: RGB): string {
  const pair = (n: number) => n.toString(16).padStart(2, '0');
  return `#${pair(r)}${pair(g)}${pair(b)}`;
}

/**
 * The threshold long and short must clear under every simulated deficiency.
 *
 * 15 is well above "noticeable on close inspection" and below the ~40 the two
 * colours manage in normal vision. It is set where it is because these are
 * small marks on a dark ground read at a glance, not large adjacent swatches
 * compared deliberately — the situation ΔE flatters least.
 */
export const MIN_DIRECTION_DELTA_E = 15;
