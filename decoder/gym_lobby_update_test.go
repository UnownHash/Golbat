package decoder

import "testing"

// Push-gateway lobby messages carry no usable publish timestamp, so each update
// is applied as it arrives (no ordering/dedup).
func TestUpdateGymRaidLobby_Applies(t *testing.T) {
	gym := &Gym{}
	gym.updateRaidLobby(2, 1000)
	if gym.RaidLobbyCount.Int64 != 2 || gym.RaidLobbyEndMs.Int64 != 1000 {
		t.Errorf("first update not applied: count=%+v end=%+v", gym.RaidLobbyCount, gym.RaidLobbyEndMs)
	}
	gym.updateRaidLobby(5, 2000)
	if gym.RaidLobbyCount.Int64 != 5 || gym.RaidLobbyEndMs.Int64 != 2000 {
		t.Errorf("second update not applied: count=%+v end=%+v", gym.RaidLobbyCount, gym.RaidLobbyEndMs)
	}
}
