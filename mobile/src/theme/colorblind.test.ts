import { direction, semantic, colors } from './colors';
import {
  deltaE,
  deltaEAs,
  parseHex,
  simulate,
  toHex,
  Deficiency,
  MIN_DIRECTION_DELTA_E,
} from './colorblind';

const DEFICIENCIES: Deficiency[] = ['protanopia', 'deuteranopia', 'tritanopia'];

/**
 * The simulation is only worth trusting if it behaves like the thing it
 * models, so it is checked against facts about dichromacy that hold
 * independently of these particular colours.
 */
describe('the dichromat simulation', () => {
  it('leaves greys alone', () => {
    // A dichromat sees no colour to confuse in a neutral, so the transform
    // must be close to the identity there. A matrix transcribed wrongly
    // almost always fails this.
    for (const grey of ['#000000', '#808080', '#ffffff']) {
      for (const deficiency of DEFICIENCIES) {
        expect(deltaE(grey, toHex(simulate(grey, deficiency)))).toBeLessThan(2);
      }
    }
  });

  it('collapses red against green for protanopes and deuteranopes', () => {
    // The defining property. Pure red and pure green are unmistakable in
    // normal vision and close to indistinguishable in red-green deficiency.
    const normal = deltaE('#ff0000', '#00ff00');
    expect(normal).toBeGreaterThan(100);

    for (const deficiency of ['protanopia', 'deuteranopia'] as const) {
      expect(deltaEAs('#ff0000', '#00ff00', deficiency)).toBeLessThan(normal / 2);
    }
  });

  it('leaves blue against yellow alone for red-green deficiencies', () => {
    // The counterpart: a protanope or deuteranope has no trouble with the
    // blue-yellow axis, so a simulation that flattened everything would fail
    // here and every other assertion in this file would be meaningless.
    for (const deficiency of ['protanopia', 'deuteranopia'] as const) {
      expect(deltaEAs('#0000ff', '#ffff00', deficiency)).toBeGreaterThan(50);
    }
  });

  it('produces a colour for every input it is given', () => {
    for (const deficiency of DEFICIENCIES) {
      const { r, g, b } = simulate(colors.jade.base, deficiency);
      for (const channel of [r, g, b]) {
        expect(Number.isInteger(channel)).toBe(true);
        expect(channel).toBeGreaterThanOrEqual(0);
        expect(channel).toBeLessThanOrEqual(255);
      }
    }
  });

  it('parses both three and six digit hex', () => {
    expect(parseHex('#fff')).toEqual({ r: 255, g: 255, b: 255 });
    expect(parseHex('#0D1512')).toEqual({ r: 13, g: 21, b: 18 });
    expect(() => parseHex('#nope')).toThrow();
  });
});

/**
 * TestLongAndShortStayApart.
 *
 * # What this prevents
 *
 * Direction is the one piece of information in this app that is carried by
 * colour alone. If long and short converged for a reader with a colour vision
 * deficiency, every signal, every marker on the chart and every row in the
 * list would become ambiguous — and it would be ambiguous silently, because
 * nothing on the screen would look wrong.
 *
 * This is why short is terracotta rather than red: the two differ in warmth as
 * well as hue, and warmth is the axis that survives red-green deficiency.
 */
describe('long and short are distinguishable', () => {
  it('are far apart in normal vision', () => {
    expect(deltaE(direction.long, direction.short)).toBeGreaterThan(
      MIN_DIRECTION_DELTA_E * 2,
    );
  });

  it.each(DEFICIENCIES)('stay apart under %s', (deficiency) => {
    const distance = deltaEAs(direction.long, direction.short, deficiency);

    expect(distance).toBeGreaterThan(MIN_DIRECTION_DELTA_E);
  });

  /**
   * The check has to be capable of failing, and it has to fail on designs
   * somebody would plausibly ship. Two of them.
   */
  it('would have failed on an ordinary green and red', () => {
    // A mid green against a mid red — the conventional trading palette, and
    // the pair a designer reaches for without thinking. ΔE 7.9 under
    // deuteranopia: not "harder to tell apart", effectively the same colour.
    expect(deltaEAs('#4caf50', '#c0504d', 'deuteranopia')).toBeLessThan(
      MIN_DIRECTION_DELTA_E,
    );
  });

  it('would have failed on a terracotta a shade off the one chosen', () => {
    // The more useful control, because it is inside the design space this
    // palette was actually chosen from. A terracotta only slightly duller and
    // darker than `short` collapses against jade for protanopes — ΔE 10.7
    // against the 17.8 the shipped pair manages.
    //
    // The margin is not large, which is the point of measuring rather than
    // eyeballing: the chosen colour is not obviously safer to look at.
    const nearMiss = deltaEAs(direction.long, '#a86a5a', 'protanopia');
    const chosen = deltaEAs(direction.long, direction.short, 'protanopia');

    expect(nearMiss).toBeLessThan(MIN_DIRECTION_DELTA_E);
    expect(chosen).toBeGreaterThan(MIN_DIRECTION_DELTA_E);
  });
});

/**
 * The semantic colours carry meaning too — ok, warn and error appear on the
 * status screen when something is wrong, which is the worst moment for two of
 * them to look alike.
 */
describe('the semantic states are distinguishable', () => {
  const pairs: [string, string][] = [
    [semantic.ok, semantic.warn],
    [semantic.ok, semantic.error],
    [semantic.warn, semantic.error],
  ];

  it.each(DEFICIENCIES)('stay apart under %s', (deficiency) => {
    for (const [a, b] of pairs) {
      expect(deltaEAs(a, b, deficiency)).toBeGreaterThan(MIN_DIRECTION_DELTA_E);
    }
  });
});

/**
 * Text has to stay readable on the surface it sits on, for everyone. Contrast
 * is not a colour-vision question — a dichromat sees lightness normally — but
 * it is checked here because it is the other way a palette fails silently.
 */
describe('text is readable on every surface', () => {
  function contrastRatio(a: string, b: string): number {
    const luminance = (hex: string) => {
      const { r, g, b: blue } = parseHex(hex);
      const channel = (c: number) => {
        const v = c / 255;
        return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
      };
      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(blue);
    };
    const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
    return (light + 0.05) / (dark + 0.05);
  }

  it('clears WCAG AA for body text on every background', () => {
    for (const surface of Object.values(colors.bg)) {
      expect(contrastRatio(colors.text.primary, surface)).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(colors.text.secondary, surface)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it('clears WCAG AA large for tertiary text, which is only ever a caption', () => {
    for (const surface of Object.values(colors.bg)) {
      expect(contrastRatio(colors.text.tertiary, surface)).toBeGreaterThanOrEqual(3);
    }
  });
});
