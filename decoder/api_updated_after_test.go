package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"

	"golbat/db"
)

// plantLivePokemon caches a live pokemon with the given updated timestamp
// (no tree insert: the visitor is driven by explicit keys) and removes it on
// cleanup.
func plantLivePokemon(t *testing.T, id uint64, updated uint32) {
	t.Helper()
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 1, Lon: 2, PokemonId: 25}}
	p.ExpireTimestamp = null.ValueFrom(uint32(time.Now().Unix() + 600))
	p.Updated = null.ValueFrom(updated)
	pokemonCache.Set(id, p, time.Minute)
	t.Cleanup(func() { pokemonCache.Delete(id) })
}

// updated_after keeps only records whose updated is strictly newer; zero
// disables the gate. The check runs at response build, after the spatial
// scan, so it never touches PokemonLookup.
func TestForEachLivePokemonResultUpdatedAfter(t *testing.T) {
	const older, boundary, newer = uint64(950001), uint64(950002), uint64(950003)
	plantLivePokemon(t, older, 1000)
	plantLivePokemon(t, boundary, 2000)
	plantLivePokemon(t, newer, 3000)
	keys := []uint64{older, boundary, newer}

	collect := func(updatedAfter int64) []uint64 {
		var got []uint64
		forEachLivePokemonResult(keys, "test", updatedAfter, func(id uint64, _ *ApiPokemonResult) { got = append(got, id) })
		return got
	}

	if got := collect(0); len(got) != 3 {
		t.Errorf("updated_after 0 must not gate: got %v", got)
	}
	if got := collect(2000); len(got) != 1 || got[0] != newer {
		t.Errorf("updated_after 2000 must keep only updated > 2000 (strict): got %v", got)
	}
	if got := collect(1999); len(got) != 2 || got[0] != boundary || got[1] != newer {
		t.Errorf("updated_after 1999 must keep 2000 and 3000 in key order: got %v", got)
	}
	if got := collect(5000); len(got) != 0 {
		t.Errorf("updated_after beyond every record must return nothing: got %v", got)
	}
}

// plantGymRecord caches a gym record (no tree insert) with the given updated.
func plantGymRecord(t *testing.T, hex string, updated int64) FortId {
	t.Helper()
	id := mustFortId(t, hex)
	gymCache.Set(id, &Gym{GymData: GymData{Id: id, Lat: 1, Lon: 2, Updated: updated}}, time.Minute)
	t.Cleanup(func() { gymCache.Delete(id) })
	return id
}

func plantPokestopRecord(t *testing.T, hex string, updated int64) FortId {
	t.Helper()
	id := mustFortId(t, hex)
	pokestopCache.Set(id, &Pokestop{PokestopData: PokestopData{Id: id, Lat: 1, Lon: 2, Updated: updated}}, time.Minute)
	t.Cleanup(func() { pokestopCache.Delete(id) })
	return id
}

func plantStationRecord(t *testing.T, hex string, updated int64) FortId {
	t.Helper()
	id := mustFortId(t, hex)
	stationCache.Set(id, &Station{StationData: StationData{Id: id, Lat: 1, Lon: 2, Updated: updated, EndTime: 1 << 40}}, time.Minute)
	// Mark battles as loaded so the read-only getter does not try to hydrate
	// them from a database this test does not have (as preload does).
	storeStationBattles(id, nil)
	t.Cleanup(func() {
		stationCache.Delete(id)
		stationBattleCache.Delete(id)
	})
	return id
}

// Each fort collector applies the same strict gate on the record's updated.
func TestFortCollectorsUpdatedAfter(t *testing.T) {
	gOld := plantGymRecord(t, "000000000000000000000000000a0001.16", 1000)
	gNew := plantGymRecord(t, "000000000000000000000000000a0002.16", 3000)
	sOld := plantPokestopRecord(t, "000000000000000000000000000a0003.16", 1000)
	sNew := plantPokestopRecord(t, "000000000000000000000000000a0004.16", 3000)
	tOld := plantStationRecord(t, "000000000000000000000000000a0005.11", 1000)
	tNew := plantStationRecord(t, "000000000000000000000000000a0006.11", 3000)
	now := time.Now().Unix()

	gyms := collectGymResults(db.DbDetails{}, []FortId{gOld, gNew}, 2000, "test")
	if len(gyms) != 1 || gyms[0].Id != gNew.String() {
		t.Errorf("gyms after 2000 = %d results, want only the newer one", len(gyms))
	}
	stops := collectPokestopResults(db.DbDetails{}, []FortId{sOld, sNew}, false, now, 2000, "test")
	if len(stops) != 1 || stops[0].Id != sNew.String() {
		t.Errorf("pokestops after 2000 = %d results, want only the newer one", len(stops))
	}
	stations := collectStationResults(db.DbDetails{}, []FortId{tOld, tNew}, 2000, "test")
	if len(stations) != 1 || stations[0].Id != tNew.String() {
		t.Errorf("stations after 2000 = %d results, want only the newer one", len(stations))
	}

	// zero disables the gate everywhere
	if len(collectGymResults(db.DbDetails{}, []FortId{gOld, gNew}, 0, "test")) != 2 ||
		len(collectPokestopResults(db.DbDetails{}, []FortId{sOld, sNew}, false, now, 0, "test")) != 2 ||
		len(collectStationResults(db.DbDetails{}, []FortId{tOld, tNew}, 0, "test")) != 2 {
		t.Error("updated_after 0 must not gate any fort type")
	}
	// strict boundary
	if len(collectGymResults(db.DbDetails{}, []FortId{gOld, gNew}, 3000, "test")) != 0 {
		t.Error("updated == updated_after must be excluded (strict >)")
	}
}

// The combined scan applies its top-level updated_after to every type group
// after the per-type limits, so counters and limit_reached describe the scan
// (unchanged) while the lists may come back short.
func TestCombinedScanUpdatedAfterGatesAfterLimits(t *testing.T) {
	withScanLimits(t)
	minLoc, maxLoc := plantCombinedForts(t, 2, 0, 0)
	// plantCombinedForts stores lookups only; give the two gyms records with
	// different updated so the gate can tell them apart.
	var ids []FortId
	fortLookupCache.Range(func(id FortId, _ FortLookup) bool { ids = append(ids, id); return true })
	if len(ids) != 2 {
		t.Fatalf("planted %d forts, want 2", len(ids))
	}
	for i, id := range ids {
		gymCache.Set(id, &Gym{GymData: GymData{Id: id, Lat: 40, Lon: -70, Updated: int64(1000 * (i + 1))}}, time.Minute)
		t.Cleanup(func() { gymCache.Delete(id) })
	}

	res := FortCombinedScanEndpoint(ApiFortCombinedScan{
		Min: minLoc, Max: maxLoc, UpdatedAfter: 1500,
		Gyms: &ApiFortTypeScanGroup{Limit: 2},
	}, db.DbDetails{})

	if len(res.Gyms) != 1 || res.Gyms[0].Updated != 2000 {
		t.Errorf("gyms = %d results, want only the one updated after 1500", len(res.Gyms))
	}
	if res.GymsStats.Examined != 2 || !res.GymsStats.LimitReached || !res.LimitReached {
		t.Errorf("counters must describe the scan before the gate: %+v top-level limit_reached %v", res.GymsStats, res.LimitReached)
	}
}
