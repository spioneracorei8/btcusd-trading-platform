/**
 * What arrives in a notification, and what it means.
 *
 * The server composes this in notify.BuildMessage. The fields mirror it, and
 * one of them needs saying out loud.
 */

export type SignalPayload = {
  signal_id?: string;
  symbol?: string;
  timeframe?: string;
  direction?: string;

  /**
   * The close the strategy decided on — NOT the entry price.
   *
   * Phase 07 made that distinction deliberately: a decision taken on a bar's
   * close cannot also fill on it, so the fill is the next bar's open plus
   * slippage and the two differ by roughly the slippage every time.
   *
   * The server's own body text says "ref". The UI must not relabel it: a
   * person comparing an alert against a chart would be off by a systematic
   * amount with no way to know.
   */
  signal_price?: string;

  stop_loss?: string;
  take_profit?: string;
  trigger?: string;
};

/** The label this price gets, everywhere it is shown. */
export const REFERENCE_PRICE_LABEL = 'reference price';

/** The signal a notification points at, if it points at one. */
export function signalIdOf(data: unknown): string | undefined {
  if (typeof data !== 'object' || data === null) return undefined;

  const id = (data as SignalPayload).signal_id;
  // A uuid, or nothing. Anything else is a payload this build does not
  // understand, and navigating on it would land on a detail screen that 404s.
  return typeof id === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)
    ? id
    : undefined;
}
