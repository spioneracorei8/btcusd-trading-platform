import { ApiError } from './errors';
import type {
  ApiErrorBody,
  CandlesResponse,
  DeviceResponse,
  Direction,
  IndicatorsResponse,
  OutcomeStatus,
  OutcomesResponse,
  PerformanceResponse,
  Signal,
  SignalsResponse,
  Status,
  Timeframe,
} from './types';

/**
 * The client for this project's API.
 *
 * It talks to one server and nothing else. There is no authentication — the
 * network is the boundary (ADR 0024) — and there is exactly one request here
 * that is not a read: registering this device for alerts.
 */

/** How long to wait before calling the server unreachable.
 *
 * Short on purpose. Over a tailnet on a working connection every endpoint
 * here answers in well under a second, and the case this bounds is not a slow
 * server but an absent one — where the request would otherwise hang until the
 * platform's own timeout, which on Android is a minute or more of spinner. */
export const REQUEST_TIMEOUT_MS = 8000;

/**
 * The most candles the app will ask for.
 *
 * The API caps this at 5000 and refuses politely. The app refuses first,
 * because a request it knows is wrong should not become a round trip — and
 * because "three years of 1m candles" is a chart gesture rather than a typo,
 * so the refusal has to be something the chart can handle rather than an
 * error somebody sees.
 */
export const MAX_CANDLES = 5000;

export type ClientOptions = {
  baseUrl: string;
  /** Injected so tests do not monkey-patch the global. */
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
};

export type CandlesQuery = {
  timeframe: Timeframe;
  from?: Date;
  to?: Date;
  limit?: number;
};

export type SignalsQuery = {
  limit?: number;
  offset?: number;
  direction?: Direction;
};

export type OutcomesQuery = {
  from?: Date;
  to?: Date;
  status?: OutcomeStatus;
  limit?: number;
  offset?: number;
};

export class ApiClient {
  readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;
  private readonly timeoutMs: number;

  constructor({ baseUrl, fetchImpl, timeoutMs }: ClientOptions) {
    this.baseUrl = baseUrl.replace(/\/+$/, '');
    // Bound, not captured. `const f = fetch; f(url)` throws "Illegal
    // invocation" in a browser, because fetch needs `this` to be the global.
    // React Native happens to tolerate a detached reference, so this would
    // have shipped and broken only on web — and only once somebody rendered
    // it, which is how it was found.
    this.fetchImpl = fetchImpl ?? ((input, init) => globalThis.fetch(input, init));
    this.timeoutMs = timeoutMs ?? REQUEST_TIMEOUT_MS;
  }

  // -------------------------------------------------------------------------
  // Reads
  // -------------------------------------------------------------------------

  candles(query: CandlesQuery, signal?: AbortSignal): Promise<CandlesResponse> {
    const limit = query.limit ?? 500;
    if (limit > MAX_CANDLES) {
      // Refused here rather than at the server. See MAX_CANDLES.
      return Promise.reject(
        new ApiError(
          'request',
          `${limit} candles is more than the ${MAX_CANDLES} this API returns; ` +
            `narrow the window or ask for a longer timeframe`,
          { code: 'limit_exceeded' },
        ),
      );
    }

    return this.get('/candles', signal, {
      timeframe: query.timeframe,
      from: query.from?.toISOString(),
      to: query.to?.toISOString(),
      limit: String(limit),
    });
  }

  indicators(
    query: { timeframe: Timeframe; from?: Date; to?: Date },
    signal?: AbortSignal,
  ): Promise<IndicatorsResponse> {
    return this.get('/indicators', signal, {
      timeframe: query.timeframe,
      from: query.from?.toISOString(),
      to: query.to?.toISOString(),
    });
  }

  signals(query: SignalsQuery = {}, signal?: AbortSignal): Promise<SignalsResponse> {
    return this.get('/signals', signal, {
      limit: query.limit === undefined ? undefined : String(query.limit),
      offset: query.offset === undefined ? undefined : String(query.offset),
      direction: query.direction,
    });
  }

  signal(id: string, abort?: AbortSignal): Promise<Signal> {
    return this.get(`/signals/${encodeURIComponent(id)}`, abort);
  }

  outcomes(query: OutcomesQuery = {}, signal?: AbortSignal): Promise<OutcomesResponse> {
    return this.get('/outcomes', signal, {
      from: query.from?.toISOString(),
      to: query.to?.toISOString(),
      status: query.status,
      limit: query.limit === undefined ? undefined : String(query.limit),
      offset: query.offset === undefined ? undefined : String(query.offset),
    });
  }

  performance(
    query: { from?: Date; to?: Date } = {},
    signal?: AbortSignal,
  ): Promise<PerformanceResponse> {
    return this.get('/performance', signal, {
      from: query.from?.toISOString(),
      to: query.to?.toISOString(),
    });
  }

  status(signal?: AbortSignal): Promise<Status> {
    return this.get('/status', signal);
  }

  device(signal?: AbortSignal): Promise<DeviceResponse> {
    return this.get('/device', signal);
  }

  // -------------------------------------------------------------------------
  // The one write
  // -------------------------------------------------------------------------

  /**
   * Registers this phone for alerts.
   *
   * The only request in this app that changes anything on the server, and it
   * changes one row that says where notifications go. See ADR 0026 for why
   * the token comes from here rather than from configuration.
   */
  registerDevice(
    body: { token: string; platform?: string; label?: string },
    signal?: AbortSignal,
  ): Promise<DeviceResponse> {
    return this.send('/device', 'POST', body, signal);
  }

  // -------------------------------------------------------------------------

  private get<T>(
    path: string,
    signal?: AbortSignal,
    params: Record<string, string | undefined> = {},
  ): Promise<T> {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined) search.set(key, value);
    }
    const query = search.toString();
    return this.request<T>(`${path}${query ? `?${query}` : ''}`, { method: 'GET' }, signal);
  }

  private send<T>(
    path: string,
    method: string,
    body: unknown,
    signal?: AbortSignal,
  ): Promise<T> {
    return this.request<T>(
      path,
      {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      },
      signal,
    );
  }

  private async request<T>(path: string, init: RequestInit, signal?: AbortSignal): Promise<T> {
    const url = `${this.baseUrl}/api/v1${path}`;

    // The caller's signal and the timeout both have to be able to abort this,
    // and the timeout has to be cleared however the request ends or a pending
    // timer keeps the runtime awake.
    const controller = new AbortController();
    const onAbort = () => controller.abort();
    signal?.addEventListener('abort', onAbort);
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);

    let response: Response;
    try {
      response = await this.fetchImpl(url, { ...init, signal: controller.signal });
    } catch (cause) {
      if (signal?.aborted) throw cause; // the caller changed its mind
      throw new ApiError(
        'unreachable',
        `no answer from ${this.baseUrl}`,
        { cause },
      );
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
    }

    const text = await response.text();

    if (!response.ok) {
      throw this.failure(response.status, text);
    }

    try {
      return JSON.parse(text) as T;
    } catch (cause) {
      throw new ApiError('malformed', 'the response was not JSON', {
        status: response.status,
        cause,
      });
    }
  }

  /** Turns a non-2xx into the kind of failure a screen can explain. */
  private failure(status: number, text: string): ApiError {
    let code: ApiErrorBody['error']['code'] | undefined;
    let message = `the server answered ${status}`;

    try {
      const body = JSON.parse(text) as Partial<ApiErrorBody>;
      if (body.error?.code) {
        code = body.error.code;
        message = body.error.message || message;
      }
    } catch {
      // A non-JSON body from a proxy or a crash. The status is still the
      // useful part, and it is already in `message`.
    }

    // 5xx is the server's problem and might pass; 4xx is this app asking for
    // something wrong and will not.
    const kind = status >= 500 ? 'server' : 'request';
    return new ApiError(kind, message, { status, code });
  }
}
