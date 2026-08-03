package helper_test

import (
	"testing"
	"time"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

func TestUTCNormalisesLocation(t *testing.T) {
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}

	local := time.Date(2026, 8, 1, 7, 0, 0, 0, bangkok)

	got := helper.UTC(local)
	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
	if !got.Equal(local) {
		t.Errorf("UTC() changed the instant: %s != %s", got, local)
	}
	if got.Hour() != 0 {
		t.Errorf("hour = %d, want 0 (07:00 +07 is midnight UTC)", got.Hour())
	}
}
