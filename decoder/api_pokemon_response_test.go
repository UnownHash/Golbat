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

// TestBuildPokemonResult_GoldenParity pins the wire-compatibility guarantee: with
// ohbem == nil, every shared NON-pvp key of the new buildPokemonResult must be
// byte-identical to the legacy buildApiPokemonResult. The pvp key is excluded from
// the byte comparison and instead asserted separately to document the intended
// wire divergence (legacy -> null; new -> {little,great,ultra} object).
func TestBuildPokemonResult_GoldenParity(t *testing.T) {
	// ohbem is nil in tests, so PVP is disabled for both builders.
	if ohbem != nil {
		t.Fatalf("expected ohbem to be nil in tests")
	}

	// A representative mix of set and unset (null) fields across types.
	p := &Pokemon{
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

	legacy := buildApiPokemonResult(p)
	fresh := buildPokemonResult(p)

	legacyBytes, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	freshBytes, err := json.Marshal(fresh)
	if err != nil {
		t.Fatalf("marshal fresh: %v", err)
	}

	var legacyMap, freshMap map[string]json.RawMessage
	if err := json.Unmarshal(legacyBytes, &legacyMap); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if err := json.Unmarshal(freshBytes, &freshMap); err != nil {
		t.Fatalf("unmarshal fresh: %v", err)
	}

	// The two builders must emit exactly the same set of keys.
	for k := range legacyMap {
		if _, ok := freshMap[k]; !ok {
			t.Errorf("key %q present in legacy but missing from new result", k)
		}
	}
	for k := range freshMap {
		if _, ok := legacyMap[k]; !ok {
			t.Errorf("key %q present in new result but missing from legacy", k)
		}
	}

	// Every shared NON-pvp key must be byte-identical.
	for k, legacyVal := range legacyMap {
		if k == "pvp" {
			continue
		}
		freshVal, ok := freshMap[k]
		if !ok {
			continue // already reported above
		}
		if string(legacyVal) != string(freshVal) {
			t.Errorf("scalar wire mismatch for key %q: legacy=%s new=%s", k, legacyVal, freshVal)
		}
	}

	// Document the intended pvp divergence as explicit assertions.
	// Legacy: Pvp is interface{} nil when ohbem == nil -> marshals to null.
	if got := string(legacyMap["pvp"]); got != "null" {
		t.Errorf("legacy pvp = %s, want null (ohbem disabled)", got)
	}
	// New: fixed-league object with all three keys null.
	if got := string(freshMap["pvp"]); got != `{"little":null,"great":null,"ultra":null}` {
		t.Errorf("new pvp = %s, want object form with null leagues", got)
	}
}

func TestPokemonScanResultV3_WireShape(t *testing.T) {
	res := PokemonScanResultV3{
		Pokemon:  []PokemonResult{{Id: "1", PokemonId: 25}},
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

func TestPokemonV2_BareArrayShape(t *testing.T) {
	res := []PokemonResult{{Id: "1", PokemonId: 25}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 || b[0] != '[' {
		t.Errorf("v2 response must be a bare array, got: %s", b)
	}
}
