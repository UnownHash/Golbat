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
	// wantPokemonData: field payload sums to 273 bytes (8-byte group 56 +
	// 4-byte group 48 + 2-byte group 26 + 1-byte group 23 + pointer group
	// 120: PokestopId/SeenType/Username/Pvp/GolbatInternal, each 8-byte
	// aligned). Go's struct alignment is 8 (driven by the uint64/float64/
	// pointer fields), so ANY ordering of this field set rounds up to 280 —
	// that is the achievable minimum, not a target this order fell short of.
	// The pointer group is placed last (after the 1-byte group) for a
	// tighter GC pointer-scan span, which forces exactly 7 bytes of
	// mandatory padding at offset 153->160; moving that group elsewhere does
	// not remove those 7 bytes, it only relocates them (e.g. to trailing
	// padding at the very end) — 273 always rounds to 280. The design doc's
	// original estimate of 232 for this order (and 264 for an unordered
	// trial) was an arithmetic error: 264 was never achievable either,
	// being below the 273-byte payload. Still 592 -> 280: a 2.1x reduction.
	// No field regrouping was made to chase the doc's wrong number — see
	// this test's file-level comment for why that's not how a fail here
	// should be resolved.
	//
	// wantPokemon: 800 -> 456, still carries `changedFields []string` and
	// `internal grpc.PokemonInternal`, both left for a later task. 456 lands
	// in the 480-byte GC size class (see the class list in this file's
	// top-level comment), comfortably under the 512-byte threshold where
	// mark cost jumps. PokemonData itself is never independently
	// heap-allocated — it's embedded in Pokemon and copied by value into
	// []PokemonData write-behind batches — so no size class applies to it
	// directly; the class boundary only matters for Pokemon.
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
