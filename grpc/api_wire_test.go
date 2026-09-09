package grpc

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The service must expose exactly the six RPCs the spec lists.
func TestGolbatApiServiceShape(t *testing.T) {
	svc := File_grpc_api_proto.Services().ByName("GolbatApi")
	if svc == nil {
		t.Fatal("GolbatApi service missing from grpc/api.proto")
	}
	want := []string{"ScanPokemon", "GetPokemon", "ScanGyms", "ScanPokestops", "ScanStations", "ScanForts"}
	if got := svc.Methods().Len(); got != len(want) {
		t.Fatalf("GolbatApi has %d methods, want %d", got, len(want))
	}
	for _, name := range want {
		if svc.Methods().ByName(protoreflect.Name(name)) == nil {
			t.Errorf("GolbatApi lacks rpc %s", name)
		}
	}
}

// Optional scalars must survive a round trip with presence intact: an unset
// IV is not the same as an IV of 0.
func TestPokemonWireRoundTripPreservesPresence(t *testing.T) {
	cp := uint32(1500)
	in := &Pokemon{
		Id:        1 << 63,
		PokemonId: 25,
		Cp:        &cp,
		Pvp:       &PvpRankings{Great: []*PvpEntry{{Pokemon: 25, Rank: 1, Percentage: 0.99}}},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Pokemon
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Id != 1<<63 || out.PokemonId != 25 {
		t.Fatalf("scalars mismatch: %+v", &out)
	}
	if out.Cp == nil || *out.Cp != 1500 {
		t.Errorf("cp = %v, want 1500", out.Cp)
	}
	if out.AtkIv != nil || out.Weight != nil || out.PokestopId != nil {
		t.Error("unset optionals must stay unset after a round trip")
	}
	if got := out.GetPvp().GetGreat(); len(got) != 1 || got[0].GetRank() != 1 {
		t.Errorf("pvp great = %v, want one rank-1 entry", got)
	}
}
