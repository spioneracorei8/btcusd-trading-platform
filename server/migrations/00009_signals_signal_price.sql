-- +goose Up
--
-- Record the price the strategy actually saw, separately from the price a
-- position would have been entered at.
--
-- They are not the same number and conflating them makes phase 07's whole
-- comparison meaningless. A strategy decides on the close of the bar it is
-- shown; the backtest fills at the *next* bar's open, plus slippage, because
-- a decision taken on a close cannot also fill on it. Recording the close as
-- the entry would put a systematic difference between live and backtest into
-- every reconciliation, forever, and it would look like slippage.
--
-- So signal_price is the close the decision was taken on, known immediately
-- and safe to notify on. entry_price stays null until the next bar opens and
-- the backtest's own convention can be applied to it.

-- +goose StatementBegin
ALTER TABLE signals
    ADD COLUMN IF NOT EXISTS signal_price numeric(20,8);
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON COLUMN signals.signal_price IS
    'The close the strategy decided on. entry_price is what a position would '
    'have been opened at: the next bar''s open plus slippage, filled in once '
    'that bar closes.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE signals DROP COLUMN IF EXISTS signal_price;
-- +goose StatementEnd
