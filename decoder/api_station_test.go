package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

// goldenSnapshotStation is a representative station with a mix of set and
// unset (null) fields across every nullable column, used to pin the exact wire
// format.
func goldenSnapshotStation() *Station {
	return &Station{
		StationData: StationData{
			Id:                "station-abc",
			Lat:               45.6789,
			Lon:               -120.9876,
			Name:              "Test Station",
			StartTime:         1699990000,
			EndTime:           1700003600,
			IsBattleAvailable: true,
			Updated:           1699999999,
			BattleLevel:       null.IntFrom(5),
			// BattleStart intentionally left null
			BattleEnd:            null.IntFrom(1700001000),
			BattlePokemonId:      null.IntFrom(150),
			BattlePokemonForm:    null.IntFrom(0),
			BattlePokemonCostume: null.IntFrom(1),
			// BattlePokemonGender intentionally left null
			BattlePokemonAlignment: null.IntFrom(2),
			BattlePokemonBreadMode: null.IntFrom(0),
			BattlePokemonMove1:     null.IntFrom(101),
			BattlePokemonMove2:     null.IntFrom(202),
			TotalStationedPokemon:  null.IntFrom(6),
			// TotalStationedGmax intentionally left null
			StationedPokemon: null.StringFrom("[{\"pokemon_id\":150}]"),
		},
	}
}

// TestBuildStationResult_GoldenSnapshot pins the exact JSON wire format of an
// ApiStationResult. Any accidental change to a json tag, field type,
// pointer/null handling, or field order will fail this test. Unset nullable
// fields serialize as null (pointers are nil, no omitempty).
func TestBuildStationResult_GoldenSnapshot(t *testing.T) {
	got, err := json.Marshal(BuildStationResult(goldenSnapshotStation()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"id":"station-abc","lat":45.6789,"lon":-120.9876,"name":"Test Station","start_time":1699990000,"end_time":1700003600,"is_battle_available":true,"updated":1699999999,"battle_level":5,"battle_start":null,"battle_end":1700001000,"battle_pokemon_id":150,"battle_pokemon_form":0,"battle_pokemon_costume":1,"battle_pokemon_gender":null,"battle_pokemon_alignment":2,"battle_pokemon_bread_mode":0,"battle_pokemon_move_1":101,"battle_pokemon_move_2":202,"total_stationed_pokemon":6,"total_stationed_gmax":null,"stationed_pokemon":"[{\"pokemon_id\":150}]"}`

	if string(got) != want {
		t.Errorf("wire format changed.\n got: %s\nwant: %s", got, want)
	}
}
