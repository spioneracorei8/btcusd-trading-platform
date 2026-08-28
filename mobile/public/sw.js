/*
 * The service worker.
 *
 * Three jobs, in order of how badly each one fails when it is wrong.
 *
 * # 1. It must never cache the API
 *
 * Phase 09 part D forbids the app recomputing anything the server computes,
 * because two implementations of a number is two numbers. A cached API
 * response is the same hazard wearing different clothes: a price from twenty
 * minutes ago, rendered as current, with nothing on screen to say so. Worse
 * than no price, because no price is visibly no price.
 *
 * So every /api/ request goes straight to the network. Not stale-while-
 * revalidate, not a short max-age — untouched.
 *
 * # 2. It must serve the shell when the network is gone
 *
 * A cold launch on a dropped tailnet should show the app and its own "cannot
 * reach the API" state, which names Tailscale and says what to do. Safari's
 * offline page says none of that, and looks like the app is broken rather than
 * the connection.
 *
 * # 3. It must be able to replace itself
 *
 * The classic PWA failure: a worker answered from the browser's HTTP cache
 * cannot update, because the update check fetches the copy it is trying to
 * replace. The server sends this file with Cache-Control: no-cache for that
 * reason (see the api's web handler), and BUILD below changes on every export,
 * so a new deployment produces a new cache and the old ones are deleted.
 */

// Replaced at export time by tools/build-web.mjs. A literal here would mean
// every build shipped the same worker bytes and no update would ever install.
const BUILD = '__BUILD__';

// Written at export time from the files that were actually emitted, so this
// cannot drift from the bundle's fingerprinted name.
const SHELL = ['__SHELL__'];

const CACHE = `btcusd-shell-${BUILD}`;

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      // One failed asset must not fail the whole install and leave the app
      // with no worker at all; the fetch handler re-fills on demand.
      await Promise.allSettled(SHELL.map((url) => cache.add(url)));

      // Deliberately no skipWaiting() here.
      //
      // Calling it would activate this worker the moment it installed, claim
      // the open page, and reload it underneath whoever is reading — and,
      // worse, silently: the update would happen with nothing to see, which is
      // half of the failure this file exists to prevent. "The user sees an old
      // build indefinitely with no way to know" and "the app reloads itself
      // mid-sentence with no way to know" are the same complaint.
      //
      // So a new build waits, the page notices it waiting and says so, and
      // skipWaiting happens below when a person taps. On a first install
      // there is no worker to displace, so the browser activates this one
      // immediately without being asked.
    })(),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name.startsWith('btcusd-shell-') && name !== CACHE) {
          await caches.delete(name);
        }
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener('message', (event) => {
  // The page asks for the new build when the person says so. See
  // src/pwa/register.ts.
  if (event.data === 'apply-update') void self.skipWaiting();
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  // Rule 1. Nothing about the API is ever cached, or served from a cache.
  if (url.pathname.startsWith('/api/')) return;

  event.respondWith(handle(request, url));
});

async function handle(request, url) {
  // A navigation is the entry document, whatever path was asked for: the app
  // routes in the browser, so /signals/{id} is a screen the server answers
  // with index.html. Network first, so a deployment is picked up on the next
  // launch rather than whenever the cache happens to expire.
  if (request.mode === 'navigate') {
    try {
      const response = await fetch(request);
      if (response.ok) {
        const cache = await caches.open(CACHE);
        await cache.put('/', response.clone());
      }
      return response;
    } catch {
      // Rule 2. The shell, so the app can say what is wrong in its own words.
      const cached = await caches.match('/', { cacheName: CACHE });
      if (cached) return cached;
      throw new Error('offline, and no shell cached yet');
    }
  }

  // Everything else is a build asset. Fingerprinted names mean a hit is
  // always correct, and a miss is a file this build has not asked for before.
  const cached = await caches.match(request, { cacheName: CACHE });
  if (cached) return cached;

  const response = await fetch(request);
  if (response.ok && isCacheable(url)) {
    const cache = await caches.open(CACHE);
    await cache.put(request, response.clone());
  }
  return response;
}

/**
 * Whether a response is worth keeping for the next launch.
 *
 * The export's own output and the install icons. Anything else — and there
 * should be nothing else, since the API is already excluded above — is fetched
 * fresh, because a cache entry nobody can invalidate is a bug waiting for a
 * deployment.
 */
function isCacheable(url) {
  return (
    url.pathname.startsWith('/_expo/') ||
    url.pathname.startsWith('/assets/') ||
    /\.(png|ico|json|js|css|woff2?)$/.test(url.pathname)
  );
}
