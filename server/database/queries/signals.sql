-- name: InsertSignal :one
-- Records one strategy decision. The unique constraint on
-- (strategy_name, strategy_version, symbol, timeframe, signal_time) makes a
-- duplicate insert fail loudly rather than notify the owner twice.
INSERT INTO signals (
    symbol, market_type, timeframe, signal_time, direction, strength,
    signal_price, entry_price, stop_loss, take_profit,
    strategy_name, strategy_version, reason
) VALUES (
    sqlc.arg(symbol), sqlc.arg(market_type), sqlc.arg(timeframe),
    sqlc.arg(signal_time), sqlc.arg(direction), sqlc.arg(strength),
    sqlc.narg(signal_price), sqlc.narg(entry_price), sqlc.narg(stop_loss),
    sqlc.narg(take_profit),
    sqlc.arg(strategy_name), sqlc.arg(strategy_version), sqlc.arg(reason)
)
RETURNING *;

-- name: FetchSignalById :one
-- One signal, for a delivery worker holding a queue row that points at it.
SELECT * FROM signals WHERE id = sqlc.arg(id);

-- name: SetSignalEntryPrice :one
-- Fill in what a position would have opened at.
--
-- It is not knowable when the signal is recorded: the decision is taken on a
-- bar's close and the fill is the next bar's open plus slippage, so this is
-- written one bar later. Only ever from null, because a second write would
-- mean two different answers to a question with one.
UPDATE signals
SET entry_price = sqlc.arg(entry_price)
WHERE id = sqlc.arg(id) AND entry_price IS NULL
RETURNING *;
