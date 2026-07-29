package protobench_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"protobench/pogo"
)

// Builders + getters compile in BOTH build modes (default and
// -tags protoopaque); this is the hybrid-API contract the harness relies on.
func TestRoundTrip(t *testing.T) {
	wild := pogo.WildPokemonProto_builder{
		EncounterId:  7,
		SpawnPointId: "ABCD",
		Pokemon:      pogo.PokemonProto_builder{Cp: 500}.Build(),
	}.Build()
	raw, err := proto.Marshal(wild)
	if err != nil {
		t.Fatal(err)
	}
	var back pogo.WildPokemonProto
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.GetEncounterId() != 7 || back.GetPokemon().GetCp() != 500 {
		t.Fatalf("round trip mismatch: id=%d cp=%d", back.GetEncounterId(), back.GetPokemon().GetCp())
	}
}
