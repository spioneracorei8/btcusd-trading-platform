# testdata

Fixtures for tests. Tests must never reach the network — an indicator or
strategy test reads a recorded slice of real market data from here instead of
calling Binance.

Empty in phase 01: there is nothing to compute yet. The first fixtures arrive
with the indicator engine in phase 03, where every indicator is checked against
values that are known in advance.
