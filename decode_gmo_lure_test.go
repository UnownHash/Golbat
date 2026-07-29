package main

import (
	"os"
	"testing"

	"google.golang.org/protobuf/proto"

	"golbat/pogo"
)

// Live-derived regression fixture (PR #391 review): current clients deliver
// lure pokemon in the repeated fort.ActiveFortPokemon wrapper while the
// singular fort.ActivePokemon stays nil — 17k captured GMOs contained zero
// singular occurrences. The nested MapPokemonProto carries zero lat/lon on
// the wire, so placement must come from the enclosing fort.
// Fixture provenance: sanitized live capture, sha256
// f8c834fb407b234854e2fc816bdd0c7c85563658996f157aef7880987c01570a.
func TestExtractFortMapPokemonFromRepeatedWrapper(t *testing.T) {
	raw, err := os.ReadFile("testdata/gmo-active-fort-pokemon.pb")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gmo pogo.GetMapObjectsOutProto
	if err := proto.Unmarshal(raw, &gmo); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	cell := gmo.MapCell[0]
	fort := cell.Fort[0]
	if fort.ActivePokemon != nil {
		t.Fatalf("fixture invariant violated: singular ActivePokemon must be nil")
	}

	got := extractFortMapPokemon(fort, cell.S2CellId, cell.AsOfTimeMs)

	if len(got) != 1 {
		t.Fatalf("extracted %d map pokemon, want 1", len(got))
	}
	mp := got[0]
	if mp.Data.EncounterId != 72623859790382856 {
		t.Errorf("EncounterId = %d, want 72623859790382856", mp.Data.EncounterId)
	}
	if mp.FortId != "sanitized-lure-fort" || mp.Lat != 1.25 || mp.Lon != 2.5 {
		t.Errorf("placement = %q (%v,%v), want sanitized-lure-fort (1.25,2.5) from the enclosing fort",
			mp.FortId, mp.Lat, mp.Lon)
	}
	if mp.Data.ExpirationTimeMs != 4102444980000 {
		t.Errorf("ExpirationTimeMs = %d, want 4102444980000", mp.Data.ExpirationTimeMs)
	}
	if mp.Cell != cell.S2CellId || mp.Timestamp != cell.AsOfTimeMs {
		t.Errorf("cell/timestamp = %d/%d, want %d/%d", mp.Cell, mp.Timestamp, cell.S2CellId, cell.AsOfTimeMs)
	}
}

func TestExtractFortMapPokemonFiltersAndDedupes(t *testing.T) {
	lure := &pogo.MapPokemonProto{SpawnpointId: "fort-x", EncounterId: 424242, PokedexTypeId: 25}
	fort := &pogo.PokemonFortProto{
		FortId:        "fort-x",
		Latitude:      10,
		Longitude:     20,
		ActivePokemon: lure,
		ActiveFortPokemon: []*pogo.FortPokemonProto{
			nil, // nil wrapper must not panic
			{SpawnType: pogo.FortPokemonProto_LURE, PokemonProto: nil},  // nil inner proto skipped
			{SpawnType: pogo.FortPokemonProto_LURE, PokemonProto: lure}, // duplicate of singular -> deduped
			{SpawnType: pogo.FortPokemonProto_POWER_UP,
				PokemonProto: &pogo.MapPokemonProto{EncounterId: 555}}, // POWER_UP is not a lure
			{SpawnType: pogo.FortPokemonProto_LURE,
				PokemonProto: &pogo.MapPokemonProto{EncounterId: 636363}}, // distinct lure kept
		},
	}

	got := extractFortMapPokemon(fort, 99, 1000)

	if len(got) != 2 {
		t.Fatalf("extracted %d map pokemon, want 2 (singular deduped against repeated, POWER_UP and nils excluded)", len(got))
	}
	if got[0].Data.EncounterId != 424242 || got[1].Data.EncounterId != 636363 {
		t.Errorf("encounter ids = %d,%d want 424242,636363", got[0].Data.EncounterId, got[1].Data.EncounterId)
	}
	for i, mp := range got {
		if mp.FortId != "fort-x" || mp.Lat != 10 || mp.Lon != 20 {
			t.Errorf("entry %d placement = %q (%v,%v), want fort-x (10,20)", i, mp.FortId, mp.Lat, mp.Lon)
		}
	}
}

func TestExtractFortMapPokemonSingularOnly(t *testing.T) {
	fort := &pogo.PokemonFortProto{
		FortId:        "fort-legacy",
		Latitude:      -3,
		Longitude:     4,
		ActivePokemon: &pogo.MapPokemonProto{SpawnpointId: "fort-legacy", EncounterId: 777},
	}

	got := extractFortMapPokemon(fort, 7, 2000)

	if len(got) != 1 || got[0].Data.EncounterId != 777 || got[0].FortId != "fort-legacy" {
		t.Fatalf("legacy singular extraction broken: %+v", got)
	}
}
