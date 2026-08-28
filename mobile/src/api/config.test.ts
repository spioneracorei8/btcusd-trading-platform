/**
 * @jest-environment jsdom
 */
import { normalise } from './config';

/*
TestASchemeLessAddressTakesThePagesScheme.

# What this prevents

This function used to assume http:// unconditionally, with a reason written
beside it: the tailnet had no TLS in front of it. Phase 09b made that false —
`tailscale serve` terminates TLS, because iOS gives a plain-HTTP page no
service worker, no push and no home-screen install.

Left as it was, typing a bare host into the settings field on an HTTPS page
would produce an http:// base URL, and the browser would block every request as
mixed content without raising anything the app can catch. Every screen would
then report the server as unreachable while the server was fine — the exact
misdiagnosis the old comment existed to prevent, reached from the other side.
*/
describe('an address typed without a scheme', () => {
  const location = globalThis.location;

  afterEach(() => {
    Object.defineProperty(globalThis, 'location', {
      value: location,
      writable: true,
      configurable: true,
    });
  });

  function servedOver(protocol: string) {
    Object.defineProperty(globalThis, 'location', {
      value: { protocol, origin: `${protocol}//page.example` },
      writable: true,
      configurable: true,
    });
  }

  it('is https on a page served over https', () => {
    servedOver('https:');
    expect(normalise('btcusd.tail1234.ts.net')).toBe('https://btcusd.tail1234.ts.net');
  });

  it('is http on a page served over http', () => {
    servedOver('http:');
    expect(normalise('100.72.14.3:8080')).toBe('http://100.72.14.3:8080');
  });

  it('leaves an address that already has one alone', () => {
    servedOver('https:');
    expect(normalise('http://100.72.14.3:8080')).toBe('http://100.72.14.3:8080');
  });

  it('still trims the trailing slash a paste picks up', () => {
    servedOver('https:');
    expect(normalise('https://btcusd.tail1234.ts.net/')).toBe('https://btcusd.tail1234.ts.net');
  });
});
