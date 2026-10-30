package decoder

import (
	"context"
	"testing"
	"time"

	"github.com/guregu/null/v6"
	"golbat/db"
	"golbat/pogo"
)

// The weather column and the IV derivation answer different questions for a
// disguised Ditto, and ProactiveIVSwitch is the one place that has to supply
// both. The column carries what the game would report for the DISPLAYED
// species, because that is what an overworld client matches a sighting on; the
// derivation follows the Ditto's own Normal typing.
//
// Type 1 is boosted by PARTLY_CLOUDY and type 5 is not (boostedWeatherLookup),
// which is what makes these two disguises discriminate.
func TestDisplayedWeatherConditionUsesTheDisguise(t *testing.T) {
	const (
		normalDisguise = 9201 // boosted by PARTLY_CLOUDY, like the Ditto under it
		otherDisguise  = 9202 // not boosted by PARTLY_CLOUDY
	)
	data := MasterFileData{
		Initialized: true,
		Pokemon: map[int]MasterFilePokemon{
			normalDisguise:                {Types: []int{1}},
			otherDisguise:                 {Types: []int{5}},
			int(pogo.HoloPokemonId_DITTO): {Types: []int{1}},
		},
	}

	newDitto := func(disguise int64) *Pokemon {
		p := &Pokemon{}
		p.SetPokemonId(int16(pogo.HoloPokemonId_DITTO))
		p.SetIsDitto(true)
		p.SetDisplayPokemonId(null.IntFrom(disguise))
		p.SetDisplayPokemonForm(null.IntFrom(0))
		return p
	}

	// The 0P state: the Ditto is boosted, its disguise is not. ownCondition
	// says PARTLY_CLOUDY because the record's own species is 132; the column
	// must still report what the disguise shows, which is no boost at all.
	t.Run("a disguise the cell does not boost stores NONE", func(t *testing.T) {
		p := newDitto(otherDisguise)
		got := displayedWeatherCondition(data, p, int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
			int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))

		if got != int64(pogo.GameplayWeatherProto_NONE) {
			t.Errorf("stored condition = %d, want %d (NONE) — the column must show the disguise, "+
				"or an overworld client cannot match this sighting",
				got, pogo.GameplayWeatherProto_NONE)
		}
	})

	// The PP state: partly cloudy boosts both, so both answers coincide.
	t.Run("a disguise the cell does boost stores that weather", func(t *testing.T) {
		p := newDitto(normalDisguise)
		got := displayedWeatherCondition(data, p, int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
			int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))

		if got != int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
			t.Errorf("stored condition = %d, want %d (PARTLY_CLOUDY)", got, pogo.GameplayWeatherProto_PARTLY_CLOUDY)
		}
	})

	// The B0 state: the cell boosts the disguise but never a Normal-typed
	// Ditto, so the column and the derivation disagree the other way round.
	t.Run("a disguise boosted by a weather the Ditto is not", func(t *testing.T) {
		p := newDitto(otherDisguise)
		got := displayedWeatherCondition(data, p, 1, int64(pogo.GameplayWeatherProto_NONE))

		if got != 1 {
			t.Errorf("stored condition = %d, want 1 — the disguise IS boosted here even though "+
				"the Ditto is not", got)
		}
	})

	// Anything that is not a disguised Ditto keeps the caller's own answer
	// untouched: one species, one weather, no second question to ask.
	t.Run("a non-Ditto is unchanged", func(t *testing.T) {
		p := &Pokemon{}
		p.SetPokemonId(int16(otherDisguise))
		if got := displayedWeatherCondition(data, p, 1, 1); got != 1 {
			t.Errorf("stored condition = %d, want the caller's own 1", got)
		}
	})

	// A real wild Ditto displays as itself, so there is no disguise to defer
	// to and its own condition is already the displayed one.
	t.Run("an undisguised Ditto is unchanged", func(t *testing.T) {
		p := &Pokemon{}
		p.SetPokemonId(int16(pogo.HoloPokemonId_DITTO))
		p.SetIsDitto(true) // no DisplayPokemonId
		got := displayedWeatherCondition(data, p, int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY),
			int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY))

		if got != int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
			t.Errorf("stored condition = %d, want PARTLY_CLOUDY — an undisguised Ditto shows its own boost", got)
		}
	})
}

// findBoostedWeathers returns a BITMASK of the weathers that boost a species,
// so asking "does this weather boost it" is a bit test against 1<<weather.
//
// Written inline as `mask&uint8(1)<<weather != 0` it is not that test. Go gives
// << and & equal precedence and associates left to right, so it parses as
// `(mask & 1) << weather`, and every value in boostedWeatherLookup is a power
// of two from 2 up — bit 0 is never set, `mask & 1` is always 0, and the answer
// is false for every species under every weather. ProactiveIVSwitch's
// newWeather is then permanently NONE: it can take a boost away and can never
// give one.
//
// This walks the real lookup table rather than a fixture, so it fails if the
// parenthesisation is lost or the table gains a value with bit 0 set.
func TestWeatherBoostsIsABitTestNotAShiftOfTheMaskedBit(t *testing.T) {
	boostedSomewhere := 0
	for typeIndex, mask := range boostedWeatherLookup {
		for weather := int32(0); weather <= 7; weather++ {
			want := mask&(uint8(1)<<uint(weather)) != 0
			if got := weatherBoosts(mask, weather); got != want {
				t.Errorf("weatherBoosts(%d, %d) = %v, want %v (type index %d)",
					mask, weather, got, want, typeIndex)
			}
			if want {
				boostedSomewhere++
			}
		}
	}
	// Without this the loop above passes just as well against an
	// always-false implementation compared with an always-false expectation.
	if boostedSomewhere == 0 {
		t.Fatal("no (type, weather) pair registered as boosted — the expectation itself is vacuous")
	}

	// A Normal-type is boosted by PARTLY_CLOUDY and nothing else. That is the
	// specific fact the Ditto handling rests on.
	const normalType = 1
	if !weatherBoosts(boostedWeatherLookup[normalType], int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY)) {
		t.Error("PARTLY_CLOUDY must boost a Normal-type — the whole Ditto boost rule depends on it")
	}
	for weather := int32(0); weather <= 7; weather++ {
		if weather == int32(pogo.GameplayWeatherProto_PARTLY_CLOUDY) {
			continue
		}
		if weatherBoosts(boostedWeatherLookup[normalType], weather) {
			t.Errorf("weather %d must not boost a Normal-type", weather)
		}
	}

	// Out-of-range must not panic: a negative shift count is a runtime panic
	// in Go, inside a sweep that holds entity locks.
	if weatherBoosts(255, -1) || weatherBoosts(255, 99) {
		t.Error("out-of-range weather must report not-boosted")
	}
}

// A weather change between two conditions that both boost a Pokemon leaves its
// IVs and CP alone — the boost state has not moved — but the condition the API
// and webhooks report must follow. Gyarados, Water/Flying, is boosted by both
// RAINY and WINDY. Driven through ProactiveIVSwitch itself.
func TestProactiveIVSwitchRecordsAConditionChangeThatKeepsTheBoost(t *testing.T) {
	sink := lureTestSetup(t)
	old := getMasterFileData()
	t.Cleanup(func() {
		masterFileMu.Lock()
		masterFileData = old
		masterFileMu.Unlock()
	})
	if err := loadMasterFileBytes(pogo.BundledMasterFile); err != nil {
		t.Fatal(err)
	}

	const encId = uint64(930701)
	const lat, lon = 51.5007, -0.1246
	now := time.Now().Unix()
	p, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pokemonCache.Delete(encId); pokemonLookupCache.Delete(encId) })
	p.SetPokemonId(int16(pogo.HoloPokemonId_GYARADOS))
	p.SetForm(null.IntFrom(int64(pogo.PokemonDisplayProto_GYARADOS_NORMAL)))
	p.SetWeather(null.IntFrom(int64(pogo.GameplayWeatherProto_RAINY)))
	p.SetLat(lat)
	p.SetLon(lon)
	p.calculateIv(10, 11, 12)
	p.SetLevel(null.IntFrom(25))
	p.SetCp(null.IntFrom(1500))
	p.SetExpireTimestamp(null.IntFrom(now + 900))
	p.SetUpdated(null.IntFrom(now - 60))
	pokemonRtreePreloadInsert(p)
	p.snapshotOldValues()
	p.newRecord = false
	unlock()
	sink.drain()

	ProactiveIVSwitch(context.Background(), db.DbDetails{}, WeatherUpdate{
		S2CellId: weatherCellIdFromLatLon(lat, lon), NewWeather: int32(pogo.GameplayWeatherProto_WINDY),
	}, false, now)

	p, unlock, _ = peekPokemonRecordReadOnly(encId, "t")
	weather, cp := int64OrZero(p.Weather), p.Cp.ValueOrZero()
	unlock()
	if weather != int64(pogo.GameplayWeatherProto_WINDY) {
		t.Errorf("weather = %d, want WINDY: the condition changed and the record still reports RAINY", weather)
	}
	if cp != 1500 {
		t.Errorf("cp = %d, want 1500 kept: the boost state did not change", cp)
	}
	if len(sink.drain()) == 0 {
		t.Error("no webhook for the weather change")
	}
}
