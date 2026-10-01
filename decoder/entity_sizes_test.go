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
// Pokemon compiles to 344 bytes, which the allocator rounds up to the
// 352-byte size class (measured: n=200000 live *Pokemon,
// runtime.MemStats.TotalAlloc delta / n = 352.0 exactly). That leaves 8 bytes
// of headroom; anything more crosses into the next class, 384, which costs 32
// bytes per cached pokemon — roughly 160 MB at 5M.
//
// If you are here because the test failed, the question is not "what is the
// new number" — it is whether the field can be narrower, packed into an
// existing byte, or live somewhere other than the per-pokemon struct. Bump the
// constant only once you have decided the cost is worth paying, and say so in
// the paragraph below.
func TestPokemonEntitySizes(t *testing.T) {
	// wantPokemonData: field payload sums to 244 bytes (8-byte group 56 +
	// 4-byte group 48 + 2-byte group 26 + 1-byte group 42, which includes
	// SeenType and the 17-byte FortId PokestopId, + pointer group 72:
	// Username/Pvp/GolbatInternal, each 8-byte aligned). Go's struct alignment
	// is 8 (driven by the uint64/float64/pointer fields), so the pointer group,
	// placed last for a tighter GC pointer-scan span, starts after 4 bytes of
	// mandatory padding, and the struct is 248. A hand-computed estimate is not
	// to be trusted over running this test.
	//
	// wantPokemon: PokemonData plus the cache-side fields — scanHistory (a
	// 24-byte slice header), PokemonOldValues (32 bytes, for webhook and stats
	// comparison) and the rest — is 344. In a production (non-dbdebug) build
	// the `debug debugChangeAccumulator` field is zero-sized (see
	// db_debug_off.go), so it costs nothing.
	//
	// PokestopId is a FortId (an inline [16]byte + uint8) rather than a string,
	// so a pokemon at a pokestop holds no second heap object for the string's
	// backing bytes; that saving is invisible to unsafe.Sizeof.
	//
	// A dbdebug build (`-tags dbdebug`) keeps debugChangeAccumulator's real
	// `[]string` accumulator (24 bytes, see db_debug.go) so it can still
	// aggregate per-field change descriptions into one dbDebugLog line per
	// save. Pokemon is therefore 368 bytes
	// under dbdebug, not the 344 pinned here for production; that's expected
	// instrumentation overhead in a build that's never deployed at scale, so
	// this test only enforces the production number.
	//
	// PokemonData itself is never independently heap-allocated — it's
	// embedded in Pokemon and copied by value into []PokemonData
	// write-behind batches — so no size class applies to it directly; the
	// class boundary only matters for Pokemon.
	const (
		wantPokemonData = 248
		wantPokemon     = 344
	)

	if got := unsafe.Sizeof(PokemonData{}); got != wantPokemonData {
		t.Errorf("unsafe.Sizeof(PokemonData{}) = %d, want %d", got, wantPokemonData)
	}
	if dbDebugEnabled {
		// dbdebug build: debugChangeAccumulator carries a real 24-byte slice
		// header, so Pokemon is 24 bytes larger (368). See the comment above
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

// TestClassMovedEntitySizes pins four entities that sit close enough to a
// Go size-class boundary that a small field change moves their allocation.
// Each struct carries a `debug debugChangeAccumulator` field (a
// build-tag-split type, a real accumulator under -tags dbdebug and
// zero-sized otherwise — see db_debug.go / db_debug_off.go), which is free in
// a production build.
//
// Pokestop, Gym, Route and Player are not pinned: a 24-byte change in their
// compiled size was measured not to move their size class. Note
// that Go's static size-class table (8, 16, 24, 32, ..., 1024, 1152, 1280,
// ...) is not by itself reliable for pointer-containing structs — only
// measuring is. On go1.26.5 a noscan (no pointer fields) 1152-byte struct
// allocates in the 1152-byte class, but an otherwise-identical struct with
// one pointer field measures into 1280: 1152 exists as a class for noscan
// objects but scan objects skip it. Every entity here contains pointers, so
// all are scan objects. This was confirmed stable across repeated
// measurement and with debug.SetGCPercent(-1) disabling concurrent GC during
// the window.
//
// Allocated sizes, measured with runtime.MemStats.TotalAlloc delta / n
// (n=100000 live pointers each, matching TestPokemonEntitySizes):
//
//	Station:    class 480
//	Spawnpoint: class 112
//	Incident:   class 256
//	Tappable:   class 208
//
// Spawnpoint is the one that matters most here: millions of entries cached,
// same order of magnitude as Pokemon.
//
// Tappable is 208 bytes including its in-memory `firstSeen int64` field (see
// Tappable.firstSeen). Without the field it is 200 bytes, which allocates in
// the same size class 208, so the field costs nothing per instance.
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
		// Station grew 472->480 when StationData.Id became FortId (a 17-byte
		// value vs. string's 16-byte header, padded to the next 8-byte
		// boundary). Still class 480 either way — no allocator regression,
		// the win documented above is intact.
		"Station":    {unsafe.Sizeof(Station{}), 480},
		"Spawnpoint": {unsafe.Sizeof(Spawnpoint{}), 104},
		// Incident grew 248->256 when IncidentData.PokestopId became FortId
		// (same 17-vs-16-byte story as Station above). 256 is itself an
		// exact Go size class, so both 248 and 256 round up to class 256 —
		// still no allocator regression.
		"Incident": {unsafe.Sizeof(Incident{}), 256},
		// Tappable grew 200->264 (class 208->288) for RewardType,
		// RewardPokemonId and Rewards, then 264->272 (still class 288) for
		// the in-memory firstSeen used by the despawn wrap clamp.
		// Tappables are short-lived and few; accepted.
		"Tappable": {unsafe.Sizeof(Tappable{}), 272},
	} {
		if tc.got != tc.want {
			t.Errorf("unsafe.Sizeof(%s{}) = %d, want %d", name, tc.got, tc.want)
		}
	}
}
