import { NavigationContainer, DefaultTheme } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';

import { colors, layout, type } from './theme';
import { ChartScreen } from './features/chart/ChartScreen';
import { DashboardScreen } from './features/dashboard/DashboardScreen';
import { PerformanceScreen } from './features/performance/PerformanceScreen';
import { SignalDetailScreen } from './features/signals/SignalDetailScreen';
import { SignalsScreen } from './features/signals/SignalsScreen';
import { StatusScreen } from './features/status/StatusScreen';

/**
 * Five tabs, and no more. Each answers one question:
 *
 *   Now          what is happening right now
 *   Signals      what has it produced
 *   Chart        what did the market do around it
 *   Performance  is it working
 *   Status       is anything broken
 *
 * The signals tab carries a stack, because a signal has a detail view and that
 * view is the point of the tab. Nothing else is nested: a five-screen
 * instrument does not need a navigation model somebody has to learn.
 */

export type SignalsStackParams = {
  SignalsList: undefined;
  SignalDetail: { id: string };
};

const Tabs = createBottomTabNavigator();
const Stack = createNativeStackNavigator<SignalsStackParams>();

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

const headerOptions = {
  headerStyle: { backgroundColor: colors.bg.base },
  headerTitleStyle: {
    color: colors.text.primary,
    fontSize: type.size.body,
    fontWeight: type.weight.medium,
  },
  headerTintColor: colors.text.secondary,
  headerShadowVisible: false,
} as const;

function SignalsTab() {
  return (
    <Stack.Navigator screenOptions={{ ...headerOptions, contentStyle: { backgroundColor: colors.bg.base } }}>
      <Stack.Screen name="SignalsList" options={{ title: 'Signals' }}>
        {({ navigation }) => (
          <SignalsScreen onOpen={(id) => navigation.navigate('SignalDetail', { id })} />
        )}
      </Stack.Screen>
      <Stack.Screen name="SignalDetail" options={{ title: 'Signal' }}>
        {({ route }) => <SignalDetailScreen id={route.params.id} />}
      </Stack.Screen>
    </Stack.Navigator>
  );
}

export function AppTabs() {
  return (
    <Tabs.Navigator
      screenOptions={{
        ...headerOptions,
        tabBarStyle: {
          backgroundColor: colors.bg.base,
          borderTopColor: colors.border.subtle,
          borderTopWidth: layout.hairline,
        },
        // Gold as an active indicator: a few points of text, which is the
        // whole accent budget.
        tabBarActiveTintColor: colors.gold.base,
        tabBarInactiveTintColor: colors.text.tertiary,
        tabBarLabelStyle: { fontSize: type.size.caption },
        // Labels only. Five tabs with plain words need no icons, and a
        // missing-icon placeholder is worse than none.
        tabBarIcon: () => null,
      }}
    >
      <Tabs.Screen name="Now" component={DashboardScreen} />
      <Tabs.Screen name="Signals" component={SignalsTab} options={{ headerShown: false }} />
      <Tabs.Screen name="Chart" component={ChartScreen} />
      <Tabs.Screen name="Performance" component={PerformanceScreen} />
      <Tabs.Screen name="Status" component={StatusScreen} />
    </Tabs.Navigator>
  );
}

export function Navigation({ children }: { children?: React.ReactNode }) {
  return (
    <NavigationContainer theme={navigationTheme}>
      {children ?? <AppTabs />}
    </NavigationContainer>
  );
}
