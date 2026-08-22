-- +goose Up
--
-- What became of each signal.
--
-- This is the half of phase 07 that makes the other half worth building. A
-- notification system delivering signals from a strategy known not to clear
-- its acceptance criteria is not useful on its own; what makes it worth
-- running is the machinery that measures whether live signals behave the way
-- the backtest said they would.
--
-- That measurement cannot be retrofitted. It has to be recording from the
-- first signal or the comparison has no history to draw on.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS signal_outcomes (
    -- One outcome per signal, which the primary key states rather than a
    -- separate unique constraint: a signal with two outcomes is not a signal
    -- that happened twice, it is a bug in the follower.
    signal_id      uuid        PRIMARY KEY REFERENCES signals (id) ON DELETE CASCADE,

    status         text        NOT NULL DEFAULT 'open',
    resolved_at    timestamptz,
    resolved_price numeric(20,8),

    -- Maximum adverse and favourable excursion, as a distance in price from
    -- the entry. Not decoration: an MAE routinely close to the stop on trades
    -- that eventually win means the stop is barely surviving, which is
    -- invisible in a win rate and decisive in practice.
    mae            numeric(20,8),
    mfe            numeric(20,8),

    bars_held      integer     NOT NULL DEFAULT 0,

    -- The engine's own accounting of the same trade. Kept per signal because
    -- a parameter change between two signals otherwise leaves two
    -- incomparable groups in one table looking alike.
    backtest_would_have jsonb,

    -- Filled when the live reading and the backtest's disagree. Empty is the
    -- expected case; the interesting one is not.
    divergence_note text       NOT NULL DEFAULT '',

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT signal_outcomes_status_check
        CHECK (status IN ('open', 'target', 'stop', 'expired', 'invalidated')),

    -- A resolved outcome has to say when and at what price; an open one must
    -- not, or a half-written row would read as a finished trade.
    CONSTRAINT signal_outcomes_resolution_check CHECK (
        (status = 'open' AND resolved_at IS NULL AND resolved_price IS NULL)
        OR (status = 'invalidated' AND resolved_at IS NOT NULL)
        OR (status <> 'open' AND status <> 'invalidated'
            AND resolved_at IS NOT NULL AND resolved_price IS NOT NULL)
    ),

    CONSTRAINT signal_outcomes_bars_held_check CHECK (bars_held >= 0)
);
-- +goose StatementEnd

-- The follower's working set: signals still being followed, oldest first.
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS signal_outcomes_open_idx
    ON signal_outcomes (created_at, signal_id)
    WHERE status = 'open';
-- +goose StatementEnd

-- Reconciliation groups by strategy and parameter set and filters by status;
-- the join back to signals is on the primary key, so this is what is left.
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS signal_outcomes_status_idx
    ON signal_outcomes (status, resolved_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS signal_outcomes;
-- +goose StatementEnd
