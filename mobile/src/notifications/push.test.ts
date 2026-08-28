/**
 * @jest-environment jsdom
 */
import { decodeKey, permissionState, pushAvailable } from './push';

/** Installs all four things push needs, so a test can remove exactly one. */
function installAll() {
  Object.defineProperty(globalThis, 'isSecureContext', { value: true, configurable: true });
  Object.defineProperty(globalThis, 'PushManager', { value: class {}, configurable: true });
  Object.defineProperty(globalThis, 'Notification', {
    value: { permission: 'granted' },
    configurable: true,
  });
  Object.defineProperty(globalThis.navigator, 'serviceWorker', {
    value: {},
    configurable: true,
  });
}

function remove(name: 'PushManager' | 'Notification' | 'serviceWorker' | 'isSecureContext') {
  if (name === 'serviceWorker') {
    Object.defineProperty(globalThis.navigator, 'serviceWorker', {
      value: undefined,
      configurable: true,
    });
    return;
  }
  Object.defineProperty(globalThis, name, {
    value: name === 'isSecureContext' ? false : undefined,
    configurable: true,
  });
}

afterEach(() => {
  for (const name of ['PushManager', 'Notification'] as const) {
    Object.defineProperty(globalThis, name, { value: undefined, configurable: true });
  }
  remove('serviceWorker');
  Object.defineProperty(globalThis, 'isSecureContext', { value: false, configurable: true });
});

/*
TestEachThingPushNeedsIsCheckedSeparately.

# What this prevents

Four separate things have to exist, and each absence is a different real
environment:

  Notification      an old browser, or one with the API switched off
  PushManager       an iOS Safari tab — this is the one that catches people,
                    because push exists only for an installed PWA
  serviceWorker     a native build, or a page served over plain http
  isSecureContext   plain http, which is what this deployment was before
                    phase 09b part B

Checking them as a group would let any three carry the fourth: a test that
removes everything at once passes whichever guard is deleted. So each is
removed on its own.

Getting this wrong in the permissive direction is the expensive one. The app
would call `pushManager.subscribe` on something that is not there, throw
somewhere the user cannot act on, and show an error instead of "add this app to
your home screen" — which is the actual instruction.
*/
describe('whether push could work here at all', () => {
  it('is true when everything is in place', () => {
    installAll();
    expect(pushAvailable()).toBe(true);
  });

  it.each([
    ['Notification', 'an old browser'],
    ['PushManager', 'an iOS Safari tab rather than the installed app'],
    ['serviceWorker', 'a native build or an insecure page'],
    ['isSecureContext', 'plain http'],
  ] as const)('is false without %s (%s)', (missing, _why) => {
    installAll();
    remove(missing);

    expect(pushAvailable()).toBe(false);
  });
});

/*
TestPermissionIsUndeterminedWhereThereIsNoAPIToAsk.

Reporting 'denied' would be wrong in a way that matters: denied is a state the
person chose and cannot be re-asked, and the app says so. Not having the API is
neither — it is "install this first", which is a different instruction.
*/
describe('the current permission', () => {
  it('is undetermined where push does not exist', () => {
    expect(permissionState()).toBe('undetermined');
  });

  it('reads the browser once it does', () => {
    installAll();
    expect(permissionState()).toBe('granted');
  });
});

/*
TestTheVAPIDKeyDecodesToBytes.

`applicationServerKey` takes a BufferSource, not the base64url string the
server sends, and there is no built-in that decodes base64url — `atob` is
base64, which uses different characters for two of its sixty-four. Feeding it
the string, or the wrong decoding, produces a subscription made against the
wrong key: it succeeds, and every push to it is then rejected by the push
service.
*/
describe('the application server key', () => {
  // A real VAPID public key: 65 bytes, uncompressed P-256 point, so it starts
  // with 0x04.
  const key =
    'BDLFBrIHg9mGNteU0m9p-FKeovhMbMUR4dBwQf3kd1P7LtzaQ4qtDFr66_2fG2835RU7WcSSOSv5lwdTKjWFl1g';

  it('decodes to the 65 bytes of an uncompressed P-256 point', () => {
    const bytes = decodeKey(key);

    expect(bytes).toBeInstanceOf(Uint8Array);
    expect(bytes).toHaveLength(65);
    expect(bytes[0]).toBe(0x04);
  });

  it('handles the two characters base64url does differently', () => {
    // This key contains both - and _, which plain atob would reject or
    // mis-decode.
    expect(key).toMatch(/[-_]/);
    expect(() => decodeKey(key)).not.toThrow();
  });

  it('accepts a padded key, which some tools produce', () => {
    const padded = key.padEnd(key.length + ((4 - (key.length % 4)) % 4), '=');

    expect(Array.from(decodeKey(padded))).toEqual(Array.from(decodeKey(key)));
  });
});
