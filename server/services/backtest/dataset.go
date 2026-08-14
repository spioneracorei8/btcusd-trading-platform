package backtest

import (
	"fmt"
	"time"
)

// Dataset selects which slice of history a run may look at.
//
// # Why this is enforced in the tooling
//
// The split only works if it is respected, and it is respected by a person
// under pressure to find a result. Making it a flag with a default, rather
// than a date range typed by hand each time, removes the small friction that
// would otherwise be resolved in favour of "just this once".
//
// It is not a lock. Anyone can pass explicit dates, and should be able to.
// What it does is make the choice explicit and, for the holdout, recorded.
type Dataset string

// The datasets.
const (
	// DatasetDev is where development, tuning and iteration happen. Run it as
	// often as you like: that is what it is for.
	DatasetDev Dataset = "dev"

	// DatasetHoldout is run once, at the end, on the single strategy already
	// chosen. Its only value is having been untouched, and a second look
	// spends that permanently.
	DatasetHoldout Dataset = "holdout"

	// DatasetCustom is an explicit range that is neither. It exists so that
	// looking at some other window is possible without pretending to be one of
	// the two that mean something.
	DatasetCustom Dataset = "custom"
)

// The split. These dates are part of the method, not configuration: changing
// them is changing what the holdout means, and doing so quietly would make
// every earlier result incomparable.
var (
	// DevFrom and DevTo bound the development set.
	DevFrom = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	DevTo   = time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC)

	// HoldoutFrom opens the holdout set. It has no end: everything after the
	// development set is held out until it is spent.
	HoldoutFrom = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Valid reports whether d is a known dataset.
func (d Dataset) Valid() bool {
	switch d {
	case DatasetDev, DatasetHoldout, DatasetCustom:
		return true
	default:
		return false
	}
}

// String returns the flag representation of the dataset.
func (d Dataset) String() string { return string(d) }

// ParseDataset converts s into a Dataset, rejecting unknown values.
func ParseDataset(s string) (Dataset, error) {
	d := Dataset(s)
	if !d.Valid() {
		return "", fmt.Errorf("unknown dataset %q (want %q, %q or %q)",
			s, DatasetDev, DatasetHoldout, DatasetCustom)
	}
	return d, nil
}

// Range returns the window a dataset covers.
//
// now bounds the holdout, which is otherwise open-ended. A custom dataset has
// no range of its own — the caller supplies the dates, which is what makes it
// custom.
func (d Dataset) Range(now time.Time) (from, to time.Time, ok bool) {
	switch d {
	case DatasetDev:
		return DevFrom, DevTo, true
	case DatasetHoldout:
		return HoldoutFrom, now.UTC(), true
	default:
		return time.Time{}, time.Time{}, false
	}
}

// Describe renders the dataset and its window for a report header.
func (d Dataset) Describe(from, to time.Time) string {
	return fmt.Sprintf("%s (%s .. %s)", d,
		from.Format("2006-01-02"), to.Format("2006-01-02"))
}

// Spent reports whether a run against this dataset consumes something that
// cannot be replaced.
//
// Only the holdout is spent, and it is spent on the first look, not the tenth.
func (d Dataset) Spent() bool { return d == DatasetHoldout }
