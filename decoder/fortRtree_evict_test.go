package decoder

import "testing"

func TestFlushFortTreeEvictionsRemovesPoints(t *testing.T) {
	ids := []FortId{
		mustFortId(t, "00000000000000000000000000000001"),
		mustFortId(t, "00000000000000000000000000000002"),
	}
	fortTreeMutex.Lock()
	for _, id := range ids {
		fortTree.Insert([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, id)
	}
	fortTreeMutex.Unlock()

	inTree := func(id FortId) bool {
		found := false
		fortTreeMutex.RLock()
		fortTree.Search([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, func(_, _ [2]float64, v FortId) bool {
			if v == id {
				found = true
				return false
			}
			return true
		})
		fortTreeMutex.RUnlock()
		return found
	}

	for _, id := range ids {
		if !inTree(id) {
			t.Fatalf("setup: %s not in tree", id)
		}
	}

	flushFortTreeEvictions([]treeEvictionEntry[FortId]{
		{id: ids[0], lat: 8.5, lon: 9.5},
		{id: ids[1], lat: 8.5, lon: 9.5},
	})

	for _, id := range ids {
		if inTree(id) {
			t.Errorf("%s still in tree after flush", id)
		}
	}
}

func TestFlushFortTreeEvictionsPreservesReindexedFort(t *testing.T) {
	id := mustFortId(t, "00000000000000000000000000000d04")
	const lat, lon = 12.5, 13.5
	gym := &Gym{GymData: GymData{Id: id, Lat: lat, Lon: lon}}
	fortRtreeUpdateGymOnSave(gym)
	t.Cleanup(func() { removeFortIndex(GYM, id) })

	flushFortTreeEvictions([]treeEvictionEntry[FortId]{{id: id, lat: lat, lon: lon}})
	for _, found := range GetFortsInBounds(lat-.01, lon-.01, lat+.01, lon+.01) {
		if found == id {
			return
		}
	}
	t.Fatal("queued eviction removed the reindexed gym")
}
