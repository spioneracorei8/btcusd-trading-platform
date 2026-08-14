# Acceptance criteria

**Written 2026-08-14, before the first strategy existed and before any
evaluation run.** Nothing had been measured when these numbers were chosen.

That timing is the only thing that makes them criteria. A threshold set after
seeing a result is not a test the result passed — it is a description of the
result. If any number here is changed, change it in a commit of its own, with
the reason, and before the run it will judge.

---

## The floor everything is measured against

**A round trip costs 0.1% before slippage** — 0.05% taker each way.

A strategy averaging 0.15% gross per trade keeps a third of it. One averaging
0.10% keeps nothing. Every criterion below exists because that floor is high
relative to what 1m–5m price movement offers, and because a backtest that did
not subtract it would clear all of them easily and mean nothing.

---

## Criteria

A strategy is accepted only if **all seven** hold on the development set
(2023-01-01 to 2024-12-31). Failing one is failing.

| # | Criterion | Threshold |
|---|---|---|
| 1 | Net return after costs | **> 0** |
| 2 | Profit factor | **> 1.3** |
| 3 | Max drawdown | **< 20%** |
| 4 | Trades in the development period | **≥ 200** |
| 5 | Total costs as a share of gross profit | **< 50%** |
| 6 | Longest losing streak | **≤ 15** |
| 7 | Concentration: profit from the best 5 trades | **< 50% of total** |

### Why each number

**1 — Net return positive.** Stated separately from the rest because it is the
one that cannot be argued with. Gross return is not eligible; ADR 0012 keeps
slippage out of the gross figure precisely so it cannot be smuggled in here.

**2 — Profit factor above 1.3.** Above 1.0 is merely "not losing", which is
indistinguishable from noise on any finite sample. 1.3 means gross profit
exceeds gross loss by a margin wide enough to survive a modest worsening in
either. It is not a demanding bar; it is the lowest bar worth acting on.

**3 — Max drawdown under 20%.** This is a single-owner system with no external
capital and no mandate. 20% is roughly the point past which a person stops
following a system mid-run — and a system abandoned at its worst moment
realises the drawdown without ever collecting the recovery. The constraint is
behavioural, not statistical, and it is real.

**4 — At least 200 trades.** Under 200 the statistics are not measuring a
strategy, they are measuring a handful of lucky bars. At 200 a 55% win rate
still has a standard error near 3.5%, so even here the win rate is worth about
one significant figure. Read it accordingly.

**5 — Costs under 50% of gross profit.** A strategy giving more than half its
gross edge to fees is one exchange fee change, one bad week of spreads, or one
tier downgrade from being unprofitable. It has no margin against the one input
guaranteed to move against a retail account.

**6 — Longest losing streak at most 15.** At the default 1% fixed-fractional
risk, 15 consecutive losses is about a 14% fall — inside criterion 3, but only
just. The number is chosen for what it does to the operator rather than to the
equity: fifteen losses in a row is roughly where a single owner watching a live
system stops believing it, and criterion 3 only holds if the system is still
running.

**7 — Concentration below 50%.** Not in the phase-06 list; added because the
spec names it as a failure mode to watch and a threshold nobody committed to is
not watched. If the best 5 trades out of 200+ carry most of the profit, the
result is a few lucky bars wearing a strategy costume, and the other 195 trades
paid fees for the privilege.

---

## Reported alongside, but not thresholded

These inform the decision without gating it. Committing to a number for each
would be guessing; refusing to look at them would be worse.

- **Regime dependence** — results by year, and by high versus low volatility. A
  strategy that only worked in 2023 has told you about 2023.
- **Cost sensitivity** — the same run at 1.5x and 2x the assumed cost. An edge
  that vanishes under modest slippage was never robust enough to trade. If net
  return goes negative at 1.5x, treat that as a failure regardless of the
  criteria above.
- **Parameter neighbourhood** — the same strategy at neighbouring parameter
  values. If EMA(21) is profitable and EMA(20) and EMA(22) are not, that is a
  fitted artefact. This is a stability report, not a search: it never selects.
- **Trend filter comparison** — filtered against unfiltered. The filter is a
  claim, and an unmeasured claim is a decoration.

---

## The holdout rule

**Development set: 2023-01-01 to 2024-12-31.** Iterate here freely.

**Holdout set: 2025-01-01 onward.** Run **once**, at the end, on the single
strategy already chosen on the development set.

If the holdout fails, the answer is that the strategy does not work. Do not
return to development and retest on it: the set's only value is having been
untouched, and a second look spends it permanently. Note the failure and
believe it.

Every holdout run appends to `docs/holdout-log.md` automatically. That is a
mirror, not a lock — nothing prevents the rule being bent, but bending it will
be visible afterwards, including to the person who bent it.

---

## What is expected to happen

**The most probable outcome is that none of these strategies clears these
criteria after costs.** That is the normal result, it is what most people
attempting this find, and it is not a fault in the code.

The entire apparatus — the honest fill model, the mandatory costs, the gap
gate, the holdout — exists so that finding this out costs a few weeks of
evaluation instead of months of losses. If the numbers say there is no edge,
the correct response is to believe them.

Writing that down here, now, is deliberate. It is much harder to accept a
disappointing result when the criteria are still negotiable.
