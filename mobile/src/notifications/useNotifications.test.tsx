/**
 * @jest-environment jsdom
 */
import { act, renderHook, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

import { ApiClient } from '../api/client';
import { ApiProvider } from '../api/provider';
import { useNotifications } from './useNotifications';

const BASE = 'http://100.72.14.3:8080';
const VAPID_KEY = 'BDLFBrIHg9mGNteU0m9p-FKeovhMbMUR4dBwQf3kd1P7LtzaQ4qtDFr66_2fG2835RU7WcSSOSv5lwdTKjWFl1g';

/** A PushSubscription the browser would hand over. */
function fakeSubscription(endpoint: string, key?: ArrayBuffer) {
  return {
    endpoint,
    options: { applicationServerKey: key },
    toJSON: () => ({ endpoint, keys: { p256dh: 'a-public-key', auth: 'an-auth-secret' } }),
    unsubscribe: jest.fn(async () => true),
  };
}

/**
 * Installs the browser globals the push path needs, and returns handles on
 * them.
 *
 * jsdom has none of these: no Notification, no PushManager, no
 * serviceWorker.ready. Which is convenient, because their *absence* is the
 * case that matters most on iOS.
 */
function installPushAPI({
  permission = 'granted',
  existing,
}: {
  permission?: NotificationPermission;
  existing?: ReturnType<typeof fakeSubscription>;
} = {}) {
  const created: { endpoint: string }[] = [];
  const requested: string[] = [];

  const pushManager = {
    getSubscription: jest.fn(async () => existing ?? null),
    subscribe: jest.fn(async ({ applicationServerKey }: { applicationServerKey: ArrayBuffer }) => {
      const made = fakeSubscription('https://web.push.apple.com/QNew', applicationServerKey);
      created.push(made);
      return made;
    }),
  };

  Object.defineProperty(globalThis, 'isSecureContext', { value: true, configurable: true });
  Object.defineProperty(globalThis, 'PushManager', { value: class {}, configurable: true });
  Object.defineProperty(globalThis, 'Notification', {
    value: {
      permission,
      requestPermission: jest.fn(async () => {
        requested.push('asked');
        return permission;
      }),
    },
    configurable: true,
  });
  Object.defineProperty(globalThis.navigator, 'serviceWorker', {
    value: {
      ready: Promise.resolve({ pushManager }),
      addEventListener: jest.fn(),
      removeEventListener: jest.fn(),
    },
    configurable: true,
  });

  return { pushManager, created, requested };
}

function removePushAPI() {
  for (const name of ['PushManager', 'Notification'] as const) {
    Object.defineProperty(globalThis, name, { value: undefined, configurable: true });
  }
  Object.defineProperty(globalThis.navigator, 'serviceWorker', {
    value: undefined,
    configurable: true,
  });
}

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
  removePushAPI();
});

/**
 * A response the client can read.
 *
 * Not `new Response(...)`: this file runs under jsdom, because that is where
 * the browser globals the push path needs can be installed — and jsdom has no
 * fetch API, so `Response` is undefined there. The client uses `.ok`,
 * `.status` and `.text()`, which is all of this.
 */
function json(body: unknown) {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

function renderNotifications({ vapidKey = VAPID_KEY }: { vapidKey?: string } = {}) {
  /** Every subscription the app posted, in order. */
  const registered: { endpoint: string; keys: { p256dh: string; auth: string } }[] = [];

  const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'POST') {
      registered.push(JSON.parse(String(init.body)));
    }
    const last = registered[registered.length - 1];
    return json({
      registered: last !== undefined,
      endpoint: last ? 'web.push.apple.com/QNew…' : undefined,
      platform: 'web',
      delivery_mode: 'notify',
      vapid_public_key: vapidKey,
      note: '',
    });
  }) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  // Built once: constructing it inline hands the provider a new client on
  // every render, which changes `register`, which re-runs the effect calling it.
  const client = new ApiClient({ baseUrl: BASE, fetchImpl });

  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={client}>{children}</ApiProvider>
    </QueryClientProvider>
  );

  const view = renderHook(() => useNotifications(), { wrapper });
  return { ...view, registered };
}

/*
TestASubscriptionIsPostedOnEveryLaunch.

# What this prevents

A push subscription is not permanent. The push service expires them, a
reinstall produces a new one, and clearing site data destroys the old one
without telling anybody. A deployment holding the previous one fails every send
with 410 Gone, which the phase 07 delivery worker correctly treats as permanent
and gives up on — so alerts stop, silently, and the symptom is a strategy that
looks like it went quiet. Which is what this system looks like on a normal day.

FCM had a token-refresh event to listen for. The web does not: the
`pushsubscriptionchange` event is in the specification and Safari does not fire
it. So a launch is the only reliable moment to check, and checking on every
launch is what replaces the listener. That is why this test is about launch
rather than about an event.
*/
describe('on launch', () => {
  it('subscribes and posts the result', async () => {
    installPushAPI();
    const { registered } = renderNotifications();

    await waitFor(() => expect(registered).toHaveLength(1));
    expect(registered[0]).toEqual({
      endpoint: 'https://web.push.apple.com/QNew',
      keys: { p256dh: 'a-public-key', auth: 'an-auth-secret' },
    });
  });

  it('reuses a subscription made against the same key rather than churning', async () => {
    // The same key the server serves, so nothing needs replacing.
    const existing = fakeSubscription('https://web.push.apple.com/QExisting');
    const { pushManager } = installPushAPI({ existing });

    // getSubscription returns it with the matching key attached.
    existing.options.applicationServerKey = keyBytes();

    const { registered } = renderNotifications();
    await waitFor(() => expect(registered).toHaveLength(1));

    expect(registered[0]!.endpoint).toBe('https://web.push.apple.com/QExisting');
    expect(pushManager.subscribe).not.toHaveBeenCalled();
    expect(existing.unsubscribe).not.toHaveBeenCalled();
  });
});

/*
TestAStaleSubscriptionIsReplacedRatherThanThrowing.

# What this prevents

`pushManager.subscribe` with a *different* applicationServerKey throws
InvalidStateError rather than replacing the existing subscription. That is
exactly the state after a VAPID rotation — and the error arrives at the app,
which can do nothing with it, while the browser holds a subscription that can
never be pushed to again.

So the stale one is unsubscribed first. Without that, rotating the server's key
pair would permanently break alerts on a phone that had already registered, and
the only fix would be reinstalling the app.
*/
describe('after the server rotates its key pair', () => {
  it('discards the old subscription and makes a new one', async () => {
    const stale = fakeSubscription('https://web.push.apple.com/QStale');
    // Subscribed against something else entirely.
    stale.options.applicationServerKey = new Uint8Array([1, 2, 3]).buffer;

    const { pushManager } = installPushAPI({ existing: stale });
    const { registered } = renderNotifications();

    await waitFor(() => expect(registered).toHaveLength(1));

    expect(stale.unsubscribe).toHaveBeenCalled();
    expect(pushManager.subscribe).toHaveBeenCalled();
    expect(registered[0]!.endpoint).toBe('https://web.push.apple.com/QNew');
  });
});

/*
TestNothingIsPostedWithoutAKeyToSubscribeAgainst.

Silent mode configures no VAPID pair, so the server serves no key. Subscribing
against an empty string throws in the browser; posting a subscription made
against nothing would put a row in `devices` that no push could ever reach.
Doing neither is the honest answer, and the alerts card already says why.
*/
describe('against a silent deployment', () => {
  it('does not subscribe at all', async () => {
    installPushAPI();
    const { registered } = renderNotifications({ vapidKey: '' });

    // Long enough for the launch effect to have run.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(registered).toEqual([]);
  });
});

/*
TestPushIsUnavailableInASafariTab.

The iOS case that catches people. Push exists only for a PWA added to the home
screen: in a tab there is no PushManager at all, permission cannot be requested,
and asking resolves to denied with no prompt shown.

The app must not post anything — a registration from a context that can never
receive is a row saying alerts will arrive when they cannot — and it must say
what to do rather than looking broken.
*/
describe('in a browser tab, where iOS grants no push', () => {
  it('registers nothing and says how to install', async () => {
    removePushAPI();
    const { result, registered } = renderNotifications();

    await waitFor(() => expect(result.current.state.willArrive).toBe(false));
    expect(registered).toEqual([]);
    expect(result.current.state.detail).toMatch(/add to home screen/i);
  });

  it('does not prompt, because the prompt would be rejected without appearing', async () => {
    removePushAPI();
    const { result } = renderNotifications();

    await act(async () => {
      await result.current.request();
    });
    // Nothing to assert on but the absence of a throw: there is no
    // Notification global to have called.
    expect(result.current.state.willArrive).toBe(false);
  });
});

/*
TestAskingFollowsAGesture.

iOS silently rejects a permission prompt that does not follow a user gesture,
and the rejection is indistinguishable from a refusal — after which the prompt
can never be shown again. So the app never asks on load; `request` is what a
button calls, and it is the only thing that asks.
*/
describe('asking for permission', () => {
  it('happens only when request() is called', async () => {
    const { requested } = installPushAPI({ permission: 'default' });
    const { result } = renderNotifications();

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(requested).toEqual([]);

    await act(async () => {
      await result.current.request();
    });
    expect(requested).toEqual(['asked']);
  });
});

/** The VAPID key as bytes, matching what push.ts decodes it to. */
function keyBytes(): ArrayBuffer {
  const padded = VAPID_KEY.padEnd(VAPID_KEY.length + ((4 - (VAPID_KEY.length % 4)) % 4), '=');
  const raw = atob(padded.replace(/-/g, '+').replace(/_/g, '/'));
  const bytes = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i += 1) bytes[i] = raw.charCodeAt(i);
  return bytes.buffer;
}
