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
//
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
	// wantPokemonData: the design doc estimated 232 for this field order, but
	// measured is 280. The doc's estimate treated "pointer-carrying, last" as
	// free; it isn't — grouping PokestopId/SeenType/Username/Pvp/GolbatInternal
	// at the end (their own 8-byte alignment, deliberately kept separate from
	// the struct's other 8-byte-aligned fields for a tighter GC pointer scan)
	// reintroduces the 7 bytes of padding a fully-monotonic size ordering would
	// have avoided. Still 592 -> 280: a 2.1x reduction. Landing inside the
	// [257, 288] Go GC size class either way, so no field regrouping was made
	// to chase the doc's number — see this test's file-level comment for why
	// that's not how a fail here should be resolved.
	//
	// wantPokemon: 800 -> 456, still carries `changedFields []string` and
	// `internal grpc.PokemonInternal`, both left for a later task.
	const (
		wantPokemonData = 280
		wantPokemon     = 456
	)

	if got := unsafe.Sizeof(PokemonData{}); got != wantPokemonData {
		t.Errorf("unsafe.Sizeof(PokemonData{}) = %d, want %d", got, wantPokemonData)
	}
	if got := unsafe.Sizeof(Pokemon{}); got != wantPokemon {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want %d", got, wantPokemon)
	}
}
