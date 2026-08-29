/**
 * Drives the built app in a browser and checks the four things that only work
 * end to end.
 *
 *   BASE=http://127.0.0.1:8099 OUT=dist node tools/pwa-check.mjs
 *
 * It builds first, so it can be run against a directory in any state.
 *
 * # Why this is not a unit test
 *
 * Each of these spans files that cannot see each other. The registration code
 * in src/pwa/ is tested against a fake registration, and public/sw.js is
 * tested by nothing — so the two halves can disagree and every test still
 * passes. They did: sw.js called skipWaiting() on install, which activates a
 * new build immediately and reloads the page underneath the reader, while
 * register.ts was built around a worker that waits for a person to say yes.
 * Both were internally consistent. Together they were wrong, and only a real
 * browser against a real export could say so.
 *
 * Exits non-zero on failure, so it can gate a release.
 */
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { writeFileSync, unlinkSync } from 'node:fs';

const BASE = process.env.BASE ?? 'http://127.0.0.1:8099';
const OUT = process.env.OUT ?? 'dist';

// Built here rather than assumed, because the update check below deliberately
// deploys a second build into the same directory and leaves it there. A run
// that started from the previous run's leftovers would produce the same build
// id twice, see no update, and fail — which is what happened, and which reads
// as a broken worker rather than a stale export.
console.log('building');
execFileSync('node', ['tools/build-web.mjs'], { env: { ...process.env, OUT }, stdio: 'pipe' });

const failures = [];
const check = (name, ok, detail = '') => {
  console.log(`  ${ok ? 'ok  ' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`);
  if (!ok) failures.push(name);
};

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH ?? '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
});
const context = await browser.newContext({ viewport: { width: 412, height: 915 } });
const page = await context.newPage();

// ---------------------------------------------------------------------------
console.log('\ninstall');
// ---------------------------------------------------------------------------
await page.goto(`${BASE}/`, { waitUntil: 'networkidle' });
await page.evaluate(() => navigator.serviceWorker.ready);
await page.waitForTimeout(1500);

const cached = await page.evaluate(async () => {
  const names = await caches.keys();
  const cache = await caches.open(names[0]);
  return { names, urls: (await cache.keys()).map((r) => new URL(r.url).pathname) };
});
check('a worker is registered and controlling', cached.names.length === 1, cached.names[0]);
check('the shell is precached', cached.urls.includes('/'));

// The rule from phase 09 part D, one layer down: a cached price rendered as
// current is worse than no price, because no price is visibly no price.
check(
  'no API response was cached',
  !cached.urls.some((u) => u.startsWith('/api/')),
  cached.urls.filter((u) => u.startsWith('/api/')).join(', '),
);
check(
  'nothing announces an update on a first install',
  (await page.getByText(/newer version/).count()) === 0,
);

// ---------------------------------------------------------------------------
console.log('\nrouting');
// ---------------------------------------------------------------------------
// The path a notification click opens, cold, with the app not running.
const signals = await (await fetch(`${BASE}/api/v1/signals?limit=1`)).json();
const id = signals.signals?.[0]?.id;
if (id) {
  await page.goto(`${BASE}/signals/${id}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2500);
  const body = await page.locator('body').innerText();
  check('a signal URL cold-loads that signal', /SHORT|LONG/.test(body), body.split('\n')[1]);
  check('the tab says which screen', (await page.title()).includes('SignalDetail'));
} else {
  check('a signal URL cold-loads that signal', false, 'no signal in the database to try');
}

// ---------------------------------------------------------------------------
console.log('\noffline');
// ---------------------------------------------------------------------------
await context.setOffline(true);

// A path the page is not already on, and a recorded navigation result.
//
// Navigating to the current URL and then reading the DOM proves nothing: a
// failed navigation leaves the previous page up, and the previous page is the
// app, already saying "cannot reach the server" because the API is offline
// too. That read as a pass with the shell fallback deleted.
let navigated = true;
await page
  .goto(`${BASE}/status`, { waitUntil: 'domcontentloaded' })
  .catch(() => {
    navigated = false;
  });
check('a cold navigation succeeds with no network at all', navigated);

await page.waitForTimeout(3000);
const offline = navigated ? await page.locator('body').innerText() : '';
check('the shell renders', /Cannot reach the server/i.test(offline));
check('it names Tailscale rather than showing a browser error', /tailscale/i.test(offline));
await context.setOffline(false);

// ---------------------------------------------------------------------------
console.log('\nupdate');
// ---------------------------------------------------------------------------
await page.goto(`${BASE}/`, { waitUntil: 'networkidle' });
await page.waitForTimeout(1000);
const before = (await page.evaluate(async () => await caches.keys()))[0];

// A new deployment. Adding a file changes the emitted list, which is what a
// code change does from the worker's point of view: a different build id, so a
// different cache.
writeFileSync('public/deploy-marker.txt', 'a new build');
try {
  execFileSync('node', ['tools/build-web.mjs'], { env: { ...process.env, OUT }, stdio: 'pipe' });
} finally {
  unlinkSync('public/deploy-marker.txt');
}

await page.evaluate(async () => {
  const registration = await navigator.serviceWorker.getRegistration();
  await registration.update();
});
await page.waitForTimeout(2500);

const banner = page.getByText(/newer version of this app is installed/);
check('a new build is announced rather than applied silently', (await banner.count()) > 0);

if ((await banner.count()) > 0) {
  await banner.click();
  await page.waitForTimeout(3500);

  const after = await page.evaluate(async () => await caches.keys());
  check('taking the update reloads onto the new build', !after.includes(before), after.join(', '));
  check('the old cache is deleted', after.length === 1, after.join(', '));
  check('the banner goes away', (await page.getByText(/newer version/).count()) === 0);
}

await browser.close();

console.log('');
if (failures.length > 0) {
  console.log(`${failures.length} failed: ${failures.join('; ')}`);
  process.exit(1);
}
console.log('all checks passed');
