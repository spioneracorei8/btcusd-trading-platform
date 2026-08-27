/**
 * Rendering values for a person to read.
 *
 * # Nothing here computes
 *
 * Every number in this app arrives already computed by the server. These
 * functions choose how to display one; none of them derives one. A win rate
 * calculated on the phone would be a second implementation of a figure that
 * already exists, and the two would drift — the phase 08 lesson about wire
 * shapes, one layer up.
 *
 * Prices in particular are strings on the wire and stay strings here. They are
 * grouped and trimmed for reading and never parsed into a float.
 */

/**
 * A price, grouped and with its trailing zeros trimmed.
 *
 * String in, string out. numeric(20,8) does not fit a float64, and the moment
 * this parsed one it would be rounding the number it was asked to display.
 */
export function price(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return '—';

  const negative = value.startsWith('-');
  const bare = negative ? value.slice(1) : value;
  const [whole = '0', fraction] = bare.split('.');

  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const trimmed = fraction?.replace(/0+$/, '');

  return `${negative ? '-' : ''}${grouped}${trimmed ? `.${trimmed}` : ''}`;
}

/** A percentage that arrived as a string, with an explicit sign. */
export function percent(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return '—';

  const trimmed = value.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '');
  return `${trimmed.startsWith('-') ? '' : '+'}${trimmed}%`;
}

/** A rate the server sent as a fraction, shown as a whole percentage. */
export function rate(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—';
  return `${(value * 100).toFixed(1)}%`;
}

export function count(value: number | null | undefined): string {
  if (value === null || value === undefined) return '—';
  return value.toLocaleString('en-GB');
}

/**
 * How long ago, in units somebody can act on.
 *
 * Coarse on purpose. "3 minutes ago" and "3 minutes and 12 seconds ago" lead
 * to the same decision, and the second invites a reader to believe the clocks
 * agree to the second.
 */
export function ago(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return 'never';

  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return 'unknown';

  return duration((now.getTime() - then.getTime()) / 1000) + ' ago';
}

/** A span of seconds, in the largest unit that still says something. */
export function duration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return 'unknown';

  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;

  const minutes = Math.floor(s / 60);
  if (minutes < 60) return `${minutes}m`;

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;

  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ${hours % 24}h`;

  const months = Math.floor(days / 30);
  if (months < 24) return `${months} months`;

  return `${(days / 365).toFixed(1)} years`;
}

/** A UTC instant, rendered as UTC. The server is UTC end to end and a phone
 * in another zone comparing this against a log would otherwise be out. */
export function utc(iso: string | null | undefined): string {
  if (!iso) return '—';

  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return 'unknown';

  return at.toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z');
}

/** Whichever is the useful half of a signal's two prices, labelled. */
export function directionLabel(direction: string): string {
  return direction.toUpperCase();
}
