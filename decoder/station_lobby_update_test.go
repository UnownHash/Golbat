package decoder

import "testing"

func TestUpdateStationBattleLobby_DedupOlder(t *testing.T) {
	station := &Station{}
	if !station.updateBattleLobby(3, 1000, 5000) { // newer -> applied
		t.Fatal("first update should apply")
	}
	if station.BattleLobbyCount.Int64 != 3 || station.BattleLobbyPubMs != 5000 {
		t.Errorf("not applied: %+v / %d", station.BattleLobbyCount, station.BattleLobbyPubMs)
	}
	if station.updateBattleLobby(9, 1000, 4000) { // older pub ms -> dropped
		t.Error("older message should be dropped")
	}
	if station.BattleLobbyCount.Int64 != 3 {
		t.Error("count must not regress on a dropped (older) message")
	}
}
