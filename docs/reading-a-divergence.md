# Reading a divergence

`make reconcile` and `GET /internal/signals/reconciliation` compare what live
signals did against what the backtest engine says the same period should have
produced. This is how to read the answer.

The comparison exists because two very different situations look identical in
a headline number. The backtest reports a 42.86% win rate on 4h. If live
signals come in at 25%, something in the pipeline is wrong — look-ahead, fill
assumptions, a cost model that flatters. If they come in at 41%, the pipeline
is sound and the problem is genuinely that the edge is thin.

**Those two conclusions demand opposite responses**, and only tracked outcomes
tell them apart. One says fix the measuring instrument; the other says stop
looking for a better rule and accept what the instrument is telling you.

---

## The table

| Symptom | Likely cause |
|---|---|
| Live win rate much lower, entries match | Strategy has no real edge; backtest was fitted |
| Live win rate much lower, entries do not match | The entries are not the ones the backtest scored; look at warm-up, gaps and the filter before blaming the edge |
| Live entry prices consistently worse | Slippage exceeds the model |
| Live wins smaller than backtest | Fill assumptions too optimistic |
| Live signals fewer than expected | Warm-up, gaps, or the filter behaving differently live |
| Live outcomes match closely | Pipeline is sound; any disappointment is the edge itself |

The report prints the relevant row rather than leaving it to be looked up, and
it prints the numbers that fired it.

### The last row is not the absence of the others

A faithful pipeline delivering a thin edge is a different problem from a
broken pipeline. Reporting "match closely" explicitly, rather than reporting
nothing, is what stops silence being read as "the comparison did not run".

### The first two rows split what looks like one symptom

A live win rate well below the backtest's means opposite things depending on
whether the entries agree. If they agree, the same trades were taken and did
worse — the rule itself was fitted to its development set. If they do not, the
live path took *different trades*, and the win rate is a symptom of that
rather than a verdict on the rule. Reading one as the other sends the search
in the wrong direction for months, which is why the report distinguishes them
instead of printing "win rate lower".

---

## Before reading any of it: the sample

Every group carries a banner until it has enough resolved signals:

```
signals resolved: 23
NOT ENOUGH DATA — differences below are within normal variation.
A meaningful comparison needs at least 100 resolved signals.
At the observed 0.10 resolved signals a day, the remaining 77 would take about 2.1 years.
```

No divergence is reported while that banner is present. This is deliberate: a
reading printed beside "differences are within normal variation" contradicts
it, and a reader will believe the more specific of the two.

The expected wait is stated because it is a decision, not a detail. At the 4h
strategy's rate of about a tenth of a trade a day, a hundred signals is nearly
three years. **If that wait is unacceptable, the answer is a higher-frequency
strategy, not a smaller sample.**

---

## What is compared, and what is not

### Only like with like

Groups are formed by strategy name, version, and the resolved parameter set
recorded on each signal. There is no total across them. Averaging across a
parameter change produces a number describing nothing — and it looks exactly
like a number describing something.

A strategy version that has moved on is reported as unavailable for
comparison rather than compared against the current code. Comparing signals
from `v1` against `v2` attributes the difference to the market.

### Invalidated signals are excluded

A signal whose window has missing data is counted and then left out of every
statistic. Whether it would have won is not knowable, and a win rate that
quietly counted guesses would be worse than one with a smaller sample. A
period with many invalidated signals is itself a finding — it means the
collector was struggling, and the rest of that period's numbers are thin.

### A win is a return, not a status

A target reached by less than the round trip charged is a losing trade.
Scalping at these timeframes is dominated by cost, so wins are counted on the
return after modelled cost and never on which level was touched.

### The cost row is modelled on both sides

This system places no orders. There is no executed cost to compare against a
modelled one, so that row states the modelled figure and says so. It becomes
a real comparison only if execution is ever added.

### Resolutions that rest on an assumption are counted separately

Two things put a number in the report that the data does not fully support:

- a bar that reached both the stop and the target, where the stop is assumed
  because a bar says nothing about the path between its four prices
- an entry that gapped past its own stop or target, where the exit is priced
  at the level even though the market never traded there on that bar

Both match what the backtest engine does, deliberately — the comparison is
only meaningful if both sides make the same assumptions. The count is
reported as `rested on assumption` so a result leaning heavily on them can be
recognised as leaning on an assumption rather than on evidence.

---

## What a divergence does not tell you

It does not say the strategy is good. No strategy in `docs/experiments.md` has
cleared the acceptance criteria, and whatever is configured live is a
placeholder for exercising the pipeline. A perfectly matching reconciliation
on a losing strategy means the losses were predicted accurately.

That is still the useful outcome: it means the instrument works, and the next
strategy tried will be measured by something that has been checked against
reality rather than only against itself.
