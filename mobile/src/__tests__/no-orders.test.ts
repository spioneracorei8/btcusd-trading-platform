import fs from 'fs';
import path from 'path';

/**
 * The app cannot place an order, and cannot look like it can.
 *
 * # Why this is a test and not a note in the README
 *
 * `CLAUDE.md` §1 says this system never places an order under any
 * circumstance, and `server/architecture_test.go` holds that on the server by
 * failing on any import that reaches a trading endpoint. This is the same rule
 * one layer up, and here it is as much about the interface as the code:
 *
 *   "No execute button, no broker link, no size calculator ending in a confirm
 *    step. The gap between showing a signal and placing an order should stay
 *    wide enough that nobody crosses it by muscle memory."
 *
 * A phone is where that gap is narrowest. The screen shows a direction, an
 * entry and a stop, and the person looking at it is one tap from their broker
 * either way — what must never happen is that the tap is *in this app*.
 *
 * So this checks two things a review would have to catch by reading: no code
 * that could reach a trading endpoint, and no wording that would make a
 * reader think a button here does anything to a position.
 */

const SRC = path.join(__dirname, '..');

/** Every source file, this test excluded. */
function sourceFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(full);
    if (!/\.tsx?$/.test(entry.name)) return [];
    if (full === __filename) return [];
    return [full];
  });
}

function read(file: string): string {
  return fs.readFileSync(file, 'utf8');
}

/** The comment stripped out, so a file explaining the rule does not break it. */
function code(body: string): string {
  return body
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '');
}

describe('nothing here can place an order', () => {
  const files = sourceFiles(SRC);

  it('finds the sources it is supposed to be checking', () => {
    // A walk that silently returned nothing would pass every assertion below.
    expect(files.length).toBeGreaterThan(0);
  });

  it('calls no endpoint that could place, amend or cancel an order', () => {
    // The paths Binance and every other venue put behind an API key. The app
    // talks to this project's own API over the tailnet and to nothing else,
    // so any of these appearing is either a mistake or a deliberate act.
    const endpoints = [
      /\/api\/v3\/order/i,
      /\/fapi\/v\d\/order/i,
      /\/sapi\/v\d\/(?:margin|futures)/i,
      /\/api\/v3\/account/i,
      /\/sapi\/v\d\/capital\/withdraw/i,
      /binance\.com/i,
      /\bwithdraw\b/i,
    ];

    for (const file of files) {
      const body = code(read(file));
      for (const pattern of endpoints) {
        expect({ file: path.relative(SRC, file), match: pattern.exec(body)?.[0] })
          .toEqual({ file: path.relative(SRC, file), match: undefined });
      }
    }
  });

  it('holds no API key, secret or signature', () => {
    // Market data is public and this project's API has no authentication
    // (ADR 0024). A credential in the app would have to be for a venue.
    const credentials = [
      /\bapiKey\b/i,
      /\bapiSecret\b/i,
      /\bsecretKey\b/i,
      /X-MBX-APIKEY/i,
      /\bhmac\b/i,
      /\bsignature\b/i,
    ];

    for (const file of files) {
      const body = code(read(file));
      for (const pattern of credentials) {
        expect({ file: path.relative(SRC, file), match: pattern.exec(body)?.[0] })
          .toEqual({ file: path.relative(SRC, file), match: undefined });
      }
    }
  });

  it('shows no control that reads as acting on a position', () => {
    // Wording, not code. A button saying "Buy" does nothing on its own, and
    // that is exactly the problem: it teaches the hand a gesture the app does
    // not honour, on a screen where the next app in the stack does.
    //
    // These are matched as user-facing strings — quoted text — rather than as
    // identifiers, so `direction === 'long'` is untouched and a <Button
    // title="Buy" /> is not.
    const forbidden = [
      /["'`]\s*(?:buy|sell|long|short)\s+now\s*["'`]/i,
      /["'`]\s*(?:place|submit|send|execute|confirm)\s+(?:the\s+)?(?:order|trade|position)\s*["'`]/i,
      /["'`]\s*(?:open|close)\s+(?:the\s+)?position\s*["'`]/i,
      /["'`]\s*execute\s*["'`]/i,
      /["'`]\s*trade\s+(?:it|this|now)\s*["'`]/i,
    ];

    for (const file of files) {
      const body = read(file);
      for (const pattern of forbidden) {
        expect({ file: path.relative(SRC, file), match: pattern.exec(body)?.[0] })
          .toEqual({ file: path.relative(SRC, file), match: undefined });
      }
    }
  });

  it('sends nothing to the API but a device registration', () => {
    // The app is a reader. The one write it makes is POST /api/v1/device,
    // which records where alerts go — see ADR 0026. Any other method that
    // changes state is either editing a signal or an outcome, which the app
    // must not do, or talking to something that is not this API.
    //
    // Test files are excluded from THIS check only: a test asserting that a
    // request used POST is not the app making one. Every other check above
    // still covers them, because a test reaching a trading endpoint would be
    // as real as a screen doing it.
    const writes: { file: string; line: string }[] = [];

    for (const file of files.filter((f) => !/\.test\.tsx?$/.test(f))) {
      code(read(file))
        .split('\n')
        .forEach((line) => {
          if (!/\b(?:POST|PUT|PATCH|DELETE)\b/.test(line)) return;
          if (/\/device\b/.test(line)) return; // the one permitted write
          writes.push({ file: path.relative(SRC, file), line: line.trim() });
        });
    }

    expect(writes).toEqual([]);
  });
});
