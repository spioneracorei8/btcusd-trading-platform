import { View } from 'react-native';

import { colors, layout } from '../theme';
import { Text } from './Text';

/**
 * The insufficient-sample banner.
 *
 * # Why it renders above the numbers
 *
 * Phase 08 found that a banner placed beside a figure loses to the figure: the
 * eye goes to the number, reads it, and forms a view before it reaches the
 * caveat. Above it, in the reading order, it is unavoidable.
 *
 * It is deliberately not styled to look dismissible. No close button, no muted
 * treatment, no icon that reads as "informational". It is `warn` on
 * `bg.raised` at full text contrast, and it stays until the sample is
 * sufficient — which on a 4h strategy at a tenth of a signal a day is close to
 * three years, and saying so is the point of the wait line.
 */
export function SampleBanner({ text }: { text: string }) {
  // The server composes this: one wording for the CLI, the API and here, so
  // nobody reads a softer version of "this number does not mean anything yet".
  const lines = text.split('\n').filter((line) => line.trim() !== '');

  return (
    <View
      accessibilityRole="alert"
      style={{
        backgroundColor: colors.bg.raised,
        borderLeftWidth: 3,
        borderLeftColor: colors.semantic.warn,
        borderRadius: layout.radius,
        padding: layout.cardPadding,
        gap: layout.space.xs,
      }}
    >
      {lines.map((line, index) => (
        <Text
          key={line}
          size="detail"
          // The first line is the claim; the rest is the explanation.
          tone={index === 0 ? 'primary' : 'secondary'}
          weight={index === 0 ? 'semibold' : 'regular'}
          style={index === 0 ? { color: colors.semantic.warn } : undefined}
        >
          {line}
        </Text>
      ))}
    </View>
  );
}
