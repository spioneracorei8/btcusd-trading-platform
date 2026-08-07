package database

import (
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// This file is the only place where database wire types meet model types.
//
// numeric columns are read as pgtype.Numeric and converted to
// decimal.Decimal: both represent a value as coefficient * 10^exponent, so
// the conversion is exact in both directions and float64 never appears.

// NumericFromDecimal converts a decimal into the pgtype value pgx writes.
func NumericFromDecimal(d decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{
		Int:   d.Coefficient(),
		Exp:   d.Exponent(),
		Valid: true,
	}
}

// NullNumericFromDecimal converts a nullable decimal into a pgtype value.
// An invalid NullDecimal becomes SQL NULL.
func NullNumericFromDecimal(d decimal.NullDecimal) pgtype.Numeric {
	if !d.Valid {
		return pgtype.Numeric{}
	}
	return NumericFromDecimal(d.Decimal)
}

// DecimalFromNumeric converts a value read from a numeric column.
//
// NaN and infinity are rejected rather than silently coerced: neither can be
// produced by the price columns, so seeing one means the data is corrupt.
func DecimalFromNumeric(n pgtype.Numeric) (decimal.Decimal, error) {
	if !n.Valid {
		return decimal.Decimal{}, fmt.Errorf("numeric value is NULL")
	}
	if n.NaN {
		return decimal.Decimal{}, fmt.Errorf("numeric value is NaN")
	}
	if n.InfinityModifier != pgtype.Finite {
		return decimal.Decimal{}, fmt.Errorf("numeric value is infinite")
	}
	if n.Int == nil {
		return decimal.NewFromBigInt(big.NewInt(0), n.Exp), nil
	}
	return decimal.NewFromBigInt(n.Int, n.Exp), nil
}

// NullDecimalFromNumeric converts a nullable numeric column. SQL NULL becomes
// an invalid NullDecimal rather than an error.
func NullDecimalFromNumeric(n pgtype.Numeric) (decimal.NullDecimal, error) {
	if !n.Valid {
		return decimal.NullDecimal{}, nil
	}
	d, err := DecimalFromNumeric(n)
	if err != nil {
		return decimal.NullDecimal{}, err
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}, nil
}

// TimestamptzFromTime converts a Go time into the pgtype value pgx writes.
// The value is normalised to UTC because every timestamp in this system is UTC.
func TimestamptzFromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: helper.UTC(t), Valid: true}
}

// TimeFromTimestamptz converts a timestamptz column into a UTC time.
func TimeFromTimestamptz(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return helper.UTC(ts.Time)
}

// TimePtrFromTimestamptz converts a nullable timestamptz column; SQL NULL
// becomes nil.
func TimePtrFromTimestamptz(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := helper.UTC(ts.Time)
	return &t
}

// UUIDFromPgtype converts a uuid column into a google/uuid value.
func UUIDFromPgtype(id pgtype.UUID) (uuid.UUID, error) {
	if !id.Valid {
		return uuid.Nil, fmt.Errorf("uuid value is NULL")
	}
	return uuid.UUID(id.Bytes), nil
}

// IntervalFromDuration converts a Go duration into the pgtype value pgx
// writes for an interval column.
//
// Timeframes are whole minutes or hours, so microsecond resolution is exact
// here; a duration finer than a microsecond is rejected rather than silently
// truncated.
func IntervalFromDuration(d time.Duration) (pgtype.Interval, error) {
	if d <= 0 {
		return pgtype.Interval{}, fmt.Errorf("interval %s is not positive", d)
	}
	if d%time.Microsecond != 0 {
		return pgtype.Interval{}, fmt.Errorf("interval %s is finer than a microsecond", d)
	}
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}, nil
}
