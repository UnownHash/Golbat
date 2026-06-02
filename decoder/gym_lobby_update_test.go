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
