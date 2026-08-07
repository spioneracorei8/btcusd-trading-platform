#!/usr/bin/env python3
"""Generate the indicator reference fixtures.

This script is documentation of provenance, not part of the build. It is
committed so the fixtures can be regenerated and audited; nothing in the Go
build runs it.

    pip install numpy pandas TA-Lib
    python3 generate_reference.py

# Why the series is synthetic

Phase 03 asks for a slice of real stored BTCUSDT candles. The build
environment cannot reach Binance (api.binance.com and data.binance.vision are
both unreachable) and phase 02 has not yet stored real candles, so the series
here is generated from a fixed seed instead.

That is sound for what these fixtures verify. The test asks whether this
implementation and TA-Lib compute the same numbers from the same bars; where
the bars came from does not affect that. The series deliberately covers the
regimes that break indicator code — trend, chop, a flat stretch, a sharp gap,
and a UTC day boundary — which a random real slice might not.

Regenerate against real candles once the collector has run. The Go code does
not change.

# Which variant

TA-Lib is the reference. Confirmed by measurement, not assumption:

  * RSI matches a hand-rolled Wilder implementation to ~7e-15, while the
    SMA-based variant differs by ~2.77 on the same series. TA-Lib is Wilder,
    which is what phase 03 section 4 requires.
  * EMA seeds with the SMA of the first `period` values, matching to 0.0.

TA-Lib has no VWAP, so VWAP is computed here directly from the agreed
definition: typical price (H+L+C)/3 weighted by base volume, reset at 00:00
UTC on the candle's open time.
"""

import numpy as np
import pandas as pd
import talib

SEED = 20260804
BARS = 5000
START_MS = 1767225600000  # 2026-01-01T00:00:00Z, a UTC midnight
MINUTE_MS = 60_000

EMA_PERIOD = 200
RSI_PERIOD = 14
ATR_PERIOD = 14


def build_series() -> pd.DataFrame:
    """Build a deterministic OHLCV series covering several market regimes."""
    rng = np.random.default_rng(SEED)

    closes = []
    price = 64000.0

    for i in range(BARS):
        if i < 1000:
            drift, vol = 0.6, 12.0        # trend up
        elif i < 2000:
            drift, vol = 0.0, 25.0        # chop
        elif i < 2200:
            drift, vol = 0.0, 0.0         # dead flat: RSI degenerate territory
        elif i == 2200:
            drift, vol = -900.0, 5.0      # sharp gap down
        elif i < 3200:
            drift, vol = -0.5, 18.0       # trend down
        else:
            drift, vol = 0.05, 30.0       # volatile drift

        price = max(price + drift + rng.normal(0, vol), 1000.0)
        closes.append(price)

    closes = np.array(closes)

    # Highs and lows bracket the close; the spread widens with volatility so
    # the true range is not a constant.
    spread = np.abs(rng.normal(0, 8.0, BARS)) + 1.0
    highs = closes + spread
    lows = closes - spread

    opens = np.empty(BARS)
    opens[0] = closes[0]
    opens[1:] = closes[:-1]

    # Keep the bracket honest after wiring opens in.
    highs = np.maximum(highs, np.maximum(opens, closes))
    lows = np.minimum(lows, np.minimum(opens, closes))

    # A stretch of zero volume, to exercise the empty-session path.
    volumes = np.abs(rng.normal(12.0, 4.0, BARS)) + 0.001
    volumes[2000:2200] = 0.0

    open_times = START_MS + np.arange(BARS) * MINUTE_MS

    return pd.DataFrame({
        "open_time_ms": open_times,
        "open": opens,
        "high": highs,
        "low": lows,
        "close": closes,
        "volume": volumes,
    })


def daily_vwap(df: pd.DataFrame) -> np.ndarray:
    """VWAP with a 00:00 UTC reset, driven by open_time.

    TA-Lib has no VWAP, so this is the definition itself rather than a second
    opinion. It is kept here so the Go implementation is checked against an
    independent expression of the same rule.
    """
    typical = (df["high"] + df["low"] + df["close"]) / 3.0
    day = (df["open_time_ms"] // 86_400_000).to_numpy()

    out = np.empty(len(df))
    cum_pv = 0.0
    cum_vol = 0.0
    current_day = None

    for i in range(len(df)):
        if current_day is None or day[i] != current_day:
            current_day = day[i]
            cum_pv = 0.0
            cum_vol = 0.0

        cum_pv += typical.iloc[i] * df["volume"].iloc[i]
        cum_vol += df["volume"].iloc[i]
        out[i] = typical.iloc[i] if cum_vol == 0 else cum_pv / cum_vol

    return out


def main() -> None:
    df = build_series()

    df.to_csv("btcusdt_1m_reference.csv", index=False,
              float_format="%.8f")

    expected = pd.DataFrame({
        "open_time_ms": df["open_time_ms"],
        "ema": talib.EMA(df["close"].to_numpy(), timeperiod=EMA_PERIOD),
        "rsi": talib.RSI(df["close"].to_numpy(), timeperiod=RSI_PERIOD),
        "atr": talib.ATR(df["high"].to_numpy(), df["low"].to_numpy(),
                         df["close"].to_numpy(), timeperiod=ATR_PERIOD),
        "vwap": daily_vwap(df),
    })
    expected.to_csv("btcusdt_1m_expected.csv", index=False,
                    float_format="%.10f", na_rep="")

    print(f"TA-Lib {talib.__version__}: wrote {len(df)} bars")
    print(f"  ema_{EMA_PERIOD} first value at index "
          f"{int(np.argmax(~np.isnan(expected['ema'])))}")
    print(f"  rsi_{RSI_PERIOD} first value at index "
          f"{int(np.argmax(~np.isnan(expected['rsi'])))}")
    print(f"  atr_{ATR_PERIOD} first value at index "
          f"{int(np.argmax(~np.isnan(expected['atr'])))}")


if __name__ == "__main__":
    main()
