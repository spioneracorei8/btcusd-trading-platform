import { useCallback, useEffect, useRef, useState } from 'react';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';

import { useApi } from '../api/provider';
import { useDevice } from '../api/queries';
import { alertState, type AlertState, type PermissionState } from './registration';
import { signalIdOf } from './payload';

/**
 * Permission, registration, and what to do when one arrives.
 *
 * # Re-registering is the whole mechanism
 *
 * FCM rotates a token whenever it likes — on reinstall, on restore to a new
 * device, and sometimes for no reason the app is told. A deployment holding
 * the previous one fails every send as UNREGISTERED, which the delivery worker
 * correctly treats as permanent and gives up on. Alerts then stop, silently,
 * and the symptom looks like a strategy that went quiet.
 *
 * So the app posts its token on every launch and on every refresh event. The
 * server treats a repeat as a success (ADR 0026) precisely so this can be
 * unconditional.
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
  const [token, setToken] = useState<string | undefined>();
  const [error, setError] = useState<unknown>();

  // Held in a ref so the listener effect does not re-subscribe every time the
  // navigator re-renders, which would drop notifications during the gap.
  const openSignal = useRef(onOpenSignal);
  openSignal.current = onOpenSignal;

  const isPhysicalDevice = Device.isDevice;

  useEffect(() => {
    let live = true;
    void Notifications.getPermissionsAsync().then(({ status }) => {
      if (live) setPermission(toPermission(status));
    });
    return () => {
      live = false;
    };
  }, []);

  /** Asks the OS, then registers if it was allowed. */
  const request = useCallback(async () => {
    const { status } = await Notifications.requestPermissionsAsync();
    setPermission(toPermission(status));
    if (status === 'granted') await register();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** Fetches the current token and sends it to the server. */
  const register = useCallback(async () => {
    if (!ready || !isPhysicalDevice) return;

    try {
      const { data } = await Notifications.getDevicePushTokenAsync();
      const value = String(data);
      setToken(value);

      await client.registerDevice({
        token: value,
        platform: 'android',
        label: Device.modelName ?? 'android',
      });
      await device.refetch();
      setError(undefined);
    } catch (cause) {
      // Worth surfacing rather than swallowing: the whole failure mode this
      // exists to prevent is alerts stopping without anybody being told.
      setError(cause);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, ready, isPhysicalDevice]);

  // Register on every launch once permission is in hand.
  useEffect(() => {
    if (permission === 'granted') void register();
  }, [permission, register]);

  // And whenever FCM rotates the token underneath us.
  useEffect(() => {
    const subscription = Notifications.addPushTokenListener(() => {
      void register();
    });
    return () => subscription.remove();
  }, [register]);

  // Tapping an alert opens that signal. This fires for a tap in every state —
  // foreground, background and cold start — because expo-notifications
  // replays the launch notification through the same listener.
  useEffect(() => {
    const subscription = Notifications.addNotificationResponseReceivedListener((response) => {
      const id = signalIdOf(response.notification.request.content.data);
      if (id) openSignal.current?.(id);
    });
    return () => subscription.remove();
  }, []);

  const state: AlertState = alertState(permission, device.data, isPhysicalDevice);

  return { state, permission, token, error, request, register, device };
}

function toPermission(status: Notifications.PermissionStatus): PermissionState {
  switch (status) {
    case 'granted':
      return 'granted';
    case 'denied':
      return 'denied';
    default:
      return 'undetermined';
  }
}

/**
 * How a notification behaves while the app is open.
 *
 * Shown, not swallowed. A signal arriving while somebody is looking at the
 * chart is exactly as interesting as one arriving while the phone is locked,
 * and an app that hides it is an app that trains its owner to distrust it.
 */
export function configureForegroundBehaviour() {
  Notifications.setNotificationHandler({
    handleNotification: async () => ({
      shouldShowBanner: true,
      shouldShowList: true,
      shouldPlaySound: true,
      shouldSetBadge: false,
    }),
  });
}
