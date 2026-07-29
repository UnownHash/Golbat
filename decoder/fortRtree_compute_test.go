package decoder

import (
	"sync"
	"testing"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
)

// updatePokestopLookup (pokestop entity lock) and
// updatePokestopIncidentLookup (incident entity lock) update the same
// fortLookupCache key from different lock domains, each preserving the
// other's fields. With plain Load->Store the pair could interleave and
// clobber (lost incident, or quest fields reverted). Compute makes the
// read-modify-write atomic per key: after any concurrent pair, BOTH
// halves' latest writes must be present.
func TestFortLookupConcurrentPokestopAndIncidentWriters(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	initQuestConditions() // updatePokestopLookup reconciles quest conditions

	const id = "compute-race-stop"
	stop := &Pokestop{PokestopData: PokestopData{
		Id: id, Lat: 50, Lon: 4,
		QuestRewardType: null.IntFrom(7),
	}}
	inc := &Incident{IncidentData: IncidentData{
		DisplayType:    3,
		Character:      44,
		Slot1PokemonId: null.IntFrom(215),
		ExpirationTime: 9_999_999_999, // far future so the upsert does not prune it
	}}

	for i := 0; i < 2000; i++ {
		fortLookupCache.Delete(id)
		updatePokestopLookup(stop) // seed entry (as a fort save would)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); updatePokestopLookup(stop) }()
		go func() { defer wg.Done(); updatePokestopIncidentLookup(id, inc) }()
		wg.Wait()

		got, ok := fortLookupCache.Load(id)
		if !ok {
			t.Fatal("entry vanished")
		}
		if got.QuestNoArRewardType != 7 {
			t.Fatalf("iteration %d: quest fields clobbered by incident writer (QuestNoArRewardType=%d)", i, got.QuestNoArRewardType)
		}
		found := false
		for _, in := range got.Incidents {
			if in.Character == 44 && in.Slot1PokemonId == 215 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("iteration %d: incident write lost (incidents=%+v)", i, got.Incidents)
		}
	}
}
