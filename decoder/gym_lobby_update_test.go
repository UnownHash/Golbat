package decoder

import "testing"

func TestUpdateGymRaidLobby_DedupOlder(t *testing.T) {
	gym := &Gym{}
	if !gym.updateRaidLobby(3, 1000, 5000) { // newer -> applied
		t.Fatal("first update should apply")
	}
	if gym.RaidLobbyCount.Int64 != 3 || gym.RaidLobbyPubMs != 5000 {
		t.Errorf("not applied: %+v / %d", gym.RaidLobbyCount, gym.RaidLobbyPubMs)
	}
	if gym.updateRaidLobby(9, 1000, 4000) { // older pub ms -> dropped
		t.Error("older message should be dropped")
	}
	if gym.RaidLobbyCount.Int64 != 3 {
		t.Error("count must not regress on a dropped (older) message")
	}
}

// Messages with no publish timestamp (pub=0) cannot be ordered and must always be
// applied, never dropped by the dedup guard.
func TestUpdateGymRaidLobby_ZeroPubAlwaysApplies(t *testing.T) {
	gym := &Gym{}
	if !gym.updateRaidLobby(2, 1000, 0) { // first, no timestamp -> applied
		t.Fatal("zero-pub update should apply on a fresh gym")
	}
	if gym.RaidLobbyCount.Int64 != 2 {
		t.Errorf("count not applied: %+v", gym.RaidLobbyCount)
	}
	if !gym.updateRaidLobby(5, 1000, 0) { // subsequent zero-pub -> still applied
		t.Error("subsequent zero-pub update should apply, not be treated as duplicate")
	}
	if gym.RaidLobbyCount.Int64 != 5 {
		t.Errorf("count not updated by second zero-pub message: %+v", gym.RaidLobbyCount)
	}
}
