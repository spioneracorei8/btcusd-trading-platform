package database

import (
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func TestDecimalNumericRoundTrip(t *testing.T) {
	// Values chosen to cover BTC prices at 8 decimal places, exchange volumes
	// and the awkward cases (zero, negatives, trailing zeros) where a float64
	// implementation would start drifting.
	inputs := []string{
		"0",
		"1",
		"-1",
		"0.00000001",
		"-0.00000001",
		"64321.12345678",
		"99999999999.99999999",
		"0.05",
		"1234.50000000",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			want, err := decimal.NewFromString(in)
			if err != nil {
				t.Fatalf("decimal.NewFromString(%q) failed: %v", in, err)
			}

			got, err := DecimalFromNumeric(NumericFromDecimal(want))
			if err != nil {
				t.Fatalf("DecimalFromNumeric() returned error: %v", err)
			}
			if !got.Equal(want) {
				t.Errorf("round trip of %s produced %s", want, got)
			}
		})
	}
}

func TestDecimalFromNumericRejectsNonFiniteValues(t *testing.T) {
	tests := []struct {
		name string
		in   pgtype.Numeric
	}{
		{name: "null", in: pgtype.Numeric{}},
		{name: "nan", in: pgtype.Numeric{NaN: true, Valid: true}},
		{
			name: "infinity",
			in:   pgtype.Numeric{InfinityModifier: pgtype.Infinity, Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecimalFromNumeric(tt.in); err == nil {
				t.Errorf("DecimalFromNumeric(%+v) returned no error", tt.in)
			}
		})
	}
}

func TestDecimalFromNumericNilIntIsZero(t *testing.T) {
	got, err := DecimalFromNumeric(pgtype.Numeric{Valid: true})
	if err != nil {
		t.Fatalf("DecimalFromNumeric() returned error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("DecimalFromNumeric() = %s, want 0", got)
	}
}

func TestNumericFromDecimalKeepsExactCoefficient(t *testing.T) {
	d := decimal.NewFromBigInt(big.NewInt(6432112345678), -8)

	n := NumericFromDecimal(d)
	if !n.Valid {
		t.Fatal("NumericFromDecimal() produced an invalid value")
	}
	if n.Exp != -8 {
		t.Errorf("Exp = %d, want -8", n.Exp)
	}
	if n.Int.String() != "6432112345678" {
		t.Errorf("Int = %s, want 6432112345678", n.Int)
	}
}

func TestNullDecimalConversion(t *testing.T) {
	t.Run("null stays null", func(t *testing.T) {
		n := NullNumericFromDecimal(decimal.NullDecimal{})
		if n.Valid {
			t.Fatal("an invalid NullDecimal must map to SQL NULL")
		}

		got, err := NullDecimalFromNumeric(n)
		if err != nil {
			t.Fatalf("NullDecimalFromNumeric() returned error: %v", err)
		}
		if got.Valid {
			t.Error("SQL NULL must map back to an invalid NullDecimal")
		}
	})

	t.Run("value survives", func(t *testing.T) {
		want := decimal.RequireFromString("64000.5")
		got, err := NullDecimalFromNumeric(NullNumericFromDecimal(decimal.NullDecimal{Decimal: want, Valid: true}))
		if err != nil {
			t.Fatalf("NullDecimalFromNumeric() returned error: %v", err)
		}
		if !got.Valid || !got.Decimal.Equal(want) {
			t.Errorf("round trip produced %+v, want %s", got, want)
		}
	})
}

func TestTimestamptzConversionNormalisesToUTC(t *testing.T) {
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}

	local := time.Date(2026, 8, 1, 17, 30, 0, 0, bangkok)

	ts := TimestamptzFromTime(local)
	if ts.Time.Location() != time.UTC {
		t.Errorf("TimestamptzFromTime() kept location %v, want UTC", ts.Time.Location())
	}

	got := TimeFromTimestamptz(ts)
	if got.Location() != time.UTC {
		t.Errorf("TimeFromTimestamptz() returned location %v, want UTC", got.Location())
	}
	if !got.Equal(local) {
		t.Errorf("TimeFromTimestamptz() = %s, want the same instant as %s", got, local)
	}
}

func TestTimePtrFromTimestamptz(t *testing.T) {
	if got := TimePtrFromTimestamptz(pgtype.Timestamptz{}); got != nil {
		t.Errorf("NULL timestamptz produced %v, want nil", got)
	}

	want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got := TimePtrFromTimestamptz(pgtype.Timestamptz{Time: want, Valid: true})
	if got == nil || !got.Equal(want) {
		t.Errorf("TimePtrFromTimestamptz() = %v, want %s", got, want)
	}
}

func TestUUIDFromPgtype(t *testing.T) {
	if _, err := UUIDFromPgtype(pgtype.UUID{}); err == nil {
		t.Error("a NULL uuid must produce an error")
	}

	raw := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	got, err := UUIDFromPgtype(pgtype.UUID{Bytes: raw, Valid: true})
	if err != nil {
		t.Fatalf("UUIDFromPgtype() returned error: %v", err)
	}
	if got.String() != "01020304-0506-0708-090a-0b0c0d0e0f10" {
		t.Errorf("UUIDFromPgtype() = %s", got)
	}
}
