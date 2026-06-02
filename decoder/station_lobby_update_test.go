package decoder

import "testing"

// Push-gateway lobby messages carry no usable publish timestamp, so each update
// is applied as it arrives (no ordering/dedup).
func TestUpdateStationBattleLobby_Applies(t *testing.T) {
	station := &Station{}
	station.updateBattleLobby(2, 1000)
	if station.BattleLobbyCount.Int64 != 2 || station.BattleLobbyEndMs.Int64 != 1000 {
		t.Errorf("first update not applied: count=%+v end=%+v", station.BattleLobbyCount, station.BattleLobbyEndMs)
	}
	station.updateBattleLobby(5, 2000)
	if station.BattleLobbyCount.Int64 != 5 || station.BattleLobbyEndMs.Int64 != 2000 {
		t.Errorf("second update not applied: count=%+v end=%+v", station.BattleLobbyCount, station.BattleLobbyEndMs)
	}
}
