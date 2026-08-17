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
//
// THERE IS NO HEADROOM. Pokemon's 352-byte compiled size is EXACTLY a Go size
// class, so the allocator hands out 352 bytes (confirmed empirically: n=200000
// live *Pokemon, runtime.MemStats.TotalAlloc delta / n = 352.0 exactly). Every
// byte you add crosses into the next class, 384. A single bool costs 32 bytes
// per cached pokemon — roughly 160 MB at 5M, about half of what task 7 won by
// removing the embedded protobuf.
//
// So a field addition here is NOT free, and this is the opposite of the
// situation before task 7, when Pokemon sat mid-class at 392 with 24 bytes of
// slack. If you are here because the test failed, the question is not "what is
// the new number" — it is whether the field can be narrower, packed into an
// existing byte, or live somewhere other than the per-pokemon struct. Bump the
// constant only once you have decided the 32 bytes are worth paying, and say so
// in the paragraph below.
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
	// build the field's replacement, `debug debugChangeAccumulator`, is defined
	// zero-sized (see db_debug_off.go), so it costs nothing — the 392 is a
	// real, full 24-byte reduction in compiled struct size, not a rounding
	// artifact. It did NOT change the GC size class the allocator actually
	// handed out: 392 still rounded up to 416, same as before that task. The durable win is the removed
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
	// A dbdebug build (`-tags dbdebug`) keeps debugChangeAccumulator's real
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
		// dbdebug build: debugChangeAccumulator carries a real 24-byte slice
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

// TestClassMovedEntitySizes pins the four entities whose allocator size
// class actually moved when their `changedFields []string` field was
// replaced by the shared debugChangeAccumulator (a build-tag-split type,
// real accumulator under -tags dbdebug and zero-sized otherwise — see
// db_debug.go / db_debug_off.go and the `debug` field on each struct below).
//
// Eight entities got this same 24-byte-field-drop treatment; only these four
// crossed a Go size-class boundary in the production (non-dbdebug) build, so
// only these four get an allocator-visible win and only these four are
// pinned here. The other four (Pokestop, Gym, Route, Player) shrank their
// compiled struct size by the same 24 bytes but landed in the SAME size
// class as before — nothing for the allocator to hand out less of — so
// pinning them would just be nine constants to maintain for four real wins.
//
// Reading Pokestop/Gym/Route/Player as "no win" off Go's static size-class
// table (8, 16, 24, 32, ..., 1024, 1152, 1280, ...) applied to the compiled
// struct sizes is not by itself reliable for pointer-containing structs —
// only measuring is. On this toolchain (go1.26.5), a noscan (no pointer
// fields) 1152-byte control struct allocates in the 1152-byte class as the
// table says, but an otherwise-identical 1152-byte struct with one pointer
// field (scan, in GC terms) measures into 1280, not 1152: 1152 exists as a
// class for noscan objects but scan objects skip it. Every entity here
// contains pointers (strings, null.String, slices, ...), so all are scan
// objects. That's why Pokestop (1176->1152) and Player (1392->1368) don't
// move a class despite each dropping exactly 24 bytes and despite a naive
// table read suggesting the after-size (1152, itself a listed class) should
// drop one: 1152 isn't an available class for a scan object at this size.
// Confirmed stable across repeated measurement and with
// debug.SetGCPercent(-1) disabling concurrent GC during the window (to rule
// out mark-assist noise).
//
// Sizes measured with runtime.MemStats.TotalAlloc delta / n (n=100000 live
// pointers each, matching TestPokemonEntitySizes's methodology):
//
//	Station:    496 (class 512) -> 472 (class 480), 32 bytes/instance saved
//	Spawnpoint: 136 (class 144) -> 104 (class 112), 32 bytes/instance saved
//	Incident:   272 (class 288) -> 248 (class 256), 32 bytes/instance saved
//	Tappable:   224 (class 224) -> 200 (class 208), 16 bytes/instance saved
//
// Spawnpoint is the one that matters most here: millions of entries cached,
// same order of magnitude as Pokemon.
//
// This test only enforces the production (non-dbdebug) numbers, same
// carve-out as TestPokemonEntitySizes: a dbdebug build's real accumulator
// changes these compiled sizes (and isn't deployed at scale), so there is
// nothing to gain by also pinning that number here.
func TestClassMovedEntitySizes(t *testing.T) {
	if dbDebugEnabled {
		return
	}
	for name, tc := range map[string]struct {
		got, want uintptr
	}{
		"Station":    {unsafe.Sizeof(Station{}), 472},
		"Spawnpoint": {unsafe.Sizeof(Spawnpoint{}), 104},
		"Incident":   {unsafe.Sizeof(Incident{}), 248},
		"Tappable":   {unsafe.Sizeof(Tappable{}), 200},
	} {
		if tc.got != tc.want {
			t.Errorf("unsafe.Sizeof(%s{}) = %d, want %d", name, tc.got, tc.want)
		}
	}
}
