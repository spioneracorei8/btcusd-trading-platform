import { RefreshControl, ScrollView, View } from 'react-native';

import { useApi } from '../../api/provider';
import { useStatus } from '../../api/queries';
import { count, duration, utc } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle, Row } from '../../components/Card';
import { Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import type { Concern, Status } from '../../api/types';

/**
 * B5 — "is anything broken".
 *
 * # Who this screen is for
 *
 * Nobody looks at it when things work. It is optimised for the day something
 * is wrong: raw values, no interpretation, easy to read aloud down a phone or
 * screenshot into a message.
 *
 * That is why nothing here is summarised into a health score, why the numbers
 * are shown as the server sent them, and why the concerns are the server's own
 * sentences rather than the app's paraphrase. A paraphrase is a second place
 * for the wording to be wrong.
 *
 * When it is unhealthy the aesthetic goes: `error`, high contrast, no
 * ornament. Someone reading this is troubleshooting.
 */
export function StatusScreen({
  alerts,
}: {
  /** Supplied by the navigator, which owns the notification hook so the tap
   * handler survives a tab change. */
  alerts?: React.ReactNode;
} = {}) {
  const { baseUrl } = useApi();
  const { data, error, isFetching, refetch } = useStatus();

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: colors.bg.base }}
      contentContainerStyle={{ padding: layout.screenPadding, gap: layout.space.md }}
      refreshControl={
        <RefreshControl
          refreshing={isFetching}
          onRefresh={() => void refetch()}
          tintColor={colors.text.secondary}
        />
      }
    >
      {error ? <Failure error={error} baseUrl={baseUrl} /> : null}
      {data ? <StatusBody status={data} /> : null}
      {alerts}
      {!data && !error ? (
        <Text tone="secondary">Reading the pipeline status…</Text>
      ) : null}
    </ScrollView>
  );
}

function StatusBody({ status }: { status: Status }) {
  return (
    <>
      <Concerns concerns={status.concerns} />

      <Card>
        <CardTitle>Instrument</CardTitle>
        <Row label="symbol" value={status.symbol} />
        <Row label="market type" value={status.market_type} />
        <Row label="observed at" value={utc(status.observed_at)} />
      </Card>

      <Card>
        <CardTitle>Collector</CardTitle>
        <Row
          label="registered"
          value={status.collector.reachable ? 'yes' : 'no — has never run for this symbol'}
          tone={status.collector.reachable ? undefined : colors.semantic.error}
        />
        <Row label="state" value={status.collector.state} />
        <Row
          label="stream connected"
          value={status.collector.ws_connected ? 'yes' : 'no'}
          tone={status.collector.ws_connected ? undefined : colors.semantic.error}
        />
        <Row label="heartbeat age" value={duration(status.collector.heartbeat_age_seconds)} />
        <Row label="last heartbeat" value={utc(status.collector.updated_at)} />
        <Row label="started at" value={utc(status.collector.started_at)} />
        <Row label="reconnects" value={count(status.collector.reconnect_count)} />
        {status.collector.last_disconnect_note ? (
          <Row label="last disconnect" value={status.collector.last_disconnect_note} mono={false} />
        ) : null}
      </Card>

      <Card>
        <CardTitle>Evaluator</CardTitle>
        <Row
          label="configured"
          value={
            status.evaluator.configured
              ? `${status.evaluator.strategy} on ${status.evaluator.timeframe}`
              : 'no strategy — this collector only collects'
          }
        />
        {status.evaluator.configured ? (
          <>
            <Row
              label="ready"
              value={status.evaluator.ready ? 'yes' : 'no'}
              tone={status.evaluator.ready ? undefined : colors.semantic.warn}
            />
            {status.evaluator.reason ? (
              <Row label="reason" value={status.evaluator.reason} mono={false} />
            ) : null}
          </>
        ) : null}
        <Row label="signals recorded" value={count(status.evaluator.signals_total)} />
        <Row label="last signal" value={utc(status.evaluator.last_signal_at)} />
        <Row label="last signal age" value={duration(status.evaluator.last_signal_age_seconds)} />
      </Card>

      <Card>
        <CardTitle>Ingestion</CardTitle>
        <Row
          label="unfilled candle gaps"
          value={count(status.ingestion.unfilled_gaps)}
          tone={status.ingestion.unfilled_gaps > 0 ? colors.semantic.warn : undefined}
        />
        {status.ingestion.timeframes.map((tf) => (
          <Row
            key={tf.timeframe}
            label={`  ${tf.timeframe}`}
            value={count(tf.unfilled_gaps)}
            tone={tf.unfilled_gaps > 0 ? colors.semantic.warn : undefined}
          />
        ))}
      </Card>

      <Card>
        <CardTitle>Outcomes</CardTitle>
        <Row label="open" value={count(status.outcomes.open)} />
        <Row label="oldest open signal" value={utc(status.outcomes.oldest_open_at)} />
        <Row
          label="oldest open age"
          value={duration(status.outcomes.oldest_open_age_seconds)}
        />
        <Row
          label="signals with no outcome row"
          value={count(status.outcomes.missing_outcome_rows)}
          tone={status.outcomes.missing_outcome_rows > 0 ? colors.semantic.warn : undefined}
        />
      </Card>

      <Card>
        <CardTitle>Delivery</CardTitle>
        <Row label="mode" value={status.delivery.mode} />
        <Row
          label="devices registered"
          value={count(status.delivery.devices_registered)}
          tone={
            status.delivery.mode === 'notify' && status.delivery.devices_registered === 0
              ? colors.semantic.warn
              : undefined
          }
        />
        <Row label="pending" value={count(status.delivery.pending)} />
        <Row label="sent" value={count(status.delivery.sent)} />
        <Row
          label="failed"
          value={count(status.delivery.failed)}
          tone={status.delivery.failed > 0 ? colors.semantic.error : undefined}
        />
        <Row label="last sent" value={utc(status.delivery.last_sent_at)} />
      </Card>

      <Text size="detail" tone="tertiary">
        {status.note}
      </Text>
    </>
  );
}

/**
 * The concerns, first on the page.
 *
 * Empty is the healthy answer and it says so rather than rendering nothing —
 * a blank space where a problem list would be is indistinguishable from a
 * check that did not run.
 *
 * Severity is labelled in words as well as coloured. The palette clears the
 * colour-blindness threshold, but a floor is not a licence: this is the screen
 * somebody reads while something is broken, and "WARN"/"FAULT" survives a
 * screenshot, a bad phone screen and a reader who sees colour differently.
 */
function Concerns({ concerns }: { concerns: Concern[] }) {
  if (concerns.length === 0) {
    return (
      <Card>
        <CardTitle>Concerns</CardTitle>
        <Text size="detail" style={{ color: colors.semantic.ok }}>
          None. Every stage reported something, and nothing it reported looks wrong.
        </Text>
      </Card>
    );
  }

  return (
    <View
      accessibilityRole="alert"
      style={{
        backgroundColor: colors.bg.raised,
        borderRadius: layout.radius,
        borderLeftWidth: 3,
        borderLeftColor: colors.semantic.error,
        padding: layout.cardPadding,
        gap: layout.space.sm,
      }}
    >
      <Text size="detail" weight="semibold" style={{ color: colors.semantic.error }}>
        {concerns.length} {concerns.length === 1 ? 'concern' : 'concerns'}
      </Text>
      {concerns.map((concern) => (
        <View key={`${concern.component}:${concern.detail}`} style={{ gap: layout.space.xs }}>
          <Text size="caption" tone="tertiary" weight="medium">
            {concern.component.toUpperCase()}
          </Text>
          <Text size="detail">{concern.detail}</Text>
        </View>
      ))}
    </View>
  );
}
