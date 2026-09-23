package decoder

import "testing"

func TestHasRealPosition(t *testing.T) {
	cases := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"the origin is an absence, not a location", 0, 0, false},
		{"latitude alone is a position", 51.5, 0, true},
		{"longitude alone is a position", 0, -0.12, true},
		{"both set", 51.5, -0.12, true},
		{"southern and eastern", -33.86, 151.21, true},
	}

	for _, c := range cases {
		if got := hasRealPosition(c.lat, c.lon); got != c.want {
			t.Errorf("%s: hasRealPosition(%v, %v) = %v, want %v", c.name, c.lat, c.lon, got, c.want)
		}
	}
}

// countIndexedAt reports how many pokemon the map index holds at exactly one
// point, which is the shape the (0,0) pile takes.
func countIndexedAt(lat, lon float64) int {
	n := 0
	pokemonTreeMutex.RLock()
	pokemonTree.Search([2]float64{lon, lat}, [2]float64{lon, lat}, func(_, _ [2]float64, _ uint64) bool {
		n++
		return true
	})
	pokemonTreeMutex.RUnlock()
	return n
}

// A pokemon whose position was never supplied must stay out of the map index.
// Indexing it puts it at the origin, and because an R-tree cannot split
// coincident points every such pokemon joins one degenerate node.
func TestPokemonWithoutPositionIsNotIndexed(t *testing.T) {
	before := countIndexedAt(0, 0)

	addPokemonToTree(&Pokemon{PokemonData: PokemonData{Id: Uint64Str(90000001), PokemonId: 25}})

	if got := countIndexedAt(0, 0); got != before {
		t.Errorf("pokemon with no position was indexed at the origin: %d entries, want %d", got, before)
	}
}

func TestPokemonWithPositionIsIndexed(t *testing.T) {
	const lat, lon = 51.5007, -0.1246

	before := countIndexedAt(lat, lon)

	addPokemonToTree(&Pokemon{PokemonData: PokemonData{Id: Uint64Str(90000002), Lat: lat, Lon: lon, PokemonId: 25}})

	if got := countIndexedAt(lat, lon); got != before+1 {
		t.Errorf("positioned pokemon was not indexed: %d entries, want %d", got, before+1)
	}
}

// The remove path needs the same guard as the insert: an encounter that
// supplies a real position removes the old entry first, so an unguarded
// remove aims a steady stream of deletes at the origin pile.
func TestUnpositionedRemoveIsNotQueued(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("removing an unpositioned pokemon panicked: %v", r)
		}
	}()

	queuePokemonTreeRemove(90000003, 0, 0)
}
