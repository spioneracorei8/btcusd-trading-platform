import { View } from 'react-native';

import { colors, layout, type } from '../theme';
import { Text } from './Text';

/**
 * The sample a figure was drawn from, shown beside it.
 *
 * # Why this is a component rather than a string
 *
 * Every performance figure in this app carries its sample size. A win rate
 * over nine trades and one over nine hundred must not be able to look alike,
 * and the way that rule decays is by somebody rendering the number without the
 * qualifier because it was only ever a convention.
 *
 * It renders at 14, not caption size. A sample size is a qualifier on the
 * number beside it — "13.3%" and "over 30 resolved" are one statement — and a
 * qualifier set smaller than the figure it qualifies loses the argument. That
 * is the single most important typographic rule here, and it is why
 * `type.size.sampleSize` exists as its own token rather than reusing
 * `caption`.
 */
export function SampleSize({ resolved, required }: { resolved: number; required: number }) {
  const thin = resolved < required;

  return (
    <View style={{ flexDirection: 'row', alignItems: 'center', gap: layout.space.xs }}>
      <Text
        size="sampleSize"
        tone={thin ? 'secondary' : 'tertiary'}
        tabular
        style={{ color: thin ? colors.semantic.warn : undefined }}
      >
        over {resolved.toLocaleString('en-GB')} resolved
      </Text>
      {thin ? (
        <Text size="sampleSize" tone="tertiary" tabular>
          of {required.toLocaleString('en-GB')} needed
        </Text>
      ) : null}
    </View>
  );
}

/** The size a sample label renders at, exported so a test can assert it
 * without reaching into the component's styles. */
export const SAMPLE_SIZE_FONT = type.size.sampleSize;
