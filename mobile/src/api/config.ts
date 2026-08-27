import AsyncStorage from '@react-native-async-storage/async-storage';

/**
 * Where the API is.
 *
 * # Why this is configurable at all
 *
 * One user, one server — so a hardcoded address would nearly work. What it
 * would not survive is the tailnet address changing, which happens when the
 * VPS is rebuilt, and the app would then be permanently unable to reach a
 * server that is running fine. A setting is cheaper than a rebuild.
 *
 * It is persisted rather than asked for on every launch, and it defaults to
 * the tailnet address so a fresh install works without being configured.
 */

const KEY = 'api.baseUrl';

/**
 * The address this deployment lives at.
 *
 * Replace with the value `tailscale ip -4` prints on the VPS. It is a default
 * rather than a constant: see above.
 */
export const DEFAULT_BASE_URL =
  process.env.EXPO_PUBLIC_API_BASE_URL ?? 'http://100.64.0.1:8080';

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
 * pasted address picks one up constantly. A missing scheme is assumed to be
 * http, because this is a tailnet address and there is no TLS in front of it
 * (ADR 0024) — assuming https would fail in a way that looks like the server
 * being down.
 */
export function normalise(url: string): string {
  const trimmed = url.trim().replace(/\/+$/, '');
  if (trimmed === '') return DEFAULT_BASE_URL;
  return /^https?:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
}
