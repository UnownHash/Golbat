package decoder

import (
	"context"
	"testing"

	"golbat/db"
	"golbat/pogo"
	"golbat/pogoshim"
)

// TestUpdatePokemonFromEncounterProtoShim locks in Wave-1 behavior for the
// hyperpb migration: updatePokemonFromEncounterProto must extract identical
// entity state via pogoshim getters as the pre-migration code extracted via
// direct *pogo.EncounterOutProto field access. Build a full synthetic
// EncounterOutProto (wild + pokemon + IVs + display + capture probability),
// wrap it exactly the way a std-engine decode would
// (pogoshim.AsEncounterOutProto(m.ProtoReflect())), and assert the resulting
// Pokemon fields.
func TestUpdatePokemonFromEncounterProtoShim(t *testing.T) {
	const encounterId = uint64(123456789)
	const timestampMs = int64(1_700_000_000_000)

	enc := &pogo.EncounterOutProto{
		Status: pogo.EncounterOutProto_ENCOUNTER_SUCCESS,
		Pokemon: &pogo.WildPokemonProto{
			EncounterId:      encounterId,
			LastModifiedMs:   timestampMs,
			Latitude:         12.3456789,
			Longitude:        -67.891234,
			SpawnPointId:     "0", // parses to spawnId 0 -> setExpireTimestampFromSpawnpoint short-circuits (no DB/cache touch)
			TimeTillHiddenMs: 890,
			Pokemon: &pogo.PokemonProto{
				Id:                encounterId,
				PokemonId:         pogo.HoloPokemonId_BULBASAUR,
				Cp:                734,
				Move1:             pogo.HoloPokemonMove_VINE_WHIP_FAST,
				Move2:             pogo.HoloPokemonMove_SLUDGE_BOMB,
				HeightM:           0.71,
				WeightKg:          6.9,
				IndividualAttack:  10,
				IndividualDefense: 11,
				IndividualStamina: 12,
				CpMultiplier:      0.5974025,
				Size:              pogo.HoloPokemonSize_M,
				PokemonDisplay: &pogo.PokemonDisplayProto{
					Form:                    pogo.PokemonDisplayProto_FORM_UNSET,
					Costume:                 pogo.PokemonDisplayProto_ANNIVERSARY,
					Gender:                  pogo.PokemonDisplayProto_MALE,
					Shiny:                   true,
					WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE,
					IsStrongPokemon:         false,
				},
			},
		},
		CaptureProbability: &pogo.CaptureProbabilityProto{
			PokeballType:           []pogo.Item{pogo.Item_ITEM_POKE_BALL},
			CaptureProbability:     []float32{0.42},
			ReticleDifficultyScale: 1.0,
		},
	}

	shimEnc := pogoshim.AsEncounterOutProto(enc.ProtoReflect())

	pokemon := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(encounterId)}}
	pokemon.newRecord = true

	pokemon.updatePokemonFromEncounterProto(context.Background(), db.DbDetails{}, shimEnc, "ash", timestampMs)

	if got, want := pokemon.Cp.ValueOrZero(), int64(734); got != want {
		t.Errorf("Cp = %d, want %d", got, want)
	}
	if got, want := pokemon.AtkIv.ValueOrZero(), int64(10); got != want {
		t.Errorf("AtkIv = %d, want %d", got, want)
	}
	if got, want := pokemon.DefIv.ValueOrZero(), int64(11); got != want {
		t.Errorf("DefIv = %d, want %d", got, want)
	}
	if got, want := pokemon.StaIv.ValueOrZero(), int64(12); got != want {
		t.Errorf("StaIv = %d, want %d", got, want)
	}
	if got, want := pokemon.Move1.ValueOrZero(), int64(pogo.HoloPokemonMove_VINE_WHIP_FAST); got != want {
		t.Errorf("Move1 = %d, want %d", got, want)
	}
	if got, want := pokemon.Move2.ValueOrZero(), int64(pogo.HoloPokemonMove_SLUDGE_BOMB); got != want {
		t.Errorf("Move2 = %d, want %d", got, want)
	}
	if got, want := pokemon.Weight.ValueOrZero(), float64(float32(6.9)); got != want {
		t.Errorf("Weight = %v, want %v", got, want)
	}
	if got, want := pokemon.Height.ValueOrZero(), float64(float32(0.71)); got != want {
		t.Errorf("Height = %v, want %v", got, want)
	}
	if got, want := pokemon.Size.ValueOrZero(), int64(pogo.HoloPokemonSize_M); got != want {
		t.Errorf("Size = %d, want %d", got, want)
	}
	if got, want := pokemon.PokemonId, int16(pogo.HoloPokemonId_BULBASAUR); got != want {
		t.Errorf("PokemonId = %d, want %d", got, want)
	}
	if got, want := pokemon.Form.ValueOrZero(), int64(pogo.PokemonDisplayProto_FORM_UNSET); got != want {
		t.Errorf("Form = %d, want %d", got, want)
	}
	if got, want := pokemon.Costume.ValueOrZero(), int64(pogo.PokemonDisplayProto_ANNIVERSARY); got != want {
		t.Errorf("Costume = %d, want %d", got, want)
	}
	if got, want := pokemon.Gender.ValueOrZero(), int64(pogo.PokemonDisplayProto_MALE); got != want {
		t.Errorf("Gender = %d, want %d", got, want)
	}
	if got, want := pokemon.Shiny.ValueOrZero(), true; got != want {
		t.Errorf("Shiny = %v, want %v", got, want)
	}
	if got, want := pokemon.IsStrong.ValueOrZero(), false; got != want {
		t.Errorf("IsStrong = %v, want %v", got, want)
	}
	if got, want := pokemon.Lat, 12.3456789; got != want {
		t.Errorf("Lat = %v, want %v", got, want)
	}
	if got, want := pokemon.Lon, -67.891234; got != want {
		t.Errorf("Lon = %v, want %v", got, want)
	}
}

// TestUpdatePokemonFromDiskEncounterProtoShim mirrors the encounter test for
// the disk-encounter path (lure/incense pokemon), which carries pokemon data
// directly under DiskEncounterOutProto.Pokemon rather than through a wild
// pokemon wrapper.
func TestUpdatePokemonFromDiskEncounterProtoShim(t *testing.T) {
	const displayId = int64(987654321)

	enc := &pogo.DiskEncounterOutProto{
		Result: pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon: &pogo.PokemonProto{
			Id:                uint64(displayId),
			PokemonId:         pogo.HoloPokemonId_CHARMANDER,
			Cp:                512,
			Move1:             pogo.HoloPokemonMove_EMBER_FAST,
			Move2:             pogo.HoloPokemonMove_FLAME_BURST,
			HeightM:           0.6,
			WeightKg:          8.5,
			IndividualAttack:  4,
			IndividualDefense: 5,
			IndividualStamina: 6,
			CpMultiplier:      0.4431,
			Size:              pogo.HoloPokemonSize_XS,
			PokemonDisplay: &pogo.PokemonDisplayProto{
				Form:                    pogo.PokemonDisplayProto_FORM_UNSET,
				Costume:                 pogo.PokemonDisplayProto_UNSET,
				Gender:                  pogo.PokemonDisplayProto_FEMALE,
				Shiny:                   false,
				WeatherBoostedCondition: pogo.GameplayWeatherProto_NONE,
				IsStrongPokemon:         false,
				DisplayId:               displayId,
			},
		},
		CaptureProbability: &pogo.CaptureProbabilityProto{
			PokeballType:           []pogo.Item{pogo.Item_ITEM_POKE_BALL},
			CaptureProbability:     []float32{0.3},
			ReticleDifficultyScale: 1.0,
		},
	}

	shimEnc := pogoshim.AsDiskEncounterOutProto(enc.ProtoReflect())

	pokemon := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(uint64(displayId))}}
	pokemon.newRecord = true

	pokemon.updatePokemonFromDiskEncounterProto(context.Background(), db.DbDetails{}, shimEnc, "misty")

	if got, want := pokemon.Cp.ValueOrZero(), int64(512); got != want {
		t.Errorf("Cp = %d, want %d", got, want)
	}
	if got, want := pokemon.AtkIv.ValueOrZero(), int64(4); got != want {
		t.Errorf("AtkIv = %d, want %d", got, want)
	}
	if got, want := pokemon.DefIv.ValueOrZero(), int64(5); got != want {
		t.Errorf("DefIv = %d, want %d", got, want)
	}
	if got, want := pokemon.StaIv.ValueOrZero(), int64(6); got != want {
		t.Errorf("StaIv = %d, want %d", got, want)
	}
	if got, want := pokemon.Move1.ValueOrZero(), int64(pogo.HoloPokemonMove_EMBER_FAST); got != want {
		t.Errorf("Move1 = %d, want %d", got, want)
	}
	if got, want := pokemon.Move2.ValueOrZero(), int64(pogo.HoloPokemonMove_FLAME_BURST); got != want {
		t.Errorf("Move2 = %d, want %d", got, want)
	}
	if got, want := pokemon.Weight.ValueOrZero(), float64(float32(8.5)); got != want {
		t.Errorf("Weight = %v, want %v", got, want)
	}
	if got, want := pokemon.Height.ValueOrZero(), float64(float32(0.6)); got != want {
		t.Errorf("Height = %v, want %v", got, want)
	}
	if got, want := pokemon.PokemonId, int16(pogo.HoloPokemonId_CHARMANDER); got != want {
		t.Errorf("PokemonId = %d, want %d", got, want)
	}
	if got, want := pokemon.Gender.ValueOrZero(), int64(pogo.PokemonDisplayProto_FEMALE); got != want {
		t.Errorf("Gender = %d, want %d", got, want)
	}
	if got, want := pokemon.SeenType.ValueOrZero(), SeenType_LureEncounter; got != want {
		t.Errorf("SeenType = %q, want %q", got, want)
	}
}
