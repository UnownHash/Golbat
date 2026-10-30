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

func assertMiniorStatsNull(t *testing.T, p *Pokemon) {
	t.Helper()
	if p.AtkIv.Valid || p.DefIv.Valid || p.StaIv.Valid || p.Iv.Valid ||
		p.Cp.Valid || p.Level.Valid || p.Move1.Valid || p.Move2.Valid ||
		p.Height.Valid || p.Weight.Valid || p.Size.Valid || p.Shiny.Valid || p.Pvp.Valid {
		t.Errorf("Minior was assigned encounter stats: %+v", p.PokemonData)
	}
	if len(p.scanHistory) != 0 || len(p.GolbatInternal) != 0 {
		t.Error("Minior recorded encounter scan history")
	}
}

func TestMiniorEncounterSetsNoStats(t *testing.T) {
	for _, form := range []pogo.PokemonDisplayProto_Form{
		0, pogo.PokemonDisplayProto_MINIOR_METEOR_BLUE, pogo.PokemonDisplayProto_MINIOR_BLUE,
		pogo.PokemonDisplayProto_MINIOR_METEOR_YELLOW, pogo.PokemonDisplayProto_MINIOR_YELLOW,
	} {
		t.Run(form.String(), func(t *testing.T) {
			p := miniorTestPokemon()
			p.SetForm(null.IntFrom(int64(form)))
			p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, form), "tester", priorSighting{})
			assertMiniorStatsNull(t, p)
			if p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) || p.Form.ValueOrZero() != uint16(form) {
				t.Error("Minior identity/form changed")
			}
		})
	}
}

func TestMiniorWeatherChangeCannotRestoreEncounterStats(t *testing.T) {
	p := miniorTestPokemon()
	// An older build may have recorded a scan; do not restore its values.
	p.scanHistory = []*pokemonScan{{Pokemon: int32(pogo.HoloPokemonId_MINIOR), Weather: 1, Level: 20, Attack: 15, Defense: 15, Stamina: 15}}
	history := p.scanHistory[0]
	p.repopulateIv(int64(pogo.GameplayWeatherProto_NONE), false)
	if p.AtkIv.Valid || p.Cp.Valid || p.Level.Valid || p.Iv.Valid {
		t.Fatal("weather change restored Minior encounter stats")
	}
	if len(p.scanHistory) != 1 || p.scanHistory[0] != history {
		t.Fatal("weather check should not clean up stored scan history")
	}
}

func TestMiniorGuardsDoNotCleanExistingStats(t *testing.T) {
	p := miniorTestPokemon()
	p.Cp = null.ValueFrom(uint16(123))
	p.Level = null.ValueFrom(uint8(20))
	p.AtkIv = null.ValueFrom(uint8(15))
	before := p.PokemonData
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, 0), "", priorSighting{})
	p.repopulateIv(int64(pogo.GameplayWeatherProto_NONE), false)
	if !reflect.DeepEqual(before, p.PokemonData) {
		t.Fatal("go-forward guards should neither clear nor overwrite existing stats")
	}
}

func TestMiniorLureAndTappableEncounters(t *testing.T) {
	lureTestSetup(t)
	ctx := context.Background()
	encounter := miniorTestEncounter(pogo.HoloPokemonId_MINIOR, pogo.PokemonDisplayProto_MINIOR_BLUE)
	p := miniorTestPokemon()
	p.updatePokemonFromDiskEncounterProto(ctx, db.DbDetails{}, &pogo.DiskEncounterOutProto{Pokemon: encounter}, "tester")
	assertMiniorStatsNull(t, p)
	if p.SeenType.Code != SeenTypeCodeLureEncounter || p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) {
		t.Fatal("lure encounter lost its identity/source")
	}
	p = miniorTestPokemon()
	p.updatePokemonFromTappableEncounterProto(ctx, db.DbDetails{},
		&pogo.ProcessTappableProto{LocationHintLat: 48.86, LocationHintLng: 8.21},
		&pogo.TappableEncounterProto{Pokemon: encounter}, "tester", time.Now().UnixMilli())
	assertMiniorStatsNull(t, p)
	if p.Lat != 48.86 || p.Lon != 8.21 || p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) {
		t.Fatal("tappable encounter lost its location/identity")
	}
}

func TestMiniorApiStatsNullAfterEncounter(t *testing.T) {
	lureTestSetup(t)
	p := miniorTestPokemon()
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, 0), "tester", priorSighting{})
	data, err := json.Marshal(buildApiPokemonResult(p))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"atk_iv", "def_iv", "sta_iv", "iv", "cp", "level", "move_1", "move_2", "height", "weight", "size", "shiny"} {
		if string(payload[key]) != "null" {
			t.Errorf("API %s = %s, want null", key, payload[key])
		}
	}
	if string(payload["pvp"]) != "{}" {
		t.Errorf("API PVP = %s, want existing empty-ranking representation", payload["pvp"])
	}
}

func TestMiniorEncounterPublishesNoIvSpawn(t *testing.T) {
	sink := lureTestSetup(t)
	p := miniorTestPokemon()
	before := p.PokemonData
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_MINIOR, 0), "tester", priorSighting{})
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, false, false, true, time.Now().Unix(), "tester")
	assertMiniorStatsNull(t, p)
	if p.Lat != before.Lat || p.Lon != before.Lon || p.SpawnId != before.SpawnId ||
		p.ExpireTimestamp != before.ExpireTimestamp || p.Form != before.Form ||
		p.Gender != before.Gender || p.Costume != before.Costume || p.Weather != before.Weather ||
		p.SeenType != before.SeenType || !p.ExpireTimestampVerified {
		t.Error("Minior's shared spawn/display fields changed")
	}
	msgs := sink.drain()
	if len(msgs) != 1 || msgs[0].whType != webhooks.PokemonNoIV {
		t.Fatalf("webhooks = %+v, want one no-IV spawn webhook", msgs)
	}
	raw, err := json.Marshal(msgs[0].message)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cp", "individual_attack", "individual_defense", "individual_stamina", "pokemon_level", "move_1", "move_2", "height", "weight", "size", "shiny", "pvp"} {
		value, present := payload[key]
		if !present || value != nil {
			t.Errorf("webhook %s = %v (present %t), want explicit null", key, value, present)
		}
	}
	if payload["pokemon_id"] != float64(pogo.HoloPokemonId_MINIOR) || payload["latitude"] != before.Lat || payload["longitude"] != before.Lon {
		t.Error("spawn missing from webhook")
	}

	// With all personal values already empty, a repeated save stays a no-op.
	p.snapshotOldValues()
	savePokemonRecordAsAtTime(context.Background(), db.DbDetails{}, p, true, false, true, time.Now().Unix(), "other-tester")
	if got := sink.drain(); len(got) != 0 {
		t.Fatalf("unchanged Minior emitted another webhook: %+v", got)
	}
}

func TestOtherSpeciesKeepsEncounterStats(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{PokemonId: 25, Weather: null.ValueFrom(uint8(1))}, newRecord: true}
	p.addEncounterPokemon(context.Background(), db.DbDetails{}, miniorTestEncounter(pogo.HoloPokemonId_PIKACHU, 0), "tester", priorSighting{})
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

// Derivation must not invent stats for Minior either: neither IVs and level
// on a first sighting, nor size and moves on a record whose IVs are present.
func TestDeriveSkipsMinior(t *testing.T) {
	p := miniorTestPokemon()
	p.deriveIvIfAbsent()
	assertMiniorStatsNull(t, p)

	p = miniorTestPokemon()
	p.AtkIv = null.ValueFrom(uint8(15))
	p.deriveSizeAndMovesIfAbsent()
	if p.Size.Valid || p.Move1.Valid || p.Move2.Valid {
		t.Errorf("derived size/moves for Minior: %+v", p.PokemonData)
	}
}

// A spawn that displays as Minior after being seen as another species must
// not keep that species' IVs, level or CP; an ordinary species change still
// keeps its IVs.
func TestSpeciesChangeIntoMiniorClearsStats(t *testing.T) {
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

	p := withStats()
	p.setPokemonDisplay(int16(pogo.HoloPokemonId_MINIOR), &pogo.PokemonDisplayProto{
		Form: pogo.PokemonDisplayProto_MINIOR_METEOR_BLUE, Gender: 3, WeatherBoostedCondition: 1,
	}, SeenTypeCodeWild)
	if p.PokemonId != int16(pogo.HoloPokemonId_MINIOR) {
		t.Fatalf("PokemonId = %d, want Minior", p.PokemonId)
	}
	if p.AtkIv.Valid || p.DefIv.Valid || p.StaIv.Valid || p.Iv.Valid || p.Level.Valid || p.Cp.Valid {
		t.Errorf("Minior kept the previous species' stats: %+v", p.PokemonData)
	}

	p = withStats()
	p.setPokemonDisplay(int16(pogo.HoloPokemonId_RAICHU), &pogo.PokemonDisplayProto{
		Form: pogo.PokemonDisplayProto_RAICHU_NORMAL, Gender: 3, WeatherBoostedCondition: 1,
	}, SeenTypeCodeWild)
	if !p.AtkIv.Valid || !p.Level.Valid {
		t.Errorf("ordinary species change dropped IVs/level: %+v", p.PokemonData)
	}
}
