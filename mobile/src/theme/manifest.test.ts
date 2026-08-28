import { readFileSync } from 'node:fs';
import path from 'node:path';

import { bg } from './colors';

const root = path.join(__dirname, '..', '..');
const html = readFileSync(path.join(root, 'public', 'index.html'), 'utf8');
const manifest = JSON.parse(
  readFileSync(path.join(root, 'public', 'manifest.json'), 'utf8'),
) as Record<string, unknown>;
const appJson = JSON.parse(readFileSync(path.join(root, 'app.json'), 'utf8')) as {
  expo: { backgroundColor?: string; name?: string };
};

/*
TestTheInstallColoursComeFromTheToken.

# What this prevents

The lint rule that keeps colour literals inside src/theme/ reads TypeScript. It
cannot see index.html or manifest.json, and those are exactly where the app's
first colour is decided — the ground a standalone PWA paints between the launch
image and the first frame of JavaScript.

Four copies of bg.base live outside the reach of that rule. If any drifts, the
app launches with a flash of the wrong colour on a phone in a dark room, from a
brief whose first requirement is "never bright". Nothing else would catch it:
it is a fraction of a second, on hardware, before anything renders.
*/
describe('the colour an installed app launches with', () => {
  it('is bg.base in the manifest', () => {
    expect(manifest.background_color).toBe(bg.base);
    expect(manifest.theme_color).toBe(bg.base);
  });

  it('is bg.base in the page that paints before the bundle runs', () => {
    const themeColor = /<meta name="theme-color" content="([^"]+)"/.exec(html)?.[1];
    expect(themeColor).toBe(bg.base);

    const painted = /background-color:\s*([^;]+);/.exec(html)?.[1]?.trim();
    expect(painted?.toLowerCase()).toBe(bg.base.toLowerCase());
  });

  it('is bg.base in app.json, which a native build would use', () => {
    expect(appJson.expo.backgroundColor).toBe(bg.base);
  });
});

/*
TestTheManifestSaysWhatIOSNeedsItTo.

Each of these produces a silent failure rather than an error. A wrong `display`
opens the app in Safari chrome with an address bar; a `start_url` outside
`scope` makes the install non-standalone; a missing icon size leaves iOS to
invent one from a screenshot of the page.
*/
describe('the manifest', () => {
  it('asks for a standalone window', () => {
    expect(manifest.display).toBe('standalone');
  });

  it('scopes the app to the whole origin, which the API shares', () => {
    expect(manifest.scope).toBe('/');
    expect(manifest.start_url).toBe('/');
  });

  it('ships the icon sizes an install actually consumes', () => {
    const icons = manifest.icons as { src: string; sizes: string }[];
    expect(icons.map((icon) => icon.sizes).sort()).toEqual(['192x192', '512x512']);

    for (const icon of icons) {
      expect(() => readFileSync(path.join(root, 'public', icon.src))).not.toThrow();
    }
  });

  it('names the app the same thing app.json does', () => {
    expect(manifest.name).toBe(appJson.expo.name);
  });
});

/*
TestTheAppleTagsArePresent.

iOS ignores most of the manifest. These are the ones that decide whether the
installed thing opens as an app or as a browser tab with an address bar, and
the failure is visible only after installing on a real phone.
*/
describe('the apple-specific tags', () => {
  it.each([
    'apple-mobile-web-app-capable',
    'apple-mobile-web-app-status-bar-style',
    'apple-mobile-web-app-title',
  ])('%s is set', (name) => {
    expect(html).toMatch(new RegExp(`<meta name="${name}" content="[^"]+"`));
  });

  it('points at an icon that exists', () => {
    const href = /<link rel="apple-touch-icon" href="([^"]+)"/.exec(html)?.[1];
    expect(href).toBeTruthy();
    expect(() => readFileSync(path.join(root, 'public', href!))).not.toThrow();
  });

  it('covers the notch, so the background is not framed in white', () => {
    expect(html).toMatch(/viewport-fit=cover/);
  });
});
