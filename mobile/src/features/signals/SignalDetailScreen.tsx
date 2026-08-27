import { ScrollView, View } from 'react-native';

import { useApi } from '../../api/provider';
import { useOutcomes, useSignal } from '../../api/queries';
import { ago, price, utc } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle, Row } from '../../components/Card';
import { Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import type { Outcome, Signal, SignalReason } from '../../api/types';

/**
 * The detail view, and the reason it exists.
 *
 * A signal without its reasoning is unreviewable six weeks later. Indicators
 * are never stored, so the values behind a decision cannot be recomputed
 * against the warm-up state the live process actually had — the `reason` blob
 * is the only record there will ever be, which is why it is jsonb rather than
 * a summary string and why this screen renders all of it.
 *
 * Anything the app does not recognise is still shown. A strategy added later
 * will put fields here that this build has never heard of, and dropping them
 * would quietly make the older app worse at the one job this screen has.
 */
export function SignalDetailScreen({ id }: { id: string }) {
  const { baseUrl } = useApi();
  const { data: signal, error, isLoading } = useSignal(id);
  const { data: outcomes } = useOutcomes({ limit: 200 });

  const outcome = outcomes?.outcomes.find((o) => o.signal_id === id);

  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: colors.bg.base }}
      contentContainerStyle={{ padding: layout.screenPadding, gap: layout.space.md }}
    >
      {error ? <Failure error={error} baseUrl={baseUrl} /> : null}
      {isLoading ? <Text tone="secondary">Reading the signal…</Text> : null}
      {signal ? <Detail signal={signal} outcome={outcome} /> : null}
    </ScrollView>
  );
}

function Detail({ signal, outcome }: { signal: Signal; outcome: Outcome | undefined }) {
  const tint = signal.direction === 'long' ? colors.direction.long : colors.direction.short;

  return (
    <>
      <Card>
        <View style={{ flexDirection: 'row', justifyContent: 'space-between' }}>
          <Text size="heading" weight="semibold" style={{ color: tint }}>
            {signal.direction.toUpperCase()}
          </Text>
          <Text size="detail" tone="tertiary">
            {ago(signal.signal_time)}
          </Text>
        </View>
        <Text size="detail" tone="secondary">
          {signal.strategy_name} {signal.strategy_version} on {signal.timeframe}
        </Text>

        {/* signal_price is the close the strategy decided on; entry_price is
            what a position would have opened at. Two numbers, and the labels
            keep them apart — phase 07 made the distinction deliberately. */}
        <Row label="reference price (decided on)" value={price(signal.signal_price)} />
        <Row label="entry (next bar's open + slippage)" value={price(signal.entry_price)} />
        <Row label="stop" value={price(signal.stop_loss)} />
        <Row label="target" value={price(signal.take_profit)} />
        <Row label="signal time" value={utc(signal.signal_time)} />
        <Row label="recorded at" value={utc(signal.created_at)} />
      </Card>

      {outcome ? <OutcomeCard outcome={outcome} /> : null}

      <ReasonCard reason={signal.reason} />

      <Text size="caption" tone="tertiary">
        This is a record of what the strategy decided. Nothing in this app places an order.
      </Text>
    </>
  );
}

function OutcomeCard({ outcome }: { outcome: Outcome }) {
  return (
    <Card>
      <CardTitle>What happened</CardTitle>
      <Row label="status" value={outcome.status} />
      <Row label="bars held" value={String(outcome.bars_held)} />
      <Row label="resolved at" value={utc(outcome.resolved_at)} />
      <Row label="resolved price" value={price(outcome.resolved_price)} />
      <Row label="worst excursion (MAE)" value={price(outcome.mae)} />
      <Row label="best excursion (MFE)" value={price(outcome.mfe)} />
      <Row
        label="net return, after costs"
        value={outcome.net_return_pct === null ? '—' : `${outcome.net_return_pct}%`}
      />

      {!outcome.measurable ? (
        <Text size="detail" style={{ color: colors.semantic.warn }}>
          The window this was followed over has missing data, so what happened is not
          knowable. It is excluded from every figure on the performance screen.
        </Text>
      ) : null}

      {outcome.divergence_note ? (
        <Text size="detail" tone="secondary">
          {outcome.divergence_note}
        </Text>
      ) : null}
    </Card>
  );
}

/**
 * The reason, in full.
 *
 * The known parts get labels; everything else is rendered as it arrived. A
 * strategy added after this build ships will carry fields this app has never
 * heard of, and the whole point of the screen is that they survive to be read.
 */
function ReasonCard({ reason }: { reason: SignalReason | undefined }) {
  if (!reason) {
    return (
      <Card>
        <CardTitle>Why</CardTitle>
        <Text size="detail" tone="secondary">
          No reason was recorded with this signal.
        </Text>
      </Card>
    );
  }

  const { trigger, strategy, indicators, ...rest } = reason;

  return (
    <Card>
      <CardTitle>Why</CardTitle>

      {trigger ? (
        <Text size="body" weight="medium">
          {trigger}
        </Text>
      ) : null}

      {strategy?.params?.length ? (
        <View style={{ gap: layout.space.xs, marginTop: layout.space.xs }}>
          <Text size="detail" tone="secondary" weight="medium">
            Parameters it was produced with
          </Text>
          {strategy.params.map((param) => (
            <Row key={param.name} label={param.name} value={param.value} />
          ))}
        </View>
      ) : null}

      {indicators && Object.keys(indicators).length > 0 ? (
        <View style={{ gap: layout.space.xs, marginTop: layout.space.xs }}>
          <Text size="detail" tone="secondary" weight="medium">
            Indicators at the deciding bar
          </Text>
          {Object.entries(indicators).map(([name, value]) => (
            <Row key={name} label={name} value={String(value)} />
          ))}
        </View>
      ) : null}

      {Object.keys(rest).length > 0 ? (
        <View style={{ gap: layout.space.xs, marginTop: layout.space.xs }}>
          <Text size="detail" tone="secondary" weight="medium">
            Everything else this strategy recorded
          </Text>
          {Object.entries(rest).map(([name, value]) => (
            <Row
              key={name}
              label={name}
              value={typeof value === 'object' ? JSON.stringify(value) : String(value)}
              mono={false}
            />
          ))}
        </View>
      ) : null}
    </Card>
  );
}
