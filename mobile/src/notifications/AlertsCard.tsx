import { useState } from 'react';
import { Linking, Pressable, View } from 'react-native';

import { colors, layout } from '../theme';
import { Card, CardTitle, Row } from '../components/Card';
import { Text } from '../components/Text';
import { PERMISSION_PRIMER } from './registration';
import type { AlertState } from './registration';
import type { DeviceResponse } from '../api/types';

/**
 * Alerts, on the status screen.
 *
 * It lives here rather than on a settings screen because there is one setting
 * and it is operational: whether this phone will hear about a signal. The day
 * somebody wonders "why did I not get an alert", this is the screen they are
 * already on.
 */
export function AlertsCard({
  state,
  device,
  token,
  error,
  onRequest,
  onRegister,
}: {
  state: AlertState;
  device: DeviceResponse | undefined;
  token: string | undefined;
  error: unknown;
  onRequest: () => void;
  onRegister: () => void;
}) {
  const [primed, setPrimed] = useState(false);

  return (
    <Card>
      <CardTitle>Alerts</CardTitle>

      <Text
        size="body"
        weight="medium"
        style={{ color: state.willArrive ? colors.semantic.ok : colors.text.primary }}
      >
        {state.headline}
      </Text>
      <Text size="detail" tone="secondary">
        {state.detail}
      </Text>

      {/* The primer, before the OS prompt. It cannot be re-asked once
          refused, so this is the one chance to say what alerts are for. */}
      {state.next === 'prime' && primed ? (
        <View style={{ gap: layout.space.sm, marginTop: layout.space.xs }}>
          <Text size="detail" weight="medium">
            {PERMISSION_PRIMER.title}
          </Text>
          <Text size="detail" tone="secondary">
            {PERMISSION_PRIMER.body}
          </Text>
          <View style={{ flexDirection: 'row', gap: layout.space.sm }}>
            <Action label={PERMISSION_PRIMER.accept} onPress={onRequest} primary />
            <Action label={PERMISSION_PRIMER.decline} onPress={() => setPrimed(false)} />
          </View>
        </View>
      ) : null}

      {state.next === 'prime' && !primed ? (
        <Action label="Set up alerts" onPress={() => setPrimed(true)} primary />
      ) : null}

      {state.next === 'register' ? (
        <Action label="Register this phone" onPress={onRegister} primary />
      ) : null}

      {state.next === 'open-settings' ? (
        <Action label="Open Android settings" onPress={() => void Linking.openSettings()} />
      ) : null}

      {device?.registered ? (
        <View style={{ marginTop: layout.space.xs, gap: layout.space.xs }}>
          <Row label="registered as" value={device.token ?? '—'} />
          <Row label="server mode" value={device.delivery_mode} />
          {device.refreshed_at ? (
            <Row label="last checked in" value={device.refreshed_at} />
          ) : null}
        </View>
      ) : null}

      {token && !device?.registered ? (
        <Text size="caption" tone="tertiary">
          This phone has a token and the server has not accepted it yet.
        </Text>
      ) : null}

      {error ? (
        <Text size="detail" style={{ color: colors.semantic.error }}>
          Registering failed: {error instanceof Error ? error.message : String(error)}. Alerts
          will not arrive until this succeeds.
        </Text>
      ) : null}
    </Card>
  );
}

function Action({
  label,
  onPress,
  primary = false,
}: {
  label: string;
  onPress: () => void;
  primary?: boolean;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      style={{
        paddingHorizontal: layout.space.lg,
        paddingVertical: layout.space.sm,
        borderRadius: layout.radius,
        borderWidth: layout.hairline,
        borderColor: primary ? colors.jade.base : colors.border.subtle,
        backgroundColor: primary ? colors.bg.overlay : 'transparent',
        alignSelf: 'flex-start',
      }}
    >
      <Text size="detail" style={primary ? { color: colors.jade.bright } : undefined}>
        {label}
      </Text>
    </Pressable>
  );
}
