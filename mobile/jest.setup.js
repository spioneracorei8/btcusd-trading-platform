// Reanimated ships its own mock; without it every component that animates
// throws in the test environment for reasons unrelated to what is being
// tested.
jest.mock('react-native-reanimated', () =>
  require('react-native-reanimated/mock'),
);

// AsyncStorage is a native module and has no implementation in the test
// environment. Its own mock is the supported way to stand it in.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock'),
);

// jsdom has no TextEncoder or TextDecoder.
//
// Some tests run under jsdom rather than the React Native environment, because
// jsdom is where the browser globals the push path needs — Notification,
// PushManager, navigator.serviceWorker — can be installed. It stops short of
// the encoding globals, and the failure lands a long way from the cause:
// `TextEncoder is not defined`, thrown from inside the URL machinery, arriving
// as a query that fetches forever and never resolves.
//
// Node has both. Handing them over is the whole fix. (`Response` is missing
// there too; the tests that need one duck-type it, since the client reads only
// `.ok`, `.status` and `.text()`.)
const { TextEncoder, TextDecoder } = require('node:util');

for (const [name, value] of Object.entries({ TextEncoder, TextDecoder })) {
  if (typeof globalThis[name] === 'undefined') {
    Object.defineProperty(globalThis, name, { value, writable: true, configurable: true });
  }
}
