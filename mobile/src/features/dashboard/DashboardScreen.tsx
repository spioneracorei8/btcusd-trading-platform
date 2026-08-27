import { RefreshControl, ScrollView, View } from 'react-native';

import { useApi } from '../../api/provider';
import { useOutcomes, useSignals, useStatus } from '../../api/queries';
import { ago, price, utc } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle, Row } from '../../components/Card';
import { Num, Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import { explainSilence } from './silence';
import type { Outcome, Signal, Status } from '../../api/types';

/**
 * B1 — "what is happening right now".
 *
 * # What "right now" honestly means here
 *
 * Almost always: nothing. A 4h strategy signals about once every ten days, so
 * the interesting design problem on this screen is not the signal, it is the
 * fortnight between signals — see silence.ts.
 */
export function DashboardScreen({
  onOpenSignal,
}: {
  onOpenSignal?: (id: string) => void;
}) {
  const { baseUrl } = useApi();
  const status = useStatus();
  const signals = useSignals({ limit: 1 });
  const outcomes = useOutcomes({ limit: 1 });

  const latest = signals.data?.signals[0];
  const latestOutcome = outcomes.data?.outcomes.find((o) => o.signal_id === latest?.id);
  const error = status.error ?? signals.error;

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: colors.bg.base }}
      contentContainerStyle={{ padding: layout.screenPadding, gap: layout.space.md }}
      refreshControl={
        <RefreshControl
          refreshing={status.isFetching}
          onRefresh={() => {
            void status.refetch();
            void signals.refetch();
            void outcomes.refetch();
          }}
          tintColor={colors.text.secondary}
        />
      }
    >
      {error ? <Failure error={error} baseUrl={baseUrl} /> : null}

      {status.data ? <Health status={status.data} /> : null}

      {latest ? (
        <LatestSignal
          signal={latest}
          outcome={latestOutcome}
          onOpen={onOpenSignal}
        />
      ) : (
        <Quiet status={status.data} />
      )}

      {status.data && latest ? <Quiet status={status.data} muted /> : null}
    </ScrollView>
  );
}

/**
 * Pipeline health — prominent when unhealthy, quiet when fine.
 *
 * When there is nothing wrong this is one line. The alternative, a permanent
 * dashboard of green ticks, trains its reader to skip the area entirely, and
 * the area is where the red would appear.
 */
function Health({ status }: { status: Status }) {
  if (status.concerns.length === 0) {
    return (
      <View style={{ flexDirection: 'row', alignItems: 'center', gap: layout.space.sm }}>
        <View
          style={{
            width: 6,
            height: 6,
            borderRadius: 3,
            backgroundColor: colors.semantic.ok,
          }}
        />
        <Text size="detail" tone="secondary">
          Pipeline healthy · collector {status.collector.state} · candles current
        </Text>
      </View>
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
        gap: layout.space.xs,
      }}
    >
      <Text size="detail" weight="semibold" style={{ color: colors.semantic.error }}>
        {status.concerns.length} {status.concerns.length === 1 ? 'concern' : 'concerns'}
      </Text>
      {status.concerns.map((concern) => (
        <Text key={concern.detail} size="detail" tone="secondary">
          {concern.component}: {concern.detail}
        </Text>
      ))}
    </View>
  );
}

/**
 * The latest signal.
 *
 * signal_price is labelled "reference" and never "entry". Phase 07 made that
 * distinction deliberately — the decision was taken on a bar's close and
 * nothing could have filled there — and relabelling it here would put a
 * systematic difference into every comparison somebody makes by eye.
 */
function LatestSignal({
  signal,
  outcome,
  onOpen,
}: {
  signal: Signal;
  outcome: Outcome | undefined;
  onOpen?: (id: string) => void;
}) {
  const tint = signal.direction === 'long' ? colors.direction.long : colors.direction.short;

  return (
    <Card onTouchEnd={onOpen ? () => onOpen(signal.id) : undefined}>
      <View style={{ flexDirection: 'row', justifyContent: 'space-between' }}>
        <CardTitle>Latest signal</CardTitle>
        <Text size="detail" tone="tertiary">
          {ago(signal.signal_time)}
        </Text>
      </View>

      <View style={{ flexDirection: 'row', alignItems: 'baseline', gap: layout.space.sm }}>
        <Text size="heading" weight="semibold" style={{ color: tint }}>
          {signal.direction.toUpperCase()}
        </Text>
        <Text size="detail" tone="secondary">
          {signal.timeframe} · {signal.strategy_name}
        </Text>
      </View>

      <Row label="reference price" value={price(signal.signal_price)} />
      <Row label="entry" value={price(signal.entry_price)} />
      <Row label="stop" value={price(signal.stop_loss)} />
      <Row label="target" value={price(signal.take_profit)} />

      {outcome ? <OutcomeSoFar outcome={outcome} /> : null}

      <Text size="caption" tone="tertiary">
        Recorded at {utc(signal.created_at)}. Nothing here places an order.
      </Text>
    </Card>
  );
}

/** Where an unresolved signal has got to, or how a resolved one ended. */
function OutcomeSoFar({ outcome }: { outcome: Outcome }) {
  const open = outcome.status === 'open';

  return (
    <View style={{ gap: layout.space.xs, marginTop: layout.space.xs }}>
      <Row
        label={open ? 'still open' : 'resolved'}
        value={open ? `${outcome.bars_held} bars held` : outcome.status}
      />
      <Row label="worst excursion (MAE)" value={price(outcome.mae)} />
      <Row label="best excursion (MFE)" value={price(outcome.mfe)} />
      {outcome.net_return_pct !== null ? (
        <Row label="net return, after costs" value={`${outcome.net_return_pct}%`} />
      ) : null}
      {!outcome.measurable ? (
        <Text size="detail" style={{ color: colors.semantic.warn }}>
          The window this was followed over has missing data, so what happened is not
          knowable. It is excluded from every figure.
        </Text>
      ) : null}
    </View>
  );
}

/** Why there is nothing to show. See silence.ts. */
function Quiet({ status, muted = false }: { status: Status | undefined; muted?: boolean }) {
  const silence = explainSilence(status);

  if (muted && silence.kind === 'no-setup') {
    // A signal is already on screen; saying "no setup found" underneath it
    // would contradict what the reader is looking at.
    return null;
  }

  return (
    <Card>
      <CardTitle>{muted ? 'Since then' : 'Right now'}</CardTitle>
      <Text
        size="body"
        weight="medium"
        style={{ color: silence.ordinary ? colors.text.primary : colors.semantic.warn }}
      >
        {silence.headline}
      </Text>
      <Text size="detail" tone="secondary">
        {silence.detail}
      </Text>
      {status?.evaluator.configured && silence.ordinary ? (
        <Num size="caption" tone="tertiary">
          {status.evaluator.signals_total} signals recorded in total · last{' '}
          {ago(status.evaluator.last_signal_at)}
        </Num>
      ) : null}
    </Card>
  );
}
