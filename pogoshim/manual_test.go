package pogoshim_test

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// TestBattleStateMapAccessors locks in the hand-written map accessors added
// in pogoshim/manual.go: BattleStateProto.actors (map<string,
// BattleActorProto>) and .pokemon (map<uint64, BattlePokemonProto>) are the
// one construct cmd/pogoshimgen does not generate at all (not just a
// missing getter -- BattleActorProto/BattlePokemonProto have no generated
// shim type, since neither is reachable from any root except through these
// two map fields). Runs both a std and a hyperpb wrap, matching every other
// dual-engine shim test in this package.
func TestBattleStateMapAccessors(t *testing.T) {
	out := &pogo.BattleStateOutProto{
		BattleState: &pogo.BattleStateProto{
			Actors: map[string]*pogo.BattleActorProto{
				"npc": {
					Id:              "npc",
					Type:            pogo.BattleActorProto_NPC,
					Team:            pogo.Team_TEAM_RED,
					ActivePokemonId: 100,
					PokemonRoster:   []uint64{100, 101},
				},
			},
			Pokemon: map[uint64]*pogo.BattlePokemonProto{
				100: {PokedexId: pogo.HoloPokemonId(147), Display: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(11)}},
				101: {PokedexId: pogo.HoloPokemonId(148)},
			},
		},
	}
	raw, err := proto.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, o pogoshim.BattleStateOutProto) {
		state := o.GetBattleState()
		if state.IsZero() {
			t.Fatalf("%s: BattleState absent", name)
		}

		actors := state.GetActors()
		if actors.Len() != 1 {
			t.Fatalf("%s: actors len = %d, want 1", name, actors.Len())
		}
		var found bool
		for a := range actors.All() {
			found = true
			if a.GetId() != "npc" {
				t.Errorf("%s: actor id = %q, want %q", name, a.GetId(), "npc")
			}
			if a.GetType() != pogo.BattleActorProto_NPC {
				t.Errorf("%s: actor type = %v, want NPC", name, a.GetType())
			}
			if a.GetTeam() != pogo.Team_TEAM_RED {
				t.Errorf("%s: actor team = %v, want TEAM_RED", name, a.GetTeam())
			}
			if a.GetActivePokemonId() != 100 {
				t.Errorf("%s: active pokemon id = %d, want 100", name, a.GetActivePokemonId())
			}
			roster := a.GetPokemonRoster()
			if roster.Len() != 2 || roster.At(0).Uint() != 100 || roster.At(1).Uint() != 101 {
				t.Errorf("%s: unexpected roster %+v", name, roster)
			}
		}
		if !found {
			t.Errorf("%s: actors.All() yielded nothing", name)
		}

		pokemon := state.GetPokemon()
		if pokemon.Len() != 2 {
			t.Fatalf("%s: pokemon len = %d, want 2", name, pokemon.Len())
		}
		bp := pokemon.Get(100)
		if bp.IsZero() {
			t.Fatalf("%s: pokemon[100] missing", name)
		}
		if bp.GetPokedexId() != pogo.HoloPokemonId(147) {
			t.Errorf("%s: pokedexId = %v, want 147", name, bp.GetPokedexId())
		}
		if !bp.HasDisplay() || bp.GetDisplay().GetForm() != pogo.PokemonDisplayProto_Form(11) {
			t.Errorf("%s: display/form mismatch", name)
		}

		if got := pokemon.Get(999); !got.IsZero() {
			t.Errorf("%s: pokemon[999] should be zero shim for a missing key, got %+v", name, got)
		}
	}

	// std wrap
	var back pogo.BattleStateOutProto
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	check("std", pogoshim.AsBattleStateOutProto(back.ProtoReflect()))

	// hyperpb wrap
	ty := hyperpb.CompileMessageDescriptor((*pogo.BattleStateOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	defer shared.Free()
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	check("hyperpb", pogoshim.AsBattleStateOutProto(msg.ProtoReflect()))
}

// TestBattleStateMapAccessors_Empty locks in the zero-map behavior (absent
// BattleState, or a BattleState with no actors/pokemon entries at all) both
// map wrappers must degrade to safely -- Len()==0, All() yields nothing,
// Get() returns the zero shim -- rather than panicking on a nil
// protoreflect.Map.
func TestBattleStateMapAccessors_Empty(t *testing.T) {
	var zero pogoshim.BattleStateOutProto
	state := zero.GetBattleState()
	if !state.IsZero() {
		t.Fatal("zero BattleStateOutProto should have a zero BattleState")
	}
	if state.GetActors().Len() != 0 {
		t.Error("zero BattleState: actors should be empty")
	}
	for range state.GetActors().All() {
		t.Error("zero BattleState: actors.All() should yield nothing")
	}
	if got := state.GetPokemon().Get(1); !got.IsZero() {
		t.Errorf("zero BattleState: pokemon.Get should be zero shim, got %+v", got)
	}
}
