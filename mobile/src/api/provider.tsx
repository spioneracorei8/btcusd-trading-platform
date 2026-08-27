import { createContext, useContext, useEffect, useMemo, useState } from 'react';

import { ApiClient } from './client';
import { DEFAULT_BASE_URL, loadBaseUrl, saveBaseUrl } from './config';

type ApiContextValue = {
  client: ApiClient;
  baseUrl: string;
  setBaseUrl: (url: string) => Promise<void>;
  /** False until the stored address has been read, so a screen does not fire
   * its first request at the default and then again at the real one. */
  ready: boolean;
};

const ApiContext = createContext<ApiContextValue | null>(null);

export function ApiProvider({
  children,
  client: injected,
}: {
  children: React.ReactNode;
  /** Supplied by tests and by the screenshot harness. */
  client?: ApiClient;
}) {
  const [baseUrl, setStored] = useState(injected?.baseUrl ?? DEFAULT_BASE_URL);
  const [ready, setReady] = useState(injected !== undefined);

  useEffect(() => {
    if (injected) return;
    let live = true;
    void loadBaseUrl().then((url) => {
      if (!live) return;
      setStored(url);
      setReady(true);
    });
    return () => {
      live = false;
    };
  }, [injected]);

  const value = useMemo<ApiContextValue>(
    () => ({
      client: injected ?? new ApiClient({ baseUrl }),
      baseUrl,
      ready,
      setBaseUrl: async (url: string) => {
        setStored(await saveBaseUrl(url));
      },
    }),
    [injected, baseUrl, ready],
  );

  return <ApiContext.Provider value={value}>{children}</ApiContext.Provider>;
}

export function useApi(): ApiContextValue {
  const value = useContext(ApiContext);
  if (!value) throw new Error('useApi outside an ApiProvider');
  return value;
}
