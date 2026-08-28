/**
 * Registering the service worker, and noticing when a new build is waiting.
 *
 * # Why the update needs a person
 *
 * A worker that will not update is the classic PWA failure — the app stays on
 * an old build indefinitely with no symptom except that nothing new appears.
 * The worker handles its half: `skipWaiting` on install, old caches deleted on
 * activate.
 *
 * The other half is that a page already open keeps running the old bundle
 * until it reloads. Reloading it underneath somebody mid-read is rude, and
 * doing nothing is the failure above with an extra step. So the app is told,
 * and it says so, and reloading is a tap.
 */

export type UpdateState = 'none' | 'ready';

type Registered = {
  /** Reloads onto the new build. */
  apply: () => void;
  /** Stops listening. */
  stop: () => void;
};

/** Whether this runtime has a service worker at all. */
export function supported(): boolean {
  return (
    typeof globalThis !== 'undefined' &&
    typeof (globalThis as { navigator?: { serviceWorker?: unknown } }).navigator?.serviceWorker !==
      'undefined' &&
    // A worker needs a secure context. On the deployment that is Tailscale's
    // certificate; on localhost the browser grants it anyway, which is what
    // makes the development loop possible.
    (globalThis as { isSecureContext?: boolean }).isSecureContext === true
  );
}

/**
 * Registers the worker and calls back when a newer build is installed.
 *
 * Returns undefined where there is no service worker to register — a native
 * build, or an insecure origin — so a caller can tell "not applicable" from
 * "failed".
 */
export async function register(
  onUpdate: (state: UpdateState) => void,
): Promise<Registered | undefined> {
  if (!supported()) return undefined;

  const container = navigator.serviceWorker;
  const registration = await container.register('/sw.js', { scope: '/' });

  /** A worker that has installed while another one is already controlling. */
  const isWaiting = (worker: ServiceWorker | null): boolean =>
    worker?.state === 'installed' && container.controller !== null;

  const announce = () => {
    if (isWaiting(registration.waiting) || isWaiting(registration.installing)) {
      onUpdate('ready');
    }
  };

  // Three ways a new build shows up: one was already waiting when this ran,
  // one arrives while the page is open, and one finishes installing after
  // `updatefound` fired.
  announce();

  const onFound = () => {
    const installing = registration.installing;
    if (!installing) return;
    installing.addEventListener('statechange', announce);
  };
  registration.addEventListener('updatefound', onFound);

  // The page reloads exactly once, when the new worker takes over. Without the
  // guard, a worker that claims clients during a reload can loop.
  let reloading = false;
  const onControllerChange = () => {
    if (reloading) return;
    reloading = true;
    globalThis.location.reload();
  };
  container.addEventListener('controllerchange', onControllerChange);

  return {
    apply: () => {
      const next = registration.waiting ?? registration.installing;
      // Telling the worker rather than reloading directly: a reload alone
      // leaves the old worker in control and the new build still waiting.
      next?.postMessage('apply-update');
    },
    stop: () => {
      registration.removeEventListener('updatefound', onFound);
      container.removeEventListener('controllerchange', onControllerChange);
    },
  };
}
