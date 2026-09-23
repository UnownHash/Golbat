package decoder

import (
	"slices"
	"testing"

	"golbat/config"
)

// Cell-only nearby pokemon are placed at the S2 cell centre and are not
// processed unless a scan rule opts in with nearby_cell_pokemon; enabling
// nearby_pokemon alone no longer implies them.
func TestProcessNearbyCellDefaultsOff(t *testing.T) {
	saved := config.Config.ScanRules
	t.Cleanup(func() { config.Config.ScanRules = saved })
	on, off := true, false

	config.Config.ScanRules = nil
	if FindScanConfiguration("", 0, 0).ProcessNearbyCell {
		t.Error("no scan rules: ProcessNearbyCell = true, want false")
	}

	config.Config.ScanRules = slices.Grow(config.Config.ScanRules[:0:0], 1)[:1]
	if FindScanConfiguration("", 0, 0).ProcessNearbyCell {
		t.Error("rule without nearby settings: ProcessNearbyCell = true, want false")
	}

	config.Config.ScanRules[0].ProcessNearby = &on
	if FindScanConfiguration("", 0, 0).ProcessNearbyCell {
		t.Error("rule with nearby_pokemon = true: ProcessNearbyCell = true, want false")
	}

	config.Config.ScanRules[0].ProcessNearbyCell = &on
	if !FindScanConfiguration("", 0, 0).ProcessNearbyCell {
		t.Error("rule with nearby_cell_pokemon = true: ProcessNearbyCell = false, want true")
	}

	config.Config.ScanRules[0].ProcessNearbyCell = &off
	if FindScanConfiguration("", 0, 0).ProcessNearbyCell {
		t.Error("rule with nearby_cell_pokemon = false: ProcessNearbyCell = true, want false")
	}
}
