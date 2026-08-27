import { StatusBar } from 'expo-status-bar';
import { Text, View } from 'react-native';

import { colors, layout, type } from './theme';

/**
 * Placeholder. The navigator and the five screens land in the next commits;
 * this exists so the entry point resolves and the theme is exercised.
 */
export default function App() {
  return (
    <View
      style={{
        flex: 1,
        backgroundColor: colors.bg.base,
        alignItems: 'center',
        justifyContent: 'center',
        padding: layout.screenPadding,
      }}
    >
      <StatusBar style="light" />
      <Text style={{ color: colors.text.primary, fontSize: type.size.heading }}>
        BTCUSD Signals
      </Text>
    </View>
  );
}
