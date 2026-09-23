package main

import (
	"testing"

	"golbat/pogo"
)

// Newer clients deliver nearby pokemon in a response-level snapshot instead
// of per map cell. The snapshot entries carry no cell or timestamp, so the
// extractor derives them from the map cell listing the pokemon's fort. A
// fort not in the response leaves the cell unset for updateFromNearby to
// derive from the pokestop's stored location. A fort-less entry is skipped:
// the scanner's position must not be used to place pokemon.
func TestExtractSnapshotNearbyPokemon(t *testing.T) {
	const cellA, cellB uint64 = 0x2a0000000000000f, 0x2b0000000000000f
	const tsA, tsB int64 = 1000, 2000

	gmo := &pogo.GetMapObjectsOutProto{
		MapCell: []*pogo.ClientMapCellProto{
			{S2CellId: cellA, AsOfTimeMs: tsA, Fort: []*pogo.PokemonFortProto{{FortId: "fort-a"}}},
			{S2CellId: cellB, AsOfTimeMs: tsB, Fort: []*pogo.PokemonFortProto{{FortId: "fort-b"}}},
		},
		NearbyPokemonSnapshot: &pogo.NearbyPokemonSnapshot{
			Status: pogo.NearbyPokemonSnapshot_COMPLETE,
			Pokemon: []*pogo.NearbyPokemonProto{
				{EncounterId: 1, FortId: "fort-b"},
				{EncounterId: 2},
				{EncounterId: 3, FortId: "fort-not-in-response"},
				nil,
				{EncounterId: 4, FortId: "fort-a"},
			},
		},
	}

	got := extractSnapshotNearbyPokemon(gmo)

	want := []struct {
		encounterId uint64
		cell        uint64
		timestamp   int64
	}{
		{1, cellB, tsB},
		{3, 0, tsA},
		{4, cellA, tsA},
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %d nearby pokemon, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Data.EncounterId != w.encounterId || g.Cell != w.cell || g.Timestamp != w.timestamp {
			t.Errorf("entry %d = encounter %d cell %#x ts %d, want encounter %d cell %#x ts %d",
				i, g.Data.EncounterId, g.Cell, g.Timestamp, w.encounterId, w.cell, w.timestamp)
		}
	}
}

func TestExtractSnapshotNearbyPokemonAbsent(t *testing.T) {
	gmo := &pogo.GetMapObjectsOutProto{MapCell: []*pogo.ClientMapCellProto{{S2CellId: 7}}}
	if got := extractSnapshotNearbyPokemon(gmo); got != nil {
		t.Fatalf("got %+v from a response with no snapshot, want nil", got)
	}
}
