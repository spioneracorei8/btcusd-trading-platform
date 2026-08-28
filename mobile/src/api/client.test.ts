import { ApiClient, MAX_CANDLES } from './client';
import { ApiError, explain } from './errors';

const BASE = 'http://100.72.14.3:8080';

/** A fetch that answers however the test needs, and records what it was asked. */
function fakeFetch(
  responder: (url: string, init: RequestInit) => Promise<Response> | Response,
) {
  const calls: { url: string; init: RequestInit }[] = [];
  const impl = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input);
    calls.push({ url, init });
    return responder(url, init);
  }) as unknown as typeof fetch;
  return { impl, calls };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function client(impl: typeof fetch, timeoutMs = 8000) {
  return new ApiClient({ baseUrl: BASE, fetchImpl: impl, timeoutMs });
}

describe('the client asks for what it was told to', () => {
  it('versions the path and keeps the base url clean', async () => {
    const { impl, calls } = fakeFetch(() => json({ concerns: [] }));
    await new ApiClient({ baseUrl: `${BASE}/`, fetchImpl: impl }).status();

    // A trailing slash on the configured base must not produce a double one.
    expect(calls[0]!.url).toBe(`${BASE}/api/v1/status`);
  });

  it('passes the window and paging through as query parameters', async () => {
    const { impl, calls } = fakeFetch(() => json({ candles: [] }));

    await client(impl).candles({
      timeframe: '4h',
      from: new Date('2024-03-01T00:00:00Z'),
      to: new Date('2024-03-02T00:00:00Z'),
      limit: 100,
    });

    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/api/v1/candles');
    expect(url.searchParams.get('timeframe')).toBe('4h');
    expect(url.searchParams.get('from')).toBe('2024-03-01T00:00:00.000Z');
    expect(url.searchParams.get('to')).toBe('2024-03-02T00:00:00.000Z');
    expect(url.searchParams.get('limit')).toBe('100');
  });

  it('omits parameters that were not given rather than sending empty ones', async () => {
    // An empty `direction=` is not the same request as no direction: the
    // first asks the server to parse "" and the second asks for both.
    const { impl, calls } = fakeFetch(() => json({ signals: [] }));
    await client(impl).signals({ limit: 10 });

    const url = new URL(calls[0]!.url);
    expect(url.searchParams.get('limit')).toBe('10');
    expect(url.searchParams.has('direction')).toBe(false);
    expect(url.searchParams.has('offset')).toBe(false);
  });

  it('escapes an id rather than pasting it into the path', async () => {
    const { impl, calls } = fakeFetch(() => json({ id: 'x' }));
    await client(impl).signal('../status');

    expect(calls[0]!.url).toBe(`${BASE}/api/v1/signals/..%2Fstatus`);
  });
});

/**
 * TestAnOversizedCandleRequestIsRefusedBeforeItIsSent.
 *
 * The API caps this and refuses politely. The app refuses first, because a
 * request it knows is wrong should not become a round trip — and on a chart
 * this is a gesture rather than a typo, so it happens often and it happens
 * while the user is dragging.
 */
describe('the client refuses what it knows the server will', () => {
  it('does not ask for more candles than the API returns', async () => {
    const { impl, calls } = fakeFetch(() => json({ candles: [] }));

    await expect(
      client(impl).candles({ timeframe: '1m', limit: MAX_CANDLES + 1 }),
    ).rejects.toMatchObject({ kind: 'request', code: 'limit_exceeded' });

    expect(calls).toEqual([]);
  });

  it('allows exactly the maximum', async () => {
    const { impl, calls } = fakeFetch(() => json({ candles: [] }));
    await client(impl).candles({ timeframe: '1m', limit: MAX_CANDLES });

    expect(calls).toHaveLength(1);
  });
});

/**
 * TestAnUnreachableServerIsItsOwnKindOfFailure.
 *
 * # What this prevents
 *
 * The commonest failure this app will ever see is Tailscale being off, and a
 * spinner is the wrong answer to that: it says "wait" about a condition that
 * will not resolve by waiting. The kind has to survive as far as the screen,
 * which is what makes it possible to say "check Tailscale" rather than
 * "Network request failed".
 */
describe('an unreachable server', () => {
  it('is reported as unreachable rather than as an error', async () => {
    const { impl } = fakeFetch(() => {
      throw new TypeError('Network request failed');
    });

    await expect(client(impl).status()).rejects.toMatchObject({
      kind: 'unreachable',
      retryable: true,
    });
  });

  it('is what a timeout produces too', async () => {
    // A tailnet address with nothing behind it does not refuse a connection,
    // it hangs. Without the timeout the platform's own would take a minute,
    // and the screen would show a spinner for all of it.
    const { impl } = fakeFetch(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () =>
            reject(new DOMException('Aborted', 'AbortError')),
          );
        }),
    );

    await expect(client(impl, 10).status()).rejects.toMatchObject({
      kind: 'unreachable',
    });
  });

  it('names Tailscale when the screen asks what to say', () => {
    const failure = new ApiError('unreachable', 'no answer');
    const { title, detail, action } = explain(failure, BASE);

    expect(title).toMatch(/cannot reach/i);
    expect(detail).toContain(BASE);
    expect(detail).toMatch(/tailscale/i);
    expect(action).toMatch(/tailscale/i);
  });

  it('does not name Tailscale for a failure that answered', () => {
    // The server replied, so the VPN is plainly up. Blaming it would send
    // somebody to the wrong place.
    for (const failure of [
      new ApiError('server', 'the candles could not be read', { status: 500 }),
      new ApiError('request', 'timeframe is required', { status: 400 }),
      new ApiError('malformed', 'the response was not JSON'),
    ]) {
      const { title, detail, action } = explain(failure, BASE);
      expect(`${title} ${detail} ${action}`).not.toMatch(/tailscale/i);
    }
  });
});

describe('a server that answers', () => {
  it('turns a 4xx into a request failure carrying the API code', async () => {
    const { impl } = fakeFetch(() =>
      json(
        { error: { code: 'invalid_parameter', message: 'timeframe is required' } },
        400,
      ),
    );

    const failure = await client(impl)
      .candles({ timeframe: '1m' })
      .then(() => null)
      .catch((e: unknown) => e as ApiError);

    expect(failure).toBeInstanceOf(ApiError);
    if (!(failure instanceof ApiError)) throw new Error('unreachable');
    expect(failure.kind).toBe('request');
    expect(failure.code).toBe('invalid_parameter');
    expect(failure.message).toBe('timeframe is required');
    // A 400 will not pass by trying again.
    expect(failure.retryable).toBe(false);
  });

  it('turns a 5xx into a server failure that is worth retrying', async () => {
    const { impl } = fakeFetch(() =>
      json({ error: { code: 'internal', message: 'the candles could not be read' } }, 500),
    );

    const failure = await client(impl)
      .status()
      .then(() => null)
      .catch((e: unknown) => e as ApiError);

    expect(failure).toBeInstanceOf(ApiError);
    if (!(failure instanceof ApiError)) throw new Error('unreachable');
    expect(failure.kind).toBe('server');
    expect(failure.retryable).toBe(true);
  });

  it('survives an error body that is not the API shape', async () => {
    // A proxy or a crash can answer HTML. The status is still the useful part
    // and must not be lost to a parse failure.
    const { impl } = fakeFetch(() => new Response('<html>502</html>', { status: 502 }));

    const failure = await client(impl)
      .status()
      .then(() => null)
      .catch((e: unknown) => e as ApiError);

    expect(failure).toBeInstanceOf(ApiError);
    if (!(failure instanceof ApiError)) throw new Error('unreachable');
    expect(failure.kind).toBe('server');
    expect(failure.status).toBe(502);
    expect(failure.message).toContain('502');
  });

  it('reports a 200 that is not JSON as malformed rather than unreachable', async () => {
    // These are different problems: one is the network, the other is a
    // version mismatch, and telling somebody to check Tailscale for the
    // second wastes their afternoon.
    const { impl } = fakeFetch(() => new Response('not json', { status: 200 }));

    await expect(client(impl).status()).rejects.toMatchObject({ kind: 'malformed' });
  });
});

describe('the write', () => {
  it('posts a device registration and nothing else', async () => {
    const { impl, calls } = fakeFetch(() => json({ registered: true }));

    await client(impl).registerDevice({
      endpoint: 'https://web.push.apple.com/Q123',
      keys: { p256dh: 'a-key', auth: 'a-secret' },
      platform: 'web',
      label: 'iPhone 14',
    });

    expect(calls[0]!.init.method).toBe('POST');
    expect(calls[0]!.url).toBe(`${BASE}/api/v1/device`);
    // The browser's own PushSubscription.toJSON() shape, posted through
    // unchanged. Unpacking and reassembling it is a place for a key to go
    // missing, and a missing key fails inside the server's encryption.
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({
      endpoint: 'https://web.push.apple.com/Q123',
      keys: { p256dh: 'a-key', auth: 'a-secret' },
      platform: 'web',
      label: 'iPhone 14',
    });
  });

  it('reads with GET everywhere else', async () => {
    const { impl, calls } = fakeFetch(() => json({}));
    const c = client(impl);

    await c.candles({ timeframe: '1m' });
    await c.indicators({ timeframe: '1m' });
    await c.signals();
    await c.signal('id');
    await c.outcomes();
    await c.performance();
    await c.status();
    await c.device();

    for (const call of calls) {
      expect(call.init.method).toBe('GET');
    }
    expect(calls).toHaveLength(8);
  });
});

/**
 * TestTheDefaultFetchIsCallable.
 *
 * # What this prevents
 *
 * Every other test here injects a fetch, which means none of them exercises
 * the one line that decides what the shipped app calls. A client built the
 * ordinary way used to capture the global as a bare reference — and
 * `const f = fetch; f(url)` throws "Illegal invocation" in a browser, because
 * fetch needs `this` to be the global.
 *
 * React Native tolerates a detached reference, so this would have shipped
 * working on Android and broken on web, and the failure looks exactly like an
 * unreachable server: the request never leaves.
 */
describe('the default fetch', () => {
  it('is callable when nothing is injected', async () => {
    const original = globalThis.fetch;
    const seen: string[] = [];

    // A global that records the call and, importantly, is only correct when
    // invoked with the right receiver — which is what the real one requires.
    globalThis.fetch = function boundOnly(this: unknown, input: RequestInfo | URL) {
      if (this !== globalThis) {
        throw new TypeError('Illegal invocation');
      }
      seen.push(String(input));
      return Promise.resolve(
        new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
      );
    } as unknown as typeof fetch;

    try {
      await new ApiClient({ baseUrl: BASE }).status();
      expect(seen).toEqual([`${BASE}/api/v1/status`]);
    } finally {
      globalThis.fetch = original;
    }
  });
});

describe('cancellation', () => {
  it('lets the caller abort without it looking like an unreachable server', async () => {
    // A screen that navigates away aborts its request. Reporting that as
    // "cannot reach the server" would flash a Tailscale warning at somebody
    // whose network is fine.
    const controller = new AbortController();
    const { impl } = fakeFetch(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () =>
            reject(new DOMException('Aborted', 'AbortError')),
          );
        }),
    );

    const pending = client(impl).status(controller.signal);
    controller.abort();

    await expect(pending).rejects.not.toBeInstanceOf(ApiError);
  });
});
