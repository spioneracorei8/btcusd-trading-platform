import { Pressable } from 'react-native';

import { Text } from '../components/Text';
import { colors, layout } from '../theme';
import { useUpdate } from './useUpdate';

/**
 * "A new version is ready", and a way to take it.
 *
 * # Why this is visible at all
 *
 * A service worker that will not update is the classic PWA failure: the app
 * sits on an old build indefinitely, and the only symptom is that nothing new
 * ever appears — which is indistinguishable from a system that has nothing new
 * to report, which is what this one looks like most days.
 *
 * The worker updates itself. What it cannot do is reload a page somebody is
 * reading. Doing that unasked is rude; doing nothing is the failure above. So
 * it is one line, at the top, in the ordinary text colour rather than an
 * alarm — a new build is not an incident.
 */
export function UpdateBanner() {
  const { state, apply } = useUpdate();
  if (state !== 'ready') return null;

  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel="Reload to use the new version"
      onPress={apply}
      style={{
        paddingHorizontal: layout.screenPadding,
        paddingVertical: layout.space.sm,
        backgroundColor: colors.bg.raised,
        borderBottomWidth: layout.hairline,
        borderBottomColor: colors.border.subtle,
        flexDirection: 'row',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: layout.space.sm,
      }}
    >
      <Text size="detail" tone="secondary">
        A newer version of this app is installed.
      </Text>
      <Text size="detail" style={{ color: colors.gold.base }}>
        Reload
      </Text>
    </Pressable>
  );
}
