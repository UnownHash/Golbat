package decoder

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"golbat/db"
	"golbat/pogo"
	"golbat/webhooks"

	"github.com/guregu/null/v6"
)

// Minior's encounter stats were player-specific for a while (#426) and were
// suppressed everywhere — observation, derivation and carry-over. They are
// spawn-level again, so Minior is treated like any other species; these tests
// pin that, i.e. that hasPlayerSpecificStats no longer excludes it.

func miniorTestPokemon() *Pokemon {
	p := &Pokemon{PokemonData: PokemonData{
		Id: 977401, PokemonId: int16(pogo.HoloPokemonId_MINIOR),
		Lat: 48.85, Lon: 8.2, SpawnId: null.ValueFrom(int64(1234)),
		ExpireTimestamp:         null.ValueFrom(uint32(time.Now().Unix() + 600)),
		ExpireTimestampVerified: true,
		Weather:                 null.ValueFrom(uint8(1)), Form: null.ValueFrom(uint16(2707)),
		Gender: null.ValueFrom(uint8(3)), Costume: null.ValueFrom(uint8(0)),
		IsStrong: null.ValueFrom(false), SeenType: SeenTypeFrom(SeenTypeCodeWild),
	}, newRecord: true}
	p.snapshotOldValues()
	return p
}

func miniorTestEncounter(species pogo.HoloPokemonId, form pogo.PokemonDisplayProto_Form) *pogo.PokemonProto {
	return &pogo.PokemonProto{
		PokemonId: species, Cp: 200, CpMultiplier: .5974,
		IndividualAttack: 10, IndividualDefense: 11, IndividualStamina: 12,
		Move1: 1, Move2: 2, HeightM: 1.5, WeightKg: 25, Size: 3,
		PokemonDisplay: &pogo.PokemonDisplayProto{Form: form, Shiny: true},
	}
}

// assertMiniorStatsRecorded checks the encounter's values landed on the record
// rather than being dropped.
func assertMiniorStatsRecorded(t *testing.T, p *Pokemon) {
	t.Helper()
	if p.AtkIv.ValueOrZero() != 10 || p.DefIv.ValueOrZero() != 11 || p.StaIv.ValueOrZero() != 12 ||
		!p.Iv.Valid || p.Cp.ValueOrZero() != 200 || !p.Level.Valid ||
		p.Move1.ValueOrZero() != 1 || p.Move2.ValueOrZero() != 2 ||
		!p.Height.Valid || !p.Weight.Valid || p.Size.ValueOrZero() != 3 || !p.Shiny.ValueOrZero() {
		t.Errorf("Minior lost its encounter stats: %+v", p.PokemonData)
	}
	if len(p.scanHistory) == 0 {
		t.Error("Minior recorded no encounter scan history")
	}
}

func TestMiniorEncounterRecordsStats(t *testing.T) {
	for _, form := range []pogo.PokemonDisplayProto_Form{
		0, pogo.PokemonDisplayProto_MINIOR_METEOR_BLUE, pogo.PokemonDisplayProto_MINIOR_BLUE,
		pogo.PokemonDisplayProto_MINIOR_METEOR_YELLOW, pogo.PokemonDisplayProto_MINIOR_YELLOW,
	} {
		t.Run(form.String(), func(t *testing.T) {
			p := miniorTestPokemon()
			p.SetForm(null.IntFrom(int64(form)))
			p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, form), "tester")
			assertMiniorStatsRecorded(t, p)
			if p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) || p.Form.ValueOrZero() != uint16(form) {
				t.Error("Minior identity/form changed")
			}
		})
	}
}

func TestMiniorLureAndTappableEncounters(t *testing.T) {
	lureTestSetup(t)
	ctx := context.Background()
	encounter := miniorTestEncounter(pogo.HoloPokemonId_MINIOR, pogo.PokemonDisplayProto_MINIOR_BLUE)
	p := miniorTestPokemon()
	p.updatePokemonFromDiskEncounterProto(ctx, db.DbDetails{}, &pogo.DiskEncounterOutProto{Pokemon: encounter}, "tester")
	assertMiniorStatsRecorded(t, p)
	if p.SeenType.Code != SeenTypeCodeLureEncounter || p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) {
		t.Fatal("lure encounter lost its identity/source")
	}
	p = miniorTestPokemon()
	p.updatePokemonFromTappableEncounterProto(ctx, db.DbDetails{},
		&pogo.ProcessTappableProto{LocationHintLat: 48.86, LocationHintLng: 8.21},
		&pogo.TappableEncounterProto{Pokemon: encounter}, "tester", time.Now().UnixMilli())
	assertMiniorStatsRecorded(t, p)
	if p.Lat != 48.86 || p.Lon != 8.21 || p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) {
		t.Fatal("tappable encounter lost its location/identity")
	}
}

func TestMiniorApiStatsPresentAfterEncounter(t *testing.T) {
	lureTestSetup(t)
	p := miniorTestPokemon()
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, 0), "tester")
	data, err := json.Marshal(buildApiPokemonResult(p))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"atk_iv", "def_iv", "sta_iv", "iv", "cp", "level", "move_1", "move_2", "height", "weight", "size", "shiny"} {
		if string(payload[key]) == "null" {
			t.Errorf("API %s = null, want the encounter's value", key)
		}
	}
}

func TestMiniorEncounterPublishesIvSpawn(t *testing.T) {
	sink := lureTestSetup(t)
	p := miniorTestPokemon()
	before := p.PokemonData
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, 0), "tester")
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "tester")
	assertMiniorStatsRecorded(t, p)
	if p.Lat != before.Lat || p.Lon != before.Lon || p.SpawnId != before.SpawnId ||
		p.ExpireTimestamp != before.ExpireTimestamp || p.Gender != before.Gender ||
		p.Costume != before.Costume || p.Weather != before.Weather ||
		p.SeenType != before.SeenType || !p.ExpireTimestampVerified {
		t.Error("Minior's shared spawn/display fields changed")
	}
	msgs := sink.drain()
	if len(msgs) != 1 || msgs[0].whType != webhooks.PokemonIV {
		t.Fatalf("webhooks = %+v, want one IV spawn webhook", msgs)
	}
	raw, err := json.Marshal(msgs[0].message)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cp", "individual_attack", "individual_defense", "individual_stamina", "pokemon_level", "move_1", "move_2", "height", "weight", "size", "shiny"} {
		if value, present := payload[key]; !present || value == nil {
			t.Errorf("webhook %s = %v (present %t), want the encounter's value", key, value, present)
		}
	}
	if payload["pokemon_id"] != float64(pogo.HoloPokemonId_MINIOR) || payload["latitude"] != before.Lat || payload["longitude"] != before.Lon {
		t.Error("spawn missing from webhook")
	}
}

func TestOtherSpeciesKeepsEncounterStats(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{PokemonId: 25, Weather: null.ValueFrom(uint8(1))}, newRecord: true}
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_PIKACHU, 0), "tester")
	if !p.AtkIv.Valid || p.AtkIv.ValueOrZero() != 10 || !p.DefIv.Valid || !p.StaIv.Valid ||
		!p.Iv.Valid || !p.Cp.Valid || !p.Level.Valid || !p.Move1.Valid || !p.Move2.Valid ||
		!p.Height.Valid || !p.Weight.Valid || !p.Size.Valid || !p.Shiny.Valid || len(p.scanHistory) == 0 {
		t.Fatalf("ordinary encounter lost stats: %+v", p.PokemonData)
	}
	before := p.PokemonData
	p.ClearDirty()
	p.repopulateIv(int64(pogo.GameplayWeatherProto_CLEAR), false)
	if !reflect.DeepEqual(before, p.PokemonData) {
		t.Fatal("ordinary unchanged-weather encounter data changed")
	}
}

// Derivation covers Minior as it does any other species: IVs and level on a
// first sighting, and size on a record whose IVs are already present. (Moves
// need a loaded masterfile, which these tests do not have.)
func TestDeriveCoversMinior(t *testing.T) {
	p := miniorTestPokemon()
	p.deriveIvIfAbsent()
	if !p.AtkIv.Valid || !p.DefIv.Valid || !p.StaIv.Valid || !p.Iv.Valid || !p.Level.Valid {
		t.Errorf("derived no IVs/level for Minior: %+v", p.PokemonData)
	}

	p = miniorTestPokemon()
	p.AtkIv = null.ValueFrom(uint8(15))
	p.deriveSizeAndMovesIfAbsent()
	if !p.Size.Valid {
		t.Errorf("derived no size for Minior: %+v", p.PokemonData)
	}
}

// A spawn that displays as Minior after being seen as another species keeps
// its IVs and level, as it does for any other species change.
func TestSpeciesChangeIntoMiniorKeepsStats(t *testing.T) {
	withStats := func() *Pokemon {
		p := miniorTestPokemon()
		p.newRecord = false
		p.PokemonId = int16(pogo.HoloPokemonId_PIKACHU)
		p.Form = null.ValueFrom(uint16(pogo.PokemonDisplayProto_PIKACHU_NORMAL))
		p.AtkIv = null.ValueFrom(uint8(15))
		p.DefIv = null.ValueFrom(uint8(14))
		p.StaIv = null.ValueFrom(uint8(13))
		p.Iv = null.ValueFrom(float32(93.3))
		p.Level = null.ValueFrom(uint8(30))
		p.Cp = null.ValueFrom(uint16(900))
		return p
	}

	for species, display := range map[pogo.HoloPokemonId]*pogo.PokemonDisplayProto{
		pogo.HoloPokemonId_MINIOR: {Form: pogo.PokemonDisplayProto_MINIOR_METEOR_BLUE, Gender: 3, WeatherBoostedCondition: 1},
		pogo.HoloPokemonId_RAICHU: {Form: pogo.PokemonDisplayProto_RAICHU_NORMAL, Gender: 3, WeatherBoostedCondition: 1},
	} {
		t.Run(species.String(), func(t *testing.T) {
			p := withStats()
			p.setPokemonDisplay(int16(species), display)
			if p.PokemonId != int16(species) {
				t.Fatalf("PokemonId = %d, want %s", p.PokemonId, species)
			}
			if p.AtkIv.ValueOrZero() != 15 || p.DefIv.ValueOrZero() != 14 || p.StaIv.ValueOrZero() != 13 ||
				!p.Iv.Valid || p.Level.ValueOrZero() != 30 {
				t.Errorf("species change dropped IVs/level: %+v", p.PokemonData)
			}
		})
	}
}
