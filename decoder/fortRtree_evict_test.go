package decoder

import "testing"

func TestFlushFortTreeEvictionsRemovesPoints(t *testing.T) {
	ids := []string{"fort-evict-a", "fort-evict-b"}
	fortTreeMutex.Lock()
	for _, id := range ids {
		fortTree.Insert([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, id)
	}
	fortTreeMutex.Unlock()

	inTree := func(id string) bool {
		found := false
		fortTreeMutex.RLock()
		fortTree.Search([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, func(_, _ [2]float64, v string) bool {
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

	flushFortTreeEvictions([]treeEvictionEntry[string]{
		{id: "fort-evict-a", lat: 8.5, lon: 9.5},
		{id: "fort-evict-b", lat: 8.5, lon: 9.5},
	})

	for _, id := range ids {
		if inTree(id) {
			t.Errorf("%s still in tree after flush", id)
		}
	}
}
