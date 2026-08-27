import { NavigationContainer, DefaultTheme } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';

import { colors, layout, type } from './theme';
import { DashboardScreen } from './features/dashboard/DashboardScreen';
import { StatusScreen } from './features/status/StatusScreen';

/**
 * Five tabs, and no more. Each answers one question.
 *
 * The signals tab carries a stack because a signal has a detail view; the
 * others are single screens. There is no drawer, no nested navigator and no
 * modal — a five-screen instrument does not need a navigation model somebody
 * has to learn.
 */

export type SignalsStackParams = {
  SignalsList: undefined;
  SignalDetail: { id: string };
};

const Tabs = createBottomTabNavigator();
const SignalsStack = createNativeStackNavigator<SignalsStackParams>();

/** Navigation's own theme, so the frame matches the screens rather than
 * defaulting to a white background behind them. */
const navigationTheme = {
  ...DefaultTheme,
  dark: true,
  colors: {
    ...DefaultTheme.colors,
    primary: colors.jade.base,
    background: colors.bg.base,
    card: colors.bg.raised,
    text: colors.text.primary,
    border: colors.border.subtle,
    notification: colors.semantic.warn,
  },
};

export function Navigation({ children }: { children?: React.ReactNode }) {
  return (
    <NavigationContainer theme={navigationTheme}>
      {children ?? <AppTabs />}
    </NavigationContainer>
  );
}

export function AppTabs() {
  return (
    <Tabs.Navigator
      screenOptions={{
        headerStyle: { backgroundColor: colors.bg.base },
        headerTitleStyle: {
          color: colors.text.primary,
          fontSize: type.size.body,
          fontWeight: type.weight.medium,
        },
        headerShadowVisible: false,
        tabBarStyle: {
          backgroundColor: colors.bg.base,
          borderTopColor: colors.border.subtle,
          borderTopWidth: layout.hairline,
        },
        // Gold as an active indicator: a few points of text, which is what
        // the accent budget is for.
        tabBarActiveTintColor: colors.gold.base,
        tabBarInactiveTintColor: colors.text.tertiary,
        tabBarLabelStyle: { fontSize: type.size.caption },
        // Labels only. Five tabs with plain words need no icons, and a
        // missing-icon placeholder is worse than none.
        tabBarIcon: () => null,
      }}
    >
      <Tabs.Screen name="Now" component={DashboardScreen} />
      <Tabs.Screen name="Status" component={StatusScreen} />
    </Tabs.Navigator>
  );
}

export { SignalsStack };
