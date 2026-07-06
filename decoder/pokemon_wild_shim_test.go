package decoder

import (
	"context"
	"testing"

	"golbat/db"
	"golbat/pogo"
	"golbat/pogoshim"

	"github.com/guregu/null/v6"
)

// TestUpdateFromWildShim locks in Wave-2b behavior for the hyperpb
// migration: updateFromWild must extract identical entity state via
// pogoshim getters (WildPokemonProto / PokemonProto / PokemonDisplayProto)
// as the pre-migration code extracted via direct *pogo.WildPokemonProto
// field access, including the weather-boost display path
// (WeatherBoostedCondition != NONE).
func TestUpdateFromWildShim(t *testing.T) {
	const encounterId = uint64(555444333)
	const timestampMs = int64(1_700_000_000_000)

	wild := &pogo.WildPokemonProto{
		EncounterId:      encounterId,
		LastModifiedMs:   timestampMs,
		Latitude:         10.111,
		Longitude:        -20.222,
		SpawnPointId:     "0", // spawnId 0 -> setExpireTimestampFromSpawnpoint short-circuits (no DB/cache touch)
		TimeTillHiddenMs: 600,
		Pokemon: &pogo.PokemonProto{
			Id:        encounterId,
			PokemonId: pogo.HoloPokemonId_SQUIRTLE,
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form:                    pogo.PokemonDisplayProto_FORM_UNSET,
				Costume:                 pogo.PokemonDisplayProto_UNSET,
				Gender:                  pogo.PokemonDisplayProto_FEMALE,
				WeatherBoostedCondition: pogo.GameplayWeatherProto_PARTLY_CLOUDY, // weather-boost path
				IsStrongPokemon:         false,
			},
		},
	}

	shimWild := pogoshim.AsWildPokemonProto(wild.ProtoReflect())

	pokemon := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encounterId)}}
	pokemon.newRecord = true

	weather := map[int64]pogo.GameplayWeatherProto_WeatherCondition{}

	pokemon.updateFromWild(context.Background(), db.DbDetails{}, shimWild, 42, weather, timestampMs, "brock")

	if got, want := pokemon.PokemonId, int16(pogo.HoloPokemonId_SQUIRTLE); got != want {
		t.Errorf("PokemonId = %d, want %d", got, want)
	}
	if got, want := pokemon.Lat, 10.111; got != want {
		t.Errorf("Lat = %v, want %v", got, want)
	}
	if got, want := pokemon.Lon, -20.222; got != want {
		t.Errorf("Lon = %v, want %v", got, want)
	}
	if got, want := pokemon.Gender.ValueOrZero(), int64(pogo.PokemonDisplayProto_FEMALE); got != want {
		t.Errorf("Gender = %d, want %d", got, want)
	}
	if got, want := pokemon.Weather.ValueOrZero(), int64(pogo.GameplayWeatherProto_PARTLY_CLOUDY); got != want {
		t.Errorf("Weather = %d, want %d (weather-boost path)", got, want)
	}
	if got, want := pokemon.SeenType.ValueOrZero(), SeenType_Wild; got != want {
		t.Errorf("SeenType = %q, want %q", got, want)
	}
	if got, want := pokemon.Username.ValueOrZero(), "brock"; got != want {
		t.Errorf("Username = %q, want %q", got, want)
	}
	if got, want := pokemon.CellId.ValueOrZero(), int64(42); got != want {
		t.Errorf("CellId = %d, want %d", got, want)
	}
	if got, want := pokemon.SpawnId.ValueOrZero(), int64(0); got != want {
		t.Errorf("SpawnId = %d, want %d", got, want)
	}
}

// TestWildSignificantUpdateShim pins the boolean decision logic of
// wildSignificantUpdate now that it reads through pogoshim.WildPokemonProto
// instead of *pogo.WildPokemonProto.
func TestWildSignificantUpdateShim(t *testing.T) {
	makeWild := func(pokemonId pogo.HoloPokemonId, form pogo.PokemonDisplayProto_Form) pogoshim.WildPokemonProto {
		w := &pogo.WildPokemonProto{
			Pokemon: &pogo.PokemonProto{
				PokemonId: pokemonId,
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form: form,
				},
			},
		}
		return pogoshim.AsWildPokemonProto(w.ProtoReflect())
	}

	pokemon := &Pokemon{PokemonData: PokemonData{
		PokemonId: int16(pogo.HoloPokemonId_BULBASAUR),
		Form:      null.IntFrom(int64(pogo.PokemonDisplayProto_FORM_UNSET)),
	}}
	pokemon.ExpireTimestampVerified = true

	if pokemon.wildSignificantUpdate(makeWild(pogo.HoloPokemonId_BULBASAUR, pogo.PokemonDisplayProto_FORM_UNSET), 1000) {
		t.Error("identical species/form should not be a significant update")
	}
	if !pokemon.wildSignificantUpdate(makeWild(pogo.HoloPokemonId_CHARMANDER, pogo.PokemonDisplayProto_FORM_UNSET), 1000) {
		t.Error("species change must be a significant update")
	}
}

// TestNearbySignificantUpdateShim pins the boolean decision logic of
// nearbySignificantUpdate now that it reads through
// pogoshim.NearbyPokemonProto instead of *pogo.NearbyPokemonProto.
func TestNearbySignificantUpdateShim(t *testing.T) {
	makeNearby := func(displayId int64, form pogo.PokemonDisplayProto_Form) pogoshim.NearbyPokemonProto {
		n := &pogo.NearbyPokemonProto{
			PokemonDisplay: &pogo.PokemonDisplayProto{
				DisplayId: displayId,
				Form:      form,
			},
		}
		return pogoshim.AsNearbyPokemonProto(n.ProtoReflect())
	}

	pokemon := &Pokemon{PokemonData: PokemonData{
		PokemonId: int16(pogo.HoloPokemonId_BULBASAUR),
		Form:      null.IntFrom(int64(pogo.PokemonDisplayProto_FORM_UNSET)),
		SeenType:  null.StringFrom(SeenType_NearbyStop),
	}}
	pokemon.ExpireTimestampVerified = true

	if pokemon.nearbySignificantUpdate(makeNearby(int64(pogo.HoloPokemonId_BULBASAUR), pogo.PokemonDisplayProto_FORM_UNSET), 1000) {
		t.Error("identical species/form at a nearby stop should not be a significant update")
	}
	if !pokemon.nearbySignificantUpdate(makeNearby(int64(pogo.HoloPokemonId_CHARMANDER), pogo.PokemonDisplayProto_FORM_UNSET), 1000) {
		t.Error("species change must be a significant update")
	}
}
