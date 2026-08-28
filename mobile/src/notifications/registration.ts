import type { DeviceResponse } from '../api/types';

/**
 * What state the app is in with respect to alerts, and what to say about it.
 *
 * # Why this is a pure function
 *
 * Registering touches three awkward things: the browser's permission prompt,
 * the push service, and the API. The decisions — whether to ask, what the
 * screen should say, whether anything is actually going to arrive — are
 * separable from all three, so they live here and the effects live in
 * useNotifications.
 */

export type PermissionState = 'undetermined' | 'granted' | 'denied';

export type AlertState = {
  /** Whether an alert would actually reach this phone. */
  willArrive: boolean;
  headline: string;
  detail: string;
  /** What the app should do next, if anything. */
  next: 'prime' | 'register' | 'nothing' | 'open-settings';
};

/**
 * The case worth spelling out.
 *
 * A phone can be registered, permitted, and still hear nothing — because the
 * deployment is in silent mode. Those are three separate switches, the app
 * knows two of them, and the third comes back on the registration response.
 * That is why `delivery_mode` is on it.
 */
export function alertState(
  permission: PermissionState,
  device: DeviceResponse | undefined,
  /**
   * False in a browser tab, where iOS grants no push at all.
   *
   * This is the case that catches people. On iOS, push exists only for a PWA
   * that has been added to the home screen and launched from its icon —
   * opened in Safari, permission cannot even be requested, and
   * `Notification.requestPermission` resolves to denied without a prompt
   * appearing. Nothing about that state announces itself, so it is named.
   */
  canReceivePush = true,
): AlertState {
  if (!canReceivePush) {
    return {
      willArrive: false,
      headline: 'Add this app to your home screen',
      detail:
        'iOS only delivers notifications to an installed app. In Safari, tap Share, then ' +
        'Add to Home Screen, and open it from the icon — permission cannot even be asked ' +
        'for from a browser tab. Everything else in the app works either way.',
      next: 'nothing',
    };
  }

  if (permission === 'undetermined') {
    return {
      willArrive: false,
      headline: 'Alerts are not set up',
      detail:
        'This app can tell you when the strategy records a signal — roughly once every ' +
        'ten days on 4h. It sends nothing else.',
      next: 'prime',
    };
  }

  if (permission === 'denied') {
    return {
      willArrive: false,
      headline: 'Notifications are switched off',
      detail:
        'iOS is blocking alerts for this app. The prompt cannot be shown again, so this ' +
        'has to be changed in Settings › Notifications. Everything else works; you will ' +
        'just have to open the app to see whether anything happened.',
      next: 'open-settings',
    };
  }

  if (!device?.registered) {
    return {
      willArrive: false,
      headline: 'This phone is not registered yet',
      detail:
        'Notifications are permitted, but the server has nowhere to send them. Signals ' +
        'are still being recorded and queued.',
      next: 'register',
    };
  }

  if (device.delivery_mode !== 'notify') {
    return {
      willArrive: false,
      headline: 'The server is in silent mode',
      detail:
        'This phone is registered, and the deployment is not sending anything. Signals ' +
        'are recorded and nothing is delivered. Set SIGNAL_MODE=notify on the server.',
      next: 'nothing',
    };
  }

  return {
    willArrive: true,
    headline: 'Alerts are on',
    detail: 'A signal will reach this phone. Nothing else will.',
    next: 'nothing',
  };
}

/**
 * What to say before the OS prompt.
 *
 * The prompt cannot be re-asked once it is refused, so the one chance to
 * explain what alerts are for comes before it. This is deliberately specific
 * about the rate: somebody expecting a live feed turns them off within a week,
 * and somebody told to expect one message every ten days is not surprised by
 * silence.
 */
export const PERMISSION_PRIMER = {
  title: 'Alerts for signals only',
  body:
    'The strategy records a signal roughly once every ten days on 4h. An alert carries ' +
    'the direction, the reference price, the stop and the target — the same numbers the ' +
    'app shows. Nothing here places an order, and no alert ever asks you to.',
  accept: 'Allow alerts',
  decline: 'Not now',
} as const;
