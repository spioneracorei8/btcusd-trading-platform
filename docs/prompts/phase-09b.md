# Phase 09b — PWA delivery for iOS

> Read `CLAUDE.md` and `docs/prompts/phase-09.md` first. This amends Phase 09's delivery target; it does not replace the screens, the theme, or the constraints.
> **Still no orders.** `CLAUDE.md` §1 stands.

---

## Why this exists

Phase 09 targeted Android and an installable APK. The device is an iPhone and
the development machine is Linux. Native iOS is closed off by both: Xcode runs
only on macOS, and even EAS Build in the cloud requires an Apple Developer
account at $99/year to sign anything that will install.

For a system that has not yet produced a strategy clearing its acceptance
criteria, that is not a good use of $99. A PWA does what this app actually
needs to do — show signals, charts, and pipeline health, and buzz the phone
when something happens.

**What carries over unchanged:** every screen in Phase 09 Part B, the theme in
Part E, the constraints in Part D, the API client, and the honest-reporting
rules. The React Native code already renders through `react-native-web` — that
is how the E6 screenshots were produced, and `tools/render.mjs` and
`tools/audit.mjs` drive it — so the screens are a delivery change, not a
rewrite.

**What changes:** the app must be served over HTTPS, and FCM native messaging
becomes Web Push.

**Be honest about the size of the second one.** `src/notifications/` is not
transport-agnostic code with FCM behind it. `useNotifications.ts` calls
`expo-notifications` directly — `getPermissionsAsync`, `getDevicePushTokenAsync`,
`addPushTokenListener`, `addNotificationResponseReceivedListener` — and
`expo-notifications` does not implement Web Push. That module is rewritten
against `Notification.requestPermission` and `PushManager`, not ported. What
survives is `registration.ts` (the three-switch logic, which is about states
rather than APIs), `payload.ts`, and both their test files. Say so up front
rather than discovering it in the middle.

---

## Part A — Web build and installability

### A1. Build target

- Expo web output via `react-native-web`, the same path `tools/audit.mjs`
  already renders through: `npx expo export --platform web`
- Static build, served by the existing api service or a small static handler
  beside it — see B2, because that choice and the HTTPS choice are the same
  choice
- Everything Phase 09 built stays: same components, same theme tokens, same
  tests

If a component turns out to be native-only, replace it rather than branching on
platform. There is one platform now.

Three things in `app.json` become dead once that is true, and dead
configuration that looks live is worse than none: the whole `android` block,
`googleServicesFile` (and the git-ignored `google-services.json` beside it),
and the `expo-build-properties` plugin whose only setting is
`usesCleartextTraffic: true` — which exists because the API is plain HTTP over
the tailnet, and which this phase makes untrue. Remove them in the same commit
that makes them dead.

### A2. Routing, which the app does not currently have

`NavigationContainer` in `src/navigation.tsx` has no `linking` configuration.
On native that is fine — navigation is state and nobody sees a URL. On web it
means all five screens and the signal detail live at `/`, and that breaks two
things this phase depends on:

- **A notification click has to open a URL.** The service worker's
  `notificationclick` handler can only navigate somewhere; with one URL there
  is nowhere to send it, and C2's "tapping an alert opens that signal" cannot
  be met
- **A standalone PWA reloads.** iOS evicts backgrounded web apps freely, and a
  relaunch that lands on the dashboard instead of where somebody was is the
  kind of small wrongness that makes an app feel broken

So: a `linking` config with a path per tab and `/signals/:id` for the detail,
and the service worker opening that path. This is new work, not carry-over.

### A3. Manifest and installability

`manifest.json` with `display: standalone`, name, `theme_color` and
`background_color` from `bg.base` — `app.json` already carries `#0D1512` as
`backgroundColor`, and the two must not drift; take both from the theme token
at build time rather than typing the literal twice. Lint forbids a hex outside
`src/theme/` and that rule should survive this phase intact.

Icons currently exist at Android sizes only (`assets/icon.png`, the adaptive
foreground and monochrome pair). iOS wants its own set; generate them from the
same source.

iOS ignores several manifest fields other platforms honour, so also emit the
`apple-mobile-web-app-*` meta tags — without them the app opens in Safari
chrome with an address bar, which defeats the point.

Verify by installing: Safari → Share → Add to Home Screen → launch from the
icon and confirm no browser chrome.

### A4. Service worker

Needed for both push and offline behaviour. Expo's metro web export does not
generate one, so this is hand-written.

- Cache the app shell so a cold launch on a dropped connection shows the UI and
  its "cannot reach the API" state rather than Safari's error page
- **Never cache API responses.** Phase 09 Part D forbids local recomputation
  for drift reasons; stale cached candles are the same hazard wearing different
  clothes. A price from twenty minutes ago rendered as current is worse than no
  price
- Version the worker and handle updates. A service worker that will not update
  is the classic PWA failure — the user sees an old build indefinitely with no
  way to know. Serve the worker file itself with `Cache-Control: no-cache`,
  or the update check is fetched from the cache it is meant to replace

---

## Part B — HTTPS

This is the part that has no way around it. iOS requires a secure context for
service workers, push, and installability. The current setup is plain HTTP over
the tailnet.

### B1. What the deployment actually has today

Worth stating, because the obvious plan is built on a file that is not in use.

`deploy/Caddyfile` exists and Phase 08 narrowed it to `/health` and `/ready`
with a 404 catch-all — but it is **not used by this deployment**, says so in
its own header, and `deploy/README.md` §9 lists "Nginx, Caddy, TLS, a public
domain" under *deliberately not here*. It is also written for a **public**
hostname (`btc.example.com`) relying on Caddy's automatic ACME. That does not
transfer: a `*.ts.net` name has no public DNS record to solve a challenge
against, so automatic HTTPS cannot issue for it and the site block would need
an explicit `tls <cert> <key>` with automation turned off.

So this is not "extend the Caddyfile". It is standing up TLS for the first
time, and the file's current shape is a starting point for a different job.

### B2. Two routes, and a recommendation

**`tailscale serve` (recommended).** Terminates TLS with the tailnet
certificate, renews it itself, and is reachable only from the tailnet by
construction — exposing it publicly is a different command (`tailscale funnel`)
rather than a misconfiguration away. One line, no renewal timer, no second
process to keep alive:

```
tailscale serve --bg --https=443 http://127.0.0.1:8080
```

If the api serves the PWA as well as `/api/v1` (A1), that single proxy covers
both, and everything is same-origin: no CORS, no second address to configure,
no mixed content.

**Caddy.** More moving parts for something this deployment does not otherwise
need: `tailscale cert` on a timer, an explicit `tls` directive, a bind to the
tailnet address only, and a process under systemd alongside the existing units.
Choose it only if something wants a real reverse proxy later, and say what that
something is.

Either way, two things are easy to miss:

- `tailscale cert` requires **HTTPS Certificates enabled in the tailnet admin
  console**, plus MagicDNS. That is a console setting, not a command, and
  without it the CLI fails in a way that reads like a bug
- Decide what happens to the plain-HTTP publish on `<tailnet-ip>:8080`. A
  secure page cannot fetch `http://` — every API call becomes mixed content and
  Safari blocks it silently. Same-origin behind the TLS terminator is the
  answer; leaving :8080 published as well is a debugging convenience that must
  not become what the app talks to

Whichever is chosen, keep what ADR 0024 was protecting: tailnet interface only,
never the public IP, and a catch-all that 404s rather than proxying whatever is
asked for. Verify from outside the tailnet that nothing is reachable, exactly
as Phase 08's checklist did.

### B3. The websocket origin check, which now genuinely fires

ADR 0024's list of what must exist before the API is exposed has an item 5:

> **CORS and origin checking.** `websocket.Accept` currently runs with
> `InsecureSkipVerify: true`, which is correct for a tailnet with no browser
> origin to check and wrong the moment a browser can reach it.

**This phase is the moment a browser can reach it.** The API stays tailnet-only,
so most of that list still does not apply — but this item is not about public
exposure, it is about a browser being the client, and after this phase it is.
`InsecureSkipVerify: true` in `server/services/stream/handler/stream_handler.go`
means any page loaded in that browser can open `/api/v1/stream` and read the
signal feed, because a websocket handshake is not subject to the same-origin
policy the way `fetch` is.

So: replace it with `OriginPatterns` naming the tailnet hostname the PWA is
served from, and update the comment, which currently gives the old reason. If
the API and the app share an origin (B2), this is one line and costs nothing.

Update ADR 0024 with what changed: TLS now exists, a browser is now a client,
item 5 is done, and the rest of the list still stands. Do not leave it
describing a configuration that no longer exists — and do not let "we updated
the ADR" become a claim that the whole list was addressed.

---

## Part C — Web Push

This replaces Phase 09 Part C. The screens and payload semantics do not change;
the transport does.

### C1. What is different from FCM

Web Push uses VAPID keys and a browser-issued subscription object, not an FCM
device token. The Phase 07 delivery worker — its retry, backoff,
permanent-versus-transient split, and `NotificationMaxAttempts = 5` — all still
apply. `notify.Sender` is already an interface with a `Channel()`, so the
worker above it does not need to learn anything. What changes is what a
registration *is*, and that reaches further than the sender:

- **`devices.token` is a single `text` column.** A subscription is three values
  — endpoint URL, `p256dh`, `auth` — so this is a migration, plus
  `models.Device`, plus the sqlc queries, plus `notify.Message.Token string`
  which currently carries the whole target
- **The `platform` CHECK is `('android', 'ios')`** and
  `constants.DevicePlatform` matches it. A PWA is not either — it is a browser
  on an OS. Add `web`, or say plainly why `ios` is the honest label for a
  home-screen PWA. Do not leave a column whose value has quietly stopped
  meaning what its comment says
- **`notifications` is unique on `(signal_id, channel)`** and
  `NotificationChannel` has one value, `fcm`. Adding `webpush` means a signal
  queued before the switch and one queued after are different rows. Decide what
  happens to in-flight `pending` rows on the FCM channel — drained, failed, or
  left — and make it something a person can see rather than a surprise
- **Payloads must be encrypted per RFC 8291.** Use a maintained library rather
  than implementing the encryption. `github.com/SherClockHolmes/webpush-go` is
  the one with actual users; state that and the reason before adding it, per
  `CLAUDE.md` §8
- **VAPID keys are new configuration.** The private key gets the treatment
  `FCM_CREDENTIALS_FILE` already gets — validated at start-up, never logged,
  never in an error message. The public key has to reach the browser, so it is
  not a secret and should not be handled as if it were
- **Retire FCM rather than keeping both.** `FCM_PROJECT_ID`,
  `FCM_CREDENTIALS_FILE`, the service-account file, `google-services.json` and
  the `expo-notifications` path all become untested code guarding a platform
  nobody can build for. Two half-tested delivery mechanisms is worse than one.
  Retire the env vars the way `FCM_DEVICE_TOKEN` was retired in Phase 09 —
  rejected at start-up with a message saying what replaced it — rather than
  ignored

### C2. iOS specifics that will bite

State these in `docs/mobile.md`, because each one produces a silent failure
that looks like a bug elsewhere:

- **Push works only from an installed PWA.** Opened in Safari, permission
  cannot even be requested. If the app is not on the home screen, notifications
  simply do not exist
- **iOS 16.4 minimum.** Check and say so plainly if the version is lower
- **Permission must follow a user gesture.** A prompt on load is silently
  rejected — prime it behind a button, which Phase 09 C1 already required for
  its own reasons, and `PERMISSION_PRIMER` already carries the words
- **Subscriptions expire.** Re-subscribe on launch and re-post if the
  subscription changed, the same rule as FCM token refresh —
  `useNotifications.test.tsx` already holds that behaviour and its assertions
  should survive the rewrite with the transport swapped underneath
- **Reinstalling produces a new subscription** and orphans the old one. The
  delivery worker's permanent-failure path handles the dead one; the app must
  register the new one

`alertState()` in `registration.ts` returns `willArrive` only when all three
switches are on — permission, a registered device, and a deployment in notify
mode. Its fourth case, "this is an emulator", becomes "this is Safari, not the
installed app", which is the same shape of answer: the honest one, not a
disabled button.

### C3. Verification

The checks from Phase 09 C3, adapted — this is the item outstanding since Phase
07 and the first real test of the whole delivery path:

- A signal produces a notification on a locked phone
- The numbers in it match the stored signal exactly, and the reference price is
  still labelled as a reference, not an entry
- Delivery is recorded `sent` in `notifications`
- An unsubscribed device results in retries then a permanent give-up, not an
  infinite queue
- Reinstalling produces a new subscription and notifications resume

Run these against the actual phone and record the result, against Phase 07's
Definition of Done. Do not tick them against a simulation.

---

## Part D — What this does not change

Everything in Phase 09 Part D still holds: no order placement, no UI resembling
it, no editing, no local recomputation, no stored credentials beyond the push
subscription. The tests enforcing those stay, `src/__tests__/no-orders.test.ts`
included — and its assertion that the only non-GET request is `/device` still
has to hold when `/device` changes shape.

The theme in Part E is unchanged. Three things to confirm survived the
transition:

- **Tabular figures.** Web font stacks handle these differently from native,
  and a price that jitters as it updates is the failure this rule exists to
  prevent. `type.tabular` is applied through `<Text tabular>` in every numeric
  cell; confirm it still produces fixed-width digits in Safari
- **`bg.base` on the manifest and status bar**, so the app does not launch with
  a white flash before the first paint
- **`normalise()` in `src/api/config.ts` assumes `http://`** for a scheme-less
  address, with a comment giving the reason: "this is a tailnet address and
  there is no TLS in front of it (ADR 0024)". After Part B that reason is
  false, and the default `DEFAULT_BASE_URL` is `http://100.64.0.1:8080`. If the
  app is same-origin, the honest fix is for the base URL to default to the
  page's own origin and for configuration to become the exception rather than
  the norm

---

## Definition of Done

- [ ] Installs to the iPhone home screen and launches without browser chrome
- [ ] Served over HTTPS with a Tailscale certificate; renewal is automatic and
      it is stated which mechanism renews it
- [ ] Unreachable from outside the tailnet, verified as in Phase 08
- [ ] The PWA and `/api/v1/*` are served, and the catch-all still 404s
- [ ] Websocket origin checking replaces `InsecureSkipVerify`, and the comment
      gives the new reason
- [ ] ADR 0024 updated: what now exists, and which items on its list are still
      outstanding
- [ ] URLs exist per screen, and `/signals/:id` opens the detail directly
- [ ] All five screens work on the phone against the live API
- [ ] Service worker caches the shell and never caches API responses
- [ ] Service worker updates cleanly to a new build
- [ ] Web Push delivers to the locked phone; all five checks in C3 verified on
      the device
- [ ] **Phase 07's outstanding Definition of Done item closed and recorded**
- [ ] Subscription refresh re-registers, with the Phase 09 tests still holding
- [ ] FCM configuration retired at start-up rather than ignored
- [ ] Dead Android configuration removed from `app.json`
- [ ] Tabular figures confirmed in the web build
- [ ] `docs/mobile.md` rewritten for PWA install, HTTPS setup, and the iOS
      constraints in C2
- [ ] ADR recording the PWA trade-off and the condition to revisit it
- [ ] No order-placing code or UI, still enforced
- [ ] No hex literal outside `src/theme/`, still enforced

---

## Out of scope

- Native iOS or Android builds
- Apple Developer account, App Store, TestFlight
- Public exposure beyond the tailnet
- Offline use beyond the cached shell
- The rest of ADR 0024's list — authentication, a secret store, rotation, rate
  limiting. The API stays tailnet-only, so only the origin-checking item fires
- Trading, in any form

---

## A note on the trade

A PWA is a real compromise and worth naming rather than glossing.
Notifications are less reliable than native — iOS may throttle a backgrounded
web app, and there is no equivalent of a high-priority native push. For a
strategy producing roughly one signal every ten days, on 4h bars, where the
response is to open a broker app and place an order by hand, that latency does
not matter. If a strategy ever emerges that needs alerts within seconds, this
decision should be revisited — and by then $99 would be an easy call.

Record that in ADR 0028: what was chosen, what it costs, and the condition
under which it should be reconsidered. A compromise nobody wrote down becomes a
constraint nobody remembers choosing.

---

## How to start

Part B first — HTTPS gates everything else, and there is no point building
against a transport that cannot carry push. B2's choice between `tailscale
serve` and Caddy also decides whether the app is same-origin, which decides
whether Part C has a CORS problem and whether A2's routing is at the root or
under a prefix. Then A, then C.

Summarise the plan and wait for approval.
