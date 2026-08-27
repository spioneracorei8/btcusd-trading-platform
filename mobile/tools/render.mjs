/**
 * Renders the app in a browser and screenshots each screen.
 *
 * # Why this exists
 *
 * The phase brief asks for every screen to be looked at in a dark room, for
 * the gold area to be measured, and for the palette to be checked. None of
 * that can be done from source, and this environment has no Android device to
 * run the real app on.
 *
 * react-native-web renders the same components through the same styles, so
 * what comes out is not the APK but it is the layout, the palette and the
 * type scale — which is what those checks are about. The device pass is
 * written up in docs/mobile.md for a phone to do.
 */
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';

const URL_BASE = process.env.APP_URL ?? 'http://127.0.0.1:8081';
const OUT = process.env.OUT ?? 'screenshots';

const SCREENS = [
  { name: 'dashboard', tab: 'Now' },
  { name: 'signals', tab: 'Signals' },
  { name: 'chart', tab: 'Chart' },
  { name: 'performance', tab: 'Performance' },
  { name: 'status', tab: 'Status' },
];

await mkdir(OUT, { recursive: true });

// The pinned browser in this environment, rather than whichever build this
// Playwright version would download. PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD is set
// here, so asking for the default one fails.
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH ?? '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
  // This environment sets HTTPS_PROXY, and Chromium picks it up — which sends
  // a request for 127.0.0.1 to an agent proxy that refuses it, and the app
  // then correctly reports the server as unreachable. Nothing in the app is
  // wrong; the browser is being told to route localhost through a proxy.
});
const page = await browser.newPage({
  // A phone, so the layout under test is the one that ships.
  viewport: { width: 412, height: 915 },
  deviceScaleFactor: 2,
  colorScheme: 'dark',
});

page.on('console', (message) => {
  if (message.type() === 'error') console.error('page error:', message.text());
});

await page.goto(URL_BASE, { waitUntil: 'networkidle', timeout: 120_000 });
// Metro compiles on first request; the bundle can take a while to evaluate.
await page.waitForTimeout(4000);

for (const { name, tab } of SCREENS) {
  const control = page.getByText(tab, { exact: true }).last();
  if (await control.count()) {
    await control.click();
    await page.waitForTimeout(1200);
  }
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
  console.log(`wrote ${OUT}/${name}.png`);
}

await browser.close();
