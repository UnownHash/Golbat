package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

func TestBuildApiPokemonResult_NullablesAndDefaults(t *testing.T) {
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:                 12345,
			Lat:                51.5,
			Lon:                -0.1,
			PokemonId:          25,
			Cp:                 null.IntFrom(500),
			AtkIv:              null.IntFrom(15),
			FirstSeenTimestamp: 1000,
			Changed:            2000,
			// Level intentionally left unset -> should be a nil pointer (null)
		},
	}

	got := buildApiPokemonResult(p) // ohbem is nil in tests -> empty PVP

	if got.Id != "12345" {
		t.Errorf("Id = %q, want \"12345\"", got.Id)
	}
	if got.Cp == nil || *got.Cp != 500 {
		t.Errorf("Cp = %v, want pointer to 500", got.Cp)
	}
	if got.Level != nil {
		t.Errorf("Level = %v, want nil (null)", got.Level)
	}
	if got.PokemonId != 25 {
		t.Errorf("PokemonId = %d, want 25", got.PokemonId)
	}
	if got.Capture1 != nil || got.IsEvent != 0 {
		t.Errorf("Capture1/IsEvent should be unset for parity, got %v / %d", got.Capture1, got.IsEvent)
	}
	if got.Pvp.Little != nil || got.Pvp.Great != nil || got.Pvp.Ultra != nil {
		t.Errorf("PVP leagues should be nil when ohbem is nil, got %+v", got.Pvp)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(m["level"]) != "null" {
		t.Errorf("level should serialize as null, got %s", m["level"])
	}
	if _, ok := m["pvp"]; !ok {
		t.Errorf("pvp key missing from output")
	}
}

// goldenSnapshotPokemon is a representative pokemon with a mix of set and unset
// (null) fields across every type, used to pin the exact wire format.
func goldenSnapshotPokemon() *Pokemon {
	return &Pokemon{
		PokemonData: PokemonData{
			Id:                      9876543210,
			PokestopId:              null.StringFrom("stop-abc"),
			SpawnId:                 null.IntFrom(7777),
			Lat:                     12.3456,
			Lon:                     -65.4321,
			Weight:                  null.FloatFrom(3.14),
			Size:                    null.IntFrom(2),
			Height:                  null.FloatFrom(0.5),
			ExpireTimestamp:         null.IntFrom(1700000000),
			Updated:                 null.IntFrom(1699999999),
			PokemonId:               150,
			Move1:                   null.IntFrom(216),
			Move2:                   null.IntFrom(94),
			Gender:                  null.IntFrom(1),
			Cp:                      null.IntFrom(3500),
			AtkIv:                   null.IntFrom(15),
			DefIv:                   null.IntFrom(14),
			StaIv:                   null.IntFrom(13),
			Iv:                      null.FloatFrom(93.33),
			Form:                    null.IntFrom(0),
			Level:                   null.IntFrom(35),
			Weather:                 null.IntFrom(1),
			Costume:                 null.IntFrom(0),
			FirstSeenTimestamp:      1699990000,
			Changed:                 1699995000,
			CellId:                  null.IntFrom(1234567890123),
			ExpireTimestampVerified: true,
			// DisplayPokemonId / DisplayPokemonForm intentionally left null
			IsDitto:  false,
			SeenType: null.StringFrom("encounter"),
			Shiny:    null.BoolFrom(true),
			// Username intentionally left null
		},
	}
}

// TestBuildApiPokemonResult_GoldenSnapshot pins the exact JSON wire format of an
// ApiPokemonResult (with ohbem disabled so pvp is {}). This struct is now shared
// by every pokemon endpoint (v1/v2/v3/search), so any accidental change to a json
// tag, field type, pointer/null handling, or field order will fail this test.
func TestBuildApiPokemonResult_GoldenSnapshot(t *testing.T) {
	if ohbem != nil {
		t.Fatalf("expected ohbem to be nil in tests")
	}

	got, err := json.Marshal(buildApiPokemonResult(goldenSnapshotPokemon()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `{"id":"9876543210","pokestop_id":"stop-abc","spawn_id":7777,"lat":12.3456,"lon":-65.4321,"weight":3.14,"size":2,"height":0.5,"expire_timestamp":1700000000,"updated":1699999999,"pokemon_id":150,"move_1":216,"move_2":94,"gender":1,"cp":3500,"atk_iv":15,"def_iv":14,"sta_iv":13,"iv":93.33,"form":0,"level":35,"weather":1,"costume":0,"first_seen_timestamp":1699990000,"changed":1699995000,"cell_id":1234567890123,"expire_timestamp_verified":true,"display_pokemon_id":null,"display_pokemon_form":null,"is_ditto":false,"seen_type":"encounter","shiny":true,"username":null,"capture_1":null,"capture_2":null,"capture_3":null,"pvp":{},"is_event":0}`

	if string(got) != want {
		t.Errorf("wire format changed.\n got: %s\nwant: %s", got, want)
	}
}

// TestApiPvpRankings_OmitsEmptyLeagues pins the wire-compat behavior that a league
// with no ranking is omitted from the JSON entirely (matching the legacy map),
// rather than emitted as null.
func TestApiPvpRankings_OmitsEmptyLeagues(t *testing.T) {
	// Only Great populated; Little and Ultra empty.
	pvp := ApiPvpRankings{Great: []ApiPvpEntry{{Pokemon: 99, Rank: 1}}}
	b, err := json.Marshal(pvp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["great"]; !ok {
		t.Errorf("great should be present: %s", b)
	}
	if _, ok := m["little"]; ok {
		t.Errorf("little should be omitted when empty: %s", b)
	}
	if _, ok := m["ultra"]; ok {
		t.Errorf("ultra should be omitted when empty: %s", b)
	}
}

func TestApiPokemonScanResultV3_WireShape(t *testing.T) {
	res := ApiPokemonScanResultV3{
		Pokemon:  []ApiPokemonResult{{Id: "1", PokemonId: 25}},
		Examined: 5,
		Skipped:  1,
		Total:    6,
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"pokemon", "examined", "skipped", "total"} {
		if _, ok := m[k]; !ok {
			t.Errorf("v3 wrapper missing key %q", k)
		}
	}
}

func TestApiPokemonV2_BareArrayShape(t *testing.T) {
	res := []ApiPokemonResult{{Id: "1", PokemonId: 25}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 || b[0] != '[' {
		t.Errorf("v2 response must be a bare array, got: %s", b)
	}
}
