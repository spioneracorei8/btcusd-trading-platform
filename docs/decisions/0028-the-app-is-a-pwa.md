# ADR 0028 — The app is a PWA, and what that costs

Status: accepted
Date: 2026-08-28
Phase: 09b

## Context

Phase 09 built the app against Android and an installable APK. The phone is an
iPhone and the development machine is Linux, which closes native iOS twice
over: Xcode runs only on macOS, and even EAS Build in the cloud will not sign
anything installable without an Apple Developer account at $99 a year.

The system this app reads has produced no strategy that clears its acceptance
criteria. Sixty-six evaluations say the edge in these rules is thin enough that
a 1.5× move in costs erases it. Spending $99 to deliver alerts from a pipeline
that has not yet found anything worth being alerted about is the wrong order to
do things in.

The screens do not care. They were built in React Native and already render
through `react-native-web` — that is how the phase 09 screenshots and the theme
audit were produced. What changes is delivery and transport, not the app.

## Decision

Ship it as a PWA, installed to the home screen from Safari, served over HTTPS
from the tailnet, with Web Push instead of FCM.

Three things follow, and they are the substance of this decision rather than
consequences of it:

**TLS is required, not optional.** iOS gives a page served over plain HTTP no
service worker, no push, and no home-screen install. `tailscale serve`
terminates TLS with the tailnet certificate and renews it itself, so there is
no reverse proxy, no ACME, and no renewal timer to forget in ninety days. It
listens on the tailnet only; publishing is a different command.

**The api serves the app.** One origin for the page and the endpoints. The
alternative — a static server beside the api — means CORS on every endpoint and
an origin allowlist on the websocket, and the reflex when a preflight fails is
to widen the allowlist until it stops failing. Same-origin has none of those
decisions in it, which is worth more here than the tidiness of separate
processes.

**The plain tailnet port goes.** A page served over HTTPS cannot fetch
`http://`; Safari blocks it and says nothing useful. Leaving `:8080` published
would leave a second door the app cannot use, which is a way to spend an
afternoon.

## What it costs

Worth naming rather than glossing, because a compromise nobody wrote down
becomes a constraint nobody remembers choosing.

**Notifications are less reliable than native.** iOS throttles a backgrounded
web app freely, and there is no equivalent of a high-priority native push.
Delivery is best-effort and the latency is not bounded.

**Push works only from the installed app.** Opened in Safari, permission cannot
even be requested. A phone that has not added the app to its home screen has no
notifications at all, and nothing about that state announces itself.

**Subscriptions expire and reinstalls orphan them.** The same class of problem
as FCM token rotation, handled the same way — the app re-subscribes on launch
and re-posts if the subscription changed.

**Nothing is reachable off the tailnet.** Unchanged from ADR 0024, and the same
cost it always was.

For a strategy producing roughly one signal every ten days, on 4h bars, where
the response is to open a broker app and place an order by hand, none of that
matters. The alert is a prompt to go and look, not a trigger.

## When to revisit

**If a strategy emerges that needs alerts within seconds.** Then the latency
matters, best-effort is not good enough, and $99 is an easy call against a rule
that is actually earning. That is the condition; it is not a matter of taste.

Two weaker signals, worth noticing but not sufficient on their own: a second
device that is not an iPhone, or iOS changing what an installed web app may do.

## Alternatives considered

**Pay the $99 and build native.** The correct answer once there is something to
deliver, and premature now. It also does not remove any of the work here —
the screens are the same either way.

**Android hardware.** The phone is the phone.

**Email or a messaging bot instead of push.** Rejected as a worse version of the
same trade: latency no better, one more account and one more credential, and a
notification that arrives in a stream of other notifications rather than as
the only thing that app ever does.

**Keep FCM as well as Web Push.** Rejected in phase 09b. There is one device,
it is iOS, and it cannot use FCM. Two delivery paths where only one is ever
exercised means the untested one is broken and nobody knows.

## References

- ADR 0024 — the API's access boundary, amended by this phase for TLS and the
  websocket origin check
- ADR 0026 — the phone registers itself, which is the shape a subscription
  slots into
- `docs/prompts/phase-09b.md` — the phase
- `deploy/README.md` §2.6 — how TLS is actually set up
