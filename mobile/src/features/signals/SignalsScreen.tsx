import { useState } from 'react';
import { FlatList, Pressable, RefreshControl, View } from 'react-native';

import { useApi } from '../../api/provider';
import { useOutcomes, useSignals } from '../../api/queries';
import { ago, price } from '../../format';
import { colors, layout } from '../../theme';
import { Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import type { Direction, Outcome, OutcomeStatus, Signal } from '../../api/types';

/**
 * B2 — "what has it produced".
 *
 * Reverse chronological, with the outcome once there is one. The detail view
 * is the point of the screen; this is the index into it.
 */
export function SignalsScreen({ onOpen }: { onOpen: (id: string) => void }) {
  const { baseUrl } = useApi();
  const [direction, setDirection] = useState<Direction | undefined>();
  const [status, setStatus] = useState<OutcomeStatus | undefined>();

  const signals = useSignals({ limit: 50, direction });
  // Outcomes are fetched alongside rather than joined server-side: the list
  // endpoint deliberately carries no outcome, and asking for both is one
  // request each rather than a new shape on the server.
  const outcomes = useOutcomes({ limit: 200, status });

  const byId = new Map((outcomes.data?.outcomes ?? []).map((o) => [o.signal_id, o]));
  const rows = (signals.data?.signals ?? []).filter(
    (signal) => status === undefined || byId.get(signal.id)?.status === status,
  );

  return (
    <View style={{ flex: 1, backgroundColor: colors.bg.base }}>
      <Filters
        direction={direction}
        status={status}
        onDirection={setDirection}
        onStatus={setStatus}
      />

      <FlatList
        data={rows}
        keyExtractor={(signal) => signal.id}
        contentContainerStyle={{
          padding: layout.screenPadding,
          paddingTop: 0,
          gap: layout.space.sm,
        }}
        refreshControl={
          <RefreshControl
            refreshing={signals.isFetching}
            onRefresh={() => {
              void signals.refetch();
              void outcomes.refetch();
            }}
            tintColor={colors.text.secondary}
          />
        }
        ListHeaderComponent={
          signals.error ? (
            <View style={{ paddingBottom: layout.space.md }}>
              <Failure error={signals.error} baseUrl={baseUrl} />
            </View>
          ) : null
        }
        ListEmptyComponent={
          signals.error ? null : (
            <Text tone="secondary" size="detail">
              {signals.isLoading
                ? 'Reading the signal history…'
                : direction || status
                  ? 'No signal matches this filter.'
                  : 'No signals recorded yet.'}
            </Text>
          )
        }
        ListFooterComponent={
          signals.data && signals.data.total > rows.length ? (
            <Text tone="tertiary" size="caption" tabular>
              Showing {rows.length} of {signals.data.total} recorded.
            </Text>
          ) : null
        }
        renderItem={({ item }) => (
          <SignalRow signal={item} outcome={byId.get(item.id)} onPress={() => onOpen(item.id)} />
        )}
      />
    </View>
  );
}

function Filters({
  direction,
  status,
  onDirection,
  onStatus,
}: {
  direction: Direction | undefined;
  status: OutcomeStatus | undefined;
  onDirection: (value: Direction | undefined) => void;
  onStatus: (value: OutcomeStatus | undefined) => void;
}) {
  return (
    <View style={{ padding: layout.screenPadding, gap: layout.space.sm }}>
      <View style={{ flexDirection: 'row', gap: layout.space.xs, flexWrap: 'wrap' }}>
        <Chip label="all" active={!direction} onPress={() => onDirection(undefined)} />
        <Chip
          label="long"
          active={direction === 'long'}
          tint={colors.direction.long}
          onPress={() => onDirection('long')}
        />
        <Chip
          label="short"
          active={direction === 'short'}
          tint={colors.direction.short}
          onPress={() => onDirection('short')}
        />
      </View>
      <View style={{ flexDirection: 'row', gap: layout.space.xs, flexWrap: 'wrap' }}>
        {([undefined, 'open', 'target', 'stop', 'expired', 'invalidated'] as const).map(
          (value) => (
            <Chip
              key={value ?? 'any'}
              label={value ?? 'any outcome'}
              active={status === value}
              onPress={() => onStatus(value)}
            />
          ),
        )}
      </View>
    </View>
  );
}

/** A filter control. Gold as the active indicator — a hairline and a few
 * points of text, which is what the accent budget is for. */
function Chip({
  label,
  active,
  tint,
  onPress,
}: {
  label: string;
  active: boolean;
  tint?: string;
  onPress: () => void;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ selected: active }}
      onPress={onPress}
      style={{
        paddingHorizontal: layout.space.md,
        paddingVertical: layout.space.xs,
        borderRadius: layout.radius,
        borderWidth: layout.hairline,
        borderColor: active ? colors.border.gold : colors.border.subtle,
        backgroundColor: active ? colors.bg.raised : 'transparent',
      }}
    >
      <Text
        size="detail"
        tone={active ? 'primary' : 'tertiary'}
        style={tint && active ? { color: tint } : undefined}
      >
        {label}
      </Text>
    </Pressable>
  );
}

const STATUS_TONE: Record<OutcomeStatus, string> = {
  open: colors.text.secondary,
  target: colors.semantic.ok,
  stop: colors.direction.short,
  expired: colors.text.tertiary,
  // Not an error colour: an invalidated outcome is not a fault, it is a
  // measurement that could not be taken.
  invalidated: colors.semantic.warn,
};

function SignalRow({
  signal,
  outcome,
  onPress,
}: {
  signal: Signal;
  outcome: Outcome | undefined;
  onPress: () => void;
}) {
  const tint = signal.direction === 'long' ? colors.direction.long : colors.direction.short;

  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      style={{
        backgroundColor: colors.bg.raised,
        borderRadius: layout.radius,
        padding: layout.cardPadding,
        gap: layout.space.xs,
      }}
    >
      <View style={{ flexDirection: 'row', justifyContent: 'space-between' }}>
        <View style={{ flexDirection: 'row', gap: layout.space.sm, alignItems: 'baseline' }}>
          <Text size="body" weight="semibold" style={{ color: tint }}>
            {signal.direction.toUpperCase()}
          </Text>
          <Text size="detail" tone="tertiary">
            {signal.timeframe}
          </Text>
        </View>
        <Text size="detail" tone="tertiary">
          {ago(signal.signal_time)}
        </Text>
      </View>

      <View style={{ flexDirection: 'row', justifyContent: 'space-between' }}>
        <Text size="detail" tone="secondary" tabular>
          ref {price(signal.signal_price)}
        </Text>
        {outcome ? (
          <Text size="detail" tabular style={{ color: STATUS_TONE[outcome.status] }}>
            {outcome.status}
            {outcome.net_return_pct !== null ? ` · ${outcome.net_return_pct}%` : ''}
          </Text>
        ) : (
          <Text size="detail" tone="tertiary">
            not followed yet
          </Text>
        )}
      </View>
    </Pressable>
  );
}
