package decoder

import "testing"

func TestIsFortDnfMatch_StationedGmax(t *testing.T) {
	gmax := true
	withGmax := &FortLookup{FortType: STATION, TotalStationedGmax: 3}
	noGmax := &FortLookup{FortType: STATION, TotalStationedGmax: 0}
	now := int64(1000)

	if !isFortDnfMatch(STATION, withGmax, &ApiFortDnfFilter{StationedGmax: &gmax}, now) {
		t.Error("station with stationed gmax should match stationed_gmax:true")
	}
	if isFortDnfMatch(STATION, noGmax, &ApiFortDnfFilter{StationedGmax: &gmax}, now) {
		t.Error("station without stationed gmax must not match stationed_gmax:true")
	}
	// null gmax filter is a wildcard — matches either
	if !isFortDnfMatch(STATION, noGmax, &ApiFortDnfFilter{}, now) {
		t.Error("no stationed_gmax constraint should match any station")
	}
}
