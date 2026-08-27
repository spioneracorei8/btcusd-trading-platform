import type { Status } from '../../api/types';

/**
 * Why the screen is showing nothing.
 *
 * # The problem this solves
 *
 * Normal output on a 4h strategy is roughly one signal every ten days. So the
 * dashboard is blank almost all of the time, and blank has at least four
 * causes that demand completely different responses:
 *
 *   - no strategy is configured, so nothing is being evaluated at all
 *   - a strategy is configured and still warming up
 *   - a strategy is warm and has found no setup — the healthy case
 *   - the collector has stopped, so nothing is even being looked at
 *
 * A screen that renders the same emptiness for all four is worse than useless:
 * it teaches its reader that blank means "nothing happened", and the day blank
 * means "the collector died three weeks ago" they will not notice.
 *
 * `/api/v1/status` exists to answer this, and this function is where that
 * answer is turned into a sentence.
 */
export type Silence = {
  /** What is actually going on, for branching and for tests. */
  kind: 'not-configured' | 'warming-up' | 'no-setup' | 'stalled' | 'unknown';
  headline: string;
  detail: string;
  /** True when this is the healthy case, so the screen can stay quiet about
   * it rather than dressing normality up as a problem. */
  ordinary: boolean;
};

export function explainSilence(status: Status | undefined): Silence {
  if (!status) {
    return {
      kind: 'unknown',
      headline: 'Waiting for the pipeline status',
      detail: 'Until this answers there is no way to tell a quiet market from a stopped one.',
      ordinary: false,
    };
  }

  // Ingestion first: a stopped collector makes every other answer stale, and
  // a warm evaluator with no candles coming in is not warm about anything.
  if (!status.collector.reachable) {
    return {
      kind: 'stalled',
      headline: 'No collector has ever run',
      detail:
        `Nothing has collected candles for ${status.symbol}. This deployment has not ` +
        `been started, rather than having gone quiet.`,
      ordinary: false,
    };
  }

  const stalled = status.concerns.some((c) => c.component === 'collector');
  if (stalled) {
    return {
      kind: 'stalled',
      headline: 'The collector has stopped',
      detail:
        status.concerns.find((c) => c.component === 'collector')?.detail ??
        'The collector is not reporting.',
      ordinary: false,
    };
  }

  if (!status.evaluator.configured) {
    return {
      kind: 'not-configured',
      headline: 'No strategy is running',
      detail:
        'This collector stores candles and evaluates nothing. That is a configuration ' +
        'rather than a fault: set STRATEGY_NAME to start evaluating.',
      ordinary: true,
    };
  }

  if (!status.evaluator.ready) {
    return {
      kind: 'warming-up',
      headline: 'The strategy is not deciding yet',
      detail: status.evaluator.reason || 'The indicators have not converged.',
      ordinary: true,
    };
  }

  return {
    kind: 'no-setup',
    headline: 'No setup found',
    detail:
      `${status.evaluator.strategy} is running on ${status.evaluator.timeframe} and has ` +
      `found nothing to signal. This is the ordinary state: a 4h strategy is quiet for ` +
      `days at a time by design.`,
    ordinary: true,
  };
}
