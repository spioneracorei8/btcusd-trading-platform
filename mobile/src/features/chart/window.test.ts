import {
  MAX_BARS,
  MINUTES,
  OVERSCAN_BARS,
  VISIBLE_BARS,
  covers,
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
  });

  /**
   * The off-by-one that produced a false warning.
   *
   * A range with both ends inclusive holds one more bar than it spans. With
   * the limit set to the span, every window with history on both sides came
   * back one bar short and flagged `truncated` — and the chart dutifully
   * said the window had held more bars than were returned. It had not; the
   * request had asked for one too few.
   */
  it('asks for one more bar than it spans, because both ends are inclusive', () => {
    const loaded = windowFor('1h', END);
    const spanBars = (loaded.to.getTime() - loaded.from.getTime()) / (60 * 60_000);

    expect(loaded.limit).toBe(spanBars + 1);
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
