package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

func TestSetBattleLobby_MarksInternalDirtyOnly(t *testing.T) {
	st := &Station{}
	st.SetBattleLobbyCount(null.IntFrom(5))
	if !st.BattleLobbyCount.Valid || st.BattleLobbyCount.Int64 != 5 {
		t.Errorf("count = %+v", st.BattleLobbyCount)
	}
	if st.dirty {
		t.Error("must not set dirty")
	}
	if !st.internalDirty {
		t.Error("must set internalDirty")
	}
}
