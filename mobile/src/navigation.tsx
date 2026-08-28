import {
  NavigationContainer,
  DefaultTheme,
  type LinkingOptions,
  type NavigationContainerRef,
} from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';

import { useRef } from 'react';

import { colors, layout, type } from './theme';
import { AlertsCard } from './notifications/AlertsCard';
import { useNotifications } from './notifications/useNotifications';
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

export type AppTabsParams = {
  Now: undefined;
  Signals: undefined;
  Chart: undefined;
  Performance: undefined;
  Status: undefined;
};

/**
 * Which URL each screen lives at.
 *
 * # Why the app has URLs at all
 *
 * On a phone this is invisible — navigation is state and nobody sees an
 * address. On the web it is the difference between an app that works and one
 * that half does, for two reasons:
 *
 *   1. A notification click has to navigate somewhere. The service worker's
 *      only move is to open a URL, and with every screen at "/" there is
 *      nowhere to send it — so tapping an alert would land on the dashboard
 *      and the signal it was about would be three taps away.
 *   2. A standalone PWA reloads. iOS evicts backgrounded web apps freely, and
 *      a relaunch that lands somewhere other than where somebody was is the
 *      small wrongness that makes an app feel broken.
 *
 * The paths are the tab names in lower case, which keeps them guessable, and
 * /signals/:id is the one that carries anything.
 */
export const linking: LinkingOptions<AppTabsParams> = {
  // The custom scheme for a native build, and the page's own origin on the
  // web. React Navigation matches the path either way; the prefix is what
  // tells it which part of an incoming URL to ignore.
  prefixes: ['btcusd://'],
  config: {
    screens: {
      Now: '',
      Signals: {
        screens: {
          SignalsList: 'signals',
          SignalDetail: 'signals/:id',
        },
      },
      Chart: 'chart',
      Performance: 'performance',
      Status: 'status',
    },
  },
};

/**
 * What the browser tab says.
 *
 * An installed PWA has no visible title, but a tab does, and so does the
 * history entry a back gesture walks through. "BTCUSD Signals" on every one of
 * them makes the history useless.
 */
const documentTitle = {
  formatter: (_options: object | undefined, route: { name?: string } | undefined) =>
    route?.name ? `${route.name} · BTCUSD Signals` : 'BTCUSD Signals',
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

export function AppTabs({ onOpenSignal }: { onOpenSignal?: (id: string) => void } = {}) {
  // The notification hook lives here, above the tabs, so a tap that arrives
  // while the app is on any screen still opens the signal — and so the
  // listener is subscribed once for the life of the process rather than
  // re-subscribed on every tab change, which would drop anything arriving in
  // the gap.
  const alerts = useNotifications({ onOpenSignal });

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
      <Tabs.Screen name="Status">
        {() => (
          <StatusScreen
            alerts={
              <AlertsCard
                state={alerts.state}
                device={alerts.device.data}
                subscription={alerts.subscription}
                error={alerts.error}
                onRequest={() => void alerts.request()}
                onRegister={() => void alerts.register()}
              />
            }
          />
        )}
      </Tabs.Screen>
    </Tabs.Navigator>
  );
}

export function Navigation({ children }: { children?: React.ReactNode }) {
  const navigator = useRef<NavigationContainerRef<Record<string, object | undefined>>>(null);

  return (
    <NavigationContainer
      ref={navigator}
      theme={navigationTheme}
      linking={linking}
      documentTitle={documentTitle}
    >
      {children ?? (
        <AppTabs
          onOpenSignal={(id) =>
            // Tapping an alert lands on that signal's detail view, wherever
            // the app happened to be.
            navigator.current?.navigate('Signals', {
              screen: 'SignalDetail',
              params: { id },
            })
          }
        />
      )}
    </NavigationContainer>
  );
}
