# ADR 0027 — The chart is drawn with react-native-svg, not imported

Status: accepted
Date: 2026-08-27
Phase: 09 (A1, B3)

## Context

Phase 09 §A1 says to evaluate `react-native-wagmi-charts` or an equivalent
candlestick library, pick one, and record why.

The chart has to do four things: draw candles for one timeframe, mark signal
entries and exits, draw a stop and a target as horizontals, and pan without
refetching on every gesture. Three of those are rectangles and lines. The
fourth is windowing arithmetic that belongs to this app either way — no chart
library knows that this API caps a response at 5000 bars, or that a request it
would truncate must be refused before it is sent.

## What the evaluation found

`react-native-wagmi-charts@2.10.0` **cannot be loaded on this stack at all.**

Expo SDK 57 ships React Native 0.86 with Reanimated 4, which moved the worklet
runtime into `react-native-worklets`. wagmi-charts is built against the
Reanimated 2/3 API and lists `react-native-redash` — itself a Reanimated 2 era
library — as a peer. Importing it fails before any component renders:

```
TypeError: Cannot read properties of undefined (reading 'loadUnpackers')
  at node_modules/react-native-worklets/.../NativeWorklets.native.ts:411
  at node_modules/react-native-reanimated/src/index.ts:5
  at node_modules/react-native-wagmi-charts/src/charts/candle/Context.tsx:2
```

That is with and without Reanimated's own jest mock, which is itself for the
2/3 line and pulls the same module in.

The failure is in the test environment, and it would be dishonest to claim
from it that the library is broken on a device — the native module exists
there. What can be claimed is stronger for this decision: **it cannot be
verified here.** There is no Android device and no emulator in this
environment (`dl.google.com` is blocked by policy, so there is no SDK to build
one with), so a library that fails to load in the only environment available
would ship untested, in the one screen where "untested" means a person cannot
see the market around their signals.

The alternatives were weighed briefly and not pursued:

- **victory-native** — the current version renders through Skia, which is
  another native dependency of the same weight, chosen to solve a performance
  problem this chart does not have.
- **A WebView chart (lightweight-charts)** — competent and the wrong shape: it
  puts a second rendering stack, a second theme and a JS bridge between the app
  and sixty rectangles.

## Decision

Draw the chart with `react-native-svg`, which is already in the dependency
tree, has no worklet coupling, and renders in jest and in the browser — so the
chart can actually be tested and screenshotted in this environment.

`src/features/chart/Candles.tsx` is about a hundred lines: bodies, wicks,
markers and two dashed horizontals. `src/features/chart/window.ts` holds the
windowing arithmetic, separately, because that is the part with rules worth
testing.

`react-native-wagmi-charts` was removed from `package.json` rather than left
unused. A dependency that does not work is worse than none: it invites the
next person to reach for it.

## Consequences

- **The chart is ours, including its bugs.** Nobody else fixes them. Against
  that: it is rectangles, it has 20 tests, and every one of the rules that
  matters — the forming bar drawn hollow, the direction colours, no gold on
  the chart — is pinned by one of them and was mutation-checked.
- **No pinch-zoom gesture.** Timeframe buttons and pan steps instead. This is
  the real cost, and it is a smaller one than it looks: the useful zoom levels
  on this data are the six collected timeframes, and a pinch that lands between
  them shows an interpolation of a series that has no values in between.
- **The forming candle is rendered by code this repository owns**, which
  matters more here than anywhere else. §3.1 permits an unclosed bar on the
  wire and requires it to be distinguishable; a third-party renderer would
  have had to be persuaded to carry a flag it has no concept of.
- If a gesture-driven chart is ever wanted, this decision is cheap to revisit:
  the windowing is already separate, and only `Candles.tsx` would be replaced.

## References

- `mobile/src/features/chart/Candles.tsx`, `window.ts` and their tests
- `docs/prompts/phase-09.md` §A1, §B3, §E5
- ADR 0024 — why there is no authentication between the app and the API
