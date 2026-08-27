import { ago, count, duration, percent, price, rate, utc } from './format';

describe('prices are formatted without being parsed', () => {
  it('keeps every digit of a value a float64 could not hold', () => {
    // The whole reason prices are strings. Parsing this to a number and back
    // loses the tail, and the loss is silent.
    expect(price('64000.12345678')).toBe('64,000.12345678');
    expect(price('0.00000001')).toBe('0.00000001');
    expect(price('99999999999999.99999999')).toBe('99,999,999,999,999.99999999');
  });

  it('groups thousands and trims trailing zeros', () => {
    expect(price('64000.00000000')).toBe('64,000');
    expect(price('1234.50000000')).toBe('1,234.5');
    expect(price('999')).toBe('999');
  });

  it('handles a negative', () => {
    expect(price('-1234.5')).toBe('-1,234.5');
  });

  it('renders an absent price as a dash rather than zero', () => {
    // A price that is not yet known and a price of nothing are different
    // facts, and a zero would be charted and averaged like a real one.
    expect(price(null)).toBe('—');
    expect(price(undefined)).toBe('—');
    expect(price('')).toBe('—');
  });
});

describe('percentages carry their sign', () => {
  it('marks a gain explicitly', () => {
    // Without the +, a column of returns reads as though everything lost.
    expect(percent('1.2345')).toBe('+1.2345%');
    expect(percent('-1.0934')).toBe('-1.0934%');
  });

  it('trims trailing zeros without losing the value', () => {
    expect(percent('0.9000')).toBe('+0.9%');
    expect(percent('1.0000')).toBe('+1%');
  });

  it('renders absent as a dash', () => {
    expect(percent(null)).toBe('—');
  });
});

describe('rates', () => {
  it('renders a fraction as a percentage', () => {
    expect(rate(0.13333333333333333)).toBe('13.3%');
    expect(rate(1)).toBe('100.0%');
  });

  it('renders null as a dash rather than zero', () => {
    // The server sends null when nothing has resolved. A zero would read as a
    // strategy that never wins, which is a different statement.
    expect(rate(null)).toBe('—');
    expect(rate(undefined)).toBe('—');
    expect(rate(NaN)).toBe('—');
  });

  it('renders an actual zero as zero', () => {
    // The counterpart: a strategy that has resolved trades and won none of
    // them is a real 0%, and must not be hidden behind the same dash.
    expect(rate(0)).toBe('0.0%');
  });
});

describe('durations use the largest unit that still says something', () => {
  it.each([
    [0, '0s'],
    [45, '45s'],
    [90, '1m'],
    [3600, '1h 0m'],
    [3660, '1h 1m'],
    [86400, '1d 0h'],
    [86400 * 9, '9d 0h'],
    [86400 * 60, '2 months'],
    [86400 * 900, '2.5 years'],
  ])('renders %i seconds as %s', (seconds, expected) => {
    expect(duration(seconds)).toBe(expected);
  });

  it('says unknown rather than guessing', () => {
    expect(duration(null)).toBe('unknown');
    expect(duration(NaN)).toBe('unknown');
  });

  it('does not render a negative span as a large positive one', () => {
    // Clock skew between the phone and the server puts "3 seconds in the
    // future" on the screen otherwise, which reads as a fault that is not one.
    expect(duration(-5)).toBe('0s');
  });
});

describe('ago', () => {
  const now = new Date('2026-08-27T12:00:00Z');

  it('renders a recent instant in minutes', () => {
    expect(ago('2026-08-27T11:58:00Z', now)).toBe('2m ago');
  });

  it('says never rather than showing the epoch', () => {
    // The status endpoint sends null for "no signal has ever been produced".
    // 1970 on the screen would read as a very old signal.
    expect(ago(null, now)).toBe('never');
  });

  it('says unknown for something it cannot parse', () => {
    expect(ago('not a date', now)).toBe('unknown');
  });
});

describe('instants are shown in UTC', () => {
  it('renders the instant the server meant, whatever zone the phone is in', () => {
    // The server is UTC end to end. A timestamp shown in local time cannot be
    // compared against a log line without arithmetic nobody does correctly at
    // two in the morning.
    expect(utc('2026-08-27T07:03:02.314015Z')).toBe('2026-08-27 07:03:02Z');
  });

  it('renders absent as a dash', () => {
    expect(utc(null)).toBe('—');
  });
});

describe('counts', () => {
  it('groups thousands', () => {
    expect(count(1234567)).toBe('1,234,567');
  });

  it('renders zero as zero and absent as a dash', () => {
    expect(count(0)).toBe('0');
    expect(count(null)).toBe('—');
  });
});
