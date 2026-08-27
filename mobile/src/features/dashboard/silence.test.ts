import { explainSilence } from './silence';
import type { Status } from '../../api/types';

/** A healthy deployment with a warm strategy and nothing to say. */
function healthy(): Status {
  return {
    symbol: 'BTCUSDT',
    market_type: 'spot',
    observed_at: '2026-08-27T12:00:00Z',
    collector: {
      reachable: true,
      state: 'live',
      ws_connected: true,
      started_at: '2026-08-27T09:00:00Z',
      updated_at: '2026-08-27T11:59:58Z',
      heartbeat_age_seconds: 2,
      reconnect_count: 0,
      last_disconnect_note: '',
    },
    evaluator: {
      configured: true,
      strategy: 'ema_crossover',
      timeframe: '4h',
      ready: true,
      reason: '',
      last_signal_at: '2026-08-17T12:00:00Z',
      last_signal_age_seconds: 864000,
      signals_total: 12,
    },
    ingestion: { unfilled_gaps: 0, timeframes: [] },
    outcomes: {
      open: 0,
      oldest_open_at: null,
      oldest_open_age_seconds: null,
      missing_outcome_rows: 0,
    },
    delivery: {
      mode: 'silent',
      pending: 0,
      sent: 0,
      failed: 0,
      last_sent_at: null,
      devices_registered: 0,
    },
    concerns: [],
    note: 'Silence is the normal output of this pipeline.',
  };
}

/**
 * TestSilenceIsLegible.
 *
 * Four different things produce an empty dashboard, and they demand
 * completely different responses. A screen that renders the same emptiness for
 * all four teaches its reader that blank means "nothing happened" — and the
 * day blank means "the collector died three weeks ago", they will not notice.
 */
describe('an empty dashboard says why it is empty', () => {
  it('distinguishes a strategy that is not configured', () => {
    const status = healthy();
    status.evaluator = { ...status.evaluator, configured: false, strategy: '', ready: false };

    const silence = explainSilence(status);
    expect(silence.kind).toBe('not-configured');
    expect(silence.detail).toMatch(/STRATEGY_NAME/);
    // A configuration, not a fault: the screen must not raise an alarm.
    expect(silence.ordinary).toBe(true);
  });

  it('distinguishes a strategy that is still warming up, and says how far', () => {
    const status = healthy();
    status.evaluator = {
      ...status.evaluator,
      ready: false,
      reason: 'the strategy has seen 40 4h bars and needs 200 before it may decide',
    };

    const silence = explainSilence(status);
    expect(silence.kind).toBe('warming-up');
    // The server's own words, not a paraphrase: the number is the useful part.
    expect(silence.detail).toContain('needs 200');
    expect(silence.ordinary).toBe(true);
  });

  it('distinguishes a warm strategy that has found nothing', () => {
    const silence = explainSilence(healthy());

    expect(silence.kind).toBe('no-setup');
    expect(silence.ordinary).toBe(true);
    // The reader has to come away knowing this is normal, or they will keep
    // checking and eventually stop checking.
    expect(silence.detail).toMatch(/ordinary state/i);
  });

  it('distinguishes a collector that has stopped, and does not call it ordinary', () => {
    const status = healthy();
    status.concerns = [
      {
        component: 'collector',
        detail: 'the last heartbeat was 4h12m ago, more than three intervals of 5s',
      },
    ];

    const silence = explainSilence(status);
    expect(silence.kind).toBe('stalled');
    expect(silence.ordinary).toBe(false);
    expect(silence.detail).toContain('4h12m');
  });

  it('distinguishes a deployment that has never run from one that stopped', () => {
    // Different problems, looked into in different places: one is a
    // deployment that was never started, the other is a process that died.
    const status = healthy();
    status.collector = { ...status.collector, reachable: false };

    const silence = explainSilence(status);
    expect(silence.kind).toBe('stalled');
    expect(silence.headline).toMatch(/has ever run/i);
    expect(silence.detail).toMatch(/not been started/i);
  });

  it('reports a stopped collector even when the strategy is also cold', () => {
    // Both are true and only one is worth acting on. A warm-up message on a
    // dead collector would send somebody to wait for something that will
    // never happen.
    const status = healthy();
    status.evaluator = { ...status.evaluator, ready: false, reason: 'still warming' };
    status.concerns = [{ component: 'collector', detail: 'the collector process may be gone' }];

    expect(explainSilence(status).kind).toBe('stalled');
  });

  it('does not treat a delivery concern as a stopped collector', () => {
    // A missing device registration is a real concern and has nothing to do
    // with whether signals are being produced.
    const status = healthy();
    status.concerns = [
      { component: 'delivery', detail: 'no device is registered, so signals will be recorded' },
    ];

    expect(explainSilence(status).kind).toBe('no-setup');
  });

  it('says so plainly before the status has arrived', () => {
    const silence = explainSilence(undefined);
    expect(silence.kind).toBe('unknown');
    expect(silence.ordinary).toBe(false);
  });
});
