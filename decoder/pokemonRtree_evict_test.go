package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

func TestFlushPokemonTreeEvictionsRemovesPoints(t *testing.T) {
	// pokemonTree is a package global; count only the ids we add.
	ids := []uint64{910001, 910002, 910003}
	for _, id := range ids {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 1.5, Lon: 2.5, PokemonId: 1, Form: null.IntFrom(0)}}
		addPokemonToTree(p)
	}

	inTree := func(id uint64) bool {
		found := false
		pokemonTreeMutex.RLock()
		pokemonTree.Search([2]float64{2.5, 1.5}, [2]float64{2.5, 1.5}, func(_, _ [2]float64, v uint64) bool {
			if v == id {
				found = true
				return false
			}
			return true
		})
		pokemonTreeMutex.RUnlock()
		return found
	}

	for _, id := range ids {
		if !inTree(id) {
			t.Fatalf("setup: %d not in tree", id)
		}
	}

	flushPokemonTreeEvictions([]treeEvictionEntry[uint64]{
		{id: 910001, lat: 1.5, lon: 2.5},
		{id: 910002, lat: 1.5, lon: 2.5},
		{id: 910003, lat: 1.5, lon: 2.5},
	})

	for _, id := range ids {
		if inTree(id) {
			t.Errorf("%d still in tree after flush", id)
		}
	}
}
