package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

func TestBuildPokemonResult_NullablesAndDefaults(t *testing.T) {
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

	got := buildPokemonResult(p) // ohbem is nil in tests -> empty PVP

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
