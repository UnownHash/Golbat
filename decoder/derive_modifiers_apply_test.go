package decoder

import (
	"context"
	"testing"

	"golbat/db"
	"golbat/derive/encounter"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
)

const (
	adjEid = uint64(8429844873227918432)
	// London and New York: two cells in two timezones.
	adjLatA, adjLonA = 51.5007, -0.1246
	adjLatB, adjLonB = 40.7128, -74.0060
	adjZoneA         = "Europe/London"
	adjZoneWide      = 3
)

var (
	adjCellA = modifierCell(0, adjLatA, adjLonA)
	adjCellB = modifierCell(0, adjLatB, adjLonB)
	// adjCellFar is ~20 km north of adjCellA, under another level-10 cell.
	adjCellFar = modifierCell(0, adjLatA+0.2, adjLonA)
)

// withModifierIndex publishes an index built from mods for one test.
func withModifierIndex(t *testing.T, mods ...*derivationModifier) *modifierIndex {
	t.Helper()
	idx := buildModifierIndex(mods, adjZoneWide)
	derivationModifierIndex.Store(idx)
	t.Cleanup(func() { derivationModifierIndex.Store(nil) })
	return idx
}

func cellSet(cells ...s2.CellID) map[s2.CellID]struct{} {
	m := make(map[s2.CellID]struct{}, len(cells))
	for _, c := range cells {
		m[c] = struct{}{}
	}
	return m
}

// zoneWideIn is a set of in-cells large enough, and spread under two
// level-10 cells, to make a modifier zone-wide (with withSpread).
func zoneWideIn() map[s2.CellID]struct{} {
	return cellSet(adjCellA, adjCellA.Next(), adjCellFar)
}

// withSpread sets m's level-10 spread from its in-cells, as the learner's
// addInCell keeps it.
func withSpread(m *derivationModifier) *derivationModifier {
	for c := range m.InCells {
		m.addInCell(c)
	}
	return m
}

func levelMod(scope modifierScope, delta int32, zone string, in map[s2.CellID]struct{}) *derivationModifier {
	return withSpread(&derivationModifier{Type: modLevelOffset, Scope: scope, Effect: modifierEffect{Delta: delta},
		Zone: zone, Status: modConfirmed, InCells: in, Outside: map[s2.CellID]struct{}{}})
}

// adjRecord is a wild record of species 1 at lat/lon, strong or not.
func adjRecord(species int16, lat, lon float64, strong bool) *Pokemon {
	p := &Pokemon{}
	p.Id = Uint64Str(adjEid)
	p.PokemonId = species
	p.Lat, p.Lon = lat, lon
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetSeenType(SeenTypeCodeWild)
	p.SetIsStrong(null.BoolFrom(strong))
	return p
}

func TestDerivationAdjustLevelOffset(t *testing.T) {
	withDeriveIv(t, true)
	withDeriveStrong(t, true)
	withModifierIndex(t, levelMod(modifierScope{PokemonID: 1, Strong: true}, 5, adjZoneA, zoneWideIn()))
	base := encounter.Derive(adjEid, false, true, 0)

	in := adjRecord(1, adjLatA, adjLonA, true)
	in.deriveIvIfAbsent()
	if got := int32(in.Level.ValueOrZero()); got != base.Level+5 {
		t.Errorf("in-zone level = %d, want base %d + 5", got, base.Level)
	}
	if got := int32(in.AtkIv.ValueOrZero()); got != base.Attack {
		t.Errorf("in-zone atk = %d, want base %d", got, base.Attack)
	}

	out := adjRecord(1, adjLatB, adjLonB, true)
	out.deriveIvIfAbsent()
	if got := int32(out.Level.ValueOrZero()); got != base.Level {
		t.Errorf("other-zone level = %d, want base %d", got, base.Level)
	}
}

// A confirmed all-strong +5 is detected and listed but never applied: a
// strong species at base levels kept getting +5 (design §7). It still gates
// sightings of strong scopes.
func TestDerivationAdjustAllStrongNotApplied(t *testing.T) {
	withDeriveIv(t, true)
	withDeriveStrong(t, true)
	idx := withModifierIndex(t, levelMod(allStrongScope, 5, adjZoneA, zoneWideIn()))
	base := encounter.Derive(adjEid, false, true, 0)

	p := adjRecord(1, adjLatA, adjLonA, true)
	p.deriveIvIfAbsent()
	if got := int32(p.Level.ValueOrZero()); !p.Level.Valid || got != base.Level {
		t.Errorf("strong record level = %v, want base %d (all-strong not applied)", p.Level, base.Level)
	}
	if adj := idx.lookup(1, 0, true, adjCellA, modifierZoneOf); adj.LevelDelta != 0 || adj.Suppress {
		t.Errorf("lookup = %+v, want zero", adj)
	}
	if !idx.wantsSighting(1, 0, true) {
		t.Error("all-strong confirmed: strong sighting not wanted")
	}
}

func TestDerivationAdjustSpeciesWinsOverAllStrong(t *testing.T) {
	withDeriveIv(t, true)
	withDeriveStrong(t, true)
	withModifierIndex(t,
		levelMod(allStrongScope, 3, adjZoneA, zoneWideIn()),
		levelMod(modifierScope{PokemonID: 1, Strong: true}, 5, adjZoneA, zoneWideIn()))
	base := encounter.Derive(adjEid, false, true, 0)

	p := adjRecord(1, adjLatA, adjLonA, true)
	p.deriveIvIfAbsent()
	if got := int32(p.Level.ValueOrZero()); got != base.Level+5 {
		t.Errorf("level = %d, want base %d + 5 (species applies, all-strong never)", got, base.Level)
	}
}

// pinMoveData is a masterfile in which species 1 has a quick and charged
// pool.
func pinMoveData(t *testing.T) {
	t.Helper()
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{214, 215}, ChargedMoves: []int{90, 91, 92}},
		},
	})
}

func pinMod(zone string, slot string, move int32) *derivationModifier {
	return &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 1},
		Effect: modifierEffect{Slot: slot, Move: move}, Zone: zone, Status: modConfirmed,
		InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}}
}

// Two confirmed charged pins covering one cell: neither applies and the
// slot is left absent, not derived (base is likely wrong too); the quick
// slot and the IVs are derived as usual.
func TestDerivationAdjustConflictingPinsWithheld(t *testing.T) {
	withDeriveIv(t, true)
	pinMoveData(t)
	withModifierIndex(t, pinMod(adjZoneA, modSlotCharged, 494), pinMod("", modSlotCharged, 300))
	draw := encounter.Derive(adjEid, false, false, 0)
	want := derivedMoves(&draw, 1, 0)

	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Move2.Valid {
		t.Errorf("Move2 = %v, want absent (disputed pin)", p.Move2)
	}
	if int32(p.Move1.ValueOrZero()) != want.Move1 || !p.AtkIv.Valid {
		t.Errorf("Move1 = %v atk = %v, want derived %d and IVs", p.Move1, p.AtkIv, want.Move1)
	}

	settled := adjRecord(1, adjLatA, adjLonA, false)
	settled.calculateIv(1, 2, 3)
	settled.deriveSizeAndMovesIfAbsent()
	if settled.Move2.Valid || !settled.Move1.Valid {
		t.Errorf("settled IVs: moves %v/%v, want derived quick, absent charged", settled.Move1, settled.Move2)
	}

	// Pins in different slots do not conflict.
	withModifierIndex(t, pinMod(adjZoneA, modSlotCharged, 494), pinMod("", modSlotQuick, 300))
	both := adjRecord(1, adjLatA, adjLonA, false)
	both.deriveIvIfAbsent()
	if both.Move1.ValueOrZero() != 300 || both.Move2.ValueOrZero() != 494 {
		t.Errorf("moves %v/%v, want pinned 300/494", both.Move1, both.Move2)
	}
}

// Two confirmed level deltas covering one record: no derived IVs or level
// (as unexplained).
func TestDerivationAdjustConflictingLevelsSuppress(t *testing.T) {
	withDeriveIv(t, true)
	idx := withModifierIndex(t,
		levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)),
		levelMod(modifierScope{PokemonID: 1}, 3, "", cellSet(adjCellA)))
	if adj := idx.lookup(1, 0, false, adjCellA, modifierZoneOf); !adj.Suppress {
		t.Errorf("lookup = %+v, want suppress", adj)
	}
	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.AtkIv.Valid || p.Level.Valid {
		t.Errorf("disputed level: atk=%v level=%v, want absent", p.AtkIv, p.Level)
	}
}

// Two confirmed size modifiers covering one record (possible only across
// scopes or zones): size absent, not base.
func TestDerivationAdjustConflictingSizeWithheld(t *testing.T) {
	withDeriveIv(t, true)
	a := sizeMod(map[string]sizeBoundary{"1/2": {Lo: 0.01, Hi: 0.02, HasLo: true, HasHi: true}})
	b := sizeMod(map[string]sizeBoundary{"1/2": {Lo: 0.01, Hi: 0.02, HasLo: true, HasHi: true}})
	b.Zone = adjZoneA
	idx := withModifierIndex(t, a, b)
	if adj := idx.lookup(1, 0, false, adjCellA, modifierZoneOf); !adj.WithholdSize || adj.Size != nil {
		t.Errorf("lookup = %+v, want size withheld", adj)
	}
	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Size.Valid || !p.AtkIv.Valid {
		t.Errorf("disputed size: size=%v atk=%v, want absent size, derived IVs", p.Size, p.AtkIv)
	}
}

func TestDerivationAdjustMovePin(t *testing.T) {
	withDeriveIv(t, true)
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{214, 215}, ChargedMoves: []int{90, 91, 92}},
		},
	})
	withModifierIndex(t, &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 1},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Status: modConfirmed,
		InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}})
	draw := encounter.Derive(adjEid, false, false, 0)
	want := derivedMoves(&draw, 1, 0)

	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.Move2.ValueOrZero() != 494 {
		t.Errorf("Move2 = %v, want pinned 494", p.Move2)
	}
	if int32(p.Move1.ValueOrZero()) != want.Move1 {
		t.Errorf("Move1 = %v, want derived %d", p.Move1, want.Move1)
	}

	observed := adjRecord(1, adjLatA, adjLonA, false)
	observed.SetMove2(null.IntFrom(90))
	observed.deriveIvIfAbsent()
	if observed.Move2.ValueOrZero() != 90 {
		t.Errorf("observed Move2 = %v, want kept 90", observed.Move2)
	}
}

func TestDerivationAdjustSizeShift(t *testing.T) {
	adj := derivationAdjust{Size: map[string]sizeBoundary{
		"1/2": {Lo: 0.0119, Hi: 0.0121, HasLo: true, HasHi: true},
		"2/3": {Lo: 0.0369, Hi: 0.0371, HasLo: true, HasHi: true},
	}}
	for _, tc := range []struct {
		roll float64
		want int32
		ok   bool
	}{{0.008, 1, true}, {0.03, 2, true}, {0.012, 0, false}, {0.5, 3, true}} {
		got, ok := adj.sizeClass(tc.roll, false, 0, 3)
		if got != tc.want || ok != tc.ok {
			t.Errorf("sizeClass(%v) = (%d, %v), want (%d, %v)", tc.roll, got, ok, tc.want, tc.ok)
		}
	}
	// A strong roll: only "4/5" exists.
	strong := derivationAdjust{Size: map[string]sizeBoundary{"4/5": {Lo: 0.6, Hi: 0.61, HasLo: true, HasHi: true}}}
	if got, ok := strong.sizeClass(0.7, true, 0, 4); got != 5 || !ok {
		t.Errorf("strong sizeClass(0.7) = (%d, %v), want (5, true)", got, ok)
	}
	if got, ok := strong.sizeClass(0.5, true, 0, 4); got != 4 || !ok {
		t.Errorf("strong sizeClass(0.5) = (%d, %v), want (4, true)", got, ok)
	}
}

func TestDerivationAdjustSizeFixedForm(t *testing.T) {
	var fixed int32
	for f := int32(1); f < 5000; f++ {
		if encounter.FormFixesSize(f) {
			fixed = f
			break
		}
	}
	if fixed == 0 {
		t.Skip("no fixed-size form")
	}
	adj := derivationAdjust{Size: map[string]sizeBoundary{"1/2": {Lo: 0.0119, Hi: 0.0121, HasLo: true, HasHi: true}}}
	if got, ok := adj.sizeClass(0.012, false, fixed, 4); got != 4 || !ok {
		t.Errorf("fixed form sizeClass = (%d, %v), want base (4, true)", got, ok)
	}
}

func TestDerivationAdjustUnexplainedSuppresses(t *testing.T) {
	withDeriveIv(t, true)
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			1: {QuickMoves: []int{214, 215}, ChargedMoves: []int{90, 91, 92}},
		},
	})
	withModifierIndex(t, &derivationModifier{Type: modUnexplained, Scope: modifierScope{PokemonID: 1},
		Status: modConfirmed, InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}})

	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if p.AtkIv.Valid || p.Level.Valid || p.Size.Valid || p.Move1.Valid || p.Move2.Valid {
		t.Errorf("unexplained wrote values: atk=%v level=%v size=%v moves=%v/%v",
			p.AtkIv, p.Level, p.Size, p.Move1, p.Move2)
	}

	settled := adjRecord(1, adjLatA, adjLonA, false)
	settled.calculateIv(1, 2, 3)
	settled.deriveSizeAndMovesIfAbsent()
	if settled.Size.Valid || settled.Move1.Valid || settled.Move2.Valid {
		t.Errorf("unexplained wrote size/moves: size=%v moves=%v/%v", settled.Size, settled.Move1, settled.Move2)
	}
}

func TestDerivationAdjustCandidateIgnored(t *testing.T) {
	withDeriveIv(t, true)
	m := levelMod(modifierScope{PokemonID: 1}, 5, "", cellSet(adjCellA))
	m.Status = modCandidate
	idx := withModifierIndex(t, m)
	if adj := idx.lookup(1, 0, false, adjCellA, modifierZoneOf); adj.LevelDelta != 0 {
		t.Errorf("candidate applied: %+v", adj)
	}
	base := encounter.Derive(adjEid, false, false, 0)
	p := adjRecord(1, adjLatA, adjLonA, false)
	p.deriveIvIfAbsent()
	if got := int32(p.Level.ValueOrZero()); got != base.Level {
		t.Errorf("level = %d, want base %d", got, base.Level)
	}
}

func TestDerivationAdjustLocalRegion(t *testing.T) {
	withDeriveIv(t, true)
	withModifierIndex(t, levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)))
	base := encounter.Derive(adjEid, false, false, 0)

	in := adjRecord(1, adjLatA, adjLonA, false)
	in.deriveIvIfAbsent()
	if got := int32(in.Level.ValueOrZero()); got != base.Level+5 {
		t.Errorf("cellA level = %d, want base %d + 5", got, base.Level)
	}

	// The level-15 map cell takes precedence over lat/lon.
	byCell := adjRecord(1, adjLatB, adjLonB, false)
	byCell.CellId = null.ValueFrom(int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(adjLatA, adjLonA)).Parent(15)))
	if got := pokemonModifierCell(byCell); got != adjCellA {
		t.Errorf("pokemonModifierCell = %v, want %v", got, adjCellA)
	}

	out := adjRecord(1, adjLatB, adjLonB, false)
	out.deriveIvIfAbsent()
	if got := int32(out.Level.ValueOrZero()); got != base.Level {
		t.Errorf("cellB level = %d, want base %d", got, base.Level)
	}

	nowhere := adjRecord(1, 0, 0, false)
	if got := pokemonModifierCell(nowhere); got != 0 {
		t.Fatalf("pokemonModifierCell(0,0) = %v, want 0", got)
	}
	nowhere.deriveIvIfAbsent()
	if got := int32(nowhere.Level.ValueOrZero()); got != base.Level {
		t.Errorf("cell-0 level = %d, want base %d", got, base.Level)
	}
}

// TestDerivationAdjustZoneUnknownNotZoneWide: a modifier in the unknown
// zone "" is never zone-wide (design §7), so it covers neither cell 0 nor a
// cell outside its in-cells.
func TestDerivationAdjustZoneUnknownNotZoneWide(t *testing.T) {
	idx := buildModifierIndex([]*derivationModifier{levelMod(modifierScope{PokemonID: 1}, 5, "", zoneWideIn())}, adjZoneWide)
	if adj := idx.lookup(1, 0, false, 0, modifierZoneOf); adj.LevelDelta != 0 {
		t.Errorf("zone \"\" modifier for cell 0: delta %d, want 0", adj.LevelDelta)
	}
}

func TestDerivationAdjustSkipsDitto(t *testing.T) {
	withDeriveIv(t, true)
	withModifierIndex(t, levelMod(modifierScope{PokemonID: 132}, 5, adjZoneA, cellSet(adjCellA)))

	p := adjRecord(132, adjLatA, adjLonA, false)
	p.IsDitto = true
	p.DisplayPokemonId = null.ValueFrom(uint16(16))
	if adj := p.modifierAdjust(); adj.LevelDelta != 0 || adj.Suppress || adj.Size != nil || adj.Quick != 0 || adj.Charged != 0 {
		t.Errorf("Ditto modifierAdjust = %+v, want zero", adj)
	}
	base := encounter.Derive(adjEid, p.statsBoosted(), false, 0)
	p.deriveIvIfAbsent()
	if got := int32(p.Level.ValueOrZero()); got != base.Level {
		t.Errorf("Ditto level = %d, want base %d", got, base.Level)
	}
}

func TestDerivationAdjustLazyZone(t *testing.T) {
	calls := 0
	zoneOf := func(s2.CellID) string { calls++; return adjZoneA }

	local := buildModifierIndex([]*derivationModifier{
		levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA))}, adjZoneWide)
	if adj := local.lookup(1, 0, false, adjCellA, zoneOf); adj.LevelDelta != 5 {
		t.Errorf("local in-cell delta = %d, want 5", adj.LevelDelta)
	}
	local.lookup(2, 0, false, adjCellA, zoneOf)
	if calls != 0 {
		t.Errorf("zoneOf called %d times for local/non-matching lookups, want 0", calls)
	}

	wide := buildModifierIndex([]*derivationModifier{
		levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, zoneWideIn()),
		withSpread(&derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 1}, Effect: modifierEffect{Slot: modSlotQuick, Move: 7},
			Zone: adjZoneA, Status: modConfirmed, InCells: zoneWideIn(), Outside: map[s2.CellID]struct{}{}}),
	}, adjZoneWide)
	adj := wide.lookup(1, 0, false, adjCellB, zoneOf)
	if calls != 1 || adj.LevelDelta != 5 || adj.Quick != 7 {
		t.Errorf("zone-wide lookup: calls %d, adj %+v; want 1 call, delta 5, quick 7", calls, adj)
	}
	wide.lookup(1, 0, false, adjCellB, zoneOf)
	if calls != 2 {
		t.Errorf("zoneOf calls = %d after two lookups, want 2", calls)
	}
}

func TestDerivationAdjustNoAllocs(t *testing.T) {
	p := adjRecord(2, adjLatA, adjLonA, false)
	derivationModifierIndex.Store(nil)
	if n := testing.AllocsPerRun(100, func() { _ = p.modifierAdjust() }); n != 0 {
		t.Errorf("nil index: %v allocs, want 0", n)
	}
	withModifierIndex(t, levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)))
	if n := testing.AllocsPerRun(100, func() { _ = p.modifierAdjust() }); n != 0 {
		t.Errorf("non-matching index: %v allocs, want 0", n)
	}
	// Withholding disputed values allocates nothing either.
	q := adjRecord(1, adjLatA, adjLonA, false)
	withModifierIndex(t,
		pinMod(adjZoneA, modSlotCharged, 494), pinMod("", modSlotCharged, 300),
		levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)),
		levelMod(modifierScope{PokemonID: 1}, 3, "", cellSet(adjCellA)))
	if q.modifierAdjust().Suppress != true {
		t.Fatal("conflicting index: want suppress")
	}
	if n := testing.AllocsPerRun(100, func() { _ = q.modifierAdjust() }); n != 0 {
		t.Errorf("conflicting index: %v allocs, want 0", n)
	}
	withModifierIndex(t, pinMod(adjZoneA, modSlotCharged, 494), pinMod("", modSlotCharged, 300))
	if !q.modifierAdjust().WithholdCharged {
		t.Fatal("conflicting pins: want charged withheld")
	}
	if n := testing.AllocsPerRun(100, func() { _ = q.modifierAdjust() }); n != 0 {
		t.Errorf("conflicting pins: %v allocs, want 0", n)
	}
}

func TestModifierIndexWantsSighting(t *testing.T) {
	var nilIdx *modifierIndex
	if nilIdx.wantsSighting(959, 3272, true) {
		t.Error("nil index wants a sighting")
	}
	pin := &derivationModifier{Type: modMovePin, Scope: modifierScope{PokemonID: 959, Form: 3272, Strong: true},
		Effect: modifierEffect{Slot: modSlotCharged, Move: 494}, Status: modCandidate,
		InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}}
	idx := buildModifierIndex([]*derivationModifier{pin}, adjZoneWide)
	if !idx.wantsSighting(959, 3272, true) {
		t.Error("candidate scope not wanted")
	}
	if idx.wantsSighting(959, 3272, false) || idx.wantsSighting(635, 0, true) || idx.wantsSighting(635, 0, false) {
		t.Error("unrelated scope wanted")
	}
	all := levelMod(allStrongScope, 5, adjZoneA, cellSet(adjCellA))
	all.Status = modCandidate
	idx = buildModifierIndex([]*derivationModifier{all}, adjZoneWide)
	if !idx.wantsSighting(635, 0, true) || idx.wantsSighting(635, 0, false) {
		t.Error("all-strong candidate: want any strong scope, no ordinary one")
	}
	if buildModifierIndex(nil, adjZoneWide) != nil {
		t.Error("empty mods built a non-nil index")
	}
}

// sizeMod is a confirmed size_cutoffs modifier for species 1 local to cellA.
func sizeMod(b map[string]sizeBoundary) *derivationModifier {
	return &derivationModifier{Type: modSizeCutoffs, Scope: modifierScope{PokemonID: 1}, Boundaries: b,
		Status: modConfirmed, InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}}
}

// Size through deriveIvIfAbsent: adjEid's unboosted roll is ~0.733 (base
// class 3). A "3/4" interval containing it leaves size absent; a "2/3" cut
// moved above it gives class 2.
func TestDerivationAdjustSizeThroughDerive(t *testing.T) {
	withDeriveIv(t, true)
	base := encounter.For(adjEid, false, 0)
	r := base.SizeRoll
	if base.Size != 3 || r <= 0.05 || r >= 0.95 {
		t.Fatalf("adjEid roll %v class %d: test assumes a mid-range class-3 roll", r, base.Size)
	}

	withModifierIndex(t, sizeMod(map[string]sizeBoundary{
		"3/4": {Lo: r - 0.01, Hi: r + 0.01, HasLo: true, HasHi: true}}))
	inside := adjRecord(1, adjLatA, adjLonA, false)
	inside.deriveIvIfAbsent()
	if inside.Size.Valid {
		t.Errorf("roll inside the moved interval: Size = %v, want absent", inside.Size)
	}
	if !inside.AtkIv.Valid {
		t.Error("IVs not derived alongside an absent size")
	}

	withModifierIndex(t, sizeMod(map[string]sizeBoundary{
		"2/3": {Lo: r + 0.01, Hi: r + 0.02, HasLo: true, HasHi: true}}))
	below := adjRecord(1, adjLatA, adjLonA, false)
	below.deriveIvIfAbsent()
	if got := below.Size.ValueOrZero(); !below.Size.Valid || got != 2 {
		t.Errorf("roll below the moved cut's lo: Size = %v, want 2", below.Size)
	}

	// The settled-IV path (deriveSizeAndMovesIfAbsent) reclassifies too.
	settled := adjRecord(1, adjLatA, adjLonA, false)
	settled.calculateIv(1, 2, 3)
	settled.deriveIvIfAbsent()
	if got := settled.Size.ValueOrZero(); got != 2 {
		t.Errorf("settled IVs: Size = %v, want 2", settled.Size)
	}
}

// repopulateIv's re-derive branch applies the adjustment for the walk being
// derived (the sighting's strong flag, not the stored one).
func TestDerivationAdjustRepopulateIv(t *testing.T) {
	withDeriveIv(t, true)
	withDeriveStrong(t, true)
	withModifierIndex(t,
		levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)),
		levelMod(modifierScope{PokemonID: 1, Strong: true}, 3, adjZoneA, cellSet(adjCellA)))

	flip := adjRecord(1, adjLatA, adjLonA, false)
	flip.calculateIv(6, 0, 11)
	flip.SetLevel(null.IntFrom(5))
	flip.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)
	if want := encounter.Derive(adjEid, true, false, 0).Level + 5; int32(flip.Level.ValueOrZero()) != want {
		t.Errorf("boost flip level = %v, want %d (boosted base + 5)", flip.Level, want)
	}

	// Stored ordinary, sighted strong: the strong key's +3 applies.
	turns := adjRecord(1, adjLatA, adjLonA, false)
	turns.calculateIv(6, 0, 11)
	turns.SetLevel(null.IntFrom(5))
	turns.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), true)
	if want := encounter.Derive(adjEid, true, true, 0).Level + 3; int32(turns.Level.ValueOrZero()) != want {
		t.Errorf("turning strong level = %v, want %d (strong base + 3)", turns.Level, want)
	}
}

// A confirmed unexplained modifier sends repopulateIv down the derive-off
// branch: with no scan history the stale boost state's IVs are cleared.
func TestDerivationAdjustRepopulateIvUnexplained(t *testing.T) {
	withDeriveIv(t, true)
	withModifierIndex(t, &derivationModifier{Type: modUnexplained, Scope: modifierScope{PokemonID: 1},
		Status: modConfirmed, InCells: cellSet(adjCellA), Outside: map[s2.CellID]struct{}{}})

	p := adjRecord(1, adjLatA, adjLonA, false)
	p.calculateIv(6, 0, 11)
	p.SetLevel(null.IntFrom(5))
	p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)
	if p.AtkIv.Valid || p.Level.Valid {
		t.Errorf("unexplained left atk=%v level=%v valid; want cleared as with derive_iv off", p.AtkIv, p.Level)
	}
}

// A wild update derives with the cell the record is being placed in, not the
// previous sighting's: a record moving from cell A (local +5 level modifier)
// to cell B gets B's values, both when the boost flip re-derives
// (repopulateIv, inside addWildPokemon) and when values are absent
// (deriveIvIfAbsent).
func TestUpdateFromWildDerivesWithThePlacedCell(t *testing.T) {
	const spawnID = int64(0x47b973e0595)
	withDeriveIv(t, true)
	seedSyncedSpawnpoint(t, spawnID)
	withModifierIndex(t, levelMod(modifierScope{PokemonID: 1}, 5, adjZoneA, cellSet(adjCellA)))
	cellA15 := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(adjLatA, adjLonA)).Parent(15))
	cellB15 := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(adjLatB, adjLonB)).Parent(15))
	wild := func(weather pogo.GameplayWeatherProto_WeatherCondition) *pogo.WildPokemonProto {
		return &pogo.WildPokemonProto{EncounterId: adjEid, SpawnPointId: "47b973e0595",
			Latitude: adjLatB, Longitude: adjLonB,
			Pokemon: &pogo.PokemonProto{PokemonId: 1,
				PokemonDisplay: &pogo.PokemonDisplayProto{WeatherBoostedCondition: weather}}}
	}

	// Derived in A, sighted in B under a boost flip.
	flip := adjRecord(1, adjLatA, adjLonA, false)
	flip.CellId = null.ValueFrom(cellA15)
	flip.deriveIvIfAbsent()
	if want := encounter.Derive(adjEid, false, false, 0).Level + 5; int32(flip.Level.ValueOrZero()) != want {
		t.Fatalf("setup: level in A = %v, want %d", flip.Level, want)
	}
	flip.updateFromWild(context.Background(), db.DbDetails{}, wild(pogo.GameplayWeatherProto_RAINY), cellB15, nil, 1700000000000, "t")
	if want := encounter.Derive(adjEid, true, false, 0).Level; int32(flip.Level.ValueOrZero()) != want {
		t.Errorf("boost flip into B: level = %v, want boosted base %d (no modifier in B)", flip.Level, want)
	}

	// No values yet, last placed in A, sighted in B.
	absent := adjRecord(1, adjLatA, adjLonA, false)
	absent.CellId = null.ValueFrom(cellA15)
	absent.updateFromWild(context.Background(), db.DbDetails{}, wild(pogo.GameplayWeatherProto_NONE), cellB15, nil, 1700000000000, "t")
	if want := encounter.Derive(adjEid, false, false, 0).Level; int32(absent.Level.ValueOrZero()) != want {
		t.Errorf("derive into B: level = %v, want base %d (no modifier in B)", absent.Level, want)
	}
}
