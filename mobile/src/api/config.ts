import AsyncStorage from '@react-native-async-storage/async-storage';

/**
 * Where the API is.
 *
 * # Why this is configurable at all
 *
 * One user, one server — so a hardcoded address would nearly work. What it
 * would not survive is the address changing, which happens when the VPS is
 * rebuilt, and the app would then be permanently unable to reach a server that
 * is running fine. A setting is cheaper than a rebuild.
 *
 * It is persisted rather than asked for on every launch.
 *
 * # Why the default is usually nothing at all
 *
 * The api serves this app (ADR 0028), so the page and the API share an origin
 * and the right default is "wherever this page came from". That is not a
 * convenience: same-origin is what lets the websocket's origin check hold with
 * nothing configured, and a configured address that points somewhere else is
 * the one way to break it from inside the app.
 */

const KEY = 'api.baseUrl';

/**
 * Where this page was served from, or undefined when there is no page.
 *
 * A native build has no `location`, and a file:// document reports an origin
 * of "null" — neither is an address, so both fall through to the constant
 * below.
 */
function servedFrom(): string | undefined {
  const location = (globalThis as { location?: { origin?: string } }).location;
  const origin = location?.origin;
  if (!origin || origin === 'null') return undefined;
  return origin;
}

/**
 * The address this deployment lives at.
 *
 * An explicit `EXPO_PUBLIC_API_BASE_URL` wins, because a development build
 * genuinely does talk to another port. Otherwise the page's own origin, which
 * is correct for every deployed case. The constant at the end is the last
 * resort for a native build with nothing set, and it is a placeholder rather
 * than a real address.
 */
export const DEFAULT_BASE_URL =
  process.env.EXPO_PUBLIC_API_BASE_URL ?? servedFrom() ?? 'http://100.64.0.1:8080';

export async function loadBaseUrl(): Promise<string> {
  try {
    const stored = await AsyncStorage.getItem(KEY);
    return normalise(stored ?? DEFAULT_BASE_URL);
  } catch {
    // Storage is unavailable on a fresh install in some conditions, and a
    // default that works is better than a screen that cannot render.
    return DEFAULT_BASE_URL;
  }
}

export async function saveBaseUrl(url: string): Promise<string> {
  const clean = normalise(url);
  await AsyncStorage.setItem(KEY, clean);
  return clean;
}

/**
 * Trims the address into something joinable.
 *
 * A trailing slash produces a double one when the path is appended, and a
 * pasted address picks one up constantly.
 *
 * # Which scheme a scheme-less address gets
 *
 * The page's own, when there is a page. This used to be http:// unconditionally
 * with a reason attached — the tailnet had no TLS in front of it — and phase
 * 09b made that false: `tailscale serve` terminates TLS, and iOS gives a
 * plain-HTTP page no service worker, no push and no install.
 *
 * Keeping http:// would now be the worst of the options. A secure page cannot
 * fetch http://; the browser blocks it as mixed content and says nothing the
 * app can catch, so every request fails in a way indistinguishable from the
 * server being down — which is exactly the misdiagnosis the old comment was
 * trying to prevent, arrived at from the other direction.
 */
export function normalise(url: string): string {
  const trimmed = url.trim().replace(/\/+$/, '');
  if (trimmed === '') return DEFAULT_BASE_URL;
  if (/^https?:\/\//i.test(trimmed)) return trimmed;

  const protocol = (globalThis as { location?: { protocol?: string } }).location?.protocol;
  return `${protocol === 'https:' ? 'https' : 'http'}://${trimmed}`;
}
