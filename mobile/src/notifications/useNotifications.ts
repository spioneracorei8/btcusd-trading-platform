import { useCallback, useEffect, useState } from 'react';

import { useApi } from '../api/provider';
import { useDevice } from '../api/queries';
import { alertState, type AlertState, type PermissionState } from './registration';
import { subscribe, permissionState, pushAvailable, type Subscription } from './push';

/**
 * Permission, subscription, and keeping the server's copy current.
 *
 * # Re-subscribing is the whole mechanism
 *
 * A push subscription is not permanent. The push service expires them, a
 * reinstall produces a new one, and clearing site data destroys the old one
 * without telling anybody. A deployment holding the previous one fails every
 * send with 410 Gone, which the delivery worker correctly treats as permanent
 * and gives up on. Alerts then stop, silently, and the symptom looks like a
 * strategy that went quiet — which is also what this system looks like on a
 * normal day.
 *
 * So the app subscribes on every launch and posts the result. The server
 * treats a repeat as a success (ADR 0026) precisely so this can be
 * unconditional.
 *
 * # What changed from FCM
 *
 * The transport, and nothing above it. There is no token-refresh event to
 * listen for on the web — `pushsubscriptionchange` exists in the specification
 * and Safari does not fire it — so a launch is the only reliable moment to
 * check, and checking on every launch is what replaces the listener.
 */
export function useNotifications({
  onOpenSignal,
}: {
  /** Tapping an alert opens that signal. */
  onOpenSignal?: (id: string) => void;
} = {}) {
  const { client, ready } = useApi();
  const device = useDevice();

  const [permission, setPermission] = useState<PermissionState>('undetermined');
  const [subscription, setSubscription] = useState<Subscription | undefined>();
  const [error, setError] = useState<unknown>();

  // On iOS this is false in a browser tab and true only in the installed app,
  // which is the distinction the whole alerts card is built around.
  const canReceivePush = pushAvailable();

  useEffect(() => {
    setPermission(permissionState());
  }, []);

  /**
   * The public key the browser subscribes against.
   *
   * Served rather than built in, so rotating the pair does not need a rebuild
   * — and empty in silent mode, where there is nothing to subscribe to.
   */
  const applicationServerKey = device.data?.vapid_public_key;

  /** Subscribes if permitted, and sends the result to the server. */
  const register = useCallback(async () => {
    if (!ready || !canReceivePush || !applicationServerKey) return;

    try {
      const current = await subscribe(applicationServerKey);
      setSubscription(current);

      await client.registerDevice(current);
      await device.refetch();
      setError(undefined);
    } catch (cause) {
      // Worth surfacing rather than swallowing: the whole failure mode this
      // exists to prevent is alerts stopping without anybody being told.
      setError(cause);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, ready, canReceivePush, applicationServerKey]);

  /** Asks the browser, then subscribes if it was allowed. */
  const request = useCallback(async () => {
    if (!canReceivePush) return;

    // Must follow a gesture. A prompt on load is rejected without appearing,
    // which is indistinguishable from the user having refused — see the
    // primer, which is behind a button for this reason as well as its own.
    const status = await Notification.requestPermission();
    setPermission(status === 'granted' ? 'granted' : status === 'denied' ? 'denied' : 'undetermined');
    if (status === 'granted') await register();
  }, [canReceivePush, register]);

  // Subscribe on every launch once permission is in hand and the key has
  // arrived. This is what replaces FCM's token-refresh listener.
  useEffect(() => {
    if (permission === 'granted') void register();
  }, [permission, register]);

  // Tapping an alert opens that signal. The service worker turns a
  // notificationclick into a navigation, so the app is opened at /signals/{id}
  // and the router does the rest — there is no event to listen for here.
  //
  // What is left is the case where the app is already open: the worker posts
  // the id rather than opening a second window.
  useEffect(() => {
    if (!canReceivePush || !onOpenSignal) return;

    const onMessage = (event: MessageEvent) => {
      const data = event.data as { type?: string; signalId?: string } | undefined;
      if (data?.type === 'open-signal' && data.signalId) onOpenSignal(data.signalId);
    };

    navigator.serviceWorker.addEventListener('message', onMessage);
    return () => navigator.serviceWorker.removeEventListener('message', onMessage);
  }, [canReceivePush, onOpenSignal]);

  const state: AlertState = alertState(permission, device.data, canReceivePush);

  return { state, permission, subscription, error, request, register, device };
}
