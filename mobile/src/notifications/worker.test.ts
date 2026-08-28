import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';

/**
 * Runs public/sw.js and returns handles on what it did.
 *
 * # Why the worker is executed rather than read
 *
 * It is the one file in this app that nothing can import: it runs in a
 * ServiceWorkerGlobalScope, which neither jest environment provides. The
 * temptation is to assert on its source text — and that is what this started
 * as, until a mutation showed the weakness. Replacing the signal id with
 * `undefined` left every `toContain` passing, because the same string still
 * appeared elsewhere in the file.
 *
 * So the globals are fabricated and the handlers are called. The assertions are
 * then about what happens rather than about what is written.
 */
function runWorker() {
  const source = readFileSync(path.join(__dirname, '..', '..', 'public', 'sw.js'), 'utf8')
    // The build stamps these; the placeholders are not valid JavaScript values
    // on their own.
    .replace('__BUILD__', 'test')
    .replace("'__SHELL__'", "'/'");

  const handlers: Record<string, (event: unknown) => void> = {};
  const shown: { title: string; options: Record<string, unknown> }[] = [];
  const opened: string[] = [];

  let windows: unknown[] = [];

  const self = {
    addEventListener: (type: string, handler: (event: unknown) => void) => {
      handlers[type] = handler;
    },
    location: { origin: 'https://btcusd.tail1234.ts.net' },
    registration: {
      showNotification: jest.fn(async (title: string, options: Record<string, unknown>) => {
        shown.push({ title, options });
      }),
    },
    clients: {
      matchAll: jest.fn(async () => windows),
      openWindow: jest.fn(async (url: string) => {
        opened.push(url);
      }),
      claim: jest.fn(async () => {}),
    },
    skipWaiting: jest.fn(async () => {}),
  };

  const caches = {
    open: jest.fn(async () => ({ add: jest.fn(), put: jest.fn() })),
    keys: jest.fn(async () => []),
    delete: jest.fn(async () => true),
    match: jest.fn(async () => undefined),
  };

  vm.runInNewContext(source, { self, caches, fetch: jest.fn(), URL, console, Error, Promise, JSON });

  return {
    shown,
    opened,
    /** Puts an already-open window in front of the worker. */
    withOpenWindow() {
      const client = { focus: jest.fn(async () => {}), postMessage: jest.fn() };
      windows = [client];
      return client;
    },
    /** Fires an event and waits for whatever it passed to waitUntil. */
    async fire(type: string, event: Record<string, unknown>) {
      const waited: Promise<unknown>[] = [];
      handlers[type]!({ ...event, waitUntil: (p: Promise<unknown>) => waited.push(p) });
      await Promise.all(waited);
    },
  };
}

const signalId = '3f2504e0-4f89-11d3-9a0c-0305e82c3301';

/** What the server actually sends: see webpush/wire.go and notify/sender.go. */
function pushEvent(payload: unknown) {
  return { data: { json: () => payload } };
}

/*
TestAPushBecomesTheNotificationTheOwnerReads.

The numbers in the alert are the ones phase 07 was careful about: the price
quoted is a reference, not an entry, and the UI must not relabel it. This checks
that what the server sent is what is displayed, rather than being dropped in
favour of a generic "you have a signal".
*/
describe('a push', () => {
  it('shows the title and body the server sent', async () => {
    const worker = runWorker();

    await worker.fire(
      'push',
      pushEvent({
        title: 'BTCUSDT 4h LONG',
        body: 'ref 30,200 · stop 29,900 · target 30,500',
        data: { signal_id: signalId },
      }),
    );

    expect(worker.shown).toHaveLength(1);
    expect(worker.shown[0]!.title).toBe('BTCUSDT 4h LONG');
    expect(worker.shown[0]!.options.body).toBe('ref 30,200 · stop 29,900 · target 30,500');
    expect(worker.shown[0]!.options.data).toEqual({ signal_id: signalId });
  });

  /*
   * userVisibleOnly is not advisory. A push handler that ends without showing
   * anything has broken that promise, and browsers answer with their own "this
   * site was updated in the background" notice — or, after enough of them, by
   * revoking the subscription. Revocation looks exactly like every other
   * "subscription gone" failure and is fixed only by reinstalling the app.
   */
  it('still shows something when the payload will not decode', async () => {
    const worker = runWorker();

    await worker.fire('push', {
      data: {
        json: () => {
          throw new Error('not JSON');
        },
      },
    });

    expect(worker.shown).toHaveLength(1);
    expect(worker.shown[0]!.title).toBeTruthy();
  });

  it('still shows something when there is no payload at all', async () => {
    const worker = runWorker();

    await worker.fire('push', { data: null });

    expect(worker.shown).toHaveLength(1);
  });

  /*
   * The delivery worker retries, and a retry landing after the first attempt
   * succeeded would otherwise stack a second identical alert.
   */
  it('collapses onto the previous alert for the same signal', async () => {
    const worker = runWorker();

    await worker.fire('push', pushEvent({ title: 'a', body: 'b', data: { signal_id: signalId } }));

    expect(worker.shown[0]!.options.tag).toBe(signalId);
  });
});

/*
TestTappingAnAlertReachesThatSignal.

The whole reason the app has URLs. With nothing open this is a cold load
straight at /signals/{id}; with the app already running it is a message, because
focusing a window does not navigate it.

This is the assertion a source-text check could not make: replacing the id with
undefined left every `toContain` passing, because the same string appears
elsewhere in the file.
*/
describe('tapping an alert', () => {
  it('opens that signal when nothing is running', async () => {
    const worker = runWorker();
    const close = jest.fn();

    await worker.fire('notificationclick', {
      notification: { close, data: { signal_id: signalId } },
    });

    expect(close).toHaveBeenCalled();
    expect(worker.opened).toEqual([`/signals/${signalId}`]);
  });

  it('focuses the running app rather than opening a second window', async () => {
    const worker = runWorker();
    const client = worker.withOpenWindow();

    await worker.fire('notificationclick', {
      notification: { close: jest.fn(), data: { signal_id: signalId } },
    });

    expect(client.focus).toHaveBeenCalled();
    expect(client.postMessage).toHaveBeenCalledWith({ type: 'open-signal', signalId });
    expect(worker.opened).toEqual([]);
  });

  it('opens the app at all when the alert carries no signal', async () => {
    const worker = runWorker();

    await worker.fire('notificationclick', {
      notification: { close: jest.fn(), data: {} },
    });

    expect(worker.opened).toEqual(['/']);
  });
});
