package pogoshim_test

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

func TestShimOverStdAndHyperpb(t *testing.T) {
	enc := &pogo.EncounterOutProto{
		Pokemon: &pogo.WildPokemonProto{
			EncounterId:  7,
			SpawnPointId: "ABCD",
			Pokemon: &pogo.PokemonProto{
				Cp:             500,
				CpMultiplier:   0.79,
				PokemonDisplay: &pogo.PokemonDisplayProto{Shiny: true},
			},
		},
	}
	raw, err := proto.Marshal(enc)
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, e pogoshim.EncounterOutProto) {
		w := e.GetPokemon()
		if w.IsZero() || w.GetEncounterId() != 7 || w.GetSpawnPointId() != "ABCD" {
			t.Fatalf("%s: wild mismatch", name)
		}
		p := w.GetPokemon()
		if p.GetCp() != 500 || p.GetCpMultiplier() != float32(0.79) {
			t.Fatalf("%s: pokemon mismatch", name)
		}
		if !p.GetPokemonDisplay().GetShiny() {
			t.Fatalf("%s: shiny lost", name)
		}
		if e.GetActiveItem() != pogo.Item_ITEM_UNKNOWN { // absent enum -> zero, typed as pogo enum
			t.Fatalf("%s: absent enum not zero", name)
		}
	}

	// std wrap
	var back pogo.EncounterOutProto
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	check("std", pogoshim.AsEncounterOutProto(back.ProtoReflect()))

	// hyperpb wrap
	ty := hyperpb.CompileMessageDescriptor((*pogo.EncounterOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	defer shared.Free()
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	check("hyperpb", pogoshim.AsEncounterOutProto(msg.ProtoReflect()))
}
