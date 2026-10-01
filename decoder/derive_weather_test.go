package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
	"golbat/derive/encounter"
	"golbat/pogo"
)

// A flip with no matching scan must repopulate for the NEW boost state, not
// blank the record, and an encounter record goes back to wild, since its
// stats are now derived. It must also invalidate CP and PVP so
// recomputeCpIfNeeded rebuilds them from the new stats, and so
// ProactiveIVSwitch's "if !pokemon.Cp.Valid" gate fires. A CP left
// stale-but-valid would silently skip SetWeather, recomputeCpIfNeeded and
// savePokemonRecordAsAtTime for every Pokemon in the cell: mutated in memory,
// never persisted, R-tree weather left stale.
func TestRepopulateIvDerivesInsteadOfClearing(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.calculateIv(6, 0, 11)
	p.SetLevel(null.IntFrom(5))
	// CP/PVP start valid, so the assertions below observe an invalidation
	// rather than a field that was never set.
	p.SetCp(null.IntFrom(300))
	p.SetPvp(null.StringFrom(`{"great":[]}`))

	// Flip to a boosting weather; no scan history exists for that state.
	p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

	if !p.AtkIv.Valid {
		t.Fatal("ivs were blanked; they should have been re-derived")
	}
	if got := p.AtkIv.ValueOrZero(); got != 8 {
		t.Fatalf("boosted atk: got %d want 8", got)
	}
	if got := p.Level.ValueOrZero(); got != 10 {
		t.Fatalf("boosted level: got %d want 10", got)
	}
	if p.SeenType.Code != SeenTypeCodeWild {
		t.Fatalf("seen_type = %v, want wild: the encounter's stats were replaced by derived ones", p.SeenType.Code)
	}
	if p.Cp.Valid {
		t.Fatalf("Cp still valid (%v) after a flip that changed IVs — ProactiveIVSwitch's "+
			"!pokemon.Cp.Valid gate would never fire and this cell would never recompute", p.Cp)
	}
	if p.Pvp.Valid {
		t.Fatalf("Pvp still valid (%v) after a flip that changed IVs — it must be cleared so it gets rebuilt", p.Pvp)
	}
}

// A strong Pokemon's IVs do not move with weather (repopulateIv's own
// "isBoostedMatches || isStrong" branch, and detectDitto's requirement that
// CompressedIv be identical across boost states for a strong scan). So on a
// flip with no matching scan, a strong Pokemon's IVs are cleared to await a
// rescan, not derived: encounter.For models the ordinary boosted-IV-floor
// mechanic, which does not apply here, and a wrong derived value is
// indistinguishable from a real one and so unrecoverable.
func TestRepopulateIvClearsStrongPokemonInsteadOfDeriving(t *testing.T) {
	p := &Pokemon{}
	p.Id = Uint64Str(8429844873227918432)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.calculateIv(6, 0, 11)
	p.SetLevel(null.IntFrom(5))

	// Flip to a boosting weather AND to strong at once (isStrong param true,
	// stored IsStrong false) with no scan history for the strong+boosted
	// state — matchingScan is nil either way.
	p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), true)

	if p.AtkIv.Valid {
		t.Fatalf("AtkIv = %v, want invalid — a strong Pokemon must be cleared, not derived", p.AtkIv)
	}
	if p.Level.Valid {
		t.Fatalf("Level = %v, want invalid — a strong Pokemon must be cleared, not derived", p.Level)
	}
}

// repopulateIv's Ditto arm reaches the same derivation branch, on the rule
// that governs a CAUGHT Ditto: boosted only in PP — partly cloudy weather over
// a disguise that partly cloudy also boosts.
//
// That is exactly what the game reports as weather_boosted_condition, so the
// arm needs nothing but the reported value. The cell's weather would be the
// wrong input: in 0P the cell IS partly cloudy, the encounter reports boosted
// stats, and the Ditto you catch is still unboosted.
//
// pokemon.Weather holds the condition the previous sighting reported, and the
// argument is this one's, so the two sides compare like for like. The three
// cases below are the whole truth table: into PP, out of PP, and neither.
func TestRepopulateIvDerivesForADittoOnItsOwnBoostRule(t *testing.T) {
	const eid = uint64(8429844873227918432)

	// A Ditto's own Form is stored as 0 (setPokemonDisplay keeps the
	// disguise's form in DisplayPokemonForm), which is what the derivation
	// must be given. storedWeather is what the PREVIOUS sighting reported.
	newDitto := func(storedWeather pogo.GameplayWeatherProto_WeatherCondition) *Pokemon {
		p := &Pokemon{}
		p.Id = Uint64Str(eid)
		p.SetIsDitto(true)
		p.SetDisplayPokemonId(null.IntFrom(56))
		p.SetDisplayPokemonForm(null.IntFrom(0))
		p.SetForm(null.IntFrom(0))
		p.SetSeenType(SeenTypeCodeEncounter)
		p.SetWeather(null.IntFrom(int64(storedWeather)))
		p.calculateIv(1, 2, 3)
		p.SetLevel(null.IntFrom(7))
		return p
	}

	t.Run("into PP derives the boosted branch", func(t *testing.T) {
		p := newDitto(pogo.GameplayWeatherProto_NONE)
		p.repopulateIv(int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY), false)

		if got := p.AtkIv.ValueOrZero(); got != 8 {
			t.Errorf("atk = %d, want 8 (the boosted derivation for this encounter id)", got)
		}
		if got := p.Level.ValueOrZero(); got != 10 {
			t.Errorf("level = %d, want 10 (boosted)", got)
		}
	})

	t.Run("out of PP derives the unboosted branch", func(t *testing.T) {
		p := newDitto(pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

		if got := p.AtkIv.ValueOrZero(); got != 6 {
			t.Errorf("atk = %d, want 6 (the unboosted derivation for this encounter id)", got)
		}
		if got := p.Level.ValueOrZero(); got != 5 {
			t.Errorf("level = %d, want 5 (unboosted)", got)
		}
	})

	// Neither side is PP, so the caught Ditto is unboosted before and after.
	// A rule keyed on the cell instead would see a change here whenever the
	// cell moved into or out of partly cloudy.
	t.Run("a weather change that is not into or out of PP derives nothing", func(t *testing.T) {
		p := newDitto(pogo.GameplayWeatherProto_NONE)
		p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

		if got := p.AtkIv.ValueOrZero(); got != 1 || p.Level.ValueOrZero() != 7 {
			t.Errorf("atk/level = %d/%d, want 1/7 — NONE -> RAINY is a boost flip for the "+
				"DISGUISE, but neither state is PP, so the caught Ditto is unboosted either way",
				got, p.Level.ValueOrZero())
		}
	})
}

// locateScan does not return nil when it holds only the OTHER boost state's
// scan — it returns that one with isBoostedMatches false. So a flip after a
// single encounter arrives with a non-nil scan describing the wrong state.
// Treating it as usable would clear the IVs and keep a ±5 level guess where the
// derivation supplies both exactly.
func TestRepopulateIvDerivesWhenOnlyTheOppositeScanIsStored(t *testing.T) {
	withDeriveIv(t, true)

	const eid = uint64(8429844873227918432)
	unboosted := encounter.For(eid, false, 0).Values
	boosted := encounter.For(eid, true, 0).Values
	if unboosted.Attack == boosted.Attack && unboosted.Level == boosted.Level {
		t.Fatal("this encounter id derives the same values for both boost states; " +
			"the assertions below could not tell the two apart")
	}

	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	// One real encounter, in the UNBOOSTED state.
	p.scanHistory = []*pokemonScan{{
		Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level:   int32(unboosted.Level), Attack: int32(unboosted.Attack),
		Defense: int32(unboosted.Defense), Stamina: int32(unboosted.Stamina),
	}}
	p.calculateIv(int64(unboosted.Attack), int64(unboosted.Defense), int64(unboosted.Stamina))
	p.SetLevel(null.IntFrom(int64(unboosted.Level)))

	// The weather turns to one that boosts this Pokemon.
	p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), false)

	if !p.AtkIv.Valid {
		t.Fatal("IVs were cleared on a flip the derivation answers exactly — the stored " +
			"unboosted scan is not a scan for the boosted state and must not block it")
	}
	if got := p.AtkIv.ValueOrZero(); got != uint8(boosted.Attack) {
		t.Errorf("atk = %d, want %d (the boosted derivation)", got, boosted.Attack)
	}
	if got := p.Level.ValueOrZero(); got != uint8(boosted.Level) {
		t.Errorf("level = %d, want %d (the boosted derivation, not the stored level ±5)",
			got, boosted.Level)
	}
}

// A strong Pokemon keeps the fallback: its stats do not move with weather, so
// the other state's scan really is its scan, and deriving would write values
// the surrounding code says cannot occur for one.
func TestRepopulateIvUsesTheOppositeScanForAStrongPokemon(t *testing.T) {
	withDeriveIv(t, true)

	const eid = uint64(8429844873227918432)
	p := &Pokemon{}
	p.Id = Uint64Str(eid)
	p.SetSeenType(SeenTypeCodeEncounter)
	p.SetIsStrong(null.BoolFrom(true))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_NONE)))
	p.scanHistory = []*pokemonScan{{
		Strong: true, Weather: int32(pogo.GameplayWeatherProto_NONE),
		Level: 20, Attack: 3, Defense: 4, Stamina: 5,
	}}

	p.repopulateIv(int64(pogo.GameplayWeatherProto_RAINY), true)

	if got := p.AtkIv.ValueOrZero(); got != 3 {
		t.Errorf("atk = %d, want 3 (the stored strong scan, not a derivation)", got)
	}
	if got := p.Level.ValueOrZero(); got != 25 {
		t.Errorf("level = %d, want 25 (the stored level, boost-adjusted)", got)
	}
}
