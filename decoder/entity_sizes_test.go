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
// TestPokemonUnderGCThreshold below is the acceptance test that enforces it.
const gcSizeThreshold = 512

// TestPokemonEntitySizes pins the in-memory footprint of the cached pokemon
// entity so that growth is a deliberate decision rather than an accident.
//
// If this fails because you added a field: do not simply update the constant.
// Go rounds every heap allocation up to a size class, so the cost of your
// field is not its own width but the distance to the next class boundary.
// The classes either side of Pokemon's 392-byte compiled size are 384 and
// 416 — unsafe.Sizeof(Pokemon{}) = 392 does NOT mean the allocator hands out
// 392 bytes; it rounds up to 416, confirmed empirically (n=200000 live
// *Pokemon, runtime.MemStats.TotalAlloc delta / n = 416.0 exactly). 392 is
// mid-class: there is 24 bytes of free space before the next class boundary
// (416) is actually crossed, so a modest field addition is "free" in
// allocator terms even though it moves unsafe.Sizeof. Check which class you
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
	// Task 5 moved SeenType out of the pointer group (null.String, 24 bytes,
	// 8-byte aligned) into the 1-byte group (NullSeenType — a uint8 code plus
	// a bool, 2 bytes, 1-byte aligned): pointer group 120 -> 96, 1-byte group
	// 23 -> 25. New payload: 56 + 48 + 26 + 25 + 96 = 251, rounding up to 256.
	// That is a 24-byte drop, not the design doc's estimated 22 — another of
	// its arithmetic misses (see the 232/264 note above); 256 is what the
	// test actually measures and is the number that matters.
	//
	// wantPokemon: 800 -> 456 from tasks 1-4, then a further 40-byte drop
	// from task 5's SeenType narrowing: PokemonData embeds the same 24-byte
	// drop as above, and PokemonOldValues (also holding a SeenType, used for
	// webhook/stats comparison) shrinks from 48 to 32 bytes for the same
	// reason, a 16-byte drop. 456 - 24 - 16 = 416, landing exactly on a GC
	// size class.
	//
	// Task 6 dropped the `changedFields []string` field (24 bytes): 416 - 24
	// = 392. In a PRODUCTION (non-dbdebug)
	// build the field's replacement, `debug pokemonDebugState`, is defined
	// zero-sized (see db_debug_off.go), so it costs nothing — the 392 is a
	// real, full 24-byte reduction in compiled struct size, not a rounding
	// artifact. It does NOT change the GC size class the allocator actually
	// hands out (see this test's top-of-function comment): 392 still rounds
	// up to 416, same as before this task. The durable win is the removed
	// field itself — one fewer pointer word in the GC scan bitmap per cached
	// pokemon, and a dead-in-production field gone from the struct — not a
	// reduction in allocated bytes.
	//
	// Task 7 took the other lever that comment named: the embedded
	// `internal grpc.PokemonInternal` (64 bytes of protobuf machinery —
	// MessageState, sizeCache, unknownFields, slice header) became
	// `scanHistory []*pokemonScan` (a 24-byte slice header), 392 - 40 = 352,
	// this test's current pinned value. Unlike task 6 this one DOES move the
	// allocator: 352 is itself a Go size class, so the bytes handed out per
	// cached pokemon go 416 -> 352. Measured the same way as the 416 above
	// (n=200000 live *Pokemon, runtime.MemStats.TotalAlloc delta / n): 352.0
	// exactly, against 416.0 for a 392-byte control struct. Each history
	// entry also shrank, 88 -> 44 bytes (allocator 96 -> 48).
	//
	// A dbdebug build (`-tags dbdebug`) keeps pokemonDebugState's real
	// `[]string` accumulator (24 bytes, see db_debug.go) so it can still
	// aggregate per-field change descriptions into one dbDebugLog line per
	// save, matching the original behavior. Pokemon is therefore 376 bytes
	// under dbdebug, not the 352 pinned here for production; that's expected
	// instrumentation overhead in a build that's never deployed at scale, so
	// this test only enforces the production number.
	//
	// PokemonData itself is never independently heap-allocated — it's
	// embedded in Pokemon and copied by value into []PokemonData
	// write-behind batches — so no size class applies to it directly; the
	// class boundary only matters for Pokemon.
	const (
		wantPokemonData = 256
		wantPokemon     = 352
	)

	if got := unsafe.Sizeof(PokemonData{}); got != wantPokemonData {
		t.Errorf("unsafe.Sizeof(PokemonData{}) = %d, want %d", got, wantPokemonData)
	}
	if dbDebugEnabled {
		// dbdebug build: pokemonDebugState carries a real 24-byte slice
		// header, so Pokemon is 24 bytes larger (376). See the comment above
		// wantPokemon for why this test doesn't pin the production number
		// here.
		if got := unsafe.Sizeof(Pokemon{}); got != wantPokemon+24 {
			t.Errorf("unsafe.Sizeof(Pokemon{}) [dbdebug build] = %d, want %d", got, wantPokemon+24)
		}
		return
	}
	if got := unsafe.Sizeof(Pokemon{}); got != wantPokemon {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want %d", got, wantPokemon)
	}
}

// TestPokemonUnderGCThreshold is the acceptance test for the packing work.
//
// Above 512 bytes Go's GC gets materially more expensive: measured across 5M
// live entries, 512 bytes with one pointer marks in 62.6ms and 520 bytes
// marks in 194.4ms. Pokemon is cached in the millions.
//
// If this fails, the entity has grown back over the line. Do not delete the
// test — find the field that pushed it over.
func TestPokemonUnderGCThreshold(t *testing.T) {
	if got := unsafe.Sizeof(Pokemon{}); got > gcSizeThreshold {
		t.Errorf("unsafe.Sizeof(Pokemon{}) = %d, want <= %d", got, gcSizeThreshold)
	}
}
