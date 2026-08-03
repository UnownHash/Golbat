package decoder

import (
	"testing"

	"golbat/config"
)

func TestFortScanLimit(t *testing.T) {
	origMax := config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxFortResults = 100
	defer func() { config.Config.Tuning.MaxFortResults = origMax }()

	if got := fortScanLimit(0); got != 100 {
		t.Errorf("default limit: got %d, want 100", got)
	}
	if got := fortScanLimit(40); got != 40 {
		t.Errorf("requested below cap: got %d, want 40", got)
	}
	if got := fortScanLimit(500); got != 100 {
		t.Errorf("requested above cap: got %d, want 100", got)
	}
}

func TestFortScanLimitReached(t *testing.T) {
	origMax := config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxFortResults = 100
	defer func() { config.Config.Tuning.MaxFortResults = origMax }()

	if fortScanLimitReached(40, 39) {
		t.Error("39 of 40 should not be limit_reached")
	}
	if !fortScanLimitReached(40, 40) {
		t.Error("40 of 40 should be limit_reached")
	}
	if !fortScanLimitReached(0, 100) {
		t.Error("100 of default 100 should be limit_reached")
	}
}
