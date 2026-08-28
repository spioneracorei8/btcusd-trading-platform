import { useEffect, useRef, useState } from 'react';

import { register, type UpdateState } from './register';

/**
 * Whether a newer build of the app is installed and waiting.
 *
 * Registers the worker on mount, which is also the only place it is
 * registered: doing it at module scope would run it in every test and in the
 * screenshot harness, where there is no worker and no reason to want one.
 */
export function useUpdate(): { state: UpdateState; apply: () => void } {
  const [state, setState] = useState<UpdateState>('none');
  const apply = useRef<() => void>(() => {});

  useEffect(() => {
    let live = true;
    let stop: (() => void) | undefined;

    void register((next) => {
      if (live) setState(next);
    })
      .then((registered) => {
        if (!registered) return;
        if (!live) {
          registered.stop();
          return;
        }
        apply.current = registered.apply;
        stop = registered.stop;
      })
      .catch(() => {
        // A worker that will not register is not worth a message. The app is
        // fully usable without one — it loses offline shell and nothing else,
        // and the person cannot act on the failure anyway.
      });

    return () => {
      live = false;
      stop?.();
    };
  }, []);

  return { state, apply: () => apply.current() };
}
