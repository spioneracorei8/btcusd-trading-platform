import { act, renderHook, waitFor } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

import { ApiClient } from '../api/client';
import { ApiProvider } from '../api/provider';
import { useNotifications } from './useNotifications';

const BASE = 'http://100.72.14.3:8080';

/** Mutable so a test can put the hook on an emulator. Named `mock*` because
 *  jest hoists the factory above every other declaration in this file. */
/**
 * Mutable so a test can put the hook on an emulator.
 *
 * Read through getters rather than returned as an object, because the import
 * is hoisted above these declarations: a factory that returns `mockDeviceState`
 * directly runs while it is still in the temporal dead zone. The getters are
 * only called later, by which time it exists. `__esModule` keeps babel from
 * copying the properties into a fresh object and freezing their values.
 */
const mockDeviceState = { isDevice: true, modelName: 'Pixel 7' };
jest.mock('expo-device', () => ({
  __esModule: true,
  get isDevice() {
    return mockDeviceState.isDevice;
  },
  get modelName() {
    return mockDeviceState.modelName;
  },
}));

/** The token FCM hands out next. Rotating it is the case under test. */
let mockToken = 'token-one';

/** The callback the hook hands to `addPushTokenListener`, so a test can fire
 *  a rotation the way FCM would. */
let mockOnRotate: (() => void) | undefined;
let mockOnResponse: ((response: unknown) => void) | undefined;

jest.mock('expo-notifications', () => ({
  __esModule: true,
  getPermissionsAsync: jest.fn(async () => ({ status: 'granted' })),
  requestPermissionsAsync: jest.fn(async () => ({ status: 'granted' })),
  getDevicePushTokenAsync: jest.fn(async () => ({ data: mockToken })),
  addPushTokenListener: jest.fn((listener: () => void) => {
    mockOnRotate = listener;
    return { remove: jest.fn() };
  }),
  addNotificationResponseReceivedListener: jest.fn((listener: (r: unknown) => void) => {
    mockOnResponse = listener;
    return { remove: jest.fn() };
  }),
}));

const clients: QueryClient[] = [];
afterEach(() => {
  while (clients.length > 0) {
    const client = clients.pop();
    client?.clear();
    client?.unmount();
  }
  mockDeviceState.isDevice = true;
  mockToken = 'token-one';
  mockOnRotate = undefined;
  mockOnResponse = undefined;
});

function json(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function renderNotifications(onOpenSignal?: (id: string) => void) {
  /** Every token the app posted, in order. */
  const registered: string[] = [];

  const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'POST') {
      registered.push(JSON.parse(String(init.body)).token);
    }

    const token = registered[registered.length - 1];
    return json({
      registered: token !== undefined,
      token: token?.slice(0, 6),
      platform: 'android',
      label: 'Pixel 7',
      delivery_mode: 'notify',
      note: '',
    });
  }) as unknown as typeof fetch;

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(queryClient);

  // Built once, outside the wrapper. Constructing it inline would hand the
  // provider a new client on every render, which changes `register`, which
  // re-runs the effect that calls it.
  const client = new ApiClient({ baseUrl: BASE, fetchImpl });

  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <ApiProvider client={client}>{children}</ApiProvider>
    </QueryClientProvider>
  );

  const view = renderHook(() => useNotifications({ onOpenSignal }), { wrapper });
  return { ...view, registered };
}

/**
 * TestARotatedTokenIsSentToTheServer.
 *
 * # What this prevents
 *
 * FCM rotates a token whenever it likes, and the phase 07 delivery worker
 * treats the resulting UNREGISTERED as permanent — correctly, because retrying
 * a dead token forever is worse. So a deployment holding the previous token
 * stops delivering and says nothing. The symptom is a strategy that appears to
 * have gone quiet, which is also what a working system looks like most days.
 *
 * The listener is the only thing standing between those two states, and until
 * this test nothing exercised it. A real rotation still needs a phone; what is
 * pinned here is that the listener is wired to the post and that the post
 * carries the new token rather than the one already on file.
 */
describe('when FCM rotates the token', () => {
  it('sends the new one, without waiting for a relaunch', async () => {
    const { registered } = renderNotifications();

    await waitFor(() => expect(registered).toEqual(['token-one']));

    mockToken = 'token-two';
    await act(async () => {
      mockOnRotate?.();
    });

    await waitFor(() => expect(registered).toEqual(['token-one', 'token-two']));
  });

  it('subscribes to rotations at all', () => {
    renderNotifications();
    expect(mockOnRotate).toBeDefined();
  });
});

describe('registration on launch', () => {
  it('posts the token once permission is in hand', async () => {
    const { result, registered } = renderNotifications();

    await waitFor(() => expect(registered).toEqual(['token-one']));
    await waitFor(() => expect(result.current.state.willArrive).toBe(true));
  });

  /**
   * An emulator has no FCM token to give. Posting a placeholder would put a
   * row in `devices` that no phone is behind, and the status screen would
   * then report a registered device while nothing could receive one.
   */
  it('sends nothing from an emulator, and says why', async () => {
    mockDeviceState.isDevice = false;
    const { result, registered } = renderNotifications();

    await waitFor(() => expect(result.current.permission).toBe('granted'));
    expect(registered).toEqual([]);
    expect(result.current.state.willArrive).toBe(false);
    expect(`${result.current.state.headline} ${result.current.state.detail}`).toMatch(
      /emulator/i,
    );
  });
});

describe('tapping an alert', () => {
  it('opens the signal the payload names', async () => {
    const opened: string[] = [];
    renderNotifications((id) => opened.push(id));

    await waitFor(() => expect(mockOnResponse).toBeDefined());
    await act(async () => {
      mockOnResponse?.({
        notification: {
          request: {
            content: { data: { signal_id: '3f2504e0-4f89-11d3-9a0c-0305e82c3301' } },
          },
        },
      });
    });

    expect(opened).toEqual(['3f2504e0-4f89-11d3-9a0c-0305e82c3301']);
  });
});
