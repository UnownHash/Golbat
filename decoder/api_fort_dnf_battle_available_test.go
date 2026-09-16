package decoder

import (
	"testing"
	"unsafe"
)

// FortLookup is held by value for every fort in fortLookupCache, so a byte
// added to it is paid once per fort. The station availability fields live in
// the struct's existing tail padding; this pins that they did not grow it.
func TestFortLookupSizeUnchangedByStationAvailability(t *testing.T) {
	if got := unsafe.Sizeof(FortLookup{}); got != 184 {
		t.Fatalf("Sizeof(FortLookup) = %d, want 184 (station availability fields must sit in existing padding)", got)
	}
}

// battle_available mirrors the station's is_battle_available flag and nothing
// else: it says nothing about the station being present or a battle running.
func TestIsFortDnfMatch_BattleAvailable(t *testing.T) {
	yes, no := true, false
	now := int64(1000)
	available := FortLookup{FortType: STATION, BattleAvailable: true, StationEndTimestamp: 500} // flag set on an ended station
	unavailable := FortLookup{FortType: STATION, BattleAvailable: false, StationEndTimestamp: 2000}

	if !isFortDnfMatch(STATION, &available, &ApiFortDnfFilter{BattleAvailable: &yes}, now) {
		t.Error("flag set should match battle_available:true regardless of the station window")
	}
	if isFortDnfMatch(STATION, &unavailable, &ApiFortDnfFilter{BattleAvailable: &yes}, now) {
		t.Error("flag clear must not match battle_available:true")
	}
	if !isFortDnfMatch(STATION, &unavailable, &ApiFortDnfFilter{BattleAvailable: &no}, now) {
		t.Error("flag clear should match battle_available:false")
	}
	if isFortDnfMatch(STATION, &available, &ApiFortDnfFilter{BattleAvailable: &no}, now) {
		t.Error("flag set must not match battle_available:false")
	}
	if !isFortDnfMatch(STATION, &available, &ApiFortDnfFilter{}, now) || !isFortDnfMatch(STATION, &unavailable, &ApiFortDnfFilter{}, now) {
		t.Error("no battle_available constraint should match either station")
	}
}

// station_active:true means the station is currently active: not inactive,
// and inside its start/end window at filter time (strict bounds, like the
// SQL is_inactive = 0 AND start_time < now AND end_time > now).
func TestIsFortDnfMatch_StationActiveWindow(t *testing.T) {
	yes, no := true, false
	now := int64(1000)
	cases := []struct {
		name   string
		lookup FortLookup
		active bool
	}{
		{"inside window", FortLookup{FortType: STATION, StationStartTimestamp: 500, StationEndTimestamp: 2000}, true},
		{"not started yet", FortLookup{FortType: STATION, StationStartTimestamp: 1500, StationEndTimestamp: 2000}, false},
		{"already ended", FortLookup{FortType: STATION, StationStartTimestamp: 100, StationEndTimestamp: 500}, false},
		{"inactive flag inside window", FortLookup{FortType: STATION, StationInactive: true, StationStartTimestamp: 500, StationEndTimestamp: 2000}, false},
		{"starts exactly now (strict)", FortLookup{FortType: STATION, StationStartTimestamp: 1000, StationEndTimestamp: 2000}, false},
		{"ends exactly now (strict)", FortLookup{FortType: STATION, StationStartTimestamp: 500, StationEndTimestamp: 1000}, false},
		{"legacy zero start", FortLookup{FortType: STATION, StationEndTimestamp: 2000}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFortDnfMatch(STATION, &tc.lookup, &ApiFortDnfFilter{StationActive: &yes}, now); got != tc.active {
				t.Errorf("station_active:true = %v, want %v", got, tc.active)
			}
			if got := isFortDnfMatch(STATION, &tc.lookup, &ApiFortDnfFilter{StationActive: &no}, now); got != !tc.active {
				t.Errorf("station_active:false = %v, want %v", got, !tc.active)
			}
		})
	}

	// "Battle available now" is the two predicates AND'd in one clause.
	live := FortLookup{FortType: STATION, BattleAvailable: true, StationStartTimestamp: 500, StationEndTimestamp: 2000}
	notStarted := FortLookup{FortType: STATION, BattleAvailable: true, StationStartTimestamp: 1500, StationEndTimestamp: 2000}
	both := ApiFortDnfFilter{BattleAvailable: &yes, StationActive: &yes}
	if !isFortDnfMatch(STATION, &live, &both, now) {
		t.Error("available flag inside the window should match the combined clause")
	}
	if isFortDnfMatch(STATION, &notStarted, &both, now) {
		t.Error("available flag before start_time must not match the combined clause")
	}
}

// The lookup carries the flag, the inactive bit and the start time verbatim
// from the station record, so a scan sees what the last decode wrote.
func TestUpdateStationLookupCarriesAvailabilityFields(t *testing.T) {
	id := mustFortId(t, "00000000000000000000000000ba7716.11")
	station := &Station{StationData: StationData{
		Id: id, Lat: 1, Lon: 2,
		StartTime: 1700000000, EndTime: 1700003600,
		IsBattleAvailable: true, IsInactive: true,
	}}
	t.Cleanup(func() { fortLookupCache.Delete(id) })

	updateStationLookupWithBattles(id, station, nil)

	fl, ok := fortLookupCache.Load(id)
	if !ok {
		t.Fatal("lookup entry missing after update")
	}
	if !fl.BattleAvailable || !fl.StationInactive || fl.StationStartTimestamp != 1700000000 || fl.StationEndTimestamp != 1700003600 {
		t.Errorf("lookup = available %v inactive %v start %d end %d, want true true 1700000000 1700003600",
			fl.BattleAvailable, fl.StationInactive, fl.StationStartTimestamp, fl.StationEndTimestamp)
	}
}
