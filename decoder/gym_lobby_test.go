package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

func TestSetRaidLobby_MarksInternalDirtyOnly(t *testing.T) {
	gym := &Gym{}
	gym.SetRaidLobbyCount(null.IntFrom(3))
	if !gym.RaidLobbyCount.Valid || gym.RaidLobbyCount.Int64 != 3 {
		t.Errorf("count = %+v", gym.RaidLobbyCount)
	}
	if gym.dirty {
		t.Error("must not set dirty (no DB write for in-memory lobby field)")
	}
	if !gym.internalDirty {
		t.Error("must set internalDirty (in-memory/rtree update)")
	}
}
