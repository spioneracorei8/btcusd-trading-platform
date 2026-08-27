import type { ApiErrorCode } from './types';

/**
 * What went wrong, in the terms the screen has to explain it in.
 *
 * # Why unreachable is its own kind
 *
 * This app talks to one server, over a tailnet, and the commonest failure by
 * a wide margin is that Tailscale is off. A spinner is the wrong answer to a
 * VPN that is switched off: it says "wait" about a condition that will not
 * resolve by waiting, and the person holding the phone has no way to tell it
 * apart from a slow server or a quiet market.
 *
 * So reachability is a distinct kind rather than an `error` with a message,
 * and every screen renders it as an instruction rather than as a failure.
 */
export type FailureKind =
  /** The request never got an answer: no route, DNS, timeout, refused. */
  | 'unreachable'
  /** The server answered, with a refusal we asked for. */
  | 'request'
  /** The server answered, and something is wrong on its side. */
  | 'server'
  /** The server answered something this app cannot read. */
  | 'malformed';

export class ApiError extends Error {
  readonly kind: FailureKind;
  readonly status?: number;
  readonly code?: ApiErrorCode;

  constructor(
    kind: FailureKind,
    message: string,
    options: { status?: number; code?: ApiErrorCode; cause?: unknown } = {},
  ) {
    super(message, { cause: options.cause });
    this.name = 'ApiError';
    this.kind = kind;
    this.status = options.status;
    this.code = options.code;
  }

  /** True when retrying might work. An unreachable server might come back;
   * a 400 will not. */
  get retryable(): boolean {
    return this.kind === 'unreachable' || this.kind === 'server';
  }
}

/**
 * What to put on the screen, for a person who is holding a phone and does not
 * have the server's logs.
 *
 * Reachability names Tailscale explicitly. That is the whole reason this
 * function exists: "Network request failed" is true, useless, and identical
 * for a VPN that is off, a server that is down, and a laptop on a different
 * network.
 */
export function explain(error: unknown, baseUrl: string): {
  title: string;
  detail: string;
  action?: string;
} {
  if (!(error instanceof ApiError)) {
    return {
      title: 'Something went wrong',
      detail: error instanceof Error ? error.message : String(error),
    };
  }

  switch (error.kind) {
    case 'unreachable':
      return {
        title: 'Cannot reach the server',
        detail:
          `Nothing answered at ${baseUrl}. This deployment is only reachable ` +
          `over the tailnet, so the usual cause is Tailscale being switched off.`,
        action: 'Open Tailscale and check this device is connected, then pull to retry.',
      };

    case 'request':
      return {
        title: 'The server refused that request',
        detail: error.message,
        action: 'This is a bug in the app rather than something you can fix.',
      };

    case 'server':
      return {
        title: 'The server had a problem',
        detail: error.message,
        action: 'The detail is in the server log against this request. Pull to retry.',
      };

    case 'malformed':
      return {
        title: 'The server said something unexpected',
        detail: error.message,
        action:
          'The app and the API may be out of step. Check the server version ' +
          'against docs/api.md.',
      };
  }
}
