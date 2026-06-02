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

// Messages with no publish timestamp (pub=0) cannot be ordered and must always be
// applied, never dropped by the dedup guard.
func TestUpdateStationBattleLobby_ZeroPubAlwaysApplies(t *testing.T) {
	station := &Station{}
	if !station.updateBattleLobby(2, 1000, 0) { // first, no timestamp -> applied
		t.Fatal("zero-pub update should apply on a fresh station")
	}
	if station.BattleLobbyCount.Int64 != 2 {
		t.Errorf("count not applied: %+v", station.BattleLobbyCount)
	}
	if !station.updateBattleLobby(5, 1000, 0) { // subsequent zero-pub -> still applied
		t.Error("subsequent zero-pub update should apply, not be treated as duplicate")
	}
	if station.BattleLobbyCount.Int64 != 5 {
		t.Errorf("count not updated by second zero-pub message: %+v", station.BattleLobbyCount)
	}
}
