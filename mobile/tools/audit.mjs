/**
 * The §E6 checks, run against the rendered app rather than against source.
 *
 * # Why these are measured and not looked at
 *
 * "Screenshot every screen at full brightness in a dark room. If any element
 * is the first thing the eye lands on and it isn't the most important element,
 * it's too bright." That is a real instruction and it cannot be automated.
 * What can be automated is the part it rests on: how much gold is on screen,
 * what the brightest thing is, and whether any of it is a fill rather than an
 * accent.
 *
 * So this walks the rendered DOM, measures every painted element, and reports.
 * The screenshots are still written for the eye; this is what makes the
 * numbers behind them checkable.
 */
import { chromium } from 'playwright';
import { mkdir, writeFile } from 'node:fs/promises';

import { group, luminance as luminanceOf, token } from './theme.mjs';

const URL_BASE = process.env.APP_URL ?? 'http://127.0.0.1:8081';
const OUT = process.env.OUT ?? 'screenshots';

const SCREENS = ['Now', 'Signals', 'Chart', 'Performance', 'Status'];

/**
 * The gold tokens, read from src/theme/colors.ts rather than copied.
 *
 * A copy would be the quiet failure this tool is supposed to catch: change a
 * gold token, and an audit matching against the old list stops recognising
 * gold at all — so the area cap passes on every screen and reports success.
 */
const GOLD = [
  ...Object.values(group('gold')),
  // The gold hairline lives with the borders and is gold for this purpose.
  token('border', 'gold'),
].map((hex) => hex.toLowerCase());

/** Anything larger than roughly a 24pt square is a fill, not an accent. */
const MAX_GOLD_AREA = 24 * 24;

/**
 * The brightest thing the palette allows: text.primary.
 *
 * "Easy on the eyes, never bright." Nothing painted should exceed it — a value
 * above this is either pure white, which the palette forbids outright, or a
 * colour that got in from outside src/theme.
 *
 * Computed from the token rather than written down, for the same reason as the
 * golds above: a ceiling that stopped matching the palette would raise or lower
 * itself silently.
 */
const MAX_LUMINANCE = Number(luminanceOf(token('text', 'primary')).toFixed(4));

await mkdir(OUT, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH ?? '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
});

const page = await browser.newPage({
  viewport: { width: 412, height: 915 },
  deviceScaleFactor: 2,
  colorScheme: 'dark',
});

await page.goto(URL_BASE, { waitUntil: 'networkidle', timeout: 120_000 });
await page.waitForTimeout(4000);

const findings = [];

for (const tab of SCREENS) {
  const control = page.getByText(tab, { exact: true }).last();
  if (await control.count()) {
    await control.click();
    await page.waitForTimeout(1200);
  }

  const measured = await page.evaluate(
    ({ golds, cap, maxLuminance }) => {
      const rgbToHex = (value) => {
        const match = value.match(/rgba?\(([^)]+)\)/);
        if (!match) return null;
        const [r, g, b, a = '1'] = match[1].split(',').map((v) => v.trim());
        if (Number(a) === 0) return null;
        return (
          '#' +
          [r, g, b]
            .map((v) => Number(v).toString(16).padStart(2, '0'))
            .join('')
        );
      };

      /** Relative luminance, for "what is the brightest thing here". */
      const luminance = (hex) => {
        const n = parseInt(hex.slice(1), 16);
        const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
          const v = c / 255;
          return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      };

      /**
       * Whether a node is actually on screen.
       *
       * React Navigation keeps inactive tabs mounted, so querying the whole
       * document measures five screens at once — and the audit then reports
       * the same "brightest element" for every tab, which is the one number
       * it exists to distinguish.
       */
      const visible = (node) => {
        for (let current = node; current; current = current.parentElement) {
          const style = getComputedStyle(current);
          if (style.display === 'none' || style.visibility === 'hidden') return false;
          if (Number(style.opacity) === 0) return false;
          if (current.getAttribute?.('aria-hidden') === 'true') return false;
        }
        return true;
      };

      const goldFills = [];
      const brightest = [];

      for (const node of document.querySelectorAll('*')) {
        const box = node.getBoundingClientRect();
        if (box.width === 0 || box.height === 0) continue;
        if (box.bottom < 0 || box.top > window.innerHeight) continue;
        if (!visible(node)) continue;

        const style = getComputedStyle(node);
        const background = rgbToHex(style.backgroundColor);
        const colour = rgbToHex(style.color);
        const border = rgbToHex(style.borderTopColor);

        // A gold FILL is a background or a border thick enough to read as one.
        // A hairline border and gold text are accents by construction.
        for (const [property, hex] of [
          ['background', background],
          ['border', border],
        ]) {
          if (!hex || !golds.includes(hex.toLowerCase())) continue;
          if (property === 'border' && parseFloat(style.borderTopWidth) <= 2) continue;

          const area = box.width * box.height;
          if (area > cap) {
            goldFills.push({
              property,
              hex,
              area: Math.round(area),
              tag: node.tagName,
              text: (node.textContent ?? '').trim().slice(0, 40),
            });
          }
        }

        // The brightest painted things, so a person can check that the
        // loudest element is the one that should be.
        const text = (node.textContent ?? '').trim();
        if (colour && text && node.children.length === 0) {
          brightest.push({
            hex: colour,
            luminance: Number(luminance(colour).toFixed(4)),
            size: Math.round(parseFloat(style.fontSize)),
            text: text.slice(0, 48),
          });
        }
      }

      brightest.sort((a, b) => b.luminance - a.luminance);
      return {
        goldFills,
        brightest: brightest.slice(0, 5),
        tooBright: brightest.filter((item) => item.luminance > maxLuminance),
      };
    },
    { golds: GOLD, cap: MAX_GOLD_AREA, maxLuminance: MAX_LUMINANCE },
  );

  findings.push({ screen: tab, ...measured });
  await page.screenshot({ path: `${OUT}/${tab.toLowerCase()}.png`, fullPage: false });
}

await browser.close();

await writeFile(`${OUT}/audit.json`, JSON.stringify(findings, null, 2));

let failed = false;
for (const { screen, goldFills, brightest, tooBright } of findings) {
  console.log(`\n${screen}`);
  if (goldFills.length > 0) {
    failed = true;
    for (const fill of goldFills) {
      console.log(
        `  GOLD FILL ${fill.hex} ${fill.area}pt² on <${fill.tag}> "${fill.text}" ` +
          `(cap ${MAX_GOLD_AREA})`,
      );
    }
  } else {
    console.log(`  gold: accent only, nothing over ${MAX_GOLD_AREA}pt²`);
  }

  if (tooBright.length > 0) {
    failed = true;
    for (const item of tooBright) {
      console.log(`  TOO BRIGHT ${item.hex} (${item.luminance}) "${item.text}"`);
    }
  } else {
    console.log(`  nothing brighter than text.primary (${MAX_LUMINANCE})`);
  }
  console.log('  brightest:');
  for (const item of brightest) {
    console.log(`    ${item.luminance} ${item.hex} ${item.size}pt "${item.text}"`);
  }
}

process.exit(failed ? 1 : 0);
