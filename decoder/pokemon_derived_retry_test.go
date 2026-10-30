package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"

	"golbat/db"
	"golbat/pogo"
)

// CP is latched — recomputeCpIfNeeded runs only when CP is absent — so a
// pokemon whose CP could not be computed on its first sighting would keep its
// derived IVs with no CP beside them for the rest of its life.
// retryMissingDerivedFields gives it another chance, and it can only be
// reached through UpdatePokemonBatch's significant-update gate: these tests
// drive that entry point rather than calling the helper, because a direct
// call cannot see whether the branch is wired to the path production takes.
//
// The two CP-retry tests stage the transient failure: ohbem is absent for the
// first sighting and built for the second. Every other input is held
// identical so the gate REFUSES the second sighting, which leaves the retry as
// the only thing that can supply the missing CP.

func cpRetryOhbemAbsent(t *testing.T) {
	t.Helper()
	old := ohbem.Load()
	ohbem.Store(nil)
	t.Cleanup(func() { ohbem.Store(old) })
}

func TestUpdatePokemonBatchRetriesLatchedCpOnWild(t *testing.T) {
	lureTestSetup(t) // memory-only + a webhook sink, so the nil DB is never used
	cpRetryOhbemAbsent(t)

	const spawnHex = "3c8d2bae1f4"
	const spawnID = int64(0x3c8d2bae1f4)
	const encId = uint64(930301)

	seedCachedSpawnpoint(t, spawnID, null.IntFrom(600))
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	seen := time.Date(2026, 9, 23, 11, 5, 0, 0, time.UTC)
	params := ScanParameters{ProcessWild: true}
	sighting := derivedDespawnBatchWild(encId, spawnHex, seen.UnixMilli())

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{sighting}, nil, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon not in cache after the first batch")
	}
	hasIv, hasCp := p.AtkIv.Valid, p.Cp.Valid
	unlock()
	if !hasIv {
		t.Fatal("first sighting did not derive IVs, so this test cannot show anything about CP")
	}
	if hasCp {
		t.Fatal("first sighting computed a CP with ohbem absent; the transient failure this test relies on did not happen")
	}

	// ohbem is now available. The sighting is byte-for-byte the one already
	// decoded, so wildSignificantUpdate refuses it and updateFromWild never
	// runs.
	withOhbem(t)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		[]RawWildPokemonData{sighting}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("wild pokemon left the cache")
	}
	defer unlock()
	if !p.Cp.Valid {
		t.Error("CP is still absent after a skipped wild sighting: a CP that failed to compute once is never retried, " +
			"so the pokemon keeps its IVs with no CP for the rest of its life")
	}
}

// The nearby path needs the retry more than the wild path does:
// nearbySignificantUpdate compares PokedexNumber (not DisplayId, which
// carries the encounter id), so a pokemon at a pokestop is skipped whenever
// nothing about it moved, which is exactly when the latch bites.
func TestUpdatePokemonBatchRetriesLatchedCpOnNearby(t *testing.T) {
	lureTestSetup(t)
	cpRetryOhbemAbsent(t)

	const encId = uint64(930302)
	fortId := mustFortId(t, "dddddddddddddddddddddddddddddddd")
	pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{Id: fortId, Lat: 51.5007, Lon: -0.1246}}, time.Minute)
	t.Cleanup(func() { pokestopCache.Delete(fortId) })

	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })

	seen := time.Date(2026, 9, 23, 11, 5, 0, 0, time.UTC)
	cell := int64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5007, -0.1246)).Parent(gmoCellLevel))
	sighting := RawNearbyPokemonData{
		Cell:      uint64(cell),
		Timestamp: seen.UnixMilli(),
		Data: &pogo.NearbyPokemonProto{
			EncounterId:    encId,
			FortId:         "dddddddddddddddddddddddddddddddd",
			PokedexNumber:  int32(pogo.HoloPokemonId_RATTATA),
			PokemonDisplay: &pogo.PokemonDisplayProto{},
		},
	}
	params := ScanParameters{ProcessNearby: true}

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		nil, []RawNearbyPokemonData{sighting}, nil, nil, "tester")

	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("nearby pokemon not in cache after the first batch")
	}
	hasIv, hasCp, seenType := p.AtkIv.Valid, p.Cp.Valid, p.SeenType.Code
	unlock()
	if seenType != SeenTypeCodeNearbyStop {
		t.Fatalf("seen type = %v, want nearby stop: a cell pokemon is always re-decoded, which would hide the latch", seenType)
	}
	if !hasIv {
		t.Fatal("first sighting did not derive IVs, so this test cannot show anything about CP")
	}
	if hasCp {
		t.Fatal("first sighting computed a CP with ohbem absent; the transient failure this test relies on did not happen")
	}

	withOhbem(t)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params,
		nil, []RawNearbyPokemonData{sighting}, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	if p == nil {
		t.Fatal("nearby pokemon left the cache")
	}
	defer unlock()
	if !p.Cp.Valid {
		t.Error("CP is still absent after a skipped nearby sighting at a pokestop: a CP that failed to compute once is " +
			"never retried, so the pokemon keeps its IVs with no CP for the rest of its life")
	}
}

// With derive_iv off, roll-marking a record changes none of its stats, so an
// observed CP on it stays. Ohbem need not be running in that configuration, so
// a CP cleared there could never come back.
func TestRollMarkingKeepsCpWhenTheStatsDoNotChange(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)
	old := ohbem.Load()
	ohbem.Store(nil)
	t.Cleanup(func() { ohbem.Store(old) })
	seedCachedSpawnpoint(t, 0x3c8d2bae1fd, null.Int{})

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16,
		Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	p.SetSeenType(SeenTypeCodeWild)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetCostume(null.IntFrom(0)) // as a real record carries them; unset reads as a display change
	p.SetGender(null.IntFrom(0))
	p.calculateIv(10, 11, 12)
	p.SetLevel(null.IntFrom(20))
	p.SetCp(null.IntFrom(640))

	p.updateFromWild(context.Background(), db.DbDetails{}, &pogo.WildPokemonProto{
		EncounterId: eidRollFires, SpawnPointId: "3c8d2bae1fd", Latitude: 51.5, Longitude: -0.1,
		Pokemon: &pogo.PokemonProto{PokemonId: pogo.HoloPokemonId(dittoRollTestPool16),
			PokemonDisplay: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16)}},
	}, 99, nil, 1700000000000, "tester")

	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must mark the record")
	}
	if got := p.Cp.ValueOrZero(); !p.Cp.Valid || got != 640 {
		t.Errorf("cp = %v, want the observed 640 kept: marking changed no stats, and nothing can recompute it", p.Cp)
	}
}

// Moves are filled only when absent too, so a pokemon first seen while the
// masterfile did not carry its move pools keeps its IVs with no moves — until
// a skipped sighting retries them, once the masterfile has them. Through
// UpdatePokemonBatch, with the second sighting unchanged so the gate refuses
// it and the retry is the only thing that can fill them.
func TestUpdatePokemonBatchRetriesMovesMissingFromTheMasterfile(t *testing.T) {
	lureTestSetup(t)
	const species = 7005
	withMasterFileData(t, MasterFileData{Initialized: true, Pokemon: map[int]MasterFilePokemon{}}) // no pools yet

	const spawnHex = "3c8d2bae203"
	seedCachedSpawnpoint(t, 0x3c8d2bae203, null.Int{})
	const encId = uint64(930303)
	pokemonCache.Delete(encId)
	t.Cleanup(func() { pokemonCache.Delete(encId) })
	seen := time.Date(2026, 9, 23, 11, 5, 0, 0, time.UTC)
	sighting := RawWildPokemonData{Cell: 5000000000000000000, Timestamp: seen.UnixMilli(), Data: &pogo.WildPokemonProto{
		EncounterId: encId, SpawnPointId: spawnHex, Latitude: 51.5007, Longitude: -0.1246,
		Pokemon: &pogo.PokemonProto{PokemonId: species, PokemonDisplay: &pogo.PokemonDisplayProto{}},
	}}
	params := ScanParameters{ProcessWild: true}

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{sighting}, nil, nil, nil, "tester")
	p, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	hasIv, hasMove := p.AtkIv.Valid, p.Move1.Valid
	unlock()
	if !hasIv || hasMove {
		t.Fatalf("fixture assumption violated: want derived IVs and no moves (iv=%v move=%v)", hasIv, hasMove)
	}

	withMasterFileData(t, MasterFileData{Initialized: true, Pokemon: map[int]MasterFilePokemon{
		species: {QuickMoves: []int{520}, ChargedMoves: []int{620}},
	}})
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{sighting}, nil, nil, nil, "tester")

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "test")
	m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero())
	unlock()
	if m1 != 520 || m2 != 620 {
		t.Errorf("moves = %d/%d after the masterfile gained the pools, want 520/620", m1, m2)
	}
}
