/**
 * @jest-environment jsdom
 */
import { register, supported } from './register';

type Listener = () => void;

/** A ServiceWorker whose state a test can drive. */
class FakeWorker {
  state: string;
  posted: unknown[] = [];
  private listeners: Listener[] = [];

  constructor(state: string) {
    this.state = state;
  }

  addEventListener(_type: string, listener: Listener) {
    this.listeners.push(listener);
  }

  postMessage(message: unknown) {
    this.posted.push(message);
  }

  /** Drives the install through to done, as the browser would. */
  finishInstalling() {
    this.state = 'installed';
    for (const listener of this.listeners) listener();
  }
}

class FakeRegistration {
  installing: FakeWorker | null = null;
  waiting: FakeWorker | null = null;
  private found: Listener[] = [];

  addEventListener(type: string, listener: Listener) {
    if (type === 'updatefound') this.found.push(listener);
  }
  removeEventListener() {}

  /** A new worker appears and starts installing. */
  startInstalling(worker: FakeWorker) {
    this.installing = worker;
    for (const listener of this.found) listener();
  }
}

function install({ controller }: { controller: boolean }) {
  const registration = new FakeRegistration();
  const container = {
    controller: controller ? {} : null,
    register: jest.fn(async () => registration),
    addEventListener: jest.fn(),
    removeEventListener: jest.fn(),
  };

  Object.defineProperty(globalThis, 'isSecureContext', {
    value: true,
    configurable: true,
  });
  Object.defineProperty(globalThis.navigator, 'serviceWorker', {
    value: container,
    configurable: true,
  });

  return { registration, container };
}

afterEach(() => {
  Object.defineProperty(globalThis.navigator, 'serviceWorker', {
    value: undefined,
    configurable: true,
  });
});

/*
TestAFirstInstallIsNotAnnouncedAsAnUpdate.

# What this prevents

The very first load installs a worker too, and it is not an update — there is
no previous build and nothing to reload onto. Announcing "a newer version is
installed" there would mean the banner appears on first launch, every time,
saying something untrue. A banner that is usually wrong is one nobody reads,
which costs the real update its only way of being noticed.

`controller` is what tells the two apart: it is null until a worker is actually
running the page.
*/
describe('the first time the app is opened', () => {
  it('installs a worker without claiming an update is waiting', async () => {
    const { registration } = install({ controller: false });

    const seen: string[] = [];
    await register((state) => seen.push(state));

    registration.startInstalling(new FakeWorker('installing'));
    registration.installing?.finishInstalling();

    expect(seen).toEqual([]);
  });
});

/*
TestANewBuildIsAnnouncedWhileThePageIsOpen.

The case the banner exists for. A worker that will not update is the classic
PWA failure — the app sits on an old build indefinitely, and the only symptom
is that nothing new ever appears, which is exactly what this system looks like
on a normal day.
*/
describe('when a new build installs', () => {
  it('is announced once it has finished installing', async () => {
    const { registration } = install({ controller: true });

    const seen: string[] = [];
    await register((state) => seen.push(state));

    const worker = new FakeWorker('installing');
    registration.startInstalling(worker);
    expect(seen).toEqual([]);

    worker.finishInstalling();
    expect(seen).toEqual(['ready']);
  });

  it('is announced when it was already waiting before the page loaded', async () => {
    const { registration } = install({ controller: true });
    registration.waiting = new FakeWorker('installed');

    const seen: string[] = [];
    await register((state) => seen.push(state));

    expect(seen).toEqual(['ready']);
  });
});

/*
TestApplyingAnUpdateTellsTheWorkerRatherThanJustReloading.

A reload alone leaves the old worker in control and the new build still
waiting: the page comes back on the same version it was on, and the banner
returns. The message is what makes the new worker take over, and the reload
then follows from `controllerchange`.
*/
describe('taking the update', () => {
  it('asks the waiting worker to activate', async () => {
    const { registration } = install({ controller: true });
    const waiting = new FakeWorker('installed');
    registration.waiting = waiting;

    const registered = await register(() => {});
    registered?.apply();

    expect(waiting.posted).toEqual(['apply-update']);
  });
});

/*
TestThereIsNothingToRegisterWithoutASecureContext.

A service worker needs one. Returning undefined rather than throwing is what
lets the caller tell "not applicable" — a native build, or plain HTTP — from
"tried and failed", and the app is fully usable either way.
*/
describe('where there is no service worker', () => {
  it('reports the runtime as unsupported', async () => {
    Object.defineProperty(globalThis.navigator, 'serviceWorker', {
      value: undefined,
      configurable: true,
    });

    expect(supported()).toBe(false);
    await expect(register(() => {})).resolves.toBeUndefined();
  });

  it('is unsupported on an insecure origin even with the API present', async () => {
    install({ controller: false });
    Object.defineProperty(globalThis, 'isSecureContext', {
      value: false,
      configurable: true,
    });

    expect(supported()).toBe(false);
    await expect(register(() => {})).resolves.toBeUndefined();
  });
});
