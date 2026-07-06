package readers

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

func TestReadGMO(t *testing.T) {
	wild := pogo.WildPokemonProto_builder{
		EncounterId:  7,
		SpawnPointId: "ABCD",
		Pokemon:      pogo.PokemonProto_builder{Cp: 500, IndividualAttack: 15}.Build(),
	}.Build()
	fort := pogo.PokemonFortProto_builder{FortId: "fort.1", Latitude: 51.5, Longitude: -0.1}.Build()
	cell := pogo.ClientMapCellProto_builder{
		S2CellId:    123,
		Fort:        []*pogo.PokemonFortProto{fort},
		WildPokemon: []*pogo.WildPokemonProto{wild},
	}.Build()
	gmo := pogo.GetMapObjectsOutProto_builder{MapCell: []*pogo.ClientMapCellProto{cell}}.Build()
	raw, err := proto.Marshal(gmo)
	if err != nil {
		t.Fatal(err)
	}

	before := Sink.Load()
	if err := Registry["GET_MAP_OBJECTS"](raw, proto.UnmarshalOptions{}); err != nil {
		t.Fatal(err)
	}
	if Sink.Load() == before {
		t.Fatal("sink unchanged — reader accessed nothing")
	}
}

func TestReadEncounter(t *testing.T) {
	enc := pogo.EncounterOutProto_builder{
		Pokemon: pogo.WildPokemonProto_builder{EncounterId: 9}.Build(),
	}.Build()
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}
	before := Sink.Load()
	if err := Registry["ENCOUNTER"](raw, proto.UnmarshalOptions{}); err != nil {
		t.Fatal(err)
	}
	if Sink.Load() == before {
		t.Fatal("sink unchanged")
	}
}
