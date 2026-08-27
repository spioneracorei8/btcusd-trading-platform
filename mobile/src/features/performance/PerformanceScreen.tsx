import { RefreshControl, ScrollView, View } from 'react-native';

import { useApi } from '../../api/provider';
import { usePerformance } from '../../api/queries';
import { count, percent, rate } from '../../format';
import { colors, layout } from '../../theme';
import { Card, CardTitle, Row } from '../../components/Card';
import { SampleBanner } from '../../components/SampleBanner';
import { SampleSize } from '../../components/SampleSize';
import { Num, Text } from '../../components/Text';
import { Failure } from '../../components/Unreachable';
import type { PerformanceGroup } from '../../api/types';

/**
 * B4 — "is it working".
 *
 * # What this screen is honestly for
 *
 * The strategy search has not produced anything clearing the acceptance
 * criteria. Best available is ema_crossover on 4h at defaults: +4.78%,
 * PF 1.13, edge gone at 1.5x cost, profitable in one of two years. So this is
 * not a screen for deciding to trade. It is a screen for noticing when live
 * behaviour departs from what the backtest said.
 *
 * Which is why the layout is what it is. The sample comes first, in full, as
 * an unmissable block — phase 08 found that a banner placed beside a figure
 * loses to the figure, because the eye reads the number and forms a view
 * before it reaches the caveat. Above it, in the reading order, it is
 * unavoidable.
 */
export function PerformanceScreen() {
  const { baseUrl } = useApi();
  const { data, error, isFetching, refetch } = usePerformance();

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

      {data?.groups.length === 0 ? (
        <Card>
          <CardTitle>Nothing to measure yet</CardTitle>
          <Text size="detail" tone="secondary">
            No signal in this window has resolved, so there is nothing to compute a figure
            from. That is the ordinary state early on.
          </Text>
        </Card>
      ) : null}

      {data?.groups.map((group) => (
        <Group key={`${group.strategy}:${group.version}:${JSON.stringify(group.params)}`} group={group} />
      ))}

      {data ? (
        <Text size="detail" tone="tertiary">
          {data.note}
        </Text>
      ) : null}
    </ScrollView>
  );
}

/**
 * One strategy at one parameter set.
 *
 * There is deliberately no total across groups. Averaging across a parameter
 * change produces a number describing nothing, and a screen that offered one
 * would be offering the most tempting number on the page.
 */
function Group({ group }: { group: PerformanceGroup }) {
  return (
    <View style={{ gap: layout.space.sm }}>
      <View>
        <Text size="body" weight="semibold">
          {group.strategy} {group.version}
        </Text>
        {group.params.length > 0 ? (
          <Text size="detail" tone="tertiary" tabular>
            {group.params.map((p) => `${p.name}=${p.value}`).join(' · ')}
          </Text>
        ) : null}
      </View>

      {/* Before the numbers. Not beside them. */}
      {group.sample.banner ? <SampleBanner text={group.sample.banner} /> : null}

      <Card>
        <View style={{ gap: layout.space.xs }}>
          <Text size="detail" tone="secondary" weight="medium">
            Expectancy, after modelled costs
          </Text>
          {/* percent() renders null as a dash, for the same reason. */}
          <Num size="price" weight="semibold">
            {percent(group.expectancy_pct)}
          </Num>
          <SampleSize resolved={group.sample.resolved} required={group.sample.required} />
          <Text size="detail" tone="secondary">
            What one signal is worth on average. Not derivable from a win rate: a 30% win
            rate at a 3:1 payoff beats a 60% one at 1:2.
          </Text>
        </View>
      </Card>

      <Card>
        <CardTitle>Live outcomes</CardTitle>
        {/* rate() renders null as a dash: a zero would read as a strategy
            that never wins, which is a different statement. */}
        <Row label="win rate" value={rate(group.win_rate)} />
        <SampleSize resolved={group.sample.resolved} required={group.sample.required} />

        <Row label="average win" value={percent(group.average_win_pct)} />
        <Row label="average loss" value={percent(group.average_loss_pct)} />
        <Row label="average cost per round trip" value={percent(group.average_cost_pct)} />

        <View style={{ height: layout.space.xs }} />

        <Row label="signals" value={count(group.signals)} />
        <Row label="resolved" value={count(group.resolved)} />
        <Row label="still open" value={count(group.still_open)} />
        <Row label="targets" value={count(group.targets)} />
        <Row label="stops" value={count(group.stops)} />
        <Row label="expired" value={count(group.expired)} />
        <Row
          label="invalidated (excluded)"
          value={count(group.invalidated_excluded)}
          tone={group.invalidated_excluded > 0 ? colors.semantic.warn : undefined}
        />
      </Card>

      {group.rested_on_assumption > 0 ? (
        <Card>
          <CardTitle>Resolutions that rested on an assumption</CardTitle>
          <Num size="heading" weight="semibold" style={{ color: colors.semantic.warn }}>
            {count(group.rested_on_assumption)}
          </Num>
          <Text size="detail" tone="secondary">
            These came from a rule rather than from the data: a bar that reached both
            levels, or an entry that gapped past one. A win rate resting largely on these
            rests on an assumption rather than on evidence.
          </Text>
        </Card>
      ) : null}

      <Wait sample={group.sample} />
    </View>
  );
}

/**
 * How long until the numbers above mean anything.
 *
 * At a tenth of a trade a day, 100 resolved signals is close to three years.
 * A performance screen that looks nearly ready to tell you something, for
 * years, is worse than one that says how long it will be.
 */
function Wait({ sample }: { sample: PerformanceGroup['sample'] }) {
  if (sample.sufficient) return null;

  return (
    <Card>
      <CardTitle>When this will mean something</CardTitle>
      <Row
        label="resolved so far"
        value={`${count(sample.resolved)} of ${count(sample.required)}`}
      />
      {sample.resolved_per_day !== null ? (
        <Row label="observed rate" value={`${sample.resolved_per_day.toFixed(2)} a day`} />
      ) : null}
      <Row label="expected wait" value={sample.expected_wait ?? 'not yet estimable'} />
    </Card>
  );
}
