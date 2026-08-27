import {
  MAX_BARS,
  MINUTES,
  OVERSCAN_BARS,
  VISIBLE_BARS,
  covers,
  tooWide,
  tooWideMessage,
  windowFor,
} from './window';

const END = new Date('2026-08-27T12:00:00Z');

/**
 * TestPanningInsideTheOverscanDoesNotRefetch.
 *
 * "Pan and zoom must not refetch per gesture." A chart issuing a request on
 * every frame of a drag is unusable over a tailnet and rude to a server that
 * answers by scanning an index. The overscan is what buys that, and it only
 * buys it if `covers` is generous enough to say yes while the finger is down.
 */
describe('the loaded window', () => {
  it('reaches a screen either side of what is visible', () => {
    const loaded = windowFor('1h', END);
    const spanBars = (loaded.to.getTime() - loaded.from.getTime()) / (60 * 60_000);

    expect(spanBars).toBe(VISIBLE_BARS + 2 * OVERSCAN_BARS);
    expect(loaded.limit).toBe(VISIBLE_BARS + 2 * OVERSCAN_BARS);
  });

  it('covers a drag that stays inside the overscan', () => {
    const loaded = windowFor('1h', END);

    // Half a screen back — an ordinary flick.
    const from = new Date(END.getTime() - (VISIBLE_BARS + 30) * 60 * 60_000);
    const to = new Date(END.getTime() - 30 * 60 * 60_000);

    expect(covers(loaded, from, to)).toBe(true);
  });

  it('does not cover a drag past the edge of what was loaded', () => {
    const loaded = windowFor('1h', END);
    const from = new Date(loaded.from.getTime() - 60 * 60_000);

    expect(covers(loaded, from, END)).toBe(false);
  });

  it('reports nothing loaded as not covering', () => {
    expect(covers(undefined, END, END)).toBe(false);
  });

  it('scales with the timeframe', () => {
    // The same number of bars at every zoom; the span is what changes.
    for (const timeframe of ['1m', '5m', '1h', '1d'] as const) {
      const loaded = windowFor(timeframe, END);
      const bars =
        (loaded.to.getTime() - loaded.from.getTime()) / (MINUTES[timeframe] * 60_000);
      expect(bars).toBe(VISIBLE_BARS + 2 * OVERSCAN_BARS);
    }
  });

  it('never asks for more than the API returns', () => {
    const loaded = windowFor('1m', END, MAX_BARS * 2);
    expect(loaded.limit).toBeLessThanOrEqual(MAX_BARS);
  });
});

/**
 * TestThreeYearsOfMinuteCandlesIsRefusedClientSide.
 *
 * The API caps the response, so the request would come back truncated and the
 * chart would draw the wrong range without either end saying so. On a chart
 * this is a pinch rather than a typo, so the refusal has to be something the
 * chart handles rather than an error somebody sees once.
 */
describe('a range wider than the API can answer', () => {
  it('is refused for 1m over three years', () => {
    const from = new Date('2023-08-27T00:00:00Z');
    expect(tooWide('1m', from, END)).toBe(true);
  });

  it('is allowed for 1d over the same three years', () => {
    // Roughly 1,100 bars: well inside the cap. The refusal is about bars, not
    // about wall-clock span.
    const from = new Date('2023-08-27T00:00:00Z');
    expect(tooWide('1d', from, END)).toBe(false);
  });

  it('is allowed at exactly the cap and refused one bar past it', () => {
    const atCap = new Date(END.getTime() - MAX_BARS * 60_000);
    const past = new Date(atCap.getTime() - 60_000);

    expect(tooWide('1m', atCap, END)).toBe(false);
    expect(tooWide('1m', past, END)).toBe(true);
  });

  it('says how many bars were asked for and what to do', () => {
    const from = new Date('2023-08-27T00:00:00Z');
    const message = tooWideMessage('1m', from, END);

    expect(message).toMatch(/bars of 1m/);
    expect(message).toMatch(/5,000/);
    expect(message).toMatch(/zoom in|longer timeframe/i);
  });
});
