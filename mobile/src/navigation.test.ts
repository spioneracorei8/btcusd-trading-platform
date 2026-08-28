import { getStateFromPath, getPathFromState } from '@react-navigation/native';

import { linking } from './navigation';

const config = linking.config;

/** The path a URL resolves to, as the screen names it reaches. */
function screensFor(path: string): string[] {
  const state = getStateFromPath(path, config);
  const names: string[] = [];

  let level = state as { routes?: { name: string; state?: unknown }[] } | undefined;
  while (level?.routes?.length) {
    const route = level.routes[level.routes.length - 1]!;
    names.push(route.name);
    level = route.state as typeof level;
  }
  return names;
}

/** The parameters the deepest screen was handed. */
function paramsFor(path: string): Record<string, unknown> | undefined {
  const state = getStateFromPath(path, config);

  let level = state as
    | { routes?: { name: string; params?: Record<string, unknown>; state?: unknown }[] }
    | undefined;
  let params: Record<string, unknown> | undefined;
  while (level?.routes?.length) {
    const route = level.routes[level.routes.length - 1]!;
    params = route.params ?? params;
    level = route.state as typeof level;
  }
  return params;
}

/*
TestASignalURLOpensThatSignal.

# What this prevents

This is the whole reason the app has URLs. A service worker's only move on a
notification click is to open one, so with every screen at "/" there is nowhere
to send it — tapping an alert would land on the dashboard, and the signal it
was about would be three taps away at a moment when somebody is looking at
their phone precisely because something happened.

It is also the one path a person reaches without the app already running, so it
is the one that must survive a cold load.
*/
describe('the URL a notification opens', () => {
  const id = '3f2504e0-4f89-11d3-9a0c-0305e82c3301';

  it('reaches the signal detail screen', () => {
    expect(screensFor(`/signals/${id}`)).toEqual(['Signals', 'SignalDetail']);
  });

  it('carries the id, so the screen knows which signal', () => {
    expect(paramsFor(`/signals/${id}`)).toEqual({ id });
  });

  it('is what the app produces for that screen, so a share is a working link', () => {
    const path = getPathFromState(
      {
        routes: [
          {
            name: 'Signals',
            state: { routes: [{ name: 'SignalDetail', params: { id } }] },
          },
        ],
      },
      config,
    );
    expect(path).toBe(`/signals/${id}`);
  });
});

/*
TestEveryTabHasItsOwnURL.

A standalone PWA reloads — iOS evicts backgrounded web apps freely — and a
relaunch that lands somewhere other than where somebody was is the kind of
small wrongness that makes an app feel broken. It also makes the browser's
history usable rather than five entries that all say the same thing.
*/
describe('each tab', () => {
  it.each([
    ['/', 'Now'],
    ['/signals', 'SignalsList'],
    ['/chart', 'Chart'],
    ['/performance', 'Performance'],
    ['/status', 'Status'],
  ])('%s opens %s', (path, screen) => {
    expect(screensFor(path)).toContain(screen);
  });

  it('does not resolve a path that is not a screen', () => {
    // The server hands an unknown path the entry document on purpose, so this
    // is where a nonsense URL actually lands. Returning undefined is what makes
    // React Navigation fall back to the initial route rather than render blank.
    expect(getStateFromPath('/not-a-screen', config)).toBeUndefined();
  });
});
