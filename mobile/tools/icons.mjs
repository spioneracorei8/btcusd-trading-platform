/**
 * Resizes the app icon into the sizes a home-screen install needs.
 *
 * # Why this exists rather than a dependency
 *
 * The source icon is 1024×1024 and iOS wants 180, the manifest wants 192 and
 * 512. Shipping the 1024 for all of them is 390 KB fetched to fill a 180-pixel
 * slot, on a phone, over a tailnet.
 *
 * The obvious answer is sharp, and this environment has Chromium already —
 * it is what renders the screenshots and the theme audit. A canvas downscale
 * is the same operation, needs nothing installed, and keeps the dependency
 * list honest.
 *
 *   node tools/icons.mjs
 *
 * The output is committed. This is not part of the build: the icon changes
 * about once, and a build step that runs a browser to produce identical bytes
 * every time is a slow way to change nothing.
 */
import { chromium } from 'playwright';
import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const SOURCE = 'assets/icon.png';
const OUT = 'public';

/**
 * What each size is for. Nothing here is decoration:
 *
 *   180  apple-touch-icon — what iOS puts on the home screen
 *   192  the manifest's small icon, and what Android would use
 *   512  the manifest's large icon, and the splash source
 */
const SIZES = [
  { file: 'apple-touch-icon.png', size: 180 },
  { file: 'icon-192.png', size: 192 },
  { file: 'icon-512.png', size: 512 },
];

const source = await readFile(SOURCE);
const dataUri = `data:image/png;base64,${source.toString('base64')}`;

const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM_PATH ?? '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'],
});
const page = await browser.newPage();

for (const { file, size } of SIZES) {
  const encoded = await page.evaluate(
    async ([uri, target]) => {
      const image = new Image();
      image.src = uri;
      await image.decode();

      const canvas = document.createElement('canvas');
      canvas.width = target;
      canvas.height = target;

      const context = canvas.getContext('2d');
      if (!context) throw new Error('no 2d context');
      // Downscaling a 1024 icon to 180 without this is visibly aliased on the
      // thin strokes, which is most of this icon.
      context.imageSmoothingEnabled = true;
      context.imageSmoothingQuality = 'high';
      context.drawImage(image, 0, 0, target, target);

      return canvas.toDataURL('image/png').split(',')[1];
    },
    [dataUri, size],
  );

  const out = path.join(OUT, file);
  await writeFile(out, Buffer.from(encoded, 'base64'));
  console.log(`${out} ${size}×${size}`);
}

await browser.close();
