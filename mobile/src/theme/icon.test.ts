import { readFileSync, statSync } from 'node:fs';
import path from 'node:path';

import { bg, gold, jade } from './colors';

const root = path.join(__dirname, '..', '..');
const record = JSON.parse(
  readFileSync(path.join(root, 'assets', 'icon.tokens.json'), 'utf8'),
) as {
  colors: { ground: string; jade: string; jadeDim: string; gold: string };
  geometry: { size: number; outer: number; inner: number; goldWidth: number };
};

/*
TestTheIconIsDrawnFromTheTokens.

# What this prevents

The icon is a PNG, which is the one artefact in this app where a colour can
hide. The lint rule that keeps literals inside src/theme/ reads source; it
cannot read pixels. So `tools/draw-icon.mjs` reads the tokens rather than
carrying them, and records what it used — and this compares that record against
what the app imports.

The failure it catches is a token changed without the icon redrawn: the app
would launch in one palette from an icon painted in another, on the same phone,
and nothing would say so. It is small and permanent, which is the worst
combination for something nobody will think to check.

If this fails, the fix is `npm run icon`.
*/
describe('the icon', () => {
  it('uses bg.base as its ground', () => {
    expect(record.colors.ground).toBe(bg.base);
  });

  it('uses the jade tokens for the mark', () => {
    expect(record.colors.jade).toBe(jade.base);
    expect(record.colors.jadeDim).toBe(jade.dim);
  });

  it('uses gold.base and nothing else gold', () => {
    expect(record.colors.gold).toBe(gold.base);
    expect(Object.values(record.colors).filter((c) => c === gold.base)).toHaveLength(1);
  });

  it('paints nothing outside the palette', () => {
    const palette = [bg.base, jade.base, jade.dim, gold.base];
    for (const value of Object.values(record.colors)) {
      expect(palette).toContain(value);
    }
  });
});

/*
TestTheGoldIsALineRatherThanAFill.

The same budget the screens are held to: gold is an accent, never a surface.
Here it is the rim on the disc's opening — a stroke of 16 units on a 1024
canvas, which is about 2% of the icon's area.

Written as a proportion rather than a look, so that redrawing the mark cannot
quietly turn the accent into a fill.
*/
describe('the gold in the icon', () => {
  it('is a thin stroke, not a filled area', () => {
    const { size, inner, goldWidth } = record.geometry;

    // The area of an annulus of radius `inner` and width `goldWidth`.
    const goldArea = 2 * Math.PI * inner * goldWidth;
    const share = goldArea / (size * size);

    expect(share).toBeLessThan(0.05);
    // And it is genuinely a line: thin relative to the canvas.
    expect(goldWidth / size).toBeLessThan(0.02);
  });

  it('sits on the opening rather than around the outside', () => {
    // An earlier draft put the gold ring outside the jade, which made the
    // brightest thing in the icon its frame rather than its subject.
    expect(record.geometry.inner).toBeLessThan(record.geometry.outer);
  });
});

/*
TestTheIconSurvivesASmallScreen.

It appears on a home screen at 60pt far more often than anyone looks at it
large. The mark has to be a shape that still says something at that size, which
means the band and the opening both need real width — a ring whose band is a
pixel and a half is a smudge.

The numbers below are the mark's proportions carried down to 60pt, which is the
smallest place iOS shows it.
*/
describe('at 60pt', () => {
  const { size, outer, inner } = record.geometry;
  const scale = 60 / size;

  it('has a jade band wide enough to read', () => {
    const band = (outer - inner) * scale;
    expect(band).toBeGreaterThan(5);
  });

  it('has an opening wide enough to read as an opening', () => {
    const opening = inner * 2 * scale;
    expect(opening).toBeGreaterThan(15);
  });

  it('leaves ground around the mark rather than filling the tile', () => {
    // iOS masks the icon to a squircle; a mark reaching the edge is clipped at
    // the corners and looks cramped everywhere else.
    expect((outer * 2) / size).toBeLessThan(0.75);
  });
});

/*
TestEverySizeAnInstallNeedsWasDrawn.

The manifest names two and iOS takes a third. A missing one is not an error
anywhere — iOS invents an icon from a screenshot of the page, which is the
worst possible fallback and looks like a bug in the app.
*/
describe('the generated sizes', () => {
  it.each([
    ['apple-touch-icon.png', 180],
    ['icon-192.png', 192],
    ['icon-512.png', 512],
  ])('%s exists and is not empty', (file) => {
    const stats = statSync(path.join(root, 'public', file));
    expect(stats.size).toBeGreaterThan(0);
  });

  it('was drawn from a source big enough for the largest of them', () => {
    const stats = statSync(path.join(root, 'assets', 'icon.png'));
    expect(stats.size).toBeGreaterThan(0);
    expect(record.geometry.size).toBeGreaterThanOrEqual(512);
  });
});
