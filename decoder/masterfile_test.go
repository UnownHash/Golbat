package decoder

import (
	"testing"

	"golbat/pogo"
)

// TestMovePoolsForResolvesSpeciesLevelPools is the base case: no form
// override at all (form 0), so the resolved lists are exactly the species'
// own quick_moves/charged_moves.
func TestMovePoolsForResolvesSpeciesLevelPools(t *testing.T) {
	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{200, 201}, ChargedMoves: []int{100, 101, 102}},
		},
	}

	quick, charged, ok := movePoolsFor(data, 1, 0)
	if !ok {
		t.Fatal("expected pools to resolve for a known species with no form override")
	}
	if len(quick) != 2 || quick[0] != 200 || quick[1] != 201 {
		t.Errorf("quick = %v, want [200 201]", quick)
	}
	if len(charged) != 3 || charged[0] != 100 || charged[1] != 101 || charged[2] != 102 {
		t.Errorf("charged = %v, want [100 101 102]", charged)
	}
}

// TestMovePoolsForFormOverridesBothLists: a form entry carrying both
// quick_moves and charged_moves overrides the species' own lists entirely.
func TestMovePoolsForFormOverridesBothLists(t *testing.T) {
	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {
				QuickMoves:   []int{200, 201},
				ChargedMoves: []int{100, 101, 102},
				Forms: map[int]MasterFileForm{
					900: {QuickMoves: []int{210}, ChargedMoves: []int{110, 111}},
				},
			},
		},
	}

	quick, charged, ok := movePoolsFor(data, 1, 900)
	if !ok {
		t.Fatal("expected pools to resolve for a form overriding both lists")
	}
	if len(quick) != 1 || quick[0] != 210 {
		t.Errorf("quick = %v, want [210] (the form's own list)", quick)
	}
	if len(charged) != 2 || charged[0] != 110 || charged[1] != 111 {
		t.Errorf("charged = %v, want [110 111] (the form's own list)", charged)
	}
}

// TestMovePoolsForChargedOnlyFormFallsBackToSpeciesQuick mirrors a real
// masterfile entry: Hisuian Growlithe (species 58, form 2792) carries
// charged_moves but no quick_moves at all in the golbat masterfile. Its quick
// pool must come from the SPECIES, not from an empty/missing form list — a form fallback that treated "does the form
// have moves" as one combined signal (the way findBoostedWeathers checks
// Types as a single field) would instead resolve an empty quick pool here
// and report ok=false. Fails if movePoolsFor's two independent per-list
// checks are collapsed into one combined check.
func TestMovePoolsForChargedOnlyFormFallsBackToSpeciesQuick(t *testing.T) {
	const growlithe = 58
	const hisuianForm = 2792
	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			growlithe: {
				QuickMoves:   []int{209, 202},
				ChargedMoves: []int{21, 131, 24},
				Forms: map[int]MasterFileForm{
					hisuianForm: {ChargedMoves: []int{64, 279, 24}}, // no QuickMoves
				},
			},
		},
	}

	quick, charged, ok := movePoolsFor(data, growlithe, hisuianForm)
	if !ok {
		t.Fatal("expected pools to resolve for a charged-only form override")
	}
	if len(quick) != 2 || quick[0] != 209 || quick[1] != 202 {
		t.Errorf("quick = %v, want [209 202] (the SPECIES' own list, since the form has none)", quick)
	}
	if len(charged) != 3 || charged[0] != 64 || charged[1] != 279 || charged[2] != 24 {
		t.Errorf("charged = %v, want [64 279 24] (the form's own list)", charged)
	}
}

// TestMovePoolsForUnknownSpecies: a pokemonId not present in the masterfile
// at all must resolve to ok=false, not a guess.
func TestMovePoolsForUnknownSpecies(t *testing.T) {
	data := MasterFileData{Initialized: true, Pokemon: map[int]MasterFilePokemon{}}

	if _, _, ok := movePoolsFor(data, 9999, 0); ok {
		t.Fatal("expected ok=false for an unknown species")
	}
}

// TestMovePoolsForEmptyPool: a species present in the masterfile but with one
// pool never populated must resolve to ok=false rather than deriving against
// a zero-length pool, which encounter.Draw.MoveIndices would panic on.
func TestMovePoolsForEmptyPool(t *testing.T) {
	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{200}, ChargedMoves: nil}, // no charged moves at all
		},
	}

	if _, _, ok := movePoolsFor(data, 1, 0); ok {
		t.Fatal("expected ok=false when one pool is empty, even though the other resolved")
	}
}

// Smeargle (235) form 1307 ships with `"quick_moves": []` and
// `"charged_moves": []` in the live masterfile. An empty form list must count
// as absent, so each list falls back to the species' own and the pools
// resolve; taken literally, the form would have nothing to draw moves from.
//
// The second half pins the empty-pool guard: with no moves anywhere,
// movePoolsFor must report ok=false, since encounter.Draw.MoveIndices reaches
// javarandom.Int, which panics on a bound <= 0.
//
// The fixture mirrors that entry rather than loading the masterfile, which the
// decoder tests do not do.
func TestMovePoolsForEmptyFormListsFallBackToSpecies(t *testing.T) {
	const smeargle, emptyMovesForm = int16(235), int16(1307)

	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			int(smeargle): {
				QuickMoves:   []int{214},
				ChargedMoves: []int{90},
				Forms: map[int]MasterFileForm{
					int(emptyMovesForm): {QuickMoves: []int{}, ChargedMoves: []int{}},
				},
			},
		},
	}

	// The form's own lists are empty, so each falls back to the species level
	// independently and the pools do resolve — the guard must not fire here.
	quick, charged, ok := movePoolsFor(data, smeargle, emptyMovesForm)
	if !ok {
		t.Fatal("empty form lists must fall back to the species pools, not fail")
	}
	if len(quick) == 0 || len(charged) == 0 {
		t.Fatalf("fell back to empty pools: quick=%v charged=%v", quick, charged)
	}

	// A species with genuinely no moves anywhere is the crash case.
	bare := MasterFileData{
		Initialized: true,
		Pokemon:     map[int]MasterFilePokemon{int(smeargle): {}},
	}
	if _, _, ok := movePoolsFor(bare, smeargle, emptyMovesForm); ok {
		t.Fatal("no pools anywhere must report ok=false; javarandom.Int panics on bound 0")
	}
}

// The masterfile's bundled copy is embedded like the pool's, so a binary run
// from anywhere — a container image carries no pogo/ directory — still has
// one.
func TestBundledMasterFileParses(t *testing.T) {
	old := getMasterFileData()
	t.Cleanup(func() {
		masterFileMu.Lock()
		masterFileData = old
		masterFileMu.Unlock()
	})
	if err := loadMasterFileBytes(pogo.BundledMasterFile); err != nil {
		t.Fatalf("the masterfile embedded in the binary does not load: %v", err)
	}
	if _, _, ok := movePoolsFor(getMasterFileData(), int16(pogo.HoloPokemonId_DITTO), 0); !ok {
		t.Error("the bundled masterfile has no move pools for Ditto")
	}
}
