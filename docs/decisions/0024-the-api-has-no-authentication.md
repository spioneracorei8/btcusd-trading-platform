# ADR 0024 — The API has no authentication; the network is the boundary

Status: accepted
Date: 2026-08-23
Phase: 08 (part B)
Amended: 2026-08-28, phase 09b — see "What phase 09b changed"

## Context

Phase 08 added the endpoints the mobile app consumes: `/api/v1/candles`,
`/indicators`, `/signals`, `/signals/{id}`, `/outcomes`, `/performance`,
`/status` and the `/stream` websocket. None of them asks who is calling.

This is a single-owner system. There is one person, one phone, one instrument.
There are no accounts, no roles, no sharing, and there is nothing to
multi-tenant. Adding a login would be adding a login to a system with one user.

What the endpoints expose is worth being precise about, because "it's only
read-only" is not the argument:

- the strategy names, versions and resolved parameter sets in use
- every signal produced, with entry, stop and target, and the full reason
  behind each one
- what happened to each of them and the aggregate performance
- the pipeline's operational state, including whether the collector is alive

That is the owner's research. It says nothing about anyone else and it moves no
money — the system cannot place an order (`CLAUDE.md` §1, enforced by
`TestNothingReachesATradingEndpoint`) — but it is not public information, and a
strategy is worth less the more people run it.

## Decision

No authentication in the application. Reachability is the control, and it is
enforced twice:

1. **Binding.** The api container publishes on `127.0.0.1` and on nothing
   else. It used to publish on this host's tailnet address as well, guarded by
   `${TAILSCALE_IP:?...}` so an unset value aborted the command rather than
   expanding to `:8080:8080` and binding every interface. Phase 09b removed
   that mapping entirely — see below — which makes the guard unnecessary
   rather than merely enforced: there is no port list left to get wrong.
2. **Firewall.** `ufw` is default-deny inbound with an allow rule on
   `tailscale0` only (`deploy/README.md` §7). Reaching the tailnet address at
   all requires being an authenticated tailnet peer.

The phone is on the tailnet. That is the whole access story.

`deploy/Caddyfile` — a public-hostname reverse proxy, kept unused since phase
07 — is narrowed by this ADR to `/health` and `/ready` and denies everything
else. It previously ended in a catch-all `handle` with a note to open it up
"after the API gains real endpoints in phase 08". Following that note now would
have published every endpoint above to the internet, unauthenticated, and
nothing in the file would have said so. The catch-all is gone; see below for
what re-opening it would require.

## Verification

The binding was checked rather than assumed, against
`deploy/docker-compose.yml` + `deploy/docker-compose.prod.yml`.

At the time this was written the api resolved to two published addresses and
postgres to one:

```
api       127.0.0.1:8080 -> 8080
          100.72.14.3:8080 -> 8080
postgres  127.0.0.1:5432 -> 5432
```

Since phase 09b the tailnet mapping is gone and it is one each:

```
api       127.0.0.1:8080 -> 8080
postgres  127.0.0.1:5432 -> 5432
```

The Go process inside the container listens on `:8080`, which is every
interface *of the container*. What constrains it is the publish mapping above;
there is no host interface it can reach that is not in that list.

## What phase 09b changed

The app became a PWA, which forced two things this ADR had written down as
future work.

**TLS exists now.** iOS gives a plain-HTTP page no service worker, no push and
no home-screen install, so the tailnet needed HTTPS. It came from
`tailscale serve`, which terminates TLS with the tailnet certificate, renews it
itself, and listens on the tailnet interface only — making it public is a
different command (`tailscale funnel`), not a misconfiguration away. The api's
tailnet port was removed at the same time: a page served over HTTPS cannot
fetch `http://`, so the plain port could not be what the app talks to, and a
second door that only half works is a way to lose an afternoon.

Item 7 on the list below asked what TLS terminating in front of the api would
mean for the hop behind it. The answer here is loopback inside one host, which
is the cheapest possible version of that question.

**Item 5 came due, and only half of it.** The list says origin checking becomes
necessary "the moment a browser can reach it". Serving the app in a browser is
that moment, and it arrived without the API becoming public — the trigger is
the client being a browser, not the network being open.

`websocket.Accept` no longer runs with `InsecureSkipVerify`. A websocket
handshake is not bound by the same-origin policy the way `fetch` is, so any
page the owner opened in the same browser could otherwise have opened
`/api/v1/stream` and read every signal with its entry, stop, target and full
reason — no prompt, no preflight to fail, nothing in a log that reads as an
intrusion.

The check needs no configuration because the app and the API share an origin:
the api serves the built app itself, so the page's origin is the host it calls.
That was chosen for this reason. Cross-origin would mean CORS on every endpoint
plus an allowlist on the websocket, and the reflex when a preflight fails is to
widen the allowlist until it stops failing. `STREAM_ALLOWED_ORIGINS` exists for
development and is empty in the deployment; `*` is refused at start-up, because
it is the check switched off while looking like it is on.

**The CORS half of item 5 is still outstanding**, in the sense that there is no
CORS handling at all — and it is not needed, because nothing is cross-origin.
The moment something is, it comes back.

Nothing else on the list moved. There is still no authentication, no secret
store, no rotation, no rate limiting, and no decision about what `/status` may
say. The API is still tailnet-only, and this decision still expires the day it
is not.

## What would have to change before this is exposed publicly

If the API is ever reachable from outside the tailnet — a public hostname, a
second user, a web client, a friend's phone — this decision expires. It does
not degrade gracefully, so the list is written down rather than reasoned out
later:

1. **Authentication before routing.** A token in `Authorization`, checked in
   middleware ahead of the `/api/v1` mount, so a new endpoint is protected by
   existing whether or not somebody remembered. Compared in constant time. The
   websocket needs it too — at the handshake, before `websocket.Accept`,
   because a subscription is a long-lived read.
2. **A place to keep the secret.** Not `.env` beside the fee configuration: a
   file with restrictive permissions, or the platform's secret store, with the
   same treatment `FCM_CREDENTIALS_FILE` already gets — validated at start-up,
   never logged, never in an error message.
3. **Rotation, and revocation that works.** A single static token that cannot
   be revoked without a redeploy is not much better than none once it has
   leaked into a screenshot.
4. **Rate limiting.** `/indicators` recomputes over a warm-up read and
   `/performance` aggregates the whole outcome history. Both are cheap for one
   phone and are a denial-of-service tool for anyone else.
5. **CORS and origin checking.** `websocket.Accept` currently runs with
   `InsecureSkipVerify: true`, which is correct for a tailnet with no browser
   origin to check and wrong the moment a browser can reach it.
6. **A decision about what `/status` may say.** It names the strategy, its
   timeframe and its warm-up state. That is operational detail for the owner
   and reconnaissance for anyone else.
7. **TLS all the way in.** Tailscale encrypts the tailnet; a public hostname
   means terminating TLS at Caddy and deciding what runs on the hop behind it.

None of that is hard. All of it is easy to half-do, and half-done
authentication reads as authentication.

## Consequences

- The app cannot reach the API without Tailscale. That is a real cost: a phone
  off the tailnet sees nothing, and it is the same cost that keeps the
  endpoints private.
- There is no audit trail of who read what. With one reader there is nothing to
  attribute.
- A device that joins the tailnet gets full read access. Tailnet membership is
  therefore the access decision, and it is made in the Tailscale admin console
  rather than in this repository.
- The endpoints stay simple: no token plumbing, no refresh, no 401 path in the
  app.

## Alternatives considered

**A static bearer token now.** Rejected as security theatre in this topology:
the token would sit in the same `.env` on the same host, and anything that
could read it is already on the tailnet. It would add a code path nobody tests
and would make the next reader believe the API is authenticated when what
protects it is the network.

**mTLS.** Correct and disproportionate. Certificate distribution to one phone,
by hand, renewing.

**Tailscale Funnel or a public hostname with Caddy basic auth.** Both make the
API publicly routable, which is the thing this decision is about; basic auth
over a public hostname needs everything in the list above anyway.

## References

- ADR 0017 — VPS deployment, where the tailnet-only posture was chosen
- ADR 0028 — the PWA, which is why TLS and the origin check exist
- `deploy/docker-compose.prod.yml` — the binding
- `deploy/README.md` §2.6 — TLS, and §7 — the firewall
- `server/services/stream/handler/stream_handler.go` — the origin check
- `docs/api.md` — the endpoints this covers
