package decoder

import (
	"context"
	"testing"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/derive/encounter"

	"github.com/guregu/null/v6"

	"golbat/pogo"
)

// seedSyncedSpawnpoint puts a synced, despawn-unknown spawnpoint into
// spawnpointCache and removes it at test cleanup. "Synced" (via
// syncFastFields) makes DespawnSecFast report synced=true, so
// setExpireTimestampFromSpawnpoint's cache hit satisfies the lookup
// entirely from memory and never reaches getSpawnpointRecord — which would
// touch db.DbDetails{}'s nil *sqlx.DB.
func seedSyncedSpawnpoint(t *testing.T, spawnID int64) *Spawnpoint {
	t.Helper()
	sp := &Spawnpoint{SpawnpointData: SpawnpointData{Id: spawnID}} // DespawnSec invalid: unknown
	sp.syncFastFields()                                            // synced=true, known=false
	spawnpointCache.Set(spawnID, sp, 0)
	t.Cleanup(func() { spawnpointCache.Delete(spawnID) })
	return sp
}

// The boost branch comes from WeatherBoostedCondition, which golbat stores as
// pokemon.Weather. Validated live vectors: this encounter id gives 6/0/11 at
// level 5 unboosted, and 8/4/6 at level 10 boosted.
func TestDeriveIvUsesStoredWeatherAsBoost(t *testing.T) {
	const eid = uint64(8429844873227918432)

	unboosted := &Pokemon{}
	unboosted.Id = Uint64Str(eid)
	unboosted.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	unboosted.SetSeenType(SeenTypeCodeWild)
	unboosted.deriveIvIfAbsent()

	if got := unboosted.AtkIv.ValueOrZero(); got != 6 {
		t.Fatalf("unboosted atk: got %d want 6", got)
	}
	if got := unboosted.Level.ValueOrZero(); got != 5 {
		t.Fatalf("unboosted level: got %d want 5", got)
	}

	boosted := &Pokemon{}
	boosted.Id = Uint64Str(eid)
	boosted.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY)))
	boosted.SetSeenType(SeenTypeCodeWild)
	boosted.deriveIvIfAbsent()

	if got := boosted.AtkIv.ValueOrZero(); got != 8 {
		t.Fatalf("boosted atk: got %d want 8", got)
	}
	if got := boosted.Level.ValueOrZero(); got != 10 {
		t.Fatalf("boosted level: got %d want 10", got)
	}
}

// Real IVs must never be overwritten by derived ones.
func TestDeriveIvLeavesExistingIvsAlone(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.calculateIv(15, 15, 15)

	p.deriveIvIfAbsent()

	if got := p.AtkIv.ValueOrZero(); got != 15 {
		t.Fatalf("existing ivs must survive, got atk %d", got)
	}
}

// Strong Pokemon must not be derived from. The ordinary boosted-floor
// mechanic encounter.For models does not apply to strong Pokemon —
// repopulateIv's own "isBoostedMatches || isStrong" branch and detectDitto's
// identical-CompressedIv-across-boost-states requirement
// for a strong scan both say a strong Pokemon's IVs do not move with
// weather — so deriving one here would silently write a wrong-but-plausible
// IV set that looks identical to a real encounter. See
// TestRepopulateIvClearsStrongPokemonInsteadOfDeriving for the companion
// guard in repopulateIv.
func TestDeriveIvIfAbsentSkipsStrongPokemon(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY)))
	p.SetSeenType(SeenTypeCodeWild)
	p.SetIsStrong(null.BoolFrom(true))

	p.deriveIvIfAbsent()

	if p.AtkIv.Valid {
		t.Errorf("AtkIv = %v, want invalid — a strong Pokemon must not be derived from", p.AtkIv)
	}
	if p.Level.Valid {
		t.Errorf("Level = %v, want invalid — a strong Pokemon must not be derived from", p.Level)
	}
}

// seen_type must survive derivation untouched on every path: a derived pokemon
// is still wild until a real encounter arrives.
func TestDeriveIvLeavesSeenTypeAlone(t *testing.T) {
	for _, code := range []SeenTypeCode{
		SeenTypeCodeWild, SeenTypeCodeLureWild, SeenTypeCodeNearbyStop, SeenTypeCodeCell,
	} {
		p := &Pokemon{}
		p.Id = Uint64Str(0x1234567890ABCDEF)
		p.SetSeenType(code)
		p.deriveIvIfAbsent()

		if p.SeenType.Code != code {
			t.Errorf("seen type changed from %v to %v; derivation must not touch it", code, p.SeenType.Code)
		}
		if !p.AtkIv.Valid {
			t.Errorf("from %v: derivation did not run, so this test proves nothing", code)
		}
	}
}

// Size must come from the derivation too, not be left unset.
func TestDeriveIvSetsSize(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetSeenType(SeenTypeCodeWild)
	p.deriveIvIfAbsent()

	if got := int32(p.Size.ValueOrZero()); got != encounter.SizeM {
		t.Fatalf("size: got %d want %d", got, encounter.SizeM)
	}
}

// The derived values must come from the boost state the SIGHTING carried, not
// whatever pokemon.Weather held beforehand. Calling deriveIvIfAbsent directly
// cannot detect a call placed before the display is applied; this can.
//
// pokemon.Weather starts stale at NONE (unboosted); the incoming
// WildPokemonProto's display carries RAINY (boosted). A correct call site —
// deriveIvIfAbsent after addWildPokemon has applied the display — sees the
// fresh boosted value and derives the boosted branch. A call site placed
// before addWildPokemon would still see the stale NONE and derive the wrong
// (unboosted) branch, silently, for every sighting through this path.
//
// Fails if the derivation (applySightingDerivation, which calls
// deriveIvIfAbsent) is moved above addWildPokemon, but only for a NEW record,
// which is why the fixture is one — see the newRecord comment below.
func TestUpdateFromWildDerivesUsingTheSightingsWeather(t *testing.T) {
	const encounterID = uint64(0x1234567890ABCDEF)
	const spawnID = int64(0x47b973e0595)

	// Keep the despawn path in memory: an uncached spawnpoint would reach the
	// database, and db.DbDetails{} wraps a nil *sqlx.DB.
	seedSyncedSpawnpoint(t, spawnID)

	// newRecord is what makes this test able to fail. setPokemonDisplay
	// calls repopulateIv on every NON-new record, and repopulateIv's
	// derivation branch re-derives on the correct boost state — which would
	// rescue a mis-ordered deriveIvIfAbsent call and hide the reordering this
	// test exists to catch. setPokemonDisplay skips repopulateIv for a new
	// record, so nothing rescues it here, and the new-record path is also
	// where a mis-ordered call corrupts data: a stale Weather picks the wrong
	// branch and the record keeps it.
	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(encounterID)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // stale

	wild := &pogo.WildPokemonProto{
		EncounterId:  encounterID,
		SpawnPointId: "47b973e0595",
		Latitude:     51.5,
		Longitude:    -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId: 25,
			PokemonDisplay: &pogo.PokemonDisplayProto{
				WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
			},
		},
	}

	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")

	want := encounter.For(encounterID, true, 0) // boosted
	if got := int64OrZero(p.AtkIv); got != int64(want.Attack) {
		t.Fatalf("atk %d, want %d — derived from the wrong boost branch, "+
			"which means deriveIvIfAbsent ran before the display was applied", got, int64(want.Attack))
	}
	if got := int64(p.Level.ValueOrZero()); got != int64(want.Level) {
		t.Fatalf("level %d, want %d — derived from the wrong boost branch", got, want.Level)
	}
}

// Same property as TestUpdateFromWildDerivesUsingTheSightingsWeather, for the
// second of the three derivation call sites. FortId is left empty so the
// pokestop-cache lookup block is skipped.
//
// A real cell id, because the function refuses to place a pokemon it has no
// cell for and returns before reaching the derivation. That guard is about
// placement and says nothing about ordering, which is what this test is for.
func TestUpdateFromNearbyDerivesUsingTheSightingsWeather(t *testing.T) {
	const encounterID = uint64(0xCAFEBABEDEADBEEF)

	// newRecord: true for the same reason as the wild test above — on a
	// non-new record setPokemonDisplay's repopulateIv call re-derives on the
	// correct branch and hides a mis-ordered call site entirely.
	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(encounterID)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // stale

	nearby := &pogo.NearbyPokemonProto{
		PokedexNumber: 25,
		PokemonDisplay: &pogo.PokemonDisplayProto{
			WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
		},
	}

	// The S2 L15 cell containing 51.5,-0.1.
	const cellId = int64(5221365921477558272)
	p.updateFromNearby(context.Background(), db.DbDetails{}, nearby, cellId, nil, 1700000000000, "tester")

	want := encounter.For(encounterID, true, 0) // boosted
	if got := int64OrZero(p.AtkIv); got != int64(want.Attack) {
		t.Fatalf("atk %d, want %d — derived from the wrong boost branch, "+
			"which means deriveIvIfAbsent ran before the display was applied", got, int64(want.Attack))
	}
}

// Same property, for the third of the three derivation call sites: the
// new-record branch of updateFromMap. newRecord is set directly since a bare
// &Pokemon{} is not a new record.
func TestUpdateFromMapDerivesUsingTheSightingsWeather(t *testing.T) {
	const encounterID = uint64(0x1122334455667788)

	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(encounterID)

	raw := RawMapPokemonData{
		Cell:      5000000000000000000,
		Timestamp: 1700000000000,
		FortId:    "",
		Lat:       51.5,
		Lon:       -0.1,
		Data: &pogo.MapPokemonProto{
			EncounterId:   encounterID,
			PokedexTypeId: 25,
			PokemonDisplay: &pogo.PokemonDisplayProto{
				WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
			},
		},
	}

	p.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester")

	want := encounter.For(encounterID, true, 0) // boosted
	if got := int64OrZero(p.AtkIv); got != int64(want.Attack) {
		t.Fatalf("atk %d, want %d — derived from the wrong boost branch, "+
			"which means deriveIvIfAbsent ran before the display was applied", got, int64(want.Attack))
	}
}

// A known Ditto whose IVs are absent is derived on the Ditto's own boost
// rule, not its disguise's. The stored condition is RAINY, where the two
// disagree: the disguise rule says boosted (8/4/6 at level 10 for this
// encounter id), while a Ditto is caught boosted only in PP.
func TestDeriveIvIfAbsentDerivesAKnownDittoOnItsOwnBranch(t *testing.T) {
	const eid = uint64(8429844873227918432)

	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetIsDitto(true)
	p.SetPokemonId(int16(pogo.HoloPokemonId_DITTO))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY)))
	p.SetSeenType(SeenTypeCodeWild)

	p.deriveIvIfAbsent()

	assertDerivedDittoBranch(t, p, eid)
}

// The same on the path production takes. An existing record already
// confirmed as a Ditto, whose IVs were cleared, is sighted again in the wild:
// setPokemonDisplay keeps IsDitto (the display is unchanged), its
// repopulateIv call returns early (RAINY is not a boost flip under the Ditto
// rule), and deriveIvIfAbsent then fills the Ditto's own stats.
func TestUpdateFromWildDerivesAKnownDittoOnItsOwnBranch(t *testing.T) {
	const eid = uint64(8429844873227918432)
	const spawnID = int64(0x47b973e0595)

	seedSyncedSpawnpoint(t, spawnID)

	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetIsDitto(true)
	p.SetPokemonId(int16(pogo.HoloPokemonId_DITTO))
	// The display must match the incoming sighting exactly, or
	// setPokemonDisplay's change branch resets IsDitto to false and this
	// stops being a known-Ditto test at all.
	p.SetDisplayPokemonId(null.IntFrom(25))
	p.SetDisplayPokemonForm(null.IntFrom(0))
	p.SetForm(null.IntFrom(0))
	p.SetCostume(null.IntFrom(0))
	p.SetGender(null.IntFrom(0))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY)))
	p.SetSeenType(SeenTypeCodeWild)

	wild := &pogo.WildPokemonProto{
		EncounterId:  eid,
		SpawnPointId: "47b973e0595",
		Latitude:     51.5,
		Longitude:    -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId: 25,
			PokemonDisplay: &pogo.PokemonDisplayProto{
				WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
			},
		},
	}

	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")

	if !p.IsDitto {
		t.Fatal("fixture broke: setPokemonDisplay reset IsDitto, so this no longer tests the known-Ditto case")
	}
	assertDerivedDittoBranch(t, p, eid)
}

func assertDerivedDittoBranch(t *testing.T, p *Pokemon, eid uint64) {
	t.Helper()
	want := encounter.For(eid, false, 0)
	disguise := encounter.For(eid, true, 0)
	if want.Attack == disguise.Attack && want.Level == disguise.Level {
		t.Fatal("fixture assumption violated: the two branches agree for this encounter id")
	}
	if !p.AtkIv.Valid || int64(p.AtkIv.ValueOrZero()) != int64(want.Attack) || int64(p.Level.ValueOrZero()) != int64(want.Level) {
		t.Errorf("AtkIv = %v, Level = %v; want %d at level %d (the Ditto's unboosted branch; the disguise's is %d at level %d)",
			p.AtkIv, p.Level, want.Attack, want.Level, disguise.Attack, disguise.Level)
	}
}

// applyDerivedValues (via deriveIvIfAbsent) must set move_1/move_2 from the
// masterfile move pools when they resolve — the production write path, as
// distinct from checkDerivation's read-only validation logging.
func TestDeriveIvIfAbsentSetsMovesWhenPoolsResolve(t *testing.T) {
	const species = 7001
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			species: {QuickMoves: []int{500, 501}, ChargedMoves: []int{600, 601, 602}},
		},
	})

	const eid = uint64(8429844873227918432)
	d := encounter.For(eid, false, 0)
	quickIdx, chargedIdx := d.MoveIndices(2, 3)
	wantMove1 := uint16([]int{500, 501}[quickIdx])
	wantMove2 := uint16([]int{600, 601, 602}[chargedIdx])

	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetPokemonId(species)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetSeenType(SeenTypeCodeWild)

	p.deriveIvIfAbsent()

	if !p.Move1.Valid || p.Move1.ValueOrZero() != wantMove1 {
		t.Errorf("Move1 = %v, want %d", p.Move1, wantMove1)
	}
	if !p.Move2.Valid || p.Move2.ValueOrZero() != wantMove2 {
		t.Errorf("Move2 = %v, want %d", p.Move2, wantMove2)
	}
}

// The flip side: an unresolved pool (here, a species absent from the
// masterfile) must leave move_1/move_2 unset rather than guessed — while
// deriveIvIfAbsent's ordinary IV/level/size derivation still happens
// normally, proving the two are independent rather than one gating the
// other.
func TestDeriveIvIfAbsentLeavesMovesUnsetWhenPoolUnresolved(t *testing.T) {
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon:     map[int]MasterFilePokemon{},
	})

	const eid = uint64(8429844873227918432)
	const species = 7002 // absent from the fixture above

	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetPokemonId(species)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetSeenType(SeenTypeCodeWild)

	p.deriveIvIfAbsent()

	if p.Move1.Valid || p.Move2.Valid {
		t.Errorf("Move1/Move2 = %v/%v, want both unset when the move pool did not resolve", p.Move1, p.Move2)
	}
	// The rest of the derivation must still have happened normally — this
	// encounter id's known unboosted level (see TestDeriveIvUsesStoredWeatherAsBoost).
	if got := p.Level.ValueOrZero(); got != 5 {
		t.Fatalf("fixture broke: level = %d, want 5", got)
	}
}

// A weather flip clears the IVs but deliberately keeps move_1, move_2 and
// size, because those do not depend on the boost state. Derivation must fill
// only what is missing, never replace an observation.
//
// This matters most for moves: the game GRANTS moves outside the pool the
// derivation draws from (event and legacy moves), so for that population the
// derived value is known wrong — checkDerivation will not even score it, via
// moveOutOfPool. Overwriting the real one would be unrecoverable, since a
// derived value is indistinguishable from an observed one once written.
func TestDeriveIvDoesNotOverwriteObservedMovesOrSize(t *testing.T) {
	const (
		observedMove1 = int64(212)
		observedMove2 = int64(131) // Body Slam: granted, absent from any pool
		observedSize  = int64(4)
	)
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetPokemonId(108) // Lickitung
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetMove1(null.IntFrom(observedMove1))
	p.SetMove2(null.IntFrom(observedMove2))
	p.SetSize(null.IntFrom(observedSize))

	// What a weather flip with no matching scan leaves behind: IVs gone,
	// moves and size still the game's own.
	p.calculateIv(15, 15, 15)
	p.clearIv(true)
	if p.AtkIv.Valid {
		t.Fatal("fixture assumption violated: clearIv must drop the IVs")
	}
	if !p.Move1.Valid || !p.Move2.Valid || !p.Size.Valid {
		t.Fatal("fixture assumption violated: clearIv must keep moves and size")
	}

	p.deriveIvIfAbsent()

	if !p.AtkIv.Valid {
		t.Error("derivation must still fill the absent IVs")
	}
	if got := int64(p.Move1.ValueOrZero()); got != observedMove1 {
		t.Errorf("move_1 = %d, want the observed %d — derivation must not replace an observed move", got, observedMove1)
	}
	if got := int64(p.Move2.ValueOrZero()); got != observedMove2 {
		t.Errorf("move_2 = %d, want the observed %d — this is the granted-move case the derivation cannot predict", got, observedMove2)
	}
	if got := int64(p.Size.ValueOrZero()); got != observedSize {
		t.Errorf("size = %d, want the observed %d", got, observedSize)
	}
}

// withDeriveIv sets the IV-derivation flag for one test and restores it.
// The package's test init applies the shipped default (on); a test wanting
// the other state has to say so.
func withDeriveIv(t *testing.T, enabled bool) {
	t.Helper()
	old := config.Config.DeriveIv
	config.Config.DeriveIv = enabled
	t.Cleanup(func() { config.Config.DeriveIv = old })
}

// derive_iv off restores stock behaviour: a sighting carries no IVs until a
// real encounter supplies them. The guard is asserted at applyDerivedValues,
// the single place derived values are written, so no caller can bypass it.
func TestDeriveIvRespectsTheConfigFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		wantIv  bool
	}{
		{"enabled fills the absent IVs", true, true},
		{"disabled leaves them absent", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDeriveIv(t, tc.enabled)

			p := &Pokemon{}
			p.Id = Uint64Str(8429844873227918432)
			p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

			p.deriveIvIfAbsent()

			if got := p.AtkIv.Valid; got != tc.wantIv {
				t.Errorf("AtkIv.Valid = %v, want %v", got, tc.wantIv)
			}
			if !tc.enabled && (p.Move1.Valid || p.Size.Valid || p.Level.Valid) {
				t.Errorf("derive_iv off must leave moves, size and level absent too: move1=%v size=%v level=%v",
					p.Move1.Valid, p.Size.Valid, p.Level.Valid)
			}
		})
	}
}

// The flag guards the write itself, not just the one entry point that reads
// it first — a caller reaching applyDerivedValues directly still writes
// nothing, so moving or adding a call site cannot write derived values with
// the flag off.
func TestApplyDerivedValuesIsGatedAtTheWrite(t *testing.T) {
	withDeriveIv(t, false)

	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.applyDerivedValues(encounter.For(8429844873227918432, false, 0))

	if p.AtkIv.Valid || p.Level.Valid || p.Size.Valid {
		t.Errorf("applyDerivedValues wrote with derive_iv off: atk=%v level=%v size=%v",
			p.AtkIv.Valid, p.Level.Valid, p.Size.Valid)
	}
}

// With derivation off, a weather flip that finds no stored scan for the new
// boost state must clear the IVs rather than leave the old state's.
//
// The failure this guards is quiet: applyDerivedValues writes nothing when
// the flag is off, so an arm that only cleared CP and PVP would leave the
// PREVIOUS boost state's IVs and level valid for the new one — and CP
// recomputable from them.
func TestRepopulateIvClearsRatherThanKeepingStaleIvsWhenDerivationIsOff(t *testing.T) {
	for _, tc := range []struct {
		name      string
		deriveIv  bool
		wantValid bool
	}{
		{"derivation on re-derives for the new state", true, true},
		{"derivation off clears instead", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDeriveIv(t, tc.deriveIv)

			p := &Pokemon{}
			p.Id = Uint64Str(8429844873227918432)
			p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
			p.calculateIv(15, 15, 15)
			p.SetLevel(null.IntFrom(20))
			if !p.AtkIv.Valid {
				t.Fatal("fixture assumption violated: the record must start with IVs")
			}

			// A flip to a boosted state, with no stored scan to match it.
			p.repopulateIv(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY), false)

			if got := p.AtkIv.Valid; got != tc.wantValid {
				t.Errorf("AtkIv.Valid = %v, want %v", got, tc.wantValid)
			}
			if !tc.deriveIv && p.Level.Valid {
				t.Error("level must be cleared too: it belongs to the boost state just left behind")
			}
			if tc.deriveIv && int64(p.AtkIv.ValueOrZero()) == 15 {
				t.Error("the IVs should have been re-derived for the new state, not left at the old ones")
			}
		})
	}
}

// A display change keeps the IVs but clears size and moves, and those must be
// filled again from the new display — not left empty because IVs are present.
// The second sighting changes species and nothing else, so no weather change
// re-derives anything: only the derivation itself can refill them.
func TestUpdateFromWildRefillsSizeAndMovesAfterADisplayChange(t *testing.T) {
	const speciesA, speciesB = 7001, 7003
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			speciesA: {QuickMoves: []int{500}, ChargedMoves: []int{600}},
			speciesB: {QuickMoves: []int{510}, ChargedMoves: []int{610}},
		},
	})
	const encounterID = uint64(8429844873227918432)
	seedCachedSpawnpoint(t, 0x3c8d2bae1f5, null.Int{}) // cached, so the nil DB is never reached
	wild := func(species int32) *pogo.WildPokemonProto {
		return &pogo.WildPokemonProto{
			EncounterId:  encounterID,
			SpawnPointId: "3c8d2bae1f5",
			Latitude:     51.5,
			Longitude:    -0.1,
			Pokemon: &pogo.PokemonProto{
				PokemonId:      pogo.HoloPokemonId(species),
				PokemonDisplay: &pogo.PokemonDisplayProto{},
			},
		}
	}

	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(encounterID)
	p.updateFromWild(context.Background(), db.DbDetails{}, wild(speciesA), 99, nil, 1700000000000, "tester")
	if !p.AtkIv.Valid || int64(p.Move1.ValueOrZero()) != 500 {
		t.Fatalf("fixture assumption violated: first sighting should derive IVs and move 500, got atk=%v move1=%v", p.AtkIv, p.Move1)
	}
	atk := p.AtkIv
	p.newRecord = false

	p.updateFromWild(context.Background(), db.DbDetails{}, wild(speciesB), 99, nil, 1700000060000, "tester")

	if p.AtkIv != atk {
		t.Errorf("IVs changed across a display change: %v -> %v", atk, p.AtkIv)
	}
	if m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero()); m1 != 510 || m2 != 610 {
		t.Errorf("moves after the species change = %v/%v, want the new species' 510/610", p.Move1, p.Move2)
	}
	if !p.Size.Valid {
		t.Error("size left empty after the display change")
	}
}

// The lure path refills size and moves after a display change, as the wild
// and nearby paths do. An existing lure_wild record re-reads its display from
// each lure GMO; a species change clears size and moves, and with the weather
// unchanged nothing else would fill them.
func TestUpdateFromMapRefillsSizeAndMovesAfterADisplayChange(t *testing.T) {
	const speciesA, speciesB = 7001, 7003
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			speciesA: {QuickMoves: []int{500}, ChargedMoves: []int{600}},
			speciesB: {QuickMoves: []int{510}, ChargedMoves: []int{610}},
		},
	})
	const encId = uint64(8429844873227918432)
	raw := func(species int32) RawMapPokemonData {
		r := testRawMapPokemon(encId, testFortIdStr(930501), 48.8584, 2.2945, time.Now().UnixMilli()+600_000)
		r.Data.PokedexTypeId = species
		return r
	}

	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(encId)
	p.updateFromMap(context.Background(), db.DbDetails{}, raw(speciesA), nil, "tester")
	if !p.AtkIv.Valid || int64(p.Move1.ValueOrZero()) != 500 {
		t.Fatalf("fixture assumption violated: first lure sighting should derive IVs and move 500, got atk=%v move1=%v", p.AtkIv, p.Move1)
	}
	p.newRecord = false

	p.updateFromMap(context.Background(), db.DbDetails{}, raw(speciesB), nil, "tester")

	if m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero()); m1 != 510 || m2 != 610 {
		t.Errorf("moves after the species change = %v/%v, want the new species' 510/610", p.Move1, p.Move2)
	}
	if !p.Size.Valid {
		t.Error("size left empty after the display change")
	}
}
