package decoder

import (
	"testing"

	"github.com/guregu/null/v6"

	"golbat/config"
)

// TestDeletedFortsNotIndexedOnLoad locks that a fort already marked deleted is
// not indexed when it reaches the cache-miss load path. The load-by-id queries
// have no deleted filter, and deletion leaves enabled and the quest fields
// intact, so an indexed deleted fort would be returned by fort scans.
func TestDeletedFortsNotIndexedOnLoad(t *testing.T) {
	prev := config.Config.FortInMemory
	config.Config.FortInMemory = true
	t.Cleanup(func() { config.Config.FortInMemory = prev })

	t.Run("pokestop", func(t *testing.T) {
		const id = "deleted-pokestop-on-load"
		t.Cleanup(func() { fortLookupCache.Delete(id) })

		fortRtreeUpdatePokestopOnGet(&Pokestop{PokestopData: PokestopData{
			Id: id, Lat: 5, Lon: 5, Enabled: null.BoolFrom(true), Deleted: true,
		}})

		if _, found := fortLookupCache.Load(id); found {
			t.Fatal("deleted pokestop was indexed into the fort lookup cache")
		}
	})

	t.Run("gym", func(t *testing.T) {
		const id = "deleted-gym-on-load"
		t.Cleanup(func() { fortLookupCache.Delete(id) })

		fortRtreeUpdateGymOnGet(&Gym{GymData: GymData{
			Id: id, Lat: 6, Lon: 6, Deleted: true,
		}})

		if _, found := fortLookupCache.Load(id); found {
			t.Fatal("deleted gym was indexed into the fort lookup cache")
		}
	})

	t.Run("a live pokestop is still indexed", func(t *testing.T) {
		const id = "live-pokestop-on-load"
		t.Cleanup(func() { fortLookupCache.Delete(id) })

		fortRtreeUpdatePokestopOnGet(&Pokestop{PokestopData: PokestopData{
			Id: id, Lat: 7, Lon: 7, Enabled: null.BoolFrom(true),
		}})

		if _, found := fortLookupCache.Load(id); !found {
			t.Fatal("live pokestop was not indexed, so the guard is too broad")
		}
	})
}
