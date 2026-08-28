import { readFileSync } from 'node:fs';
import path from 'node:path';

import { signalIdOf } from './payload';

const worker = readFileSync(path.join(__dirname, '..', '..', 'public', 'sw.js'), 'utf8');
const wireGo = readFileSync(
  path.join(__dirname, '..', '..', '..', 'server', 'services', 'notify', 'repository', 'webpush', 'wire.go'),
  'utf8',
);

/*
TestBothEndsOfThePushPayloadAgree.

# What this prevents

The push payload crosses a boundary nothing type-checks. Go marshals it in
wire.go, the push service forwards ciphertext neither end can inspect, and
public/sw.js reads it back out with `event.data.json()`. Rename a field on one
side and nothing fails: the send succeeds, the notification arrives, and it
says "BTCUSDT signal — Open the app to see it" instead of the direction, the
reference price, the stop and the target. Tapping it opens the dashboard rather
than the signal.

That failure needs a real phone and a real signal to notice, which is roughly
once every ten days.

So both files are read and the field names compared. It is coarse — it matches
text rather than parsing either language — and it is the only thing standing
between a rename and a silent regression.

What the *worker* does with those fields is checked by executing it; see
worker.test.ts. This file covers only the half that spans two languages, where
nothing can be executed together.
*/
describe('the push payload', () => {
  it.each(['title', 'body', 'data'])('field %s is sent by the server', (field) => {
    expect(wireGo).toContain(`json:"${field}"`);
  });

  it.each(['title', 'body', 'data'])('field %s is read by the service worker', (field) => {
    expect(worker).toMatch(new RegExp(`payload\\.${field}`));
  });

  it('is parsed the same way the in-app payload reader parses it', () => {
    // signalIdOf is what the app uses for a payload it receives directly.
    // Both must accept the same key, or a tap works from one path and not the
    // other.
    expect(signalIdOf({ signal_id: '3f2504e0-4f89-11d3-9a0c-0305e82c3301' })).toBe(
      '3f2504e0-4f89-11d3-9a0c-0305e82c3301',
    );
  });
});
