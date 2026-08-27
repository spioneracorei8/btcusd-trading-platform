import { View, ViewProps } from 'react-native';

import { colors, layout } from '../theme';
import { Text } from './Text';

/**
 * A section.
 *
 * Separated from the page by a background step rather than by a border.
 * Borders only where a real edge exists — a card floating on a slightly
 * lighter ground reads as a group without a line drawn round it.
 */
export function Card({ style, children, ...rest }: ViewProps) {
  return (
    <View
      {...rest}
      style={[
        {
          backgroundColor: colors.bg.raised,
          borderRadius: layout.radius,
          padding: layout.cardPadding,
          gap: layout.space.sm,
        },
        style,
      ]}
    >
      {children}
    </View>
  );
}

export function CardTitle({ children }: { children: string }) {
  return (
    <Text size="detail" tone="secondary" weight="medium">
      {children}
    </Text>
  );
}

/** A label and a value on one line, which is most of the status screen. */
export function Row({
  label,
  value,
  tone,
  mono = true,
}: {
  label: string;
  value: string;
  tone?: string;
  mono?: boolean;
}) {
  return (
    <View
      style={{
        flexDirection: 'row',
        justifyContent: 'space-between',
        alignItems: 'flex-start',
        gap: layout.space.md,
      }}
    >
      <Text size="detail" tone="secondary" style={{ flexShrink: 1 }}>
        {label}
      </Text>
      <Text
        size="detail"
        tabular={mono}
        style={{ color: tone ?? colors.text.primary, textAlign: 'right', flexShrink: 1 }}
      >
        {value}
      </Text>
    </View>
  );
}
