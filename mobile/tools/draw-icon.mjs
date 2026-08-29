/**
 * Draws the app icon from the theme tokens.
 *
 *   npm run icon        # this, then the resize
 *
 * # What it draws, and why
 *
 * A jade bi disc: a flat ring with an open centre, which is the oldest and
 * plainest jade object there is. It is geometric, it is calm, and it is not a
 * candlestick — a chart rendered at 60 pixels is a smudge, and every trading
 * app has one.
 *
 * The proportions were chosen at 60px and checked upwards, not the other way
 * round. This sits on a home screen at small sizes far more often than anybody
 * looks at it large, and a ring is among the most robust shapes there is when
 * there are only a few dozen pixels to say it in.
 *
 * # The gold
 *
 * A rim on the opening, and nothing else: about 2% of the icon's area, no fill.
 * At 180px it reads as a fine warm line; by 60px it has faded to almost
 * nothing, which is what an accent should do rather than being the first thing
 * the eye lands on. An earlier version put the gold ring *outside* the jade and
 * that is exactly what went wrong — the brightest thing in the icon was the
 * frame rather than the subject.
 *
 * # Where the colours come from
 *
 * src/theme/colors.ts, through tools/theme.mjs. A PNG is the one artefact in
 * this app where a colour literal could hide from the lint rule, so it is not
 * typed here — and the values used are written to assets/icon.tokens.json,
 * which src/theme/icon.test.ts compares against the tokens. Change a token
 * without redrawing and that test says so.
 */
import { chromium } from 'playwright';
import { writeFile } from 'node:fs/promises';
import path from 'node:path';

import { token } from './theme.mjs';

const OUT = 'assets';

/**
 * The mark, in viewBox units of a 1024 square.
 *
 * outer/inner are the jade band's edges; goldWidth is the rim on the opening.
 * Nothing here is arbitrary — see the note above about choosing at 60px.
 */
const GEOMETRY = { size: 1024, outer: 344, inner: 224, goldWidth: 16 };

const colors = {
  ground: token('bg', 'base'),
  jade: token('jade', 'base'),
  jadeDim: token('jade', 'dim'),
  gold: token('gold', 'base'),
};

/**
 * The icon at any size.
 *
 * The jade runs from its lighter tone at the top to its dimmer one at the
 * bottom — the brief's "soft light", which is light from above. It is subtle
 * enough to disappear by 60px, which is the right way for it to fail.
 */
function svg(size) {
  const { outer, inner, goldWidth } = GEOMETRY;
  const radius = (outer + inner) / 2;
  const band = outer - inner;

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}"
       viewBox="0 0 ${GEOMETRY.size} ${GEOMETRY.size}">
    <defs>
      <linearGradient id="jade" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stop-color="${colors.jade}"/>
        <stop offset="1" stop-color="${colors.jadeDim}"/>
      </linearGradient>
    </defs>

    <!-- Full bleed: iOS masks this into a squircle itself, and a margin drawn
         here would show up as a dark border inside the mask. -->
    <rect width="${GEOMETRY.size}" height="${GEOMETRY.size}" fill="${colors.ground}"/>

    <circle cx="512" cy="512" r="${radius}" fill="none"
            stroke="url(#jade)" stroke-width="${band}"/>
    <circle cx="512" cy="512" r="${inner}" fill="none"
            stroke="${colors.gold}" stroke-width="${goldWidth}"/>
  </svg>`;
}

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH ?? '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
});
const page = await browser.newPage();

// icon.png is the source the resize pipeline works from; favicon.png is what
// the web export turns into favicon.ico.
for (const [file, size] of [
  ['icon.png', 1024],
  ['favicon.png', 196],
]) {
  await page.setViewportSize({ width: size, height: size });
  await page.setContent(
    `<style>html,body{margin:0;padding:0;background:${colors.ground}}svg{display:block}</style>` +
      svg(size),
  );
  const out = path.join(OUT, file);
  await page.screenshot({ path: out, omitBackground: false });
  console.log(`${out} ${size}×${size}`);
}

await browser.close();

// The record src/theme/icon.test.ts checks against, so a token changed without
// a redraw is a failing test rather than an icon that quietly does not match
// the app it launches.
const record = path.join(OUT, 'icon.tokens.json');
await writeFile(record, JSON.stringify({ colors, geometry: GEOMETRY }, null, 2) + '\n');
console.log(`${record}`);
