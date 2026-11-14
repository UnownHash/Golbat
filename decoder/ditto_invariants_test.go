package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"golbat/config"
	"golbat/db"
	"golbat/derive/encounter"
	"golbat/pogo"
)

// The evidence-based Ditto detection (detectDitto) must be completely
// independent of derivation. It reads cross-boost scan history written by real encounters and
// reaches its verdict from levels alone, so turning derive_iv off must change
// nothing about it — an operator who turns derivation off still gets Ditto
// detection.
//
// This drives the ">B0" confirmation: two stored scans, and a third sighting
// whose disguise IS weather-boosted while the level did not move. Whatever is
// underneath is not boosted by this weather, so it is not the displayed
// species.
func TestDetectDittoConfirmsFromCrossBoostEvidenceWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, false)

	const disguise = int16(56)
	const unboostedLevel = int32(20)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:        Uint64Str(123456789),
			PokemonId: disguise,
		},
		scanHistory: []*pokemonScan{
			{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
				Level: unboostedLevel, Attack: 10, Defense: 10, Stamina: 10},
			{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_RAINY),
				Level: unboostedLevel + 5, Attack: 10, Defense: 10, Stamina: 10},
		},
	}

	// Disguise reported as boosted, but the level did not rise with it.
	scan := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_RAINY),
		Level: unboostedLevel, Attack: 10, Defense: 10, Stamina: 10,
	}

	if _, err := p.detectDitto(scan); err != nil {
		t.Fatalf("detectDitto returned an error with derivation off: %s", err)
	}
	if !p.IsDitto {
		t.Fatal("detectDitto did not confirm a Ditto from cross-boost evidence — " +
			"turning derive_iv off must not disable the evidence-based detection")
	}
	if p.PokemonId != int16(pogo.HoloPokemonId_DITTO) {
		t.Errorf("PokemonId = %d, want %d (DITTO)", p.PokemonId, pogo.HoloPokemonId_DITTO)
	}
	if got := int16(p.DisplayPokemonId.ValueOrZero()); got != disguise {
		t.Errorf("DisplayPokemonId = %d, want %d (the disguise)", got, disguise)
	}
	if p.dittoRollMarked {
		t.Error("dittoRollMarked set by an evidence-based confirmation — the provenance flag " +
			"must distinguish detectDitto's own verdict from a roll prediction")
	}
}

// The evidence-based detection must also be able to REFUTE with derivation off.
// A confirmation that cannot be taken back is worse than none.
func TestDetectDittoRefutesWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, false)

	const disguise = int16(56)
	const unboostedLevel = int32(20)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(123456789),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		scanHistory: []*pokemonScan{
			{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
				Level: unboostedLevel, Attack: 10, Defense: 10, Stamina: 10},
		},
	}

	// Unboosted display, partly cloudy cell: a real Ditto would have been
	// boosted and shown unboostedLevel+5. It shows unboostedLevel, so it is
	// the disguise's own stats.
	scan := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
		CellWeather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
		Level:       unboostedLevel, Attack: 10, Defense: 10, Stamina: 10,
	}

	if _, err := p.detectDitto(scan); err != nil {
		t.Fatalf("detectDitto returned an error with derivation off: %s", err)
	}
	if p.IsDitto {
		t.Fatal("detectDitto did not refute the Ditto from contradicting evidence with derive_iv off")
	}
	if p.PokemonId != disguise {
		t.Errorf("PokemonId = %d, want %d (reverted to the disguise)", p.PokemonId, disguise)
	}
}

// With derivation off, a boost flip that finds no stored scan must fall back to
// clearing the stats, for a Ditto exactly as for anything else. Leaving the
// previous boost state's IVs and level in place would present values that are
// now wrong.
func TestRepopulateIvClearsRatherThanDerivesWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)

	t.Run("ordinary pokemon", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = Uint64Str(8429844873227918432)
		p.SetSeenType(SeenTypeCodeEncounter)
		p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
		p.calculateIv(6, 0, 11)
		p.SetLevel(null.IntFrom(5))

		p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

		if p.AtkIv.Valid || p.Level.Valid {
			t.Errorf("atk/level = %v/%v, want both cleared — with derive_iv off a flip with no "+
				"stored scan must clear, not keep the old boost state's values", p.AtkIv, p.Level)
		}
	})

	t.Run("ditto", func(t *testing.T) {
		p := &Pokemon{}
		p.Id = Uint64Str(8429844873227918432)
		p.SetIsDitto(true)
		p.SetDisplayPokemonId(null.IntFrom(56))
		p.SetForm(null.IntFrom(0))
		p.SetSeenType(SeenTypeCodeEncounter)
		// PP previously: partly cloudy over a disguise it also boosts, which
		// is the only state in which a caught Ditto is boosted.
		p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)))
		p.calculateIv(6, 0, 11)
		p.SetLevel(null.IntFrom(5))

		// Leaving PP: the caught Ditto is no longer boosted.
		p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

		if p.AtkIv.Valid || p.Level.Valid {
			t.Errorf("atk/level = %v/%v, want both cleared — a Ditto leaving its boost with "+
				"derive_iv off must clear, not keep boosted values", p.AtkIv, p.Level)
		}
	})
}

// ditto_derivation and derive_iv are separate switches, and the roll writes two
// different kinds of thing: an IDENTITY (this record is a Ditto) and derived
// STATS. With derivation off only the identity may be written — every derived
// write is gated at applyDerivedValues, and the moves are gated too.
//
// The record then looks like any unencountered Ditto: known species, no
// stats. That is honest, and a later real encounter still resolves
// it through detectDitto.
func TestMaybeMarkDittoFromRollWritesNoStatsWithDerivationOff(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)
	withDeriveIv(t, false)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}

	p.maybeMarkDittoFromRoll()

	if !p.IsDitto {
		t.Fatal("the roll must still mark the identity with derive_iv off — ditto_derivation is its own switch")
	}
	if p.AtkIv.Valid || p.Level.Valid {
		t.Errorf("atk/level = %v/%v, want both unset — derive_iv off must write no derived stats",
			p.AtkIv, p.Level)
	}
	if p.Move1.Valid || p.Move2.Valid {
		t.Errorf("moves = %v/%v, want both unset with derive_iv off", p.Move1, p.Move2)
	}
}

// The whole rule on one record, through the real wild sighting path: the
// columns a map client reads describe the DISGUISE, and the stats describe the
// Ditto you would actually CATCH.
//
// Species 56 is Fighting-typed and PARTLY_CLOUDY boosts Normal and Rock, so in
// a partly cloudy cell the game reports no boost for this disguise. That is
// the 0P state, and it is the one that discriminates: the encounter would
// REPORT boosted stats, because the Ditto underneath is Normal-typed, but a
// caught Ditto is boosted only in PP — so the record must hold the UNBOOSTED
// derivation. A rule keyed on the cell alone writes the boosted one here.
func TestDittoRecordSeparatesDisguiseWeatherFromDittoStats(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)
	withDeriveIv(t, true)

	const spawnID = int64(0x47b973e0595)
	seedSyncedSpawnpoint(t, spawnID)

	const lat, lon = 51.5, -0.1
	weather := map[int64]pogo.GameplayWeatherProto_WeatherCondition{
		weatherCellIdFromLatLon(lat, lon): pogo.GameplayWeatherProto_PARTLY_CLOUDY,
	}

	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(eidRollFires)
	wild := &pogo.WildPokemonProto{
		EncounterId:  eidRollFires,
		SpawnPointId: "47b973e0595",
		Latitude:     lat,
		Longitude:    lon,
		Pokemon: &pogo.PokemonProto{
			PokemonId: pogo.HoloPokemonId(dittoRollTestPool16),
			// The game reports the DISGUISE's boost condition, and partly
			// cloudy does not boost a Fighting type.
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form:                    pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16),
				WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE,
			},
		},
	}

	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, weather, 1700000000000, "tester")

	if !p.IsDitto {
		t.Fatal("expected the roll to mark this record as a Ditto")
	}
	if got := int16(p.DisplayPokemonId.ValueOrZero()); got != dittoRollTestPool16 {
		t.Errorf("DisplayPokemonId = %d, want %d (the disguise)", got, dittoRollTestPool16)
	}

	// The disguise's half: the weather column is what the game reported, so a
	// client can match the sighting it can see.
	if got := int64OrZero(p.Weather); got != int64(pogo.GameplayWeatherProto_NONE) {
		t.Errorf("weather = %d, want %d (NONE, the DISGUISE's condition)",
			got, pogo.GameplayWeatherProto_NONE)
	}

	// The Ditto's half: what you would catch, which in 0P is the UNBOOSTED
	// set even though the cell is partly cloudy.
	want := encounter.For(eidRollFires, false, 0).Values
	boosted := encounter.For(eidRollFires, true, 0).Values
	if want == boosted {
		t.Fatal("this encounter id derives identically for both boost states, so the " +
			"assertions below could not tell a cell-keyed rule from a PP-keyed one")
	}
	if got := p.AtkIv.ValueOrZero(); got != uint8(want.Attack) {
		t.Errorf("atk = %d, want %d (the UNBOOSTED derivation — a caught Ditto is boosted "+
			"only in PP, and this is 0P)", got, want.Attack)
	}
	if got := p.Level.ValueOrZero(); got != uint8(want.Level) {
		t.Errorf("level = %d, want %d (unboosted)", got, want.Level)
	}
}

// An encounter publishes the CAUGHT Ditto, which is not what the encounter
// reported.
//
// The ">0P" confirmation is where the two visibly differ. detectDitto hands
// back the stored UNBOOSTED scan as caughtIv while the encounter itself
// observed the boosted set — because a Ditto under a disguise that partly
// cloudy does not boost reports boosted stats and is caught unboosted. Taking
// the observation would publish IVs and a level five too high.
//
// This holds with derivation on or off, and that is the point: detectDitto
// recovered the caught set from real observations, so it is definitive and the
// derivation is not consulted. Derivation only fills in where nothing observed
// the caught set at all.
func TestEncounterPublishesTheCaughtDittoNotWhatItReported(t *testing.T) {
	const disguise = 56

	// addEncounterPokemon derives the level from cp_multiplier through a
	// PIECEWISE function, so the fixture takes its levels from that function
	// rather than naming a number.
	const cpMultiplier = float32(0.8)
	boostedLevel := cpMultiplierToLevel(cpMultiplier)
	unboostedLevel := boostedLevel - 5

	newRecord := func() *Pokemon {
		p := &Pokemon{
			PokemonData: PokemonData{
				Id:        Uint64Str(123456789),
				PokemonId: int16(disguise),
			},
			scanHistory: []*pokemonScan{
				{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
					Level: unboostedLevel, Attack: 1, Defense: 2, Stamina: 3},
				{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_RAINY),
					Level: boostedLevel, Attack: 1, Defense: 2, Stamina: 3},
			},
		}
		p.SetSeenType(SeenTypeCodeEncounter)
		p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
		return p
	}
	proto := func() *pogo.PokemonProto {
		return &pogo.PokemonProto{
			PokemonId:         pogo.HoloPokemonId(disguise),
			IndividualAttack:  11,
			IndividualDefense: 12,
			IndividualStamina: 13,
			CpMultiplier:      cpMultiplier,
			PokemonDisplay:    &pogo.PokemonDisplayProto{},
		}
	}

	for _, derive := range []bool{true, false} {
		name := "derivation off"
		if derive {
			name = "derivation on"
		}
		t.Run(name, func(t *testing.T) {
			withDeriveIv(t, derive)
			p := newRecord()

			p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto(), "")

			if !p.IsDitto {
				t.Fatal("the \">0P\" branch should have confirmed a Ditto — the fixture no " +
					"longer reaches the case this test is about")
			}
			// The same answer either way, and it is the OBSERVED one:
			// detectDitto recovered the caught set from real history, so the
			// derivation has nothing to add and does not get asked. Never the
			// 11/12/13 this encounter reported, which in 0P is the seen set.
			wantAtk, wantLevel := uint8(1), uint8(unboostedLevel)
			if got := p.AtkIv.ValueOrZero(); got != wantAtk {
				t.Errorf("atk = %d, want %d (the caught set). 11 is what the encounter REPORTED, "+
					"which for a Ditto in 0P is the boosted set it is not caught with", got, wantAtk)
			}
			if got := p.Level.ValueOrZero(); got != wantLevel {
				t.Errorf("level = %d, want %d (the caught level)", got, wantLevel)
			}
		})
	}
}

// withOhbem gives a test a real CP/PVP calculator. Other tests in this package
// rely on ohbem being nil, and none of them run in parallel, so it is restored
// on cleanup — the same contract TestReloadOhbemFromMasterFile_ConcurrentQuery
// already relies on.
func withOhbem(t *testing.T) {
	t.Helper()
	oldPvp := config.Config.Pvp
	config.Config.Pvp.Enabled = true
	config.Config.Pvp.LevelCaps = []int{50, 51}
	InitialiseOhbem()
	if ohbem.Load() == nil {
		t.Fatal("InitialiseOhbem did not initialise ohbem")
	}
	t.Cleanup(func() {
		config.Config.Pvp = oldPvp
		ohbem.Store(nil)
	})
}

// The CP half of the rule: a Ditto's CP describes the DISGUISE, because CP is
// what a map shows and the map shows the disguise. It is the one column that
// cannot be derived from the stats beside it — those belong to the Ditto — so
// nothing downstream can reconstruct it if this is wrong.
//
// The 0P state is the discriminating one. The Ditto is boosted and its disguise
// is not, so the record's own IVs and level are the BOOSTED Ditto's; computing
// CP from them would show a disguise that cannot exist at that CP. The unboosted
// scan is what the disguise would really have.
func TestRecomputeCpDescribesTheDisguiseNotTheDitto(t *testing.T) {
	withOhbem(t)
	o := ohbem.Load()

	const disguise = 56
	const lat, lon = 51.5, -0.1
	weather := map[int64]pogo.GameplayWeatherProto_WeatherCondition{
		weatherCellIdFromLatLon(lat, lon): pogo.GameplayWeatherProto_PARTLY_CLOUDY,
	}

	unboosted := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level: 20, Attack: 10, Defense: 11, Stamina: 12,
	}

	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(123456789),
			IsDitto:          true,
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			Lat:              lat,
			Lon:              lon,
		},
		scanHistory: []*pokemonScan{unboosted},
	}
	p.SetDisplayPokemonForm(null.IntFrom(0))
	// The record carries the BOOSTED Ditto's stats, per the rule.
	p.calculateIv(15, 15, 15)
	p.SetLevel(null.IntFrom(25))
	// And the DISGUISE's weather, which in 0P reads as no boost.
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	p.recomputeCpIfNeeded(context.Background(), db.DbDetails{}, weather)

	if !p.Cp.Valid {
		t.Fatal("no CP computed for a Ditto in the 0P state")
	}

	wantCp, err := o.CalculateCp(disguise, 0, 0,
		int(unboosted.Attack), int(unboosted.Defense), int(unboosted.Stamina), float64(unboosted.Level))
	if err != nil {
		t.Fatalf("CalculateCp: %v", err)
	}
	if got := int(p.Cp.ValueOrZero()); got != wantCp {
		t.Errorf("Cp = %d, want %d — CP must describe the DISGUISE, computed from the unboosted "+
			"scan, not from the boosted Ditto stats stored beside it", got, wantCp)
	}

	// The two ways of getting it wrong, named so a failure says which.
	if fromDitto, err := o.CalculateCp(int(pogo.HoloPokemonId_DITTO), 0, 0, 15, 15, 15, 25); err == nil {
		if int(p.Cp.ValueOrZero()) == fromDitto {
			t.Error("Cp was computed for species 132 — the map shows the disguise, not the Ditto")
		}
	}
	if fromBoosted, err := o.CalculateCp(disguise, 0, 0, 15, 15, 15, 25); err == nil {
		if int(p.Cp.ValueOrZero()) == fromBoosted {
			t.Error("Cp was computed from the record's own boosted Ditto stats — the 0P override " +
				"exists so the disguise is shown at a CP it could actually have")
		}
	}
}

// The same function on an ordinary pokemon computes for its own species from
// its own stats. Without this the test above passes just as well against an
// implementation that always reads the unboosted scan.
func TestRecomputeCpUsesTheRecordsOwnSpeciesWhenNotADitto(t *testing.T) {
	withOhbem(t)
	o := ohbem.Load()

	const species = 56
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(987654321), PokemonId: species, Lat: 51.5, Lon: -0.1}}
	p.SetForm(null.IntFrom(0))
	p.calculateIv(15, 14, 13)
	p.SetLevel(null.IntFrom(30))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	p.recomputeCpIfNeeded(context.Background(), db.DbDetails{}, nil)

	wantCp, err := o.CalculateCp(species, 0, 0, 15, 14, 13, 30)
	if err != nil {
		t.Fatalf("CalculateCp: %v", err)
	}
	if got := int(p.Cp.ValueOrZero()); got != wantCp {
		t.Errorf("Cp = %d, want %d (own species, own stats)", got, wantCp)
	}
}

// A boosted Ditto whose first encounter arrives with no cell weather must
// still be filed under the boosted state. Filed as unboosted, its own recorded
// level matches the next identical encounter, which reaches the 0N branch and
// clears a real Ditto — so the cost of getting this wrong is the record, not
// just its precision.
//
// Nothing here supplies the cell: the fixture's weather is the disguise's, and
// no weather record exists. Only the observed values can answer, which is what
// dittoBoostFromObservedValues reads them for.
func TestEncounterFilesABoostedDittoCorrectlyWithNoCellWeather(t *testing.T) {
	withDeriveIv(t, true)

	const eid = eidRollFires
	boosted := encounter.For(eid, true, 0).Values
	unboosted := encounter.For(eid, false, 0).Values
	if boosted == unboosted {
		t.Fatal("this encounter id derives identically for both boost states, so no observation " +
			"could distinguish them and the inference under test is undecidable")
	}

	// The level reaches the scan through cp_multiplier's piecewise function,
	// so it has to be found rather than named.
	var cpMultiplier float32
	for cm := float32(0.1); cm < 0.9; cm += 0.0001 {
		if cpMultiplierToLevel(cm) == boosted.Level {
			cpMultiplier = cm
			break
		}
	}
	if cpMultiplier == 0 {
		t.Fatalf("no cp_multiplier produces level %d", boosted.Level)
	}

	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(eid),
			IsDitto:          true,
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
			DisplayPokemonId: null.ValueFrom(uint16(56)),
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)
	// The DISGUISE reads as unboosted, which is what a Fighting type does in
	// partly cloudy weather. Nothing here says what the cell actually is.
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	// The observed values are the BOOSTED Ditto's.
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(56),
		IndividualAttack:  boosted.Attack,
		IndividualDefense: boosted.Defense,
		IndividualStamina: boosted.Stamina,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}, "")

	if !p.IsDitto {
		t.Fatal("the roll-marked Ditto was reset by its first encounter")
	}
	if len(p.scanHistory) != 1 {
		t.Fatalf("scanHistory has %d entries, want 1", len(p.scanHistory))
	}
	if got := p.scanHistory[0].Weather; got != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
		t.Errorf("stored scan filed under weather %d, want %d (PARTLY_CLOUDY). Filed as unboosted, "+
			"the next identical encounter matches this level, reaches 0N and clears a real Ditto",
			got, pogo.GameplayWeatherProto_PARTLY_CLOUDY)
	}
}

// Hydration must not resurrect a mark that was deliberately cleared. The
// stored bytes still say roll-marked and the history is still empty, so
// everything populateInternal looks at says "restore it" — only the live
// IsDitto says otherwise. See its own comment there for why that wins.
func TestHydrationDoesNotResurrectAClearedRollMark(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(eidRollFires)
	p.markDittoFromRoll("roll", nil, nil)
	p.rewriteGolbatInternal()
	if !p.dittoRollMarked || len(p.GolbatInternal) == 0 {
		t.Fatal("fixture assumption violated: the record must be roll-marked and serialised")
	}

	// A display change clears the identity — and with it the provenance.
	p.SetIsDitto(false)
	if p.dittoRollMarked {
		t.Fatal("SetIsDitto(false) must clear dittoRollMarked at its single choke point")
	}
	// Still-empty history, still-stale bytes: exactly what hydration sees.
	p.scanHistory = nil

	p.populateInternal()

	if p.dittoRollMarked {
		t.Error("hydration restored a roll-mark that was cleared; the live IsDitto is the " +
			"authority, not the bytes written before it changed")
	}
}

// A roll-mark is a prediction, and its first encounter can refute it. In
// partly cloudy weather a real Ditto is seen boosted, and a boosted roll
// cannot produce a level at or below 5 or any IV below 4. Such values are
// positive evidence against the prediction — not the absence of evidence the
// keep-branch exists to tolerate — so the mark must go.
//
// Filed as boosted history instead, the observation becomes the expectation:
// later identical encounters match it and keep agreeing with a Ditto that was
// never there. A species left in ditto_pool after it stops being eligible is
// how this arises.
func TestDetectDittoDropsARollMarkTheFirstEncounterContradicts(t *testing.T) {
	const disguise = int16(56)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(123456789),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		dittoRollMarked: true,
	}

	// Partly cloudy cell, so a real Ditto would be seen boosted — but an IV
	// of 2 is below the boosted floor of 4.
	scan := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
		CellWeather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
		Level:       20, Attack: 2, Defense: 10, Stamina: 10,
	}
	if !scan.MustBeUnboosted() {
		t.Fatal("fixture assumption violated: these values must be impossible for a boosted roll")
	}

	_, _ = p.detectDitto(scan)

	if p.IsDitto {
		t.Error("the roll-mark survived an observation that refutes it — a boosted Ditto " +
			"cannot have an IV below 4, so this is evidence against the prediction")
	}
	if p.dittoRollMarked {
		t.Error("dittoRollMarked must clear with IsDitto at its single choke point")
	}
	if p.PokemonId != disguise {
		t.Errorf("PokemonId = %d, want %d (reverted to the disguise)", p.PokemonId, disguise)
	}
}

// A roll-marked Ditto's FIRST encounter, in 0P — where what the encounter
// reports and what you catch come apart.
//
// detectDitto has no history, so it takes its keep branch and hands back this
// encounter's own scan. That scan is the SEEN set — boosted, because the Ditto
// under the disguise is Normal-typed and the cell is partly cloudy — and the
// Ditto you catch there is unboosted. Publishing the scan puts IVs and a level
// five too high on the record, and overwrites the correct caught values the
// roll had already derived a moment earlier.
//
// detectDitto's caught set is nil in 0P with no history, so with derivation
// on the caught one is taken from the encounter id.
func TestEncounterKeepsTheCaughtSetForARollMarkedDittoIn0P(t *testing.T) {
	withDeriveIv(t, true)

	const disguise = 56
	seen := encounter.For(eidRollFires, true, 0).Values    // what 0P reports
	caught := encounter.For(eidRollFires, false, 0).Values // what you catch there
	if seen == caught {
		t.Fatal("this encounter id derives identically for both branches, so the assertions " +
			"below could not tell the seen set from the caught one")
	}

	var cpMultiplier float32
	for cm := float32(0.1); cm < 0.9; cm += 0.0001 {
		if cpMultiplierToLevel(cm) == seen.Level {
			cpMultiplier = cm
			break
		}
	}
	if cpMultiplier == 0 {
		t.Fatalf("no cp_multiplier produces the seen level %d", seen.Level)
	}

	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(eidRollFires),
			IsDitto:          true,
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)
	// 0P: the game reports no boost for this disguise.
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(disguise),
		IndividualAttack:  seen.Attack,
		IndividualDefense: seen.Defense,
		IndividualStamina: seen.Stamina,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}, "")

	if !p.IsDitto {
		t.Fatal("the roll-marked Ditto was reset by its first encounter")
	}
	if got := p.AtkIv.ValueOrZero(); got != uint8(caught.Attack) {
		t.Errorf("atk = %d, want %d (the caught set). %d is what this encounter reported, which "+
			"in 0P is the boosted set the Ditto is not caught with", got, caught.Attack, seen.Attack)
	}
	if got := p.Level.ValueOrZero(); got != uint8(caught.Level) {
		t.Errorf("level = %d, want %d (the caught level); %d is the reported one",
			got, caught.Level, seen.Level)
	}
}

// ohbem indexes its CP multiplier table at level*2-2, so level 0 panics with
// "index out of range [-2]". Nothing in the decode path recovers, so that
// takes the goroutine down rather than losing one CP.
//
// Zero is reachable without anything looking wrong: SetLevel clamps into
// uint8, so an adjustment below 1 stores a VALID 0 rather than an absent one
// and passes recomputeCpIfNeeded's Valid checks.
func TestRecomputeCpSurvivesALevelClampedToZero(t *testing.T) {
	withOhbem(t)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(987654321), PokemonId: 220, Lat: 51.5, Lon: -0.1}}
	p.SetForm(null.IntFrom(0))
	p.calculateIv(15, 15, 15)
	// What a boost adjustment below 1 leaves behind: clamped, and still Valid.
	p.SetLevel(null.IntFrom(-3))
	if !p.Level.Valid || p.Level.ValueOrZero() != 0 {
		t.Fatalf("fixture assumption violated: level = %v, want a valid 0", p.Level)
	}

	// Must not panic.
	p.recomputeCpIfNeeded(context.Background(), db.DbDetails{}, nil)

	if p.Cp.Valid {
		t.Errorf("Cp = %d for a level-0 record; there is no such level, so no CP is the "+
			"honest answer", p.Cp.ValueOrZero())
	}
}

// With derivation off, the caught set has only one source: detectDitto. So the
// roll-marked branch has to honour caughtIv's contract rather than hand back
// what the encounter reported.
//
// In 0P the encounter reports the SEEN set — boosted — and the Ditto is caught
// unboosted. Returning the scan there publishes a level five too high; nil is
// the honest answer, and the caller's existing 0P handling does the rest.
func TestRollMarkedDittoIn0PPublishesTheCaughtLevelWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, true)

	const disguise = 56
	// A boosted level, reached through cp_multiplier's piecewise function.
	const wantSeen = int32(35)
	var cpMultiplier float32
	for cm := float32(0.1); cm < 0.95; cm += 0.0001 {
		if cpMultiplierToLevel(cm) == wantSeen {
			cpMultiplier = cm
			break
		}
	}
	if cpMultiplier == 0 {
		t.Fatalf("no cp_multiplier produces level %d", wantSeen)
	}

	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(950001),
			IsDitto:          true,
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			Lat:              51.5,
			Lon:              -0.1,
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))

	// 0P needs a partly cloudy CELL. addEncounterPokemon reads that from the
	// weather cache, and with derivation off there is no other source — the
	// observed-values inference is gated on it.
	cellId := weatherCellIdFromLatLon(p.Lat, p.Lon)
	w, _ := weatherCache.GetOrSetFunc(cellId, func() *Weather { return &Weather{Id: cellId} })
	w.GameplayCondition = null.IntFrom(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))
	w.UpdatedMs = time.Now().UnixMilli() // this hour's weather
	weatherCache.Set(cellId, w, time.Minute)
	t.Cleanup(func() { weatherCache.Delete(cellId) })

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(disguise),
		IndividualAttack:  15,
		IndividualDefense: 15,
		IndividualStamina: 15,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}, "")

	if !p.IsDitto {
		t.Fatal("the roll-mark must survive a first encounter with no history")
	}
	if got := p.Level.ValueOrZero(); got != uint8(wantSeen-5) {
		t.Errorf("level = %d, want %d. %d is what the encounter REPORTED, which in 0P is the "+
			"boosted set the Ditto is not caught with", got, wantSeen-5, wantSeen)
	}
	if p.AtkIv.Valid {
		t.Errorf("AtkIv = %v, want unset: with derivation off the Ditto's unboosted IVs are "+
			"not recoverable from a single boost state", p.AtkIv)
	}
}

// The mirror of the sub-floor refutation. Outside partly cloudy a Ditto is
// never boosted, so an observation that can only be boosted refutes the
// prediction just as surely.
func TestDetectDittoDropsARollMarkBoostedOutsidePartlyCloudy(t *testing.T) {
	const disguise = int16(56)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(950002),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		dittoRollMarked: true,
	}

	// Rainy cell, so a real Ditto is unboosted — but level 33 can only be a
	// boosted roll.
	scan := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
		CellWeather: int32(pogo.GameplayWeatherProto_RAINY),
		Level:       33, Attack: 10, Defense: 10, Stamina: 10,
	}
	if !scan.MustBeBoosted() {
		t.Fatal("fixture assumption violated: these values must be impossible for an unboosted roll")
	}

	_, _ = p.detectDitto(scan)

	if p.IsDitto {
		t.Error("the roll-mark survived an observation that refutes it — outside partly cloudy " +
			"a Ditto cannot be boosted, so a boosted-only level is evidence against it")
	}
	if p.PokemonId != disguise {
		t.Errorf("PokemonId = %d, want %d (reverted to the disguise)", p.PokemonId, disguise)
	}
}

// ohbem is built for CP whenever derivation is on, so its presence does not
// mean PVP was asked for. An operator who turns PVP off must not get ranks
// computed, cached, persisted or served.
func TestPvpDisabledProducesNoRanksEvenWithOhbemBuiltForCp(t *testing.T) {
	withOhbem(t) // leaves Pvp.Enabled true
	prev := config.Config.Pvp.Enabled
	config.Config.Pvp.Enabled = false
	t.Cleanup(func() { config.Config.Pvp.Enabled = prev })

	if ohbem.Load() == nil {
		t.Fatal("fixture assumption violated: ohbem must be present, as it is with derive_iv on")
	}

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(950003), PokemonId: 220}}
	p.SetForm(null.IntFrom(0))
	p.calculateIv(15, 15, 15)
	p.SetLevel(null.IntFrom(20))

	if got := buildApiPvpRankings(p); len(got.Little)+len(got.Great)+len(got.Ultra) != 0 {
		t.Errorf("PVP ranks returned with pvp.enabled false: %+v", got)
	}
}

// PP is not 0P, and only one of them lacks an observed caught set.
//
// The cell being partly cloudy makes the Ditto SEEN boosted either way. What
// separates them is whether the weather boosts the DISGUISE too: in PP it
// does, so a caught Ditto is boosted and the observed stats already ARE the
// caught set. Returning no caught set there makes the caller subtract five
// levels and clear the IVs — discarding data the game just handed us.
func TestRollMarkedDittoInPPKeepsTheObservedStatsWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, true)

	const disguise = 56
	const wantLevel = int32(30)
	var cpMultiplier float32
	for cm := float32(0.1); cm < 0.95; cm += 0.0001 {
		if cpMultiplierToLevel(cm) == wantLevel {
			cpMultiplier = cm
			break
		}
	}
	if cpMultiplier == 0 {
		t.Fatalf("no cp_multiplier produces level %d", wantLevel)
	}

	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(970001),
			IsDitto:          true,
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			Lat:              51.5,
			Lon:              -0.1,
		},
		dittoRollMarked: true,
	}
	p.SetSeenType(SeenTypeCodeEncounter)
	// PP: the game reports a partly cloudy boost for the DISGUISE as well.
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY)))

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(disguise),
		IndividualAttack:  11,
		IndividualDefense: 12,
		IndividualStamina: 13,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay: &pogo.PokemonDisplayProto{
			WeatherBoostedCondition: pogo.GameplayWeatherProto_PARTLY_CLOUDY,
		},
	}, "")

	if !p.IsDitto {
		t.Fatal("the roll-mark must survive a first encounter with no history")
	}
	if got := p.Level.ValueOrZero(); got != uint8(wantLevel) {
		t.Errorf("level = %d, want %d (as observed). %d is the 0P answer — five levels off "+
			"data the encounter actually reported", got, wantLevel, wantLevel-5)
	}
	if got := p.AtkIv.ValueOrZero(); got != 11 {
		t.Errorf("atk = %d, want 11 (as observed); the IVs must not be cleared in PP", got)
	}
}

// A stored expiry on a DIFFERENT phase must still be corrected — that is what
// applyVerifiedDespawn is for, and its late-sighting guard, which holds only a
// passed expiry on the same phase, must not swallow it.
func TestVerifiedDespawnStillCorrectsAStoredExpiryOnAnotherPhase(t *testing.T) {
	const despawnSecond = 1500
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	correct := base.Unix() - int64(despawnSecondOfHour(base.Unix())) + int64(despawnSecond)

	p := &Pokemon{}
	p.FirstSeenTimestamp = uint32(correct - 600)
	// A verified expiry half an hour early, already passed: a different
	// second-of-hour.
	p.SetExpireTimestamp(null.IntFrom(correct - 1800))
	p.SetExpireTimestampVerified(true)

	p.applyVerifiedDespawn(despawnSecond, (correct-300)*1000)

	if got := int64(p.ExpireTimestamp.ValueOrZero()); got != correct {
		t.Errorf("expiry = %d, want %d — a stored expiry on a different phase is exactly what "+
			"this function exists to replace", got, correct)
	}
}

// A ditto_pool entry names a species and exactly one form, because a Ditto
// only ever wears a species' NORMAL form. Keying on species alone would let
// the roll fire on 2% of the variants too, and nothing downstream could tell
// those marks from real ones.
func TestDittoPoolEntryParsing(t *testing.T) {
	const (
		mankey         = int16(56)
		mankeyNormal   = int16(999) // MANKEY_NORMAL
		growlithe      = int16(58)
		growlitheNorm  = int16(280)  // GROWLITHE_NORMAL
		growlitheHisui = int16(2792) // GROWLITHE_HISUIAN
	)

	t.Run("a bare species means its normal form", func(t *testing.T) {
		got, err := parseDittoPoolEntry("56")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.pokemonId != mankey || got.form != mankeyNormal {
			t.Errorf("got %+v, want species %d form %d", got, mankey, mankeyNormal)
		}
	})

	t.Run("an explicit form is taken as given", func(t *testing.T) {
		got, err := parseDittoPoolEntry("58:2792")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.pokemonId != growlithe || got.form != growlitheHisui {
			t.Errorf("got %+v, want species %d form %d", got, growlithe, growlitheHisui)
		}
	})

	// The reason the form is in the key at all.
	t.Run("a variant is not the disguise a bare entry admits", func(t *testing.T) {
		withDittoPool(t, growlithe)
		if !inDittoPool(growlithe, growlitheNorm) {
			t.Error("the normal form must be a member")
		}
		if inDittoPool(growlithe, growlitheHisui) {
			t.Error("a Hisuian Growlithe is not a Ditto disguise, and a bare entry must not admit it")
		}
	})

	t.Run("an explicit variant entry admits only that form", func(t *testing.T) {
		withDittoPoolEntries(t, "58:2792")
		if !inDittoPool(growlithe, growlitheHisui) {
			t.Error("an explicitly named form must be a member")
		}
		if inDittoPool(growlithe, growlitheNorm) {
			t.Error("naming one form must not admit the others")
		}
	})

	// A species whose forms are all variants has no NORMAL to resolve to, so
	// a bare entry for one names no form at all. Defaulting it to 0 builds a
	// pool entry that can never match, because the game always sends one of
	// the real forms — and it does so without saying anything. Lycanroc is
	// MIDDAY/MIDNIGHT/DUSK; Squawkabilly is GREEN/BLUE/YELLOW/WHITE, and a
	// real GMO showed one sent as SQUAWKABILLY_YELLOW.
	t.Run("a bare species with no normal form is refused", func(t *testing.T) {
		for _, id := range []string{"745", "931"} {
			got, err := parseDittoPoolEntry(id)
			if err == nil {
				t.Errorf("parse(%q) = %+v, want an error: the species has no NORMAL form, so this "+
					"entry would sit in the pool matching nothing", id, got)
			}
		}
	})

	// Naming the form is the way to add one of those.
	t.Run("naming the form admits a species that has no normal form", func(t *testing.T) {
		got, err := parseDittoPoolEntry("931:2987") // SQUAWKABILLY_YELLOW
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.pokemonId != 931 || got.form != 2987 {
			t.Errorf("got %+v, want species 931 form 2987", got)
		}
	})

	// The two species the game gives no forms at all. Form 0 is what it sends
	// for them, so a bare entry is the right spelling and must still work.
	t.Run("a species with no forms at all means form 0", func(t *testing.T) {
		for _, id := range []int16{29, 32} { // Nidoran female, Nidoran male
			form, ok := normalFormOf(id)
			if !ok || form != 0 {
				t.Errorf("normalFormOf(%d) = %d,%v; want 0,true — the game sends form 0 for a "+
					"species with no forms", id, form, ok)
			}
		}
	})

	// One species name is a prefix of another, so a plain prefix match would
	// lend PORYGON_Z's forms to PORYGON. Both resolve to their own NORMAL.
	t.Run("a species whose name prefixes another keeps its own forms", func(t *testing.T) {
		for _, tc := range []struct {
			id   int16
			want string
		}{{137, "PORYGON_NORMAL"}, {474, "PORYGON_Z_NORMAL"}} {
			form, ok := normalFormOf(tc.id)
			if !ok || pogo.PokemonDisplayProto_Form_name[int32(form)] != tc.want {
				t.Errorf("normalFormOf(%d) = %d (%s),%v; want %s", tc.id, form,
					pogo.PokemonDisplayProto_Form_name[int32(form)], ok, tc.want)
			}
		}
	})

	// The enum is compiled in, so it lags the game. A species the build has
	// never heard of can still be added by naming its form — only a BARE
	// entry needs the species to be known, because only a bare entry has to
	// look up a NORMAL form to mean.
	t.Run("an unknown species is admitted when the form is named", func(t *testing.T) {
		if _, err := parseDittoPoolEntry("9500"); err == nil {
			t.Error("a bare entry for an unknown species has no form to resolve and must be refused")
		}
		got, err := parseDittoPoolEntry("9500:1234")
		if err != nil {
			t.Fatalf("naming the form must admit a species newer than the compiled enum: %v", err)
		}
		if got.pokemonId != 9500 || got.form != 1234 {
			t.Errorf("got %+v, want species 9500 form 1234", got)
		}
	})

	t.Run("unparseable entries are rejected, not widened", func(t *testing.T) {
		for _, bad := range []string{"", "abc", "56:xyz", "0", "-4", "56:-1"} {
			if _, err := parseDittoPoolEntry(bad); err == nil {
				t.Errorf("parse(%q) succeeded; a pool entry nobody can read must be dropped "+
					"rather than silently admitting every form of something", bad)
			}
		}
	})

	// Plain TOML integers unmarshal to strings, so a bare-integer entry,
	// surrounding space and all, means the normal form.
	t.Run("a bare entry with surrounding space means the normal form", func(t *testing.T) {
		got, err := parseDittoPoolEntry(" 56 ")
		if err != nil || got.form != mankeyNormal {
			t.Errorf("got %+v err=%v, want species %d form %d", got, err, mankey, mankeyNormal)
		}
	})
}

// normalForm is normalFormOf's form alone, for fixtures that name a species
// known to have a NORMAL form. Tests that care about the "no NORMAL form"
// case call normalFormOf directly.
func normalForm(id int16) int16 {
	f, _ := normalFormOf(id)
	return f
}

// A weather-cache miss is not evidence. With derive_iv off, a roll-marked
// record's first encounter can arrive before its cell's weather is cached,
// leaving CellWeather at its NONE default. The keep branch would read that as
// "not partly cloudy" — where a level of 31-35 is impossible for a Ditto — and
// reset a genuine 0P Ditto seen boosted to its disguise. The observed values
// settle the cell's weather instead, as with derive_iv on: a roll-marked
// record rests on that derivation whichever way the flag is set.
//
// Through addEncounterPokemon, because the inference lives there and a
// direct detectDitto call cannot see whether it ran.
func TestRollMarkedDittoSurvivesAWeatherCacheMissWithDerivationOff(t *testing.T) {
	withDeriveIv(t, false)
	withDittoDerivation(t, true)

	// Boosted branch: level 34, 15/6/8. Unboosted: level 29, 12/0/9.
	const encId = uint64(8429844873227918443)
	const disguise = int16(56)
	const lat, lon = 51.5007, -0.1246

	newRecord := func() *Pokemon {
		p := &Pokemon{
			PokemonData: PokemonData{
				Id:               Uint64Str(encId),
				IsDitto:          true,
				DisplayPokemonId: null.ValueFrom(uint16(disguise)),
				PokemonId:        int16(pogo.HoloPokemonId_DITTO),
				Lat:              lat,
				Lon:              lon,
			},
			dittoRollMarked: true,
		}
		p.SetSeenType(SeenTypeCodeEncounter)
		p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE))) // unboosted display: 0P
		weatherCache.Delete(weatherCellIdFromLatLon(lat, lon))            // and nothing cached for the cell
		return p
	}
	encounter := func(atk, def, sta int32) *pogo.PokemonProto {
		return &pogo.PokemonProto{
			PokemonId:         pogo.HoloPokemonId(disguise),
			IndividualAttack:  atk,
			IndividualDefense: def,
			IndividualStamina: sta,
			CpMultiplier:      0.7527, // level 34: MustBeBoosted
			PokemonDisplay:    &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE},
		}
	}

	t.Run("the boosted branch's own values keep the mark", func(t *testing.T) {
		p := newRecord()
		p.addEncounterPokemon(context.Background(), db.DbDetails{}, encounter(15, 6, 8), "")

		if !p.IsDitto {
			t.Fatalf("a roll-marked Ditto seen at level 34 was reset to %d on a weather-cache miss: the miss was "+
				"read as 'not partly cloudy', where that level cannot occur", p.PokemonId)
		}
		if len(p.scanHistory) != 1 || p.scanHistory[0].Weather != int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
			t.Errorf("scan filed as %+v, want one scan filed as boosted (PARTLY_CLOUDY)", p.scanHistory)
		}
		// 0P with no stored unboosted set: the caught Ditto is five levels
		// down and its IVs are unknown.
		if got := p.Level.ValueOrZero(); got != 29 {
			t.Errorf("level = %d, want 29 (the 0P caught level)", got)
		}
		if p.AtkIv.Valid {
			t.Error("IVs kept from the seen set; in 0P with no history the caught set is unknown")
		}
	})

	// The inference only speaks when the values match one derived branch.
	// Values that match neither leave the weather unknown, and a level of
	// 34 then refutes — the inference must not blanket-suppress that.
	t.Run("values matching neither branch still refute", func(t *testing.T) {
		p := newRecord()
		p.addEncounterPokemon(context.Background(), db.DbDetails{}, encounter(1, 1, 1), "")

		if p.IsDitto {
			t.Fatal("a roll-marked record whose observed values match neither derived branch kept its mark " +
				"at a level a Ditto cannot have outside partly cloudy")
		}
		if p.PokemonId != disguise {
			t.Errorf("PokemonId = %d, want %d (reverted to the disguise)", p.PokemonId, disguise)
		}
	})
}

// Weather changes on the hour and the cache keeps an entry up to an hour, so
// just past the hour it can hold the previous one. That must not refute a
// roll-marked Ditto: a stale CLEAR reads as "not partly cloudy", where a level
// of 31-35 cannot occur for a Ditto. Treated as unknown, the observed values
// settle the cell's weather — here the boosted branch, so partly cloudy — and
// the mark stands. The same record with this hour's CLEAR is refuted, as it
// should be.
func TestStaleCachedWeatherDoesNotRefuteARollMarkedDitto(t *testing.T) {
	withDittoDerivation(t, true)

	// Boosted branch: level 34, 15/6/8.
	const encId = uint64(8429844873227918443)
	const disguise = int16(56)
	const lat, lon = 51.5007, -0.1246
	now := time.Now()
	hourStart := now.Truncate(time.Hour)

	for _, c := range []struct {
		name      string
		updated   time.Time
		wantDitto bool
	}{
		{"weather from the previous hour is not evidence", hourStart.Add(-time.Minute), true},
		{"this hour's weather refutes", hourStart.Add(time.Second), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if now.Sub(hourStart) < 5*time.Second {
				t.Skip("too close to the hour boundary to place the two weather records reliably")
			}
			cellId := weatherCellIdFromLatLon(lat, lon)
			weatherCache.Set(cellId, &Weather{Id: cellId, UpdatedMs: c.updated.UnixMilli(),
				GameplayCondition: null.IntFrom(int64(pogo.GameplayWeatherProto_CLEAR))}, time.Minute)
			t.Cleanup(func() { weatherCache.Delete(cellId) })

			p := &Pokemon{
				PokemonData: PokemonData{
					Id: Uint64Str(encId), IsDitto: true, PokemonId: int16(pogo.HoloPokemonId_DITTO),
					DisplayPokemonId: null.ValueFrom(uint16(disguise)), Lat: lat, Lon: lon,
				},
				dittoRollMarked: true,
			}
			p.SetSeenType(SeenTypeCodeEncounter)
			p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
			p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
				PokemonId:        pogo.HoloPokemonId(disguise),
				IndividualAttack: 15, IndividualDefense: 6, IndividualStamina: 8,
				CpMultiplier:   0.7527, // level 34: MustBeBoosted
				PokemonDisplay: &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE},
			}, "")

			if p.IsDitto != c.wantDitto {
				t.Errorf("is_ditto = %v, want %v", p.IsDitto, c.wantDitto)
			}
		})
	}
}

// A roll-marked record's first encounter refutes the mark when its stats
// follow the disguise's boost rather than the Ditto's. In rain over a
// disguise rain boosts, a Ditto is seen unboosted and anything else boosted;
// in partly cloudy weather over a disguise it does not boost, the reverse.
// Stats matching the disguise's branch are evidence the record is not a
// Ditto; where the two branches coincide, nothing is. Driven through addEncounterPokemon with the cell's weather in the
// cache, as production reads it.
func TestFirstEncounterRefutesARollMarkWhoseStatsFollowTheDisguise(t *testing.T) {
	withDeriveIv(t, true)
	const disguise = 56
	// Its unboosted stats are possible for a boosted roll too, so only the
	// branch comparison, not MustBeUnboosted, can refute the partly cloudy
	// case.
	const eid = uint64(8429844873227918446)

	for _, c := range []struct {
		name          string
		cell          pogo.GameplayWeatherProto_WeatherCondition
		reported      pogo.GameplayWeatherProto_WeatherCondition
		statsBoosted  bool
		wantStillMark bool
	}{
		{"rain, stats follow the disguise", pogo.GameplayWeatherProto_RAINY, pogo.GameplayWeatherProto_RAINY, true, false},
		{"rain, stats follow the Ditto", pogo.GameplayWeatherProto_RAINY, pogo.GameplayWeatherProto_RAINY, false, true},
		{"partly cloudy, stats follow the disguise", pogo.GameplayWeatherProto_PARTLY_CLOUDY, pogo.GameplayWeatherProto_NONE, false, false},
		// Clear over a disguise clear does not boost: Ditto and disguise are
		// both unboosted, so the stats cannot tell them apart.
		{"clear, both unboosted", pogo.GameplayWeatherProto_CLEAR, pogo.GameplayWeatherProto_NONE, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			stats := encounter.For(eid, c.statsBoosted, 0).Values
			other := encounter.For(eid, !c.statsBoosted, 0).Values
			if stats.Attack == other.Attack && stats.Level == other.Level {
				t.Fatal("fixture assumption violated: the two branches agree for this encounter id")
			}
			var cpMultiplier float32
			for cm := float32(0.1); cm < 0.9; cm += 0.0001 {
				if cpMultiplierToLevel(cm) == stats.Level {
					cpMultiplier = cm
					break
				}
			}
			if cpMultiplier == 0 {
				t.Fatalf("no cp_multiplier produces level %d", stats.Level)
			}

			p := &Pokemon{
				PokemonData: PokemonData{
					Id:               Uint64Str(eid),
					IsDitto:          true,
					PokemonId:        int16(pogo.HoloPokemonId_DITTO),
					DisplayPokemonId: null.ValueFrom(uint16(disguise)),
					Lat:              51.5,
					Lon:              -0.1,
				},
				dittoRollMarked: true,
			}
			p.SetSeenType(SeenTypeCodeEncounter)
			p.SetWeather(null.IntFrom(int64(c.reported)))

			cellId := weatherCellIdFromLatLon(p.Lat, p.Lon)
			w, _ := weatherCache.GetOrSetFunc(cellId, func() *Weather { return &Weather{Id: cellId} })
			w.GameplayCondition = null.IntFrom(int64(c.cell))
			w.UpdatedMs = time.Now().UnixMilli()
			weatherCache.Set(cellId, w, time.Minute)
			t.Cleanup(func() { weatherCache.Delete(cellId) })

			p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
				PokemonId:         pogo.HoloPokemonId(disguise),
				IndividualAttack:  stats.Attack,
				IndividualDefense: stats.Defense,
				IndividualStamina: stats.Stamina,
				CpMultiplier:      cpMultiplier,
				PokemonDisplay:    &pogo.PokemonDisplayProto{WeatherBoostedCondition: c.reported},
			}, "")

			if p.IsDitto != c.wantStillMark {
				t.Errorf("IsDitto = %v, want %v", p.IsDitto, c.wantStillMark)
			}
		})
	}
}
