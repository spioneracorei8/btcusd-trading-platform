import { View } from 'react-native';

import { explain } from '../api/errors';
import { colors, layout } from '../theme';
import { Text } from './Text';

/**
 * What a screen shows instead of a spinner when the request failed.
 *
 * A spinner is the wrong answer to a VPN that is switched off: it says "wait"
 * about a condition that will not resolve by waiting. This says what happened
 * and what to do about it — and for the commonest case by a wide margin, what
 * to do is open Tailscale.
 */
export function Failure({ error, baseUrl }: { error: unknown; baseUrl: string }) {
  const { title, detail, action } = explain(error, baseUrl);

  return (
    <View
      style={{
        backgroundColor: colors.bg.raised,
        borderRadius: layout.radius,
        padding: layout.cardPadding,
        gap: layout.space.sm,
      }}
    >
      <Text size="body" weight="semibold" style={{ color: colors.semantic.warn }}>
        {title}
      </Text>
      <Text size="detail" tone="secondary">
        {detail}
      </Text>
      {action ? (
        <Text size="detail" tone="secondary">
          {action}
        </Text>
      ) : null}
    </View>
  );
}
