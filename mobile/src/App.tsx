import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StatusBar } from 'expo-status-bar';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { ApiProvider } from './api/provider';
import { Navigation } from './navigation';
import { UpdateBanner } from './pwa/UpdateBanner';
import { configureForegroundBehaviour } from './notifications/useNotifications';
import type { ApiClient } from './api/client';

/**
 * How often a failed request is tried again.
 *
 * Once. This app talks to one server over a VPN, and the failure it will
 * actually see is that the VPN is off — which no number of retries fixes, and
 * which every retry delays telling the user about.
 */
// A signal arriving while somebody is looking at the chart is exactly as
// interesting as one arriving while the phone is locked. Set once, at module
// scope, because the handler is global to the process.
configureForegroundBehaviour();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

/** The root, and the shape the screenshot harness renders against. */
export function Root({ client }: { client?: ApiClient } = {}) {
  return (
    <SafeAreaProvider>
      <QueryClientProvider client={queryClient}>
        <ApiProvider client={client}>
          <StatusBar style="light" />
          {/* Above the navigator, so it is not inside a screen that scrolls
              away from it. It renders nothing until a build is waiting. */}
          <UpdateBanner />
          <Navigation />
        </ApiProvider>
      </QueryClientProvider>
    </SafeAreaProvider>
  );
}

/**
 * The registered component. It takes no props: registerRootComponent passes
 * Expo's own initial props, and a root that also accepted a client would let
 * those two disagree about what it is being given.
 */
export default function App() {
  return <Root />;
}
