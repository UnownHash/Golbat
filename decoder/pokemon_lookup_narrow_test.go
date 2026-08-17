package decoder

import (
	"math"
	"testing"
	"unsafe"

	"github.com/guregu/null/v6"
)

// TestPokemonLookupSizes pins the scan-filter structs' layout. PokemonLookup
// is loaded once per candidate on every scan — 15-20M/s in production
// profiles, 14% of CPU (see PokemonLookupCacheItem's doc comment) — and it is
// stored inline in the lookup map, so its width is a direct multiplier on
// that path's cache pressure. Widening a field here is not free; if this test
// fails, that is the change to justify, not the number to update.
//
// PokemonLookup carries two dead bytes today: Xxs and Xxl are neither written
// by updatePokemonLookup nor read by any filter (the XXS/XXL filters read
// Size instead — api_pokemon_scan_v1.go). They are pinned as-is rather than
// removed here because removing them is a separate change.
func TestPokemonLookupSizes(t *testing.T) {
	for name, tc := range map[string]struct {
		got, want uintptr
	}{
		"PokemonLookup":          {unsafe.Sizeof(PokemonLookup{}), 18},
		"PokemonPvpLookup":       {unsafe.Sizeof(PokemonPvpLookup{}), 6},
		"PokemonLookupCacheItem": {unsafe.Sizeof(PokemonLookupCacheItem{}), 26},
	} {
		if tc.got != tc.want {
			t.Errorf("unsafe.Sizeof(%s{}) = %d, want %d", name, tc.got, tc.want)
		}
	}
}

// TestLookupNarrowingNeverAliasesTheSentinel is the regression test for the
// bug these helpers exist for: a stored value at its column's ceiling used to
// be converted straight into the lookup's signed slot, and int8(255) is -1 —
// the "no value" sentinel every DNF filter checks against. A pokemon with a
// clamped field silently dropped out of every filtered scan.
//
// The ceiling is only the loudest case. Everything from 128 (or 32768) up
// converted to some negative number and failed every `>= Min` comparison, so
// the assertions below sweep the whole aliasing range, not just the ceiling.
func TestLookupNarrowingNeverAliasesTheSentinel(t *testing.T) {
	for _, v := range []uint8{128, 200, 254, math.MaxUint8} {
		if got := lookupInt8(null.ValueFrom(v)); got != math.MaxInt8 {
			t.Errorf("lookupInt8(%d) = %d, want %d (saturated, never negative)", v, got, math.MaxInt8)
		}
	}
	for _, v := range []uint16{32768, 60000, math.MaxUint16} {
		if got := lookupInt16(null.ValueFrom(v)); got != math.MaxInt16 {
			t.Errorf("lookupInt16(%d) = %d, want %d (saturated, never negative)", v, got, math.MaxInt16)
		}
	}

	// In-range values are untouched, and absence is still -1: the sentinel
	// has to keep working, it is only the collision that is being removed.
	if got := lookupInt8(null.ValueFrom(uint8(15))); got != 15 {
		t.Errorf("lookupInt8(15) = %d, want 15", got)
	}
	if got := lookupInt16(null.ValueFrom(uint16(3000))); got != 3000 {
		t.Errorf("lookupInt16(3000) = %d, want 3000", got)
	}
	if got := lookupInt8(null.Value[uint8]{}); got != -1 {
		t.Errorf("lookupInt8(invalid) = %d, want -1", got)
	}
	if got := lookupInt16(null.Value[uint16]{}); got != -1 {
		t.Errorf("lookupInt16(invalid) = %d, want -1", got)
	}
}

// TestLookupFormNeverAliasesTheWildcardKey covers the one field whose -1 is
// not an absence marker. api_pokemon_common.go looks a pokemon's filters up
// by {pokemonId, form}, then falls back to {pokemonId, -1} and {-1, -1} — so
// a form converted to -1 does not read as "no form", it silently picks up the
// wildcard filter set for that pokemon id. Absent form stays 0, matching the
// column, and an out-of-range one gets a key of its own.
func TestLookupFormNeverAliasesTheWildcardKey(t *testing.T) {
	if got := lookupForm(null.Value[uint16]{}); got != 0 {
		t.Errorf("lookupForm(invalid) = %d, want 0 (form has no absent sentinel)", got)
	}
	for _, v := range []uint16{32768, math.MaxUint16} {
		got := lookupForm(null.ValueFrom(v))
		if got == -1 {
			t.Errorf("lookupForm(%d) = -1, the wildcard-form filter key", v)
		}
		if got != math.MaxInt16 {
			t.Errorf("lookupForm(%d) = %d, want %d", v, got, math.MaxInt16)
		}
	}
	if got := lookupForm(null.ValueFrom(uint16(952))); got != 952 {
		t.Errorf("lookupForm(952) = %d, want 952", got)
	}
}

// TestLookupIvHandlesLegacyOutOfRangePercentages covers the field the review's
// table missed and the only one with a trigger that does not need a protocol
// change. iv is float(5,2) unsigned, so the column holds up to 999.99: a row
// written before clampIv capped each stat at 15 can carry iv = 566.67 from a
// single stat stored at the tinyint's 255. int8(566) is 54 and int8(384) is
// -128, so the old conversion turned a perfect-looking row into a low or
// absent one.
func TestLookupIvHandlesLegacyOutOfRangePercentages(t *testing.T) {
	for _, v := range []float32{128, 200, 384, 566.67, 999.99} {
		if got := lookupIv(null.ValueFrom(v)); got != math.MaxInt8 {
			t.Errorf("lookupIv(%v) = %d, want %d (saturated)", v, got, math.MaxInt8)
		}
	}
	if got := lookupIv(null.Value[float32]{}); got != -1 {
		t.Errorf("lookupIv(invalid) = %d, want -1", got)
	}
	// Still floors rather than rounds, as before.
	if got := lookupIv(null.ValueFrom(float32(97.7))); got != 97 {
		t.Errorf("lookupIv(97.7) = %d, want 97", got)
	}
	if got := lookupIv(null.ValueFrom(float32(0))); got != 0 {
		t.Errorf("lookupIv(0) = %d, want 0", got)
	}
	if got := lookupIv(null.ValueFrom(float32(100))); got != 100 {
		t.Errorf("lookupIv(100) = %d, want 100", got)
	}
}

// TestUpdatePokemonLookupSaturatesClampedFields walks the real path rather
// than the helpers: a pokemon whose every narrowed field was clamped at its
// column ceiling must still be filterable. Before this fix its Level, Gender
// and Size read as -1 (absent), its Cp as -1, and its Form landed on the
// wildcard key.
func TestUpdatePokemonLookupSaturatesClampedFields(t *testing.T) {
	p := &Pokemon{}
	p.Id = 0xF00D
	p.PokemonId = 25
	p.SetLevel(null.IntFrom(math.MaxUint8))
	p.SetGender(null.IntFrom(math.MaxUint8))
	p.SetSize(null.IntFrom(math.MaxUint8))
	p.SetWeather(null.IntFrom(math.MaxUint8))
	p.SetCp(null.IntFrom(math.MaxUint16))
	p.SetForm(null.IntFrom(math.MaxUint16))
	p.SetIv(null.FloatFrom(566.67))

	t.Cleanup(func() { pokemonLookupCache.Delete(uint64(p.Id)) })
	updatePokemonLookup(p, false, nil)

	item, ok := pokemonLookupCache.Load(uint64(p.Id))
	if !ok {
		t.Fatal("updatePokemonLookup stored no lookup entry")
	}
	lookup := item.PokemonLookup
	for name, got := range map[string]int16{
		"Level":   int16(lookup.Level),
		"Gender":  int16(lookup.Gender),
		"Size":    int16(lookup.Size),
		"Weather": int16(lookup.Weather),
		"Iv":      int16(lookup.Iv),
	} {
		if got < 0 {
			t.Errorf("PokemonLookup.%s = %d, want non-negative (a clamped value must not read as absent)", name, got)
		}
	}
	if lookup.Cp < 0 {
		t.Errorf("PokemonLookup.Cp = %d, want non-negative", lookup.Cp)
	}
	if lookup.Form == -1 {
		t.Error("PokemonLookup.Form = -1, the wildcard-form filter key")
	}
}
