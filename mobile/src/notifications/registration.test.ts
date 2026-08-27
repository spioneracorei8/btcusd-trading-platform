import { PERMISSION_PRIMER, alertState } from './registration';
import { REFERENCE_PRICE_LABEL, signalIdOf } from './payload';
import type { DeviceResponse } from '../api/types';

function device(overrides: Partial<DeviceResponse> = {}): DeviceResponse {
  return {
    registered: true,
    token: 'fMEP0v…',
    platform: 'android',
    delivery_mode: 'notify',
    note: 'Signals will be delivered to this device.',
    ...overrides,
  };
}

/**
 * TestRegisteredIsNotTheSameAsWillArrive.
 *
 * # What this prevents
 *
 * Three switches have to be on before an alert reaches this phone: Android
 * must permit it, the server must know the token, and the deployment must be
 * in notify mode. The app can see all three, and a screen that reported only
 * the one it controls would tell somebody "alerts are on" while the server
 * quietly sent nothing.
 *
 * That is the failure this phase exists to close: phase 07 could never deliver
 * because there was no token, and the way that would recur is an app that
 * believes registering was enough.
 */
describe('whether an alert will actually arrive', () => {
  it('is false before the permission has been asked for', () => {
    const state = alertState('undetermined', undefined);

    expect(state.willArrive).toBe(false);
    expect(state.next).toBe('prime');
    // The rate is stated up front. Somebody expecting a live feed turns
    // alerts off within a week.
    expect(state.detail).toMatch(/ten days/);
  });

  it('is false when Android is blocking them, and says where to fix it', () => {
    const state = alertState('denied', device());

    expect(state.willArrive).toBe(false);
    expect(state.next).toBe('open-settings');
    // And it must not read as a fault in the app or the server.
    expect(state.detail).toMatch(/everything else works/i);
  });

  it('is false when permission is granted but the server has no token', () => {
    const state = alertState('granted', device({ registered: false }));

    expect(state.willArrive).toBe(false);
    expect(state.next).toBe('register');
    // The signals are not lost while this is true — they queue and wait.
    expect(state.detail).toMatch(/recorded and queued/i);
  });

  it('is false when the deployment is in silent mode, however registered the phone is', () => {
    // The case a naive app gets wrong. Everything on this device is correct
    // and nothing will ever arrive.
    const state = alertState('granted', device({ delivery_mode: 'silent' }));

    expect(state.willArrive).toBe(false);
    expect(state.headline).toMatch(/silent mode/i);
    expect(state.detail).toMatch(/SIGNAL_MODE=notify/);
    // Nothing for the app to do: this is a server setting.
    expect(state.next).toBe('nothing');
  });

  it('is true only when all three are in place', () => {
    const state = alertState('granted', device());

    expect(state.willArrive).toBe(true);
    expect(state.next).toBe('nothing');
  });

  it('is false on an emulator, and says why rather than looking broken', () => {
    const state = alertState('granted', device(), false);

    expect(state.willArrive).toBe(false);
    expect(state.detail).toMatch(/emulator/i);
    expect(state.detail).toMatch(/everything else in the app works/i);
  });
});

/**
 * The primer, which is the one chance to explain what alerts are for. The OS
 * prompt cannot be re-asked once it is refused.
 */
describe('the permission primer', () => {
  it('says how often an alert will arrive', () => {
    expect(PERMISSION_PRIMER.body).toMatch(/ten days/);
  });

  it('says what an alert carries', () => {
    expect(PERMISSION_PRIMER.body).toMatch(/reference price/);
    expect(PERMISSION_PRIMER.body).toMatch(/stop/);
    expect(PERMISSION_PRIMER.body).toMatch(/target/);
  });

  it('says that nothing here places an order', () => {
    // The gap between an alert and a broker has to stay wide, and a
    // notification is where it is narrowest: it arrives unbidden, on a lock
    // screen, with prices on it.
    expect(PERMISSION_PRIMER.body).toMatch(/places an order/i);
  });

  it('offers a way to decline that is not a dead end', () => {
    expect(PERMISSION_PRIMER.decline).toBeTruthy();
  });
});

/**
 * TestTheReferencePriceIsNotRelabelled.
 *
 * Phase 07 made the distinction deliberately: signal_price is the close the
 * strategy decided on, and a decision taken on a bar's close cannot fill on
 * it. Calling it "entry" in a notification would make every comparison against
 * the app or a chart off by roughly the slippage, systematically, with nothing
 * to indicate it.
 */
describe('the notification payload', () => {
  it('labels the decided price as a reference price', () => {
    expect(REFERENCE_PRICE_LABEL).toMatch(/reference/i);
    expect(REFERENCE_PRICE_LABEL).not.toMatch(/entry/i);
  });

  it('finds the signal a notification points at', () => {
    expect(signalIdOf({ signal_id: 'e6d0e070-39a3-4fa2-907c-1dc470c83d3d' })).toBe(
      'e6d0e070-39a3-4fa2-907c-1dc470c83d3d',
    );
  });

  it('refuses anything that is not a uuid rather than navigating on it', () => {
    // A payload this build does not understand would otherwise open a detail
    // screen that 404s, which reads as a deleted signal.
    for (const data of [
      {},
      null,
      undefined,
      'nonsense',
      { signal_id: '' },
      { signal_id: 'not-a-uuid' },
      { signal_id: 42 },
      { signal_id: '../status' },
    ]) {
      expect(signalIdOf(data)).toBeUndefined();
    }
  });
});
