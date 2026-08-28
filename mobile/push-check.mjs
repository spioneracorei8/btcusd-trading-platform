import { chromium } from 'playwright';

const BASE = 'http://127.0.0.1:8099';
const context = await chromium.launchPersistentContext(process.env.PROFILE, {
  executablePath: '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
  viewport: { width: 412, height: 915 },
});
await context.grantPermissions(['notifications'], { origin: BASE });

const page = await context.newPage();
const errors = [];
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

await page.goto(BASE + '/', { waitUntil: 'networkidle' });
await page.evaluate(() => navigator.serviceWorker.ready);

const attempt = await page.evaluate(async () => {
  const r = await navigator.serviceWorker.ready;
  try {
    const s = await r.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: new Uint8Array(65).fill(4),
    });
    return { ok: true, endpoint: s.endpoint };
  } catch (e) {
    return { ok: false, error: String(e).slice(0, 160) };
  }
});
console.log('subscribe          ', attempt.ok ? attempt.endpoint.slice(0, 60) : attempt.error);
console.log('console errors     ', errors.slice(0, 2).join(' | ') || '(none)');
await context.close();
