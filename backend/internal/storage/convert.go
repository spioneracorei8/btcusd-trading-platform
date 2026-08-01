package storage

import (
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// This file is the only place where database wire types meet domain types.
//
// numeric columns are read as pgtype.Numeric and converted to
// decimal.Decimal: both represent a value as coefficient * 10^exponent, so
// the conversion is exact in both directions and float64 never appears.

// numericFromDecimal converts a decimal into the pgtype value pgx writes.
func numericFromDecimal(d decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{
		Int:   d.Coefficient(),
		Exp:   d.Exponent(),
		Valid: true,
	}
}

// nullNumericFromDecimal converts a nullable decimal into a pgtype value.
// An invalid NullDecimal becomes SQL NULL.
func nullNumericFromDecimal(d decimal.NullDecimal) pgtype.Numeric {
	if !d.Valid {
		return pgtype.Numeric{}
	}
	return numericFromDecimal(d.Decimal)
}

// decimalFromNumeric converts a value read from a numeric column.
//
// NaN and infinity are rejected rather than silently coerced: neither can be
// produced by the price columns, so seeing one means the data is corrupt.
func decimalFromNumeric(n pgtype.Numeric) (decimal.Decimal, error) {
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

// nullDecimalFromNumeric converts a nullable numeric column. SQL NULL becomes
// an invalid NullDecimal rather than an error.
func nullDecimalFromNumeric(n pgtype.Numeric) (decimal.NullDecimal, error) {
	if !n.Valid {
		return decimal.NullDecimal{}, nil
	}
	d, err := decimalFromNumeric(n)
	if err != nil {
		return decimal.NullDecimal{}, err
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}, nil
}

// timestamptzFromTime converts a Go time into the pgtype value pgx writes.
// The value is normalised to UTC because every timestamp in this system is UTC.
func timestamptzFromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// timeFromTimestamptz converts a timestamptz column into a UTC time.
func timeFromTimestamptz(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// timePtrFromTimestamptz converts a nullable timestamptz column; SQL NULL
// becomes nil.
func timePtrFromTimestamptz(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// uuidFromPgtype converts a uuid column into a google/uuid value.
func uuidFromPgtype(id pgtype.UUID) (uuid.UUID, error) {
	if !id.Valid {
		return uuid.Nil, fmt.Errorf("uuid value is NULL")
	}
	return uuid.UUID(id.Bytes), nil
}
