/**
 * The browser's push API, and the three ways it is not available.
 *
 * # Why this is separate from the hook
 *
 * Every function here touches a global that does not exist in jest, in a
 * native build, or on an iOS device that has not installed the app. Keeping
 * them in one file means the hook can be tested by replacing this, and means
 * each guard is written once rather than at every call site.
 */

/** What the app posts to /api/v1/device. The browser's own shape. */
export type Subscription = {
  endpoint: string;
  keys: { p256dh: string; auth: string };
};

/**
 * Whether push could work here at all.
 *
 * Three separate things have to be true, and on iOS the third is the one that
 * catches people: push exists only for a PWA added to the home screen and
 * launched from its icon. In a Safari tab there is no PushManager at all —
 * not a denied permission, no API.
 */
export function pushAvailable(): boolean {
  const scope = globalThis as {
    Notification?: unknown;
    PushManager?: unknown;
    navigator?: { serviceWorker?: unknown };
    isSecureContext?: boolean;
  };

  return (
    typeof scope.Notification !== 'undefined' &&
    typeof scope.PushManager !== 'undefined' &&
    typeof scope.navigator?.serviceWorker !== 'undefined' &&
    scope.isSecureContext === true
  );
}

/** The current permission, without prompting. */
export function permissionState(): 'undetermined' | 'granted' | 'denied' {
  if (!pushAvailable()) return 'undetermined';
  switch (Notification.permission) {
    case 'granted':
      return 'granted';
    case 'denied':
      return 'denied';
    default:
      return 'undetermined';
  }
}

/**
 * Subscribes this browser, reusing the existing subscription when there is one.
 *
 * # Why the existing one is checked first
 *
 * `subscribe` with the same key returns the same subscription, but with a
 * *different* key it throws InvalidStateError rather than replacing it — which
 * is exactly what happens after a VAPID rotation. So a stale one is
 * unsubscribed first, and the caller gets a working subscription rather than
 * an error it cannot act on.
 */
export async function subscribe(applicationServerKey: string): Promise<Subscription> {
  const registration = await navigator.serviceWorker.ready;
  const key = decodeKey(applicationServerKey);

  const existing = await registration.pushManager.getSubscription();
  if (existing) {
    if (sameKey(existing, key)) return toWire(existing);
    // A different application server than the one this was made against. It
    // can never be pushed to again, so it goes.
    await existing.unsubscribe();
  }

  const created = await registration.pushManager.subscribe({
    // Required by every browser: a push that shows nothing is a push that
    // could be used to track somebody silently, and none of them allow it.
    // It is also what this app wants — every push here is an alert.
    userVisibleOnly: true,
    applicationServerKey: key,
  });
  return toWire(created);
}

/** Whether a subscription was made against this application server key. */
function sameKey(subscription: PushSubscription, key: Uint8Array<ArrayBuffer>): boolean {
  const existing = subscription.options?.applicationServerKey;
  if (!existing) return false;

  const bytes = new Uint8Array(existing);
  if (bytes.length !== key.length) return false;
  return bytes.every((byte, index) => byte === key[index]);
}

/** The subscription in the shape the API takes, which is the browser's own. */
function toWire(subscription: PushSubscription): Subscription {
  const json = subscription.toJSON();
  return {
    endpoint: json.endpoint ?? subscription.endpoint,
    keys: {
      p256dh: json.keys?.p256dh ?? '',
      auth: json.keys?.auth ?? '',
    },
  };
}

/**
 * base64url to bytes.
 *
 * `applicationServerKey` takes a BufferSource, not the base64url string the
 * server sends, and there is no built-in that decodes base64url — `atob` is
 * base64, which uses different characters for two of its sixty-four. Those two
 * are what the replace below is for, and a key containing either would
 * otherwise decode to the wrong bytes: the subscription would be created
 * against a key the server does not hold, and every push to it rejected.
 *
 * Padding is not added. `atob` performs forgiving-base64 decoding, which
 * accepts an unpadded value and strips a padded one — so a pad step here would
 * be code no test could tell the absence of.
 */
export function decodeKey(base64url: string): Uint8Array<ArrayBuffer> {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');

  const raw = atob(base64);
  const bytes = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i += 1) bytes[i] = raw.charCodeAt(i);
  return bytes;
}
