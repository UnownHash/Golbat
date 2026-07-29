package decoder

import "testing"

// TestIsFortDnfMatch_StationActive locks the station liveness gate: stations
// are the one ephemeral fort type, and expired ones accumulate in the index —
// station_active:true matches only stations whose end_time is in the future.
func TestIsFortDnfMatch_StationActive(t *testing.T) {
	active := true
	inactive := false
	now := int64(1000)
	live := FortLookup{FortType: STATION, StationEndTimestamp: 2000}
	dead := FortLookup{FortType: STATION, StationEndTimestamp: 500}

	if !isFortDnfMatch(STATION, &live, &ApiFortDnfFilter{StationActive: &active}, now) {
		t.Error("live station should match station_active:true")
	}
	if isFortDnfMatch(STATION, &dead, &ApiFortDnfFilter{StationActive: &active}, now) {
		t.Error("expired station must not match station_active:true")
	}
	if !isFortDnfMatch(STATION, &dead, &ApiFortDnfFilter{StationActive: &inactive}, now) {
		t.Error("expired station should match station_active:false")
	}
	if !isFortDnfMatch(STATION, &dead, &ApiFortDnfFilter{}, now) {
		t.Error("no station_active constraint should match any station")
	}
	// composes with gmax within a clause (AND)
	gmax := true
	liveGmax := FortLookup{FortType: STATION, StationEndTimestamp: 2000, TotalStationedGmax: 2}
	deadGmax := FortLookup{FortType: STATION, StationEndTimestamp: 500, TotalStationedGmax: 2}
	f := ApiFortDnfFilter{StationActive: &active, StationedGmax: &gmax}
	if !isFortDnfMatch(STATION, &liveGmax, &f, now) {
		t.Error("live gmax station should match combined clause")
	}
	if isFortDnfMatch(STATION, &deadGmax, &f, now) {
		t.Error("expired gmax station must not match combined clause")
	}
}
