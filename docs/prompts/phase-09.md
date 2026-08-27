# Phase 09 — Mobile app

> Read `CLAUDE.md` fully before starting.
> Phases 01–08 are merged. The API is live on the tailnet and documented in `docs/api.md`.
> **This phase places no orders.** `CLAUDE.md` §1 stands, and here it is a UI constraint as much as a code one.

---

## Context

This is the last phase of the original plan. It closes the loop Phase 07 left open — FCM has never delivered to a real device, because there was no device token to deliver to.

**Who this is for.** One person, one phone, on a tailnet. No accounts, no onboarding, no settings screen full of options nobody will change. If a decision would be different for a thousand users, make the one-user decision.

**What the app is honestly for.** The strategy search has not produced anything clearing `docs/acceptance-criteria.md`. Best available is `ema_crossover` on 4h at defaults — +4.78%, PF 1.13, edge gone at 1.5× cost, profitable in one of two years. The app is not a way to act on signals with confidence. It is a way to see what the system is producing, and to notice when live behaviour departs from what the backtest said.

That has a concrete consequence: **the app must be as honest as the reports are.** Every screen showing a performance number shows the sample size beside it. A win rate over nine trades must not render like one over nine hundred. The CLI and API already follow this rule; it matters more on a phone, because a phone invites glancing, and a glance is where a number gets believed without its caveats.

---

## Part A — Foundations

### A1. Stack

- React Native with Expo, TypeScript
- Android first. iOS only with a Mac and a developer account — do not build for a platform you cannot run on
- APK, sideloaded. No app store
- React Query for server state, `useState` for the rest. No Redux
- Charts: evaluate `react-native-wagmi-charts` or an equivalent candlestick library, pick one, record why in an ADR

### A2. Connectivity

The app reaches the API only over the tailnet.

- **If the API is unreachable, say so plainly and name Tailscale as the likely cause.** A spinner is the wrong answer to a VPN that is switched off
- API base URL configurable and persisted, defaulting to the tailnet address
- No auth, matching Phase 08 B1. The network is the boundary

### A3. Structure

```
src/
  features/
    dashboard/
    signals/
    chart/
    performance/
    status/
  api/          # client and types, from docs/api.md
  theme/        # tokens only — see Part E
  components/   # shared primitives
```

Types come from `docs/api.md`. If app and API disagree on a field name, that is the API's shape and the app's bug — the wire-drift lesson from Phase 08.

---

## Part B — Screens

Five, and no more. Each answers one question.

### B1. Dashboard — "what is happening right now"

- Current price with the forming candle, marked as forming
- Latest signal: direction, entry, stop, target, how long ago
- If a signal is unresolved: current price against its levels, MAE and MFE so far
- Pipeline health from `/api/v1/status` — prominent when unhealthy, quiet when fine

**Silence must be legible.** Normal output on 4h is roughly one signal every ten days. A screen showing nothing must distinguish *no setup found* from *evaluator not warm* from *collector stopped*. That is what `/api/v1/status` was built for, and this is where it earns its keep.

### B2. Signals — "what has it produced"

- Reverse-chronological list: direction, timeframe, time, outcome once resolved
- Detail view with the full `reason` payload: indicator values, trend state, resolved parameters
- Filter by direction and by outcome status

The detail view is the point. A signal without its reasoning is unreviewable six weeks later, which is why `reason` is jsonb rather than a summary string.

### B3. Chart — "what did the market do around it"

- Candlestick chart, timeframe selectable
- Signal markers at entry, with stop and target drawn
- Resolved outcomes marked at their exit

Pan and zoom must not refetch per gesture. Fetch a window, cache it, extend at the edges. **A request for three years of 1m candles must be refused client-side** — the API caps it, but the app should not make requests it knows are wrong.

### B4. Performance — "is it working"

- Live outcomes: win rate, average win and loss, expectancy, **all after modelled costs**
- The matched-versus-surplus split from the Phase 07 audit, with the surplus explained rather than folded in silently
- Insufficient-sample banner rendered **before** the numbers, not beside them — Phase 08 found that a banner beside a figure loses to the figure

At 0.1 trades a day, 100 resolved signals is close to three years. **State the expected wait on this screen.** A performance screen that looks nearly ready to tell you something, for years, is worse than one that says how long it will be.

### B5. Status — "is anything broken"

Collector state, evaluator readiness and reason, last signal time, oldest unresolved outcome, notification failures, data gaps.

Nobody looks at this when things work. Optimise for the day something is wrong: raw values, no interpretation, easy to read aloud or screenshot.

---

## Part C — Push notifications

### C1. Token registration

- Request permission on first launch, explaining what alerts are for **before** the OS prompt
- Send the token to the API; add `POST /api/v1/device` to store it
- Re-register on refresh. FCM rotates tokens, and a stale one is the permanent-failure case the Phase 07 worker gives up on — without re-registration, alerts stop silently
- Handle refusal gracefully. The app works without notifications

### C2. Receiving

- Foreground, background, and quit states all handled
- Tapping an alert opens that signal's detail view
- The payload's **reference price is not the entry price**. Phase 07 made that distinction deliberately; the UI must not relabel it

### C3. End-to-end verification

The first real test of the delivery path. Set `SIGNAL_MODE=notify`, register a real token, confirm:

- A signal produces an alert on a locked phone
- The alert's numbers match the stored signal exactly
- Delivery is recorded `sent` in `notifications`
- An unreachable phone retries then permanently gives up, not an infinite queue
- A reinstall produces a new token and alerts resume

Record the result against Phase 07's Definition of Done, which has carried this as its one unticked item since that phase closed.

---

## Part D — What the app must not do

- **No order placement, and no UI that looks like it.** No execute button, no broker link, no size calculator ending in a confirm step. The gap between showing a signal and placing an order should stay wide enough that nobody crosses it by muscle memory
- No editing signals or outcomes. The app is a reader
- No local recomputation of indicators or performance. If the phone computes a win rate, there are two implementations and they will drift — Phase 08's wire-shape lesson, one layer up
- No stored credentials beyond the FCM token

Add a test asserting no order-related string appears in the app source, matching `architecture_test.go` on the server side.

---

## Part E — Visual design

The reference is the jade-and-gold palette of Chinese xianxia illustration — deep teal-greens, aged gold, soft light. Muted, not luminous. The brief is explicit: **easy on the eyes, never bright.**

### E1. Why restraint is a functional requirement here

This is a screen someone checks at night, half-awake, to see whether anything happened. It is also a screen that must not make a thin edge look like a strong one. Gold is the trap: at full saturation it reads as celebration, and there is nothing to celebrate — the system's honest output is usually silence and occasionally a marginal signal.

So gold is used as **accent and structure**, never as fill. Large gold surfaces are out. A hairline border, a small marker, an active tab indicator — that is the budget.

### E2. Tokens

Define once in `src/theme/`. No literal colours anywhere else, enforced by lint.

**Surfaces** — deep desaturated teal-black, not pure black. Pure black makes gold vibrate against it and is harsh in a dark room.

```
bg.base      #0D1512   near-black with a green cast
bg.raised    #131E1A   cards, sheets
bg.overlay   #18251F   modals, menus
border.subtle #1F2E27  hairlines between sections
border.gold  #4A4032   the gold hairline — dim, not metallic
```

**Jade** — the primary. Muted, closer to celadon than emerald.

```
jade.dim     #3E6B5A
jade.base    #5A9179   primary actions, active states
jade.bright  #7DB89C   reserved for the single most important element on screen
```

**Gold** — accent only. Aged and dusty, never yellow.

```
gold.dim     #8A7A52
gold.base    #B39B63   accents, markers, active indicators
gold.bright  #D4BC85   used at most once per screen, if at all
```

**Text** — warm off-white, never `#FFFFFF`. Pure white on dark is the most common cause of eye strain in night use.

```
text.primary   #E8E4D9
text.secondary #A8A294
text.tertiary  #6E6B61   captions, sample sizes, timestamps
```

**Direction colours.** Long and short must not be red and green, because green is the interface colour throughout and a green long would be indistinguishable from ordinary chrome.

```
long   #5A9179   jade — same family as the UI
short  #C67B5C   muted terracotta
```

Terracotta rather than red: it belongs to the same warm-earth palette as the gold and does not read as an error state. Both hold up under the common forms of colour blindness, since they differ in warmth as well as hue.

**Semantic.** Distinct from direction, so a losing trade never looks like a system fault.

```
ok    #5A9179
warn  #B39B63
error #B85C4A
```

### E3. Typography

- **Numbers use tabular figures.** Non-negotiable — proportional digits make a live price jitter as it updates
- One typeface, three weights. Inter or the system font; do not ship a display face for a five-screen tool
- Sizes: 28 / 20 / 16 / 14 / 12. Prices at 28 and 20, body 16 and 14, captions 12
- **Sample sizes render at 14, not 12.** They are qualifiers on the number beside them, and a qualifier set in caption size loses the argument. This is the single most important typographic rule in the app

### E4. Layout and motion

- 4px spacing scale. 16px screen padding, 12px card padding
- 8px corner radius, uniform. No large rounds — this is an instrument, not a lifestyle app
- Separation by background step, not by border. Borders only where a real edge exists
- **No gradients on surfaces.** A single very subtle radial behind the dashboard header is the whole permitted budget, at under 4% opacity
- Motion: 150ms fades, 200ms transitions, ease-out. No spring, no bounce. Numbers changing must not animate — a price sliding between values is unreadable and implies precision that isn't there
- **No glow, no neon, no shadow spread.** The reference art is lit softly; a UI imitating it with bloom looks like a game menu

### E5. Where the theme must not interfere

Three places the palette bends to the content:

- **The insufficient-sample banner** uses `warn` on `bg.raised` at full text contrast. It is not decorative and must not be styled to be dismissible-looking
- **The status screen when unhealthy** drops the aesthetic entirely: `error`, high contrast, no ornament. Someone reading it is troubleshooting
- **Charts** use the direction colours for candles and nothing else. No gold gridlines, no jade fills under the price line. The chart is data; the theme is the frame around it

### E6. Verification

- Screenshot every screen at full brightness in a dark room. If any element is the first thing the eye lands on and it isn't the most important element, it's too bright
- Check the largest gold area on each screen. If it exceeds roughly a 24px square, it's a fill, not an accent
- Run every screen through a colour-blindness simulator and confirm long and short stay distinguishable
- Lint fails on any hex literal outside `src/theme/`

---

## Definition of Done

Ticked means verified here, by something that keeps holding. Unticked items
carry the reason, and four of them need hardware this environment does not
have — see "What has not been done here" in `docs/mobile.md`.

- [ ] **Builds to an installable APK** — `dl.google.com` is blocked by the
      proxy policy in this environment, so there is no Android SDK and no
      build. The Expo config and `eas.json` are in place; the command is in
      `docs/mobile.md`
- [ ] **All five screens work against the live API over the tailnet** — all
      five were rendered against a live API and read, but on loopback. There
      is no tailnet here, and the address is configuration rather than code
- [x] Unreachable API produces a clear message naming Tailscale — and names it
      only for `unreachable`, not for a 500, which would send somebody to
      debug the wrong machine
- [x] Forming candles are visibly marked as forming — drawn hollow at lower
      opacity, and said in words underneath
- [x] Every performance figure shows its sample size, at 14pt
- [x] Insufficient-sample banner renders before the numbers
- [x] Chart pans and zooms without refetching per gesture — the fetched window
      is held while the view moves inside it; two steps cost nothing and the
      third extends it, checked in jest and in a browser against the live API
- [ ] **Push notifications verified end to end, all four cases in C3** — needs
      a phone. The procedure is written out in `docs/mobile.md` so it can be
      run against one rather than ticked against a simulation
- [ ] **Token refresh re-registers** — the listener is wired to the post and
      carries the new token, held by `useNotifications.test.tsx`. A real FCM
      rotation has not happened, so this is not ticked
- [ ] **Phase 07's unticked Definition of Done item closed and recorded** —
      still open. It has been outstanding since that phase and stays open;
      what is new is a written procedure that closes it
- [x] No order-placing code or UI, enforced by a test — `src/__tests__/no-orders.test.ts`
- [x] No hex literal outside `src/theme/`, enforced by lint
- [x] Colour-blindness check passed for long versus short — computed rather
      than eyeballed: Viénot–Brettel–Mollon simulation, CIE76 ΔE, held at a
      minimum separation by `src/theme/colorblind.test.ts`
- [x] `docs/mobile.md` covers building, installing, configuring, and the theme tokens

Two of E6's three visual checks are also automated, in `mobile/tools/audit.mjs`:
the largest gold area per screen, and whether anything outranks `text.primary`
in luminance. The third — whether the eye lands on the right element first — is
a judgement and is left to a person, with the screenshots to do it from.

---

## Out of scope

- iOS, without the hardware and account
- App store distribution
- Multiple users or devices
- Offline mode beyond caching what was last fetched
- Trading, in any form
- Resuming the strategy search

---

## After this phase

The nine-phase plan is complete. What exists is a system that collects market data reliably, computes indicators correctly, backtests honestly, and reports what it finds without flattering it.

What it has not produced is a profitable strategy. Sixty-six evaluations say the same thing several ways: the edge in these rules is thin enough that a 1.5× move in costs erases it, and the best-looking result was a spike its own neighbours contradicted.

That is a real finding, and the system was built to be able to deliver it. The next useful step is not another parameter sweep — it is a venue with tighter spreads, price data from the venue actually being traded, or an edge from somewhere other than moving averages. Those are decisions to make with evidence, and there is now a machine that produces evidence.

---

## How to start

Part A, then B1 and B5 — dashboard and status, the two that make the rest debuggable. Build the theme tokens in Part A so nothing is written with literal colours and retrofitted later. Summarise the plan and wait for approval.
