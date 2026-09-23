package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"golbat/decoder"
	"golbat/pogo"
	"golbat/stats_collector"
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

	got := appendSnapshotNearbyPokemon(nil, gmo)

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
	if got := appendSnapshotNearbyPokemon(nil, gmo); got != nil {
		t.Fatalf("got %+v from a response with no snapshot, want nil", got)
	}
}

// The snapshot is only extracted when nearby pokemon are processed; a scan
// rule that disables them skips the work entirely.
func TestDecodeGMOSkipsSnapshotWhenNearbyDisabled(t *testing.T) {
	statsCollector = stats_collector.NewNoopStatsCollector()
	data, err := proto.Marshal(&pogo.GetMapObjectsOutProto{
		Status: pogo.GetMapObjectsOutProto_SUCCESS,
		MapCell: []*pogo.ClientMapCellProto{
			{S2CellId: 0x2a0000000000000f, AsOfTimeMs: 1000, Fort: []*pogo.PokemonFortProto{{FortId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},
		},
		NearbyPokemonSnapshot: &pogo.NearbyPokemonSnapshot{
			Status:  pogo.NearbyPokemonSnapshot_COMPLETE,
			Pokemon: []*pogo.NearbyPokemonProto{{EncounterId: 1, FortId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, params := range []decoder.ScanParameters{
		{ProcessPokemon: false, ProcessNearby: true},
		{ProcessPokemon: true, ProcessNearby: false},
	} {
		res := decodeGMO(context.Background(), &ProtoData{Data: data}, params)
		if !strings.HasSuffix(res, " 0 nearby") {
			t.Errorf("decodeGMO with %+v = %q, want no nearby pokemon extracted", params, res)
		}
	}
}

// A per-cell nearby pokemon without a fort is a cell pokemon, placed at the
// cell centre. It is not collected unless cell pokemon are processed.
func TestDecodeGMOSkipsCellPokemonWhenCellDisabled(t *testing.T) {
	statsCollector = stats_collector.NewNoopStatsCollector()
	data, err := proto.Marshal(&pogo.GetMapObjectsOutProto{
		Status: pogo.GetMapObjectsOutProto_SUCCESS,
		MapCell: []*pogo.ClientMapCellProto{{
			S2CellId:      0x2a0000000000000f,
			AsOfTimeMs:    1000,
			NearbyPokemon: []*pogo.NearbyPokemonProto{{EncounterId: 2}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	params := decoder.ScanParameters{ProcessPokemon: true, ProcessNearby: true, ProcessNearbyCell: false}
	if res := decodeGMO(context.Background(), &ProtoData{Data: data}, params); !strings.HasSuffix(res, " 0 nearby") {
		t.Errorf("decodeGMO with cell pokemon off = %q, want the fort-less nearby pokemon skipped", res)
	}
}
