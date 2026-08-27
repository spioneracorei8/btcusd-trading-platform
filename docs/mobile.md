# The mobile app

One person, one phone, on a tailnet. Five screens, no accounts, no settings
beyond the address of the server.

**It cannot place an order and it must never look like it can.** That is
`CLAUDE.md` §1, and on a phone it is a UI constraint as much as a code one:
the gap between showing a signal and placing one has to stay wide enough that
nobody crosses it by muscle memory. Enforced by
`src/__tests__/no-orders.test.ts`.

## Contents

- [What it is honestly for](#what-it-is-honestly-for)
- [Building](#building)
- [Installing](#installing)
- [Configuring](#configuring)
- [The five screens](#the-five-screens)
- [Push notifications](#push-notifications)
- [Closing phase 07's open item](#closing-phase-07s-open-item)
- [The theme](#the-theme)
- [Verification](#verification)
- [What has not been done here](#what-has-not-been-done-here)

---

## What it is honestly for

The strategy search has not produced anything clearing
`docs/acceptance-criteria.md`. The best available is `ema_crossover` on 4h at
defaults: **+4.78%, profit factor 1.13, edge gone at 1.5× cost, profitable in
one of two years.**

So this is not an app for acting on signals with confidence. It is a way to see
what the system is producing, and to notice when live behaviour departs from
what the backtest said.

That has a concrete consequence throughout: **every performance figure shows
the sample it was drawn from, at 14pt, and the insufficient-sample banner
renders above the numbers rather than beside them.** Phase 08 found that a
banner beside a figure loses to the figure — the eye reads the number and forms
a view before it reaches the caveat. A phone invites glancing, and a glance is
where a number gets believed without its caveats.

---

## Building

```console
$ cd mobile
$ npm install
$ npm run check          # typecheck, lint, test
```

`npm run check` is what has to pass. It runs:

| | |
|---|---|
| `npm run typecheck` | `tsc --noEmit`, strict, with `noUncheckedIndexedAccess` |
| `npm run lint` | ESLint, including the rule that fails on a colour literal outside `src/theme/` |
| `npm test` | Jest — 169 tests |

### The APK

Android only. iOS needs a Mac and a developer account; do not build for a
platform you cannot run on.

```console
$ npx expo prebuild --platform android
$ cd android && ./gradlew assembleRelease
# app/build/outputs/apk/release/app-release.apk
```

This needs the Android SDK and a JDK. **It has not been run in the development
environment for this phase** — `dl.google.com` is blocked by network policy
there, so no SDK could be installed. Everything else in this document was
verified; the APK build is the one step waiting on a machine that can reach
Google.

Before the first build you need `google-services.json` from the Firebase
console, in `mobile/`. It is git-ignored: it identifies the project and is per
deployment, so a committed one is the wrong project's.

---

## Installing

Sideload. There is no app store listing and there will not be one.

```console
$ adb install -r app-release.apk
```

Or copy the APK to the phone and open it, with "install unknown apps" allowed
for whatever opened it.

---

## Configuring

One setting: where the API is.

It defaults to `EXPO_PUBLIC_API_BASE_URL` at build time, or
`http://100.64.0.1:8080` — replace that with what `tailscale ip -4` prints on
the VPS. The value is persisted on the device, so it survives a restart, and it
is a setting rather than a constant because a rebuilt VPS gets a new tailnet
address and the app would otherwise be permanently unable to reach a server
that is running fine.

**The app reaches the API only over the tailnet.** There is no authentication —
the network is the boundary (ADR 0024) — so if Tailscale is off, nothing works.
That is the commonest failure this app will ever see, and it is handled
explicitly rather than as a spinner:

> **Cannot reach the server**
> Nothing answered at http://100.72.14.3:8080. This deployment is only
> reachable over the tailnet, so the usual cause is Tailscale being switched
> off.
> Open Tailscale and check this device is connected, then pull to retry.

A spinner is the wrong answer to a VPN that is switched off: it says "wait"
about a condition that will not resolve by waiting. Note that this message
appears **only** when the request got no answer — a 500 from a server that
replied does not blame the VPN, because that would send somebody to the wrong
place.

---

## The five screens

Each answers one question.

### Now — what is happening right now

The latest signal with its levels, its outcome so far, and pipeline health.

**Most of the time this screen is blank, and that is the interesting part.** A
4h strategy signals about once every ten days, so blank is the normal state —
and blank has four causes that demand completely different responses:

| What it says | What it means |
|---|---|
| No strategy is running | `STRATEGY_NAME` is unset. A configuration, not a fault. |
| The strategy is not deciding yet | Warming up. Carries the server's own reason, with the bar counts. |
| No setup found | Warm, running, and nothing to signal. **The ordinary state.** |
| The collector has stopped | Ingestion is down. Nothing is even being looked at. |

A screen rendering the same emptiness for all four teaches its reader that
blank means "nothing happened" — and the day blank means "the collector died
three weeks ago", they would not notice.

The decided price is labelled **reference price**, never entry. A decision
taken on a bar's close cannot fill on it, so the entry is the next bar's open
plus slippage and the two differ every time.

### Signals — what has it produced

Reverse-chronological, filterable by direction and by outcome status. Tapping a
row opens the detail view, which is the point of the tab: it carries the full
`reason` — the trigger, the resolved parameters, the indicator values at the
deciding bar, **and anything this build has never heard of**.

That last part matters. Indicators are never stored, so the values behind a
decision cannot be recomputed against the warm-up state the live process
actually had. The reason blob is the only record there will ever be, and a
strategy shipped after this app will record fields it has no name for.

### Chart — what did the market do around it

Candles for a selectable timeframe, with signal entries and exits marked and
the stop and target drawn as dashed horizontals.

It opens on **where the series ends**, not on wall-clock now: a chart anchored
on the clock is blank whenever the collector is behind, and blank looks
identical to a market that stopped trading. `/api/v1/status` carries
`latest_open_time` per timeframe for exactly this.

A forming candle — one that has not closed — is drawn **hollow and at lower
opacity**, and the chart says so in words. The websocket is the one place in
this system permitted to send an unclosed bar, and a flag nothing renders is a
flag nobody sees.

Panning holds a window one screen wider than the view in each direction, so an
ordinary drag costs no request. Asking for more bars than the API returns is
refused before the request is sent.

### Performance — is it working

Live outcomes, after modelled costs, grouped by strategy and parameter set.

- The **sample banner is above the numbers**, not beside them.
- Every figure carries **its sample size at 14pt** — not caption size. A
  sample size is a qualifier on the number beside it, and a qualifier set
  smaller than the figure loses the argument.
- **Expectancy** is the headline, because a win rate alone does not decide
  anything: 30% at a 3:1 payoff beats 60% at 1:2.
- **The expected wait is stated.** At a tenth of a trade a day, 100 resolved
  signals is close to three years. A performance screen that looks nearly ready
  to tell you something, for years, is worse than one that says how long.
- **There is no total across groups.** Averaging across a parameter change
  produces a number describing nothing.

### Status — is anything broken

Collector state, evaluator readiness and reason, last signal, oldest unresolved
outcome, data gaps per timeframe, delivery counts, registered devices, and the
alerts card.

Nobody looks at this when things work. It is raw values, no interpretation, and
the server's own wording for every concern — easy to read aloud down a phone or
screenshot into a message. Severity is labelled in words as well as coloured.

---

## Push notifications

### Three switches

An alert reaches the phone only when all three are on, and the app reports all
three because an app that checked only the one it controls would say "alerts
are on" while the server sent nothing:

1. **Android permits notifications** for this app.
2. **The server knows this phone's token** — `POST /api/v1/device`.
3. **The deployment is in notify mode** — `SIGNAL_MODE=notify`.

### Registration

The app posts its FCM token on every launch and on every refresh event,
unconditionally. The server treats a repeat as a success (ADR 0026) precisely
so it can be.

This is not belt-and-braces. **FCM rotates tokens whenever it likes** — on
reinstall, on restore to a new device, sometimes for no reason the app is told
— and a deployment holding the previous one fails every send as `UNREGISTERED`,
which the delivery worker correctly treats as permanent and gives up on. Alerts
then stop silently, and the symptom looks like a strategy that went quiet.

Signals recorded while no device is registered **wait rather than failing**.
They deliver as soon as a phone registers.

### The permission prompt

The app explains before the OS asks, because the OS prompt cannot be re-asked
once it is refused:

> **Alerts for signals only**
> The strategy records a signal roughly once every ten days on 4h. An alert
> carries the direction, the reference price, the stop and the target — the
> same numbers the app shows. Nothing here places an order, and no alert ever
> asks you to.

Refusing is handled: everything else works, and the status screen offers a
route into Android's settings.

### Receiving

Foreground, background and quit are all handled, and a notification arriving
while the app is open is **shown, not swallowed** — a signal arriving while
somebody is looking at the chart is exactly as interesting as one arriving on a
locked phone.

Tapping an alert opens that signal's detail view, from wherever the app was.

The payload's `signal_price` is a **reference price, not the entry.** The
server's own body text says `ref`. The UI does not relabel it.

---

## Closing phase 07's open item

Phase 07's Definition of Done has carried one unticked line since it closed:
`SIGNAL_MODE=notify` delivers to a real device. **It is still open.** Everything
up to the last hop is built and tested; what has not happened is a signal
arriving on a physical phone, which needs hardware the development environment
does not have.

Here is the procedure. It needs an Android phone, a real Firebase project, and
about twenty minutes.

### Before you start

1. Create a Firebase project and add an Android app with the package name
   `com.spioneracorei8.btcusdsignals`.
2. Download `google-services.json` into `mobile/`.
3. Generate a service account key (Project settings → Service accounts) and put
   it on the VPS at the path `FCM_CREDENTIALS_HOST_FILE` points at. **Never
   commit it** — `.gitignore` covers `fcm-service-account.json`.
4. On the VPS, set `SIGNAL_MODE=notify` and `FCM_PROJECT_ID`, and restart with
   the notify overlay:
   ```console
   $ docker compose -f docker-compose.yml -f docker-compose.notify.yml up -d
   ```
   There is no `FCM_DEVICE_TOKEN` any more, and the collector **refuses to
   start** if one is set. See ADR 0026.

### The four checks

**1. A signal produces an alert on a locked phone.**

Install the APK, open it, allow notifications. Confirm the status screen says
*Alerts are on* and `devices_registered` is 1.

Then lock the phone and wait for a signal — or force one by inserting a row and
letting the delivery worker pick it up:

```console
$ psql "$DATABASE_URL" -c "
  INSERT INTO signals (id, symbol, market_type, timeframe, signal_time,
                       direction, strength, signal_price, stop_loss,
                       take_profit, strategy_name, strategy_version, reason)
  VALUES (gen_random_uuid(), 'BTCUSDT', 'spot', '4h', now(), 'long', 50,
          64000, 63500, 65000, 'ema_crossover', 'v1',
          '{\"trigger\":\"delivery check\"}'::jsonb)
  RETURNING id;"
```

**Expected:** an alert within a minute. Title `BTCUSDT 4h LONG`, body carrying
`ref 64000 · stop 63500 · target 65000`.

**2. The alert's numbers match the stored signal exactly.**

Compare the alert against `GET /api/v1/signals/{id}`. The body rounds for
reading; the `data` payload is exact. Check specifically that the price on the
alert is the **reference** price and not the entry — they differ by roughly the
slippage.

**3. Delivery is recorded `sent`.**

```console
$ psql "$DATABASE_URL" -c "
  SELECT status, attempts, sent_at, last_error FROM notifications
  ORDER BY created_at DESC LIMIT 3;"
```

**Expected:** `sent`, `attempts` 1, `sent_at` populated, `last_error` empty.
`GET /api/v1/status` should show `delivery.sent` incremented and
`delivery.failed` at 0.

**4. An unreachable phone retries and then gives up.**

Uninstall the app without deregistering, then insert another signal.

**Expected:** Firebase answers `UNREGISTERED`, the worker treats it as
permanent and marks the row `failed` on the **first** attempt — not after five.
`delivery.failed` becomes 1 and the status screen shows it. The queue does not
grow without bound.

**5. A reinstall produces a new token and alerts resume.**

Reinstall, open the app, confirm the status screen shows a **different** masked
token prefix and `registered_at` has moved. Insert another signal.

**Expected:** delivered. This is the rotation path, and it is the one that
silently breaks a deployment holding a token in a config file.

### Recording the result

Tick the line in `docs/prompts/phase-07.md` and note the date and the token
prefix. If any check fails, the failure is more interesting than the pass —
write down which and what the row said.

---

## The theme

Deep teal-black surfaces, muted jade, aged gold as accent only, warm off-white
text. Defined once in `src/theme/` and nowhere else.

```
bg.base       #0D1512   near-black with a green cast
bg.raised     #131E1A   cards
bg.overlay    #18251F   modals
border.subtle #1F2E27   hairlines
border.gold   #4A4032   the gold hairline

jade.dim      #3E6B5A
jade.base     #5A9179   primary actions, active states
jade.bright   #7DB89C   at most one element per screen

gold.dim      #8A7A52
gold.base     #B39B63   accents, markers, the active tab
gold.bright   #D4BC85   at most once per screen, if at all

text.primary   #E8E4D9
text.secondary #A8A294
text.tertiary  #8D897C

long   #5A9179   jade — the same family as the UI
short  #C67B5C   muted terracotta

ok    #5A9179
warn  #B39B63
error #ECA89A
```

**Gold is never a fill.** A hairline, a small marker, an active tab indicator —
that is the budget, and `tools/audit.mjs` fails on any gold area over roughly a
24pt square.

**Long and short are not red and green.** Green is the interface colour
throughout, so a green long would read as chrome rather than as a fact about
the trade. Terracotta belongs to the same warm-earth family as the gold and
does not read as an error state.

**Numbers use tabular figures**, without exception. Proportional digits change
width as they change value, so a live price jitters horizontally as it updates
and a column of returns fails to line up.

### Two values that differ from the phase brief

Both were changed because they failed a measurement, and both are documented at
the point of definition in `src/theme/colors.ts`.

| Token | Brief | Now | Why |
|---|---|---|---|
| `text.tertiary` | `#6E6B61` | `#8D897C` | Contrast 3.48/3.21/**2.98** against the three surfaces. Tertiary is 12pt, below WCAG's "large" threshold, so the bar is 4.5 — it missed on all three. A timestamp is not decoration. Now 4.54 at worst. |
| `semantic.error` | `#B85C4A` | `#ECA89A` | Converged with `warn` under deuteranopia: **ΔE 11.8** against the 15 this palette holds itself to. The status screen when unhealthy is exactly where those two must not look alike. Hue cannot fix it — red-green deficiency flattens the axis they differ on — so lightness does. |

`long` against `short`, the pair the brief asked to verify, passes unchanged:
**ΔE 17.8** under protanopia, its tightest.

---

## Verification

### What runs on every change

```console
$ npm run check
```

Three things are enforced mechanically rather than described:

**No colour literal outside `src/theme/`** — an ESLint rule and a test that
walks the source. Tokens that are only documented drift, because the day
somebody needs "just a slightly different green" nothing stops them.

**No order-placing code or UI** — `src/__tests__/no-orders.test.ts` checks for
trading endpoints, venue credentials, wording that reads as acting on a
position, and that the only non-GET request in the app is the device
registration. Ten planted violations were caught; the one legitimate write
passed.

**Long and short stay distinguishable** — `src/theme/colorblind.test.ts`
applies the Viénot–Brettel–Mollon dichromat transforms and asserts a CIE76 ΔE
floor under protanopia, deuteranopia and tritanopia. It is a transform rather
than a look through a simulator so the rule holds every run. The simulation
itself is checked against facts that hold independently of this palette — it
leaves greys alone, collapses red against green for red-green deficiencies, and
leaves blue against yellow alone for them — because a matrix transcribed
wrongly would otherwise pass everything.

### The rendered checks

`react-native-web` through Chromium is not the APK, but it is the same
components, styles, palette and type scale — which is what the screenshot audit
is about.

```console
# Terminal 1: the API
$ cd server && go run .

# Terminal 2: a CORS shim, because a browser enforces an origin and Android
# does not
$ cd mobile && node tools/cors-proxy.mjs

# Terminal 3: the app
$ EXPO_PUBLIC_API_BASE_URL=http://127.0.0.1:8100 npx expo start --web

# Terminal 4
$ node tools/render.mjs    # screenshots/{now,signals,chart,performance,status}.png
$ node tools/audit.mjs     # the §E6 measurements; exits non-zero on a failure
```

`audit.mjs` checks two things and reports a third:

- **no gold fill** — nothing gold larger than 24×24pt
- **nothing brighter than `text.primary`** — "easy on the eyes, never bright",
  as a luminance ceiling
- **the five brightest elements per screen**, so a person can confirm that the
  loudest thing on each screen is the one that should be

Both checks were verified to fail: planting a gold card background and swapping
`text.primary` for pure white were each caught.

### What still needs a person

The audit measures; it does not judge. Look at the screenshots at full
brightness in a dark room and ask the question the brief asks: *is the first
thing your eye lands on the most important element on that screen?*

---

## What has not been done here

Stated plainly rather than left to be discovered:

- **No APK was built.** `dl.google.com` is blocked in the development
  environment, so there is no Android SDK.
- **Nothing ran on a device or an emulator.** No hardware, and no SDK to build
  an emulator with.
- **Push was not verified end to end.** That is phase 07's open item, and the
  procedure above is what closes it.
- **Nothing was tested over an actual tailnet.** The API was reached on
  loopback.
- **iOS is out of scope**, and stays out until there is a Mac and a developer
  account.
