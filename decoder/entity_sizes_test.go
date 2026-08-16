package decoder

import (
	"testing"
	"unsafe"
)

// gcSizeThreshold is the object size above which Go's garbage collector gets
// materially more expensive. Measured across 5M live entries: a 512-byte object
// carrying one pointer marks in 62.6ms, a 520-byte one in 194.4ms.
//
// Pokemon is cached in the millions, so staying under this is the entire point
// of the packing work in
// docs/superpowers/specs/2026-08-16-pokemon-struct-packing-design.md.
//nolint:unused
const gcSizeThreshold = 512

// TestPokemonEntitySizes pins the in-memory footprint of the cached pokemon
// entity so that growth is a deliberate decision rather than an accident.
//
// If this fails because you added a field: do not simply update the constant.
// Go rounds every allocation up to a size class, so the cost of your field is
// not its own width but the distance to the next class boundary. The classes
// either side of Pokemon are 416, 448, 480 and 512 bytes. Check which one you
// landed in, and whether the field could be narrower or live somewhere else.
func TestPokemonEntitySizes(t *testing.T) {
	const (
		wantPokemonData = 592
		wantPokemon     = 800
	)

	if got := unsafe.Sizeof(PokemonData{}); got != wantPokemonData {
		t.Errorf("unsafe.Sizeof(PokemonData{}) = %d, want %d", got, wantPokemonData)
	}
	if got := unsafe.Sizeof(Pokemon{}); got != wantPokemon {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want %d", got, wantPokemon)
	}
}
