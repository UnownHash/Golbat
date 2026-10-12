package decoder

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/derive/encounter"
	"golbat/pogo"

	"github.com/golang/geo/s2"
	"github.com/guregu/null/v6"
)

// withDittoDerivation sets config.Config.DittoDerivation for the duration of the test,
// restoring the pre-test value on cleanup. See withDittoPool
// (derive_validation_test.go) for why decoder package tests must set this
// explicitly: config.Config starts zero-valued in a test binary (no test
// calls config.ReadConfig()), so ditto_derivation's production default of true
// never applies here.
func withDittoDerivation(t *testing.T, enabled bool) {
	t.Helper()
	old := config.Config.DittoDerivation
	config.Config.DittoDerivation = enabled
	t.Cleanup(func() { config.Config.DittoDerivation = old })
}

// eidRollFires and eidRollDoesNotFire are the two reference encounter ids
// TestDittoSuspectRollTrueAndFalse (derive_validation_test.go) pins against an
// independent computation of advance 3 from the java.util.Random spec, not
// this package: eidRollFires's advance 3 is 0.019145... (below
// dittoCandidateThreshold, 1/50 = 0.02); eidRollDoesNotFire's is 0.036411...
// (above it). Every test here that needs "the roll fires" or "the roll does
// not fire" is therefore anchored to a value nothing in this package computed.
const (
	eidRollFires        = uint64(8429844873227918442)
	eidRollDoesNotFire  = uint64(8429844873227918432)
	dittoRollTestPool16 = int16(56) // Mankey: in the published pool
	// The normal form of that species: what the game reports for it, and what
	// a bare ditto_pool entry resolves to. A Ditto only wears the normal form.
	dittoRollTestPoolForm16 = int16(999) // MANKEY_NORMAL
)

// Marking fires for a pool species whose roll is below threshold — the
// conjunction of pool membership AND the roll, not the roll alone.
func TestMaybeMarkDittoFromRollFiresForPoolSpeciesBelowThreshold(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	// The normal form: a bare pool entry resolves to it, and it is what the
	// game reports for this species.
	p.SetForm(null.IntFrom(int64(dittoRollTestPoolForm16)))

	p.maybeMarkDittoFromRoll() // cell weather is irrelevant to whether marking fires at all

	if !p.IsDitto {
		t.Fatal("expected the record to be marked as a Ditto")
	}
	if !p.dittoRollMarked {
		t.Error("expected dittoRollMarked to be set")
	}
	if got := p.DisplayPokemonId.ValueOrZero(); got != uint16(dittoRollTestPool16) {
		t.Errorf("DisplayPokemonId = %v, want %d (the disguise species)", got, dittoRollTestPool16)
	}
	if got := p.DisplayPokemonForm.ValueOrZero(); got != uint16(dittoRollTestPoolForm16) {
		t.Errorf("DisplayPokemonForm = %v, want %d (the disguise form)", got, dittoRollTestPoolForm16)
	}
	if p.PokemonId != int16(pogo.HoloPokemonId_DITTO) {
		t.Errorf("PokemonId = %d, want DITTO (%d)", p.PokemonId, int16(pogo.HoloPokemonId_DITTO))
	}
	if got := p.Form.ValueOrZero(); got != 0 {
		t.Errorf("Form = %v, want 0", got)
	}
}

// A pool species whose roll is ABOVE threshold must not be marked — pins
// the threshold half of the conjunction.
func TestMaybeMarkDittoFromRollDoesNotFireForPoolSpeciesAboveThreshold(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollDoesNotFire), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}

	p.maybeMarkDittoFromRoll()

	if p.IsDitto {
		t.Fatal("must not mark: this encounter id's advance 3 is above dittoCandidateThreshold")
	}
	if p.dittoRollMarked {
		t.Error("dittoRollMarked must stay false")
	}
}

// A non-pool species whose roll fires must not be marked either — the pool
// is a GATE, not a hint: the game only ever substitutes Ditto for an
// eligible species, so a low roll on an ineligible species is not evidence
// of anything.
func TestMaybeMarkDittoFromRollDoesNotFireForNonPoolSpeciesEvenIfRollFires(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	const nonPoolSpecies = int16(1) // Bulbasaur
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: nonPoolSpecies}}

	p.maybeMarkDittoFromRoll()

	if p.IsDitto {
		t.Fatal("must not mark: species 1 is not in the configured pool, even though the roll fires")
	}
}

// TestMaybeMarkDittoFromRollScanHistoryGate protects an assumption of
// detectDitto: its history-present branch (the `if
// unboostedScan != nil || boostedScan != nil` arm) ASSUMES any scan history
// on an IsDitto record is the Ditto's own confirmed history and uses it to
// recover true IVs across boost-state transitions. A record that already
// has real (non-Ditto) scan history from earlier encounters — pool
// membership and the roll notwithstanding — must not be roll-marked, or a
// later encounter would hand that branch the DISGUISE's scans to interpret
// as Ditto scans, corrupting its IV recovery. detectDitto is authoritative
// over any record it has real data about, not only over conclusions it has
// already reached.
//
// Both directions are asserted so the guard is pinned as a GATE, not a
// blanket suppression: history present must block marking, and history
// absent must not — otherwise a mutation that always returns early (or
// never does) could pass half of this coverage by accident.
func TestMaybeMarkDittoFromRollScanHistoryGate(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)

	t.Run("does not mark when scan history exists", func(t *testing.T) {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
		p.scanHistory = []*pokemonScan{{
			Weather: int32(pogo.GameplayWeatherProto_NONE), Level: 5,
			Attack: 10, Defense: 10, Stamina: 10, Pokemon: int32(dittoRollTestPool16),
		}}

		p.maybeMarkDittoFromRoll()

		if p.IsDitto {
			t.Fatal("must not mark: this record already has real scan history, which detectDitto's " +
				"history-present branch would misinterpret as confirmed Ditto data if marked")
		}
	})

	t.Run("still marks when scan history is empty", func(t *testing.T) {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}

		p.maybeMarkDittoFromRoll()

		if !p.IsDitto {
			t.Fatal("expected marking to still fire when there is no scan history at all — " +
				"the history guard must not disable the feature entirely")
		}
	})
}

// A strong Pokemon must never be roll-marked. The roll fires on 2% of ids
// regardless of what the species is, and every downstream consumer of the
// result says this combination cannot exist: deriveIvIfAbsent excludes strong,
// repopulateIv's re-derive branch excludes strong, and its Ditto arm calls a
// strong Ditto unhandleable outright.
//
// Marking is also terminal here. detectDitto returns on `scan.Strong` before
// any Ditto logic runs, so no later encounter takes the identity back — unlike
// every other way a record can be wrongly marked. That is what makes this a
// guard rather than a tidy-up.
func TestMaybeMarkDittoFromRollExcludesStrongPokemon(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)

	t.Run("does not mark a strong Pokemon", func(t *testing.T) {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
		p.SetIsStrong(null.BoolFrom(true))

		p.maybeMarkDittoFromRoll()

		if p.IsDitto {
			t.Fatal("marked a strong Pokemon as Ditto: detectDitto cannot undo this, and the " +
				"derived IVs written with it come from a model that does not describe a strong Pokemon")
		}
		if p.AtkIv.Valid {
			t.Errorf("AtkIv = %v, want unset — no ordinary-model IVs may be written for a strong Pokemon", p.AtkIv)
		}
		if p.Move1.Valid || p.Move2.Valid {
			t.Errorf("moves = %v/%v, want unset — Ditto's moves must not be written either", p.Move1, p.Move2)
		}
	})

	// The same record, not strong, must still mark — otherwise the guard
	// above would pass just as well by disabling the feature.
	t.Run("still marks the same record when not strong", func(t *testing.T) {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
		p.SetIsStrong(null.BoolFrom(false))

		p.maybeMarkDittoFromRoll()

		if !p.IsDitto {
			t.Fatal("expected the roll to fire for this id when the record is not strong")
		}
	})
}

// The scan-history guard must also see history that is PERSISTED but not
// yet lazily decoded into pokemon.scanHistory in memory (populateInternal's
// job) — the shape of a record freshly loaded from the database. A bare
// `len(pokemon.scanHistory) > 0` check, without first calling
// populateInternal, would read 0 here and roll-mark a record that in fact
// already has real confirmed history on file, defeating the guard for
// exactly the records most likely to already carry history: existing,
// previously-encountered ones. legacyGolbatInternalHex (pokemon_scan_test.go)
// is a real golbat_internal payload that also pins populateInternal's
// decoding, reused here rather than hand-built.
func TestMaybeMarkDittoFromRollScanHistoryGateHydratesFromGolbatInternal(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)

	stored, err := hex.DecodeString(legacyGolbatInternalHex)
	if err != nil {
		t.Fatalf("bad golden hex: %s", err)
	}

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	p.GolbatInternal = stored // persisted history; scanHistory not yet hydrated

	p.maybeMarkDittoFromRoll()

	if p.IsDitto {
		t.Fatal("must not mark: GolbatInternal carries persisted scan history that the guard " +
			"must hydrate (via populateInternal) before deciding, not skip because scanHistory " +
			"happened to still be nil in memory")
	}
}

// ditto_derivation = false must suppress marking entirely, even when every other
// condition (pool membership, roll below threshold) holds.
func TestMaybeMarkDittoFromRollSuppressedWhenDisabled(t *testing.T) {
	withDittoDerivation(t, false)
	withDittoPool(t, 56, 58, 81, 100, 273, 283, 355)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}

	p.maybeMarkDittoFromRoll()

	if p.IsDitto {
		t.Fatal("ditto_derivation = false must suppress marking entirely")
	}
	if p.dittoRollMarked {
		t.Error("dittoRollMarked must stay false when the feature is disabled")
	}
}

// The pool decision comes from config.Config.DittoPool alone: a species
// outside the usual Ditto disguises marks when this test's config lists it,
// and a usual disguise (56) does NOT mark when this test's pool excludes it —
// the second half proves the code consults config rather than a hardcoded
// fallback that also contains the usual disguises.
func TestMaybeMarkDittoFromRollPoolIsConfigDriven(t *testing.T) {
	withDittoDerivation(t, true)

	// Any real species that is not a usual Ditto disguise. It has to be
	// a real one: a bare pool entry resolves through the species' NORMAL form,
	// so an invented id has no form to mean and is refused at load.
	const customSpecies = int16(25) // Pikachu
	customForm := normalForm(customSpecies)
	withDittoPool(t, customSpecies)

	included := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: customSpecies, Form: null.ValueFrom(uint16(customForm))}}
	included.maybeMarkDittoFromRoll()
	if !included.IsDitto {
		t.Fatal("species 25 is this test's configured pool and must mark")
	}

	excluded := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	excluded.maybeMarkDittoFromRoll()
	if excluded.IsDitto {
		t.Fatal("species 56 is NOT in this test's pool (only 25 is) and must not mark — " +
			"proves the decision is driven by config, not a leftover hardcoded list")
	}
}

// A wild sighting the roll marks ends up holding the Ditto's own derived
// values, not its disguise's. This drives updateFromWild end to end, so it
// covers applySightingDerivation's call site as well as the marking itself.
func TestUpdateFromWildMarksDittoAndDerivesItsOwnValues(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, 56)

	const spawnID = int64(0x47b973e0595)
	seedSyncedSpawnpoint(t, spawnID)

	// newRecord: true for the same reason as
	// TestUpdateFromWildDerivesUsingTheSightingsWeather — on a non-new
	// record, setPokemonDisplay's repopulateIv call could derive these values
	// itself, and the test would pass without applySightingDerivation.
	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(eidRollFires)

	wild := &pogo.WildPokemonProto{
		EncounterId:  eidRollFires,
		SpawnPointId: "47b973e0595",
		Latitude:     51.5,
		Longitude:    -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId: pogo.HoloPokemonId(dittoRollTestPool16),
			// The disguise's normal form, which is what the game reports and
			// what a bare ditto_pool entry resolves to.
			// RAINY is what makes this discriminate: the disguise's rule
			// reads it as boosted, while a caught Ditto is boosted only in
			// PP, so the two rules derive different values.
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form:                    pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16),
				WeatherBoostedCondition: pogo.GameplayWeatherProto_RAINY,
			},
		},
	}

	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")

	if !p.IsDitto {
		t.Fatal("expected the record to be marked as a Ditto")
	}
	ditto := encounter.For(eidRollFires, false, 0).Values
	disguise := encounter.For(eidRollFires, true, 0).Values
	if ditto.Attack == disguise.Attack && ditto.Level == disguise.Level {
		t.Fatal("this encounter id derives identically either way, so this test cannot tell " +
			"which rule was applied")
	}
	if got := p.AtkIv.ValueOrZero(); got != uint8(ditto.Attack) {
		t.Fatalf("atk = %d, want %d (the Ditto's own branch; the disguise's is %d)",
			got, ditto.Attack, disguise.Attack)
	}
}

// A roll-marked record's FIRST real encounter always has empty scan
// history: scanHistory is populated only by addEncounterPokemon itself,
// after detectDitto runs. That makes missing history the EXPECTED outcome for a
// roll-marked record, not the unexpected condition
// "missing past scans; Ditto will be reset" exists to flag.
//
// Resetting the record here would be quiet and wrong: a roll-marked record
// would lose the mark on its very first encounter and revert to the disguise, treating missing history as
// REFUTING the prediction rather than as unable to VERIFY it. A single boost
// state is no better evidence about Ditto-ness than the roll already
// supplied.
//
// detectDitto KEEPS a roll-marked record on this branch: no
// error, no reset — IsDitto and dittoRollMarked both stay set, PokemonId
// stays DITTO, and the passed-in scan (this encounter's own observed
// values) comes back unchanged for the caller to apply and append to
// scanHistory normally, so a later encounter has real history to reason
// from. See TestDetectDittoStillReturnsErrorForGenuineMissingHistory for
// the companion case this must not swallow, and
// TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic for proof
// that a roll-marked record WITH usable history is unaffected by this
// branch.
func TestDetectDittoKeepsRollMarkedRecordOnMissingHistory(t *testing.T) {
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

	scan := &pokemonScan{Pokemon: int32(disguise), Level: 5, Attack: 10, Defense: 10, Stamina: 10}

	result, err := p.detectDitto(scan)

	if err != nil {
		t.Fatalf("expected no error for a roll-marked record with missing history, got: %s", err)
	}
	if result != scan {
		t.Error("expected the passed-in scan to be returned unchanged")
	}
	if !p.IsDitto {
		t.Error("expected IsDitto to remain true — absence of confirmation is not refutation")
	}
	if !p.dittoRollMarked {
		t.Error("expected dittoRollMarked to remain true — this record's origin is still the roll")
	}
	if p.PokemonId != int16(pogo.HoloPokemonId_DITTO) {
		t.Errorf("PokemonId = %d, want %d (DITTO) — must NOT be reverted to the disguise species",
			p.PokemonId, int16(pogo.HoloPokemonId_DITTO))
	}
}

// TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic pins the
// boundary of the keep-branch above: it applies ONLY when there is no prior
// scan to reason from at all. A roll-marked record that already has usable
// history (both an unboosted and a boosted scan on file) must go through
// detectDitto's ordinary evidence-based logic instead — including that
// logic's own authority to CLEAR the flag when real cross-boost evidence
// contradicts the prediction (the "0N" branch, reached when this round's
// scan agrees with the unboosted history under partly-cloudy cell weather,
// i.e. the disguise's own IVs stayed put across the weather change rather
// than showing the Ditto's WOULD-be-boosted pattern).
//
// If the keep-branch's `dittoRollMarked` check were ever hoisted above the
// `boostedScan != nil` / `unboostedScan != nil` checks (swallowing this
// case too), this record would stay marked as a Ditto forever regardless of
// what the history actually shows — silently defeating detectDitto's own
// authority over records it has real data about.
func TestDetectDittoRollMarkedRecordWithHistoryUsesNormalDittoLogic(t *testing.T) {
	const disguise = int16(56)
	const unboostedLevel = int32(20)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(123456789),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
			PokemonId:        int16(pogo.HoloPokemonId_DITTO),
		},
		dittoRollMarked: true,
		scanHistory: []*pokemonScan{
			{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE), Level: unboostedLevel, Attack: 10, Defense: 10, Stamina: 10},
			{Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY), Level: unboostedLevel + 5, Attack: 10, Defense: 10, Stamina: 10},
		},
	}

	// This round: disguise displayed with no weather boost, but the CELL is
	// partly cloudy (a Ditto would have been boosted here). The observed
	// level matches unboostedLevel exactly — the disguise's own IVs, not
	// the Ditto's boosted pattern — which is real cross-boost evidence
	// AGAINST the Ditto hypothesis and reaches the "0N" reset branch.
	scan := &pokemonScan{
		Pokemon: int32(disguise), Weather: int32(pogo.GameplayWeatherProto_NONE),
		CellWeather: int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
		Level:       unboostedLevel, Attack: 10, Defense: 10, Stamina: 10,
	}

	_, err := p.detectDitto(scan)

	if err != nil {
		t.Fatalf("expected no error, got: %s", err)
	}
	if p.IsDitto {
		t.Fatal("expected IsDitto to be cleared by the 0N branch — real cross-boost evidence " +
			"contradicted the roll-mark, and the keep-branch must not have swallowed this case")
	}
	if p.dittoRollMarked {
		t.Error("expected dittoRollMarked to be cleared along with IsDitto (SetIsDitto(false)'s " +
			"single choke point)")
	}
	if p.PokemonId != disguise {
		t.Errorf("PokemonId = %d, want %d (reverted to the disguise species)", p.PokemonId, disguise)
	}
}

// The companion case: a GENUINELY detectDitto-confirmed record (dittoRollMarked
// false) with missing history must still return its error. This is what
// stops the keep-branch above from silently swallowing every
// missing-history case — only a roll-marked record is kept;
// detectDitto's own prior conclusion, once reached, is never treated as
// something the roll can excuse away.
func TestDetectDittoStillReturnsErrorForGenuineMissingHistory(t *testing.T) {
	const disguise = int16(56)
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:               Uint64Str(987654321),
			IsDitto:          true,
			DisplayPokemonId: null.ValueFrom(uint16(disguise)),
		},
		// dittoRollMarked intentionally left false (zero value).
	}

	scan := &pokemonScan{Pokemon: int32(disguise), Level: 5, Attack: 10, Defense: 10, Stamina: 10}

	_, err := p.detectDitto(scan)

	if err == nil {
		t.Fatal("expected an error for a genuinely detectDitto-confirmed record with missing history")
	}
	if p.IsDitto {
		t.Error("expected IsDitto to still be reset to false even on the error path")
	}
}

// TestAddEncounterPokemonMarksFirstEncounterOfPoolSpeciesFromRoll drives the
// real addEncounterPokemon call site rather than a direct
// maybeMarkDittoFromRoll/detectDitto call: an encounter can be the FIRST time
// a record is ever seen (no prior wild sighting ever ran
// updateFromWild/updateFromMap/updateFromNearby), so the roll must be
// reachable from the encounter path or the prediction is never made at all.
// p here has never been through any of the three sighting paths — scanHistory
// is nil, exactly the shape of a genuinely first-ever encounter — so this
// also exercises the ordering requirement: maybeMarkDittoFromRoll must run
// and see the scan-history gate
// pass BEFORE detectDitto runs and BEFORE this encounter's own scan is
// appended to scanHistory (that append happens only in the err == nil
// branch after detectDitto returns).
func TestAddEncounterPokemonMarksFirstEncounterOfPoolSpeciesFromRoll(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)

	// PokemonId set directly (standing in for what setPokemonDisplay does
	// on every real call site before addEncounterPokemon runs) so
	// maybeMarkDittoFromRoll's inDittoPool(pokemon.PokemonId) check sees
	// this encounter's displayed species.
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	p.SetSeenType(SeenTypeCodeEncounter)

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoRollTestPool16),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "", priorSighting{})

	if !p.IsDitto {
		t.Fatal("expected the record to be marked as a Ditto by the roll on its first-ever encounter")
	}
	if !p.dittoRollMarked {
		t.Error("expected dittoRollMarked to be set")
	}
	if p.PokemonId != int16(pogo.HoloPokemonId_DITTO) {
		t.Errorf("PokemonId = %d, want %d (DITTO) — the record must not have been reset back to the "+
			"disguise: with no prior history, detectDitto's keep-branch must have kept it",
			p.PokemonId, int16(pogo.HoloPokemonId_DITTO))
	}
}

// TestAddEncounterPokemonWithPriorHistoryDoesNotMarkFromRoll is the
// companion case that pins the scan-history gate still works correctly on
// the encounter path: a record that already has REAL prior scan history
// (from an earlier real encounter of this same, non-Ditto record) must not
// be roll-marked just because a later encounter's roll happens to fire.
//
// This is the ordering concern made concrete: if addEncounterPokemon
// appended THIS encounter's own scan to scanHistory before calling
// maybeMarkDittoFromRoll (instead of after, as it actually does), the gate
// would see non-empty history on every single call and marking would never
// fire on this path at all — the opposite failure from the one this test
// checks for, but proof that the ordering matters in both directions (see
// TestAddEncounterPokemonMarksFirstEncounterOfPoolSpeciesFromRoll for the
// empty-history direction).
//
// The fixture's prior scan and this round's proto report identical,
// ordinary (non-Ditto) stats so detectDitto's own general history-present
// logic (unrelated to roll-marking) has no level/weather discrepancy to
// react to either — isolating this assertion to the roll-marking gate
// alone.
func TestAddEncounterPokemonWithPriorHistoryDoesNotMarkFromRoll(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)

	const cpMultiplier = float32(0.5)
	wantLevel := cpMultiplierToLevel(cpMultiplier)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	p.SetSeenType(SeenTypeCodeEncounter)
	p.scanHistory = []*pokemonScan{
		{Pokemon: int32(dittoRollTestPool16), Weather: int32(pogo.GameplayWeatherProto_NONE),
			Level: wantLevel, Attack: 8, Defense: 8, Stamina: 8},
	}

	proto := &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoRollTestPool16),
		IndividualAttack:  8,
		IndividualDefense: 8,
		IndividualStamina: 8,
		CpMultiplier:      cpMultiplier,
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}

	p.addEncounterPokemon(context.Background(), db.DbDetails{}, proto, "", priorSighting{})

	if p.IsDitto {
		t.Fatal("must not mark: this record already had scan history BEFORE this encounter — the " +
			"scan-history gate must see only PRIOR history, not this encounter's own just-appended scan")
	}
	if p.dittoRollMarked {
		t.Error("dittoRollMarked must stay false")
	}
}

// TestDittoBranchForAllFourStates is the direct test of dittoBranchFor. A
// discovery signal must not rest on a combination the game cannot produce:
// a boosted disguise reporting weather_boosted_condition NONE, or
// PARTLY_CLOUDY with a non-PARTLY_CLOUDY-boosted disguise type, never occurs,
// because the game reports weather_boosted_condition for the DISPLAYED
// species. Each row below is instead a genuine (observed,
// expected, opposite, strong) combination a real encounter can produce:
// "expected"/"opposite" are drawn straight from the two derived branches a
// real encounter always has, "neither" is any third value, and "unknown" is
// any strong Pokemon regardless of what its IVs are (see the last two rows,
// which pin that strong is checked BEFORE any comparison, not merely when
// the comparison would otherwise disagree).
func TestDittoBranchForAllFourStates(t *testing.T) {
	const eid = uint64(246813579246813)
	const form = int32(0)
	unboosted := encounter.For(eid, false, form).Values
	boosted := encounter.For(eid, true, form).Values
	if unboosted == boosted {
		t.Fatal("test fixture is worthless: this encounter id derives identical boosted and " +
			"unboosted values, so this test cannot distinguish the four states — pick a different id")
	}
	third := encounter.Values{
		Attack: unboosted.Attack, Defense: unboosted.Defense, Stamina: unboosted.Stamina,
		Level: unboosted.Level + 1, Size: unboosted.Size,
	}
	if third == unboosted || third == boosted {
		t.Fatal("test fixture is worthless: the deliberately-wrong 'third' value coincides with " +
			"a real branch — pick a different id")
	}

	for _, tc := range []struct {
		name     string
		observed encounter.Values
		strong   bool
		want     dittoBranch
	}{
		{"expected: observed matches the branch scan.Weather implies", unboosted, false, dittoBranchExpected},
		{"opposite: observed matches the OTHER branch", boosted, false, dittoBranchOpposite},
		{"neither: observed matches neither branch", third, false, dittoBranchNeither},
		{"unknown: a strong Pokemon with mismatched IVs", third, true, dittoBranchUnknown},
		{"unknown: a strong Pokemon even with IVs that would otherwise read 'expected'", unboosted, true, dittoBranchUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dittoBranchFor(tc.observed, unboosted, boosted, tc.strong); got != tc.want {
				t.Errorf("dittoBranchFor(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

// A roll-marked Ditto carries its disguise's moves — what an encounter
// reports for it — derived for the displayed species, not Ditto's own.
func TestMaybeMarkDittoFromRollDerivesTheDisguisesMoves(t *testing.T) {
	const dittoQuick, dittoCharged = 242, 133
	const mankeyQuick, mankeyCharged = 216, 250
	withDittoDerivation(t, true)
	withDittoPool(t, 56)
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			int(pogo.HoloPokemonId_DITTO): {Types: []int{1}, QuickMoves: []int{dittoQuick}, ChargedMoves: []int{dittoCharged}},
			int(dittoRollTestPool16):      {Types: []int{2}, QuickMoves: []int{mankeyQuick}, ChargedMoves: []int{mankeyCharged}},
		},
	})

	p := &Pokemon{}
	p.Id = Uint64Str(eidRollFires)
	p.SetPokemonId(dittoRollTestPool16)
	p.SetForm(null.IntFrom(int64(dittoRollTestPoolForm16)))

	p.maybeMarkDittoFromRoll()

	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must have marked this record")
	}
	if m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero()); m1 != mankeyQuick || m2 != mankeyCharged {
		t.Errorf("moves = %d/%d, want the disguise's %d/%d", m1, m2, mankeyQuick, mankeyCharged)
	}
}

// A roll-marked Ditto's first encounter is stored under the DITTO's boost
// state, not the disguise's.
//
// weather_boosted_condition describes the displayed species, so a Ditto in
// partly cloudy weather wearing a disguise that weather does not boost
// arrives with Weather=NONE beside values that were rolled boosted.
// locateAllScans files a stored scan by that field alone, so leaving it
// would file a boosted observation as the unboosted one — and the next
// encounter would compare a boosted level against an unboosted expectation
// and reset a Ditto nothing had contradicted.
func TestDetectDittoFilesRollMarkedScanUnderTheDittosOwnBoostState(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cellWeather pogo.GameplayWeatherProto_WeatherCondition
		wantWeather pogo.GameplayWeatherProto_WeatherCondition
		// 0P reports the SEEN set and the Ditto is caught unboosted, and
		// nothing here knows those values — no history, which is why this
		// branch exists. So there is no caught set to hand back.
		wantCaught bool
	}{
		{"partly cloudy boosts a Ditto", pogo.GameplayWeatherProto_PARTLY_CLOUDY, pogo.GameplayWeatherProto_PARTLY_CLOUDY, false},
		{"any other weather does not", pogo.GameplayWeatherProto_RAINY, pogo.GameplayWeatherProto_NONE, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Pokemon{}
			p.Id = Uint64Str(eidRollFires)
			p.markDittoFromRoll("roll", nil, nil)
			if !p.dittoRollMarked {
				t.Fatal("fixture assumption violated: the record must be roll-marked")
			}

			// The disguise is not boosted by this weather, so the proto
			// reports NONE even where the Ditto itself is boosted.
			scan := &pokemonScan{
				Weather:     int32(pogo.GameplayWeatherProto_NONE),
				CellWeather: int32(tc.cellWeather),
				Level:       25, Attack: 10, Defense: 11, Stamina: 12,
			}

			got, err := p.detectDitto(scan)
			if err != nil {
				t.Fatalf("a roll-marked record with no history must be kept, got error: %v", err)
			}
			if !p.IsDitto {
				t.Error("the roll-mark must survive a first encounter with no history")
			}
			// How the scan is FILED, read off the scan itself rather than the
			// return value — the two are different questions and only one of
			// them has an answer in 0P.
			if scan.Weather != int32(tc.wantWeather) {
				t.Errorf("stored scan Weather = %d, want %d — a scan filed under the disguise's "+
					"boost state makes the next encounter reset a Ditto nothing contradicted",
					scan.Weather, tc.wantWeather)
			}
			if (got != nil) != tc.wantCaught {
				t.Errorf("caughtIv non-nil = %v, want %v. In 0P the encounter reports the boosted "+
					"set and the Ditto is caught unboosted, so handing back this scan would "+
					"publish a level five too high whenever derivation is off",
					got != nil, tc.wantCaught)
			}
		})
	}
}

// A roll-mark survives a save and reload when the internal protobuf is
// persisted. Without the provenance a reloaded record keeps IsDitto but
// looks like a detectDitto-confirmed Ditto with no history — the shape the
// missing-history branch resets, discarding a prediction nothing had
// contradicted.
func TestDittoRollMarkSurvivesInternalRoundTrip(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(eidRollFires)
	p.markDittoFromRoll("roll", nil, nil)
	if !p.dittoRollMarked {
		t.Fatal("fixture assumption violated: the record must be roll-marked")
	}
	p.rewriteGolbatInternal()

	// What a reload gives back: the stored bytes, nothing else.
	reloaded := &Pokemon{}
	reloaded.GolbatInternal = p.GolbatInternal
	reloaded.SetIsDitto(true)
	reloaded.populateInternal()

	if !reloaded.dittoRollMarked {
		t.Error("dittoRollMarked did not survive the round trip; a reloaded prediction " +
			"would be reset by detectDitto's missing-history branch")
	}
}

// With no stored internal data the flag is absent: the provenance is
// unknown, so the record does not claim to be roll-marked.
func TestDittoRollMarkAbsentWithoutInternalData(t *testing.T) {
	reloaded := &Pokemon{}
	reloaded.SetIsDitto(true)
	reloaded.populateInternal()

	if reloaded.dittoRollMarked {
		t.Error("no internal data can only mean the provenance is unknown, not roll-marked")
	}
}

// The record answers two different questions and the halves deliberately
// disagree: weather and CP describe what a map shows — the DISGUISE — while
// IVs and level describe what you actually catch, which for a Ditto in
// PARTLY_CLOUDY is boosted.
//
// Storing the Ditto's own boost state in the weather column instead would
// also skip recomputeCpIfNeeded's cell-weather arm, which runs only when the
// stored weather is NONE, and produce CP for the wrong thing.
func TestSetPokemonDisplayStoresTheDisguisesWeather(t *testing.T) {
	const disguise = int16(56)
	p := &Pokemon{}
	p.Id = Uint64Str(eidRollFires)
	p.SetPokemonId(disguise)
	p.SetForm(null.IntFrom(0))
	p.SetCostume(null.IntFrom(0))
	p.SetGender(null.IntFrom(0))
	p.markDittoFromRoll("roll", nil, nil)

	// The disguise is not boosted by this weather, so the display says NONE
	// even though the cell is partly cloudy and the Ditto itself is boosted.
	display := &pogo.PokemonDisplayProto{WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE}
	p.setPokemonDisplay(disguise, display, SeenTypeCodeWild)

	if got := int64OrZero(p.Weather); got != int64(pogo.GameplayWeatherProto_NONE) {
		t.Errorf("stored weather = %d, want %d (the disguise's) — the weather column describes "+
			"what a map shows, and recomputeCpIfNeeded's Ditto arm keys on it being NONE",
			got, pogo.GameplayWeatherProto_NONE)
	}
}

// On the nearby placement path the display — and with it derivation and CP —
// is applied after the record's coordinates are set, not before. A new
// sighting that roll-marks a Ditto in NONE weather has recomputeCpIfNeeded
// resolve the cell's weather from Lat/Lon; done before placement those are
// 0,0, the batch weather misses, and the fallback is a database read under
// the pokemon lock, on every such sighting, because a missing weather row is
// not cached.
//
// The database here is nil, so reaching it is the failure: the batch map
// carries the weather for the pokestop's cell and nothing else.
func TestUpdateFromNearbyPlacesBeforeResolvingADittosCellWeather(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)
	withOhbem(t)

	const lat, lon = 51.5007, -0.1246
	const fort = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	fortId := mustFortId(t, fort)
	pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{Id: fortId, Lat: lat, Lon: lon}}, time.Minute)
	t.Cleanup(func() { pokestopCache.Delete(fortId) })

	weather := map[int64]pogo.GameplayWeatherProto_WeatherCondition{
		weatherCellIdFromLatLon(lat, lon): pogo.GameplayWeatherProto_PARTLY_CLOUDY,
	}
	nearby := &pogo.NearbyPokemonProto{
		FortId:        fort,
		PokedexNumber: int32(dittoRollTestPool16),
		PokemonDisplay: &pogo.PokemonDisplayProto{
			Form:                    pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16),
			WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE,
		},
	}
	p := &Pokemon{}
	p.Id = Uint64Str(eidRollFires)
	p.newRecord = true

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("updateFromNearby reached the (nil) database resolving cell weather: the display was "+
				"applied before the coordinates were set, so the lookup was for 0,0 and missed the batch weather (%v)", r)
		}
	}()
	if !p.updateFromNearby(context.Background(), db.DbDetails{}, nearby, 0, weather, 1700000000000, "tester") {
		t.Fatal("sighting at a known pokestop refused")
	}
	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must fire and mark this sighting")
	}
	if p.Lat != lat || p.Lon != lon {
		t.Fatalf("placed at %.4f,%.4f, want the pokestop %.4f,%.4f", p.Lat, p.Lon, lat, lon)
	}
	if !p.Cp.Valid {
		t.Error("no CP computed for the roll-marked Ditto")
	}
}

// A roll-marked Ditto whose display changes stops being a Ditto, and its IVs
// and level — taken on the Ditto's own boost rule, boosted only in PP — must
// not carry over to the new identity. In overcast weather the new display is
// boosted and the Ditto was not, so the branches differ.
func TestADittoLosingItsIdentityIsReDerivedForTheNewDisplay(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)
	seedCachedSpawnpoint(t, 0x3c8d2bae1f7, null.Int{})
	const bulbasaur, bulbasaurNormal = int32(1), int32(163)
	wild := func(species, form int32) *pogo.WildPokemonProto {
		return &pogo.WildPokemonProto{
			EncounterId: eidRollFires, SpawnPointId: "3c8d2bae1f7", Latitude: 51.5, Longitude: -0.1,
			Pokemon: &pogo.PokemonProto{
				PokemonId: pogo.HoloPokemonId(species),
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form:                    pogo.PokemonDisplayProto_Form(form),
					WeatherBoostedCondition: pogo.GameplayWeatherProto_OVERCAST,
				},
			},
		}
	}

	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(eidRollFires)
	p.updateFromWild(context.Background(), db.DbDetails{}, wild(int32(dittoRollTestPool16), int32(dittoRollTestPoolForm16)), 99, nil, 1700000000000, "tester")
	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must mark the first sighting")
	}
	p.newRecord = false

	p.updateFromWild(context.Background(), db.DbDetails{}, wild(bulbasaur, bulbasaurNormal), 99, nil, 1700000060000, "tester")

	if p.IsDitto {
		t.Fatal("fixture assumption violated: the display change must clear the Ditto identity")
	}
	want := encounter.For(eidRollFires, true, bulbasaurNormal).Values
	if int64OrZero(p.AtkIv) != int64(want.Attack) || int64(p.Level.ValueOrZero()) != int64(want.Level) {
		t.Errorf("atk/level = %v/%v, want %d/%d — the Ditto's unboosted values carried over to a boosted Bulbasaur",
			p.AtkIv, p.Level, want.Attack, want.Level)
	}
}

// A species added to the pool after a record of it was already derived keeps
// the disguise's derived moves when the roll then marks it.
func TestRollMarkingKeepsDerivedDisguiseMoves(t *testing.T) {
	const dittoQuick, dittoCharged = 242, 133
	const mankeyQuick, mankeyCharged = 216, 250
	withDittoDerivation(t, true)
	withMasterFileData(t, MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			int(pogo.HoloPokemonId_DITTO): {Types: []int{1}, QuickMoves: []int{dittoQuick}, ChargedMoves: []int{dittoCharged}},
			int(dittoRollTestPool16):      {Types: []int{2}, QuickMoves: []int{mankeyQuick}, ChargedMoves: []int{mankeyCharged}},
		},
	})
	seedCachedSpawnpoint(t, 0x3c8d2bae1f9, null.Int{})
	wild := &pogo.WildPokemonProto{
		EncounterId: eidRollFires, SpawnPointId: "3c8d2bae1f9", Latitude: 51.5, Longitude: -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId:      pogo.HoloPokemonId(dittoRollTestPool16),
			PokemonDisplay: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16)},
		},
	}

	withDittoPoolEntries(t) // the published pool does not have Mankey yet
	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(eidRollFires)
	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")
	if p.IsDitto || int64(p.Move1.ValueOrZero()) != mankeyQuick {
		t.Fatalf("fixture assumption violated: unmarked, with Mankey's derived moves (is_ditto=%v move1=%v)", p.IsDitto, p.Move1)
	}
	p.newRecord = false

	withDittoPool(t, dittoRollTestPool16) // a refresh adds it
	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000060000, "tester")

	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must mark it once Mankey is in the pool")
	}
	if m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero()); m1 != mankeyQuick || m2 != mankeyCharged {
		t.Errorf("moves = %d/%d, want the disguise's %d/%d", m1, m2, mankeyQuick, mankeyCharged)
	}
}

// An encounter of a Ditto keeps the moves it reported: the disguise's, which
// is what you catch when the disguise is shiny.
func TestEncounterOfADittoKeepsTheReportedMoves(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)

	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires), PokemonId: dittoRollTestPool16, Form: null.ValueFrom(uint16(dittoRollTestPoolForm16))}}
	p.SetSeenType(SeenTypeCodeEncounter)
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, &pogo.PokemonProto{
		PokemonId:         pogo.HoloPokemonId(dittoRollTestPool16),
		IndividualAttack:  10,
		IndividualDefense: 10,
		IndividualStamina: 10,
		CpMultiplier:      0.5,
		Move1:             pogo.HoloPokemonMove(216),
		Move2:             pogo.HoloPokemonMove(250),
		PokemonDisplay:    &pogo.PokemonDisplayProto{},
	}, "", priorSighting{})

	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must have marked this record")
	}
	if m1, m2 := int64(p.Move1.ValueOrZero()), int64(p.Move2.ValueOrZero()); m1 != 216 || m2 != 250 {
		t.Errorf("moves = %d/%d, want the reported 216/250", m1, m2)
	}
}

// Marking an already-derived sighting replaces its IVs and level with the
// Ditto's, so the CP derived from the disguise's values no longer describes
// the record. CP is latched — recomputeCpIfNeeded runs only when it is absent
// — so it must be invalidated at the mark, or the old CP is published with the
// new stats. Overcast: the disguise is boosted and the Ditto is not, so the two
// sets, and their CPs, differ.
func TestRollMarkingRecomputesCpForTheDittosStats(t *testing.T) {
	withDittoDerivation(t, true)
	withOhbem(t)
	seedCachedSpawnpoint(t, 0x3c8d2bae1fb, null.Int{})
	wild := &pogo.WildPokemonProto{
		EncounterId: eidRollFires, SpawnPointId: "3c8d2bae1fb", Latitude: 51.5, Longitude: -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId: pogo.HoloPokemonId(dittoRollTestPool16),
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form:                    pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16),
				WeatherBoostedCondition: pogo.GameplayWeatherProto_OVERCAST,
			},
		},
	}

	withDittoPoolEntries(t) // not in the pool yet
	p := &Pokemon{newRecord: true}
	p.Id = Uint64Str(eidRollFires)
	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")
	if p.IsDitto || !p.Cp.Valid {
		t.Fatalf("fixture assumption violated: unmarked with a derived CP (is_ditto=%v cp=%v)", p.IsDitto, p.Cp)
	}
	disguiseCp := p.Cp
	p.newRecord = false

	withDittoPool(t, dittoRollTestPool16) // a refresh adds it
	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000060000, "tester")
	if !p.IsDitto {
		t.Fatal("fixture assumption violated: the roll must mark it once it is in the pool")
	}

	// What the record's CP should be: recomputed from scratch for its state.
	fresh := &Pokemon{PokemonData: p.PokemonData}
	fresh.Cp = null.Value[uint16]{}
	fresh.recomputeCpIfNeeded(context.Background(), db.DbDetails{}, nil)
	if !fresh.Cp.Valid {
		t.Fatal("fixture assumption violated: CP cannot be computed for the marked record")
	}
	if fresh.Cp == disguiseCp {
		t.Fatal("fixture assumption violated: the Ditto's CP equals the disguise's, so this cannot show a stale one")
	}
	if p.Cp != fresh.Cp {
		t.Errorf("cp = %v after marking, want %v for the Ditto's stats (the disguise's derived CP was %v)", p.Cp, fresh.Cp, disguiseCp)
	}
}

// A pool refresh that adds a species a cached record already displays must
// let the roll mark it on its next sighting, even though nothing about the
// sighting has changed. The significance checks skip an unchanged sighting;
// without asking the roll, the record would stay its disguise for the rest of
// its life. Through UpdatePokemonBatch, for wild and nearby, because the
// skipping happens there.
func TestUpdatePokemonBatchMarksARecordAPoolRefreshMadeEligible(t *testing.T) {
	lureTestSetup(t)
	withDittoDerivation(t, true)
	display := func() *pogo.PokemonDisplayProto {
		return &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16)}
	}
	seen := time.Date(2026, 8, 23, 12, 5, 0, 0, time.UTC)

	t.Run("wild", func(t *testing.T) {
		seedCachedSpawnpoint(t, 0x3c8d2bae201, null.Int{})
		pokemonCache.Delete(eidRollFires)
		t.Cleanup(func() { pokemonCache.Delete(eidRollFires) })
		wild := func(at time.Time) RawWildPokemonData {
			return RawWildPokemonData{Cell: 5000000000000000000, Timestamp: at.UnixMilli(), Data: &pogo.WildPokemonProto{
				EncounterId: eidRollFires, SpawnPointId: "3c8d2bae201", Latitude: 51.5007, Longitude: -0.1246,
				Pokemon: &pogo.PokemonProto{PokemonId: pogo.HoloPokemonId(dittoRollTestPool16), PokemonDisplay: display()},
			}}
		}
		params := ScanParameters{ProcessWild: true}

		withDittoPoolEntries(t) // not in the pool yet
		UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{wild(seen)}, nil, nil, nil, "t")
		withDittoPool(t, dittoRollTestPool16) // a refresh adds it
		UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, []RawWildPokemonData{wild(seen.Add(time.Minute))}, nil, nil, nil, "t")

		p, unlock, _ := peekPokemonRecordReadOnly(eidRollFires, "t")
		isDitto := p.IsDitto
		unlock()
		if !isDitto {
			t.Error("an unchanged wild sighting after the refresh was skipped without the roll being asked")
		}
	})

	t.Run("nearby at a pokestop", func(t *testing.T) {
		const fort = "ffffffffffffffffffffffffffffff01"
		fortId := mustFortId(t, fort)
		pokestopCache.Set(fortId, &Pokestop{PokestopData: PokestopData{Id: fortId, Lat: 51.5007, Lon: -0.1246}}, time.Minute)
		t.Cleanup(func() { pokestopCache.Delete(fortId) })
		pokemonCache.Delete(eidRollFires)
		t.Cleanup(func() { pokemonCache.Delete(eidRollFires) })
		cell := uint64(s2.CellIDFromLatLng(s2.LatLngFromDegrees(51.5007, -0.1246)).Parent(gmoCellLevel))
		nearby := func(at time.Time) RawNearbyPokemonData {
			return RawNearbyPokemonData{Cell: cell, Timestamp: at.UnixMilli(), Data: &pogo.NearbyPokemonProto{
				EncounterId: eidRollFires, FortId: fort, PokedexNumber: int32(dittoRollTestPool16), PokemonDisplay: display(),
			}}
		}
		params := ScanParameters{ProcessNearby: true}

		withDittoPoolEntries(t)
		UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, nil, []RawNearbyPokemonData{nearby(seen)}, nil, nil, "t")
		p, unlock, _ := peekPokemonRecordReadOnly(eidRollFires, "t")
		seenType := p.SeenType.Code
		unlock()
		if seenType != SeenTypeCodeNearbyStop {
			t.Fatalf("fixture assumption violated: seen type %v, want nearby stop (a cell pokemon is always re-admitted)", seenType)
		}
		withDittoPool(t, dittoRollTestPool16)
		UpdatePokemonBatch(context.Background(), db.DbDetails{}, params, nil, []RawNearbyPokemonData{nearby(seen.Add(time.Minute))}, nil, nil, "t")

		p, unlock, _ = peekPokemonRecordReadOnly(eidRollFires, "t")
		isDitto := p.IsDitto
		unlock()
		if !isDitto {
			t.Error("an unchanged nearby sighting after the refresh was skipped without the roll being asked")
		}
	})
}

// A record an earlier encounter showed is not a Ditto, reloaded without its
// scan history (pokemon_internal_to_db off), must not be marked again by its
// next sighting: marking would overwrite the IVs, level and moves that
// encounter observed.
func TestSightingDoesNotRemarkAnEncounteredRecordReloadedWithoutHistory(t *testing.T) {
	withDittoDerivation(t, true)
	withDittoPool(t, dittoRollTestPool16)

	const spawnID = int64(0x47b973e0595)
	seedSyncedSpawnpoint(t, spawnID)

	// As loaded from the database: encountered, not a Ditto, no history.
	p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(eidRollFires)}}
	p.SetPokemonId(dittoRollTestPool16)
	p.SetForm(null.IntFrom(int64(dittoRollTestPoolForm16)))
	p.SetCostume(null.IntFrom(0))
	p.SetGender(null.IntFrom(0))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.SetSeenType(SeenTypeCodeEncounter)
	p.calculateIv(10, 5, 12)
	p.SetLevel(null.IntFrom(28))
	p.SetMove1(null.IntFrom(221))
	p.SetMove2(null.IntFrom(30))
	p.snapshotOldValues()

	wild := &pogo.WildPokemonProto{
		EncounterId:  eidRollFires,
		SpawnPointId: "47b973e0595",
		Latitude:     51.5,
		Longitude:    -0.1,
		Pokemon: &pogo.PokemonProto{
			PokemonId: pogo.HoloPokemonId(dittoRollTestPool16),
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form: pogo.PokemonDisplayProto_Form(dittoRollTestPoolForm16),
			},
		},
	}
	p.updateFromWild(context.Background(), db.DbDetails{}, wild, 99, nil, 1700000000000, "tester")

	if p.IsDitto {
		t.Fatal("an encountered record was marked as a Ditto again on reload")
	}
	if p.AtkIv.ValueOrZero() != 10 || p.Level.ValueOrZero() != 28 || p.Move1.ValueOrZero() != 221 {
		t.Errorf("observed values overwritten: atk %d level %d move1 %d, want 10, 28, 221",
			p.AtkIv.ValueOrZero(), p.Level.ValueOrZero(), p.Move1.ValueOrZero())
	}
}
