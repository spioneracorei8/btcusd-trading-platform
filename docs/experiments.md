# Experiment log

Appended after every evaluation run. Not automated: writing the line is the
point, because the act of recording a result you dislike is the discipline
this file exists to impose.

## Why this file exists

Two reasons, and the second matters more.

**You will otherwise retry things you already rejected.** Six weeks from now
the memory of "I tried EMA(9/21) and it was flat" is gone, and the idea will
look fresh.

**If you run fifty variants and pick the best, the count is the finding.** The
winner of fifty coin-flip contests is not a skilled coin-flipper. A backtest
that was chosen out of fifty has roughly fifty chances to look good by
accident, and nothing in its own report can tell you that happened. This log is
the only record of the denominator.

When you reach a result worth acting on, count the entries above it first.

## Format

One entry per run, newest at the bottom.

```
### <date> — <strategy> <version>

- **Dataset:** dev | holdout | custom (range)
- **Parameters:** what differed from the documented defaults, or "defaults"
- **Filter:** trend filter name and version, or none
- **Sizing:** mode and risk
- **Net return after costs:** x.xx%
- **Profit factor / max drawdown / trades:** x.xx / x.xx% / n
- **Costs as share of gross profit:** x.xx%
- **Concentration (best 5):** x.xx% of gross profit
- **Verdict against docs/acceptance-criteria.md:** pass | fail (which criterion)
- **Note:** one line — what you learned, not what you hoped
```

## Entries

_No evaluation runs yet._

The tooling is built and verified, but no strategy has been evaluated against
real BTCUSDT data. The first entry belongs to whoever runs:

```bash
go run ./backtest --strategy=ema_crossover --dataset=dev --compare --cost-sweep
```

on a database that actually holds the 2023–2024 history.
