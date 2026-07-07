package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"github.com/guregu/null/v6"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// The webhook lineup must omit slots without a known pokemon rather than emit
// null entries.
func TestIncidentLineup_OmitsNullSlots(t *testing.T) {
	incident := &Incident{IncidentData: IncidentData{
		Slot1PokemonId: null.IntFrom(100),
		Slot1Form:      null.IntFrom(1047),
		// slots 2 and 3 left invalid (unknown reserves)
	}}

	lineup := incidentLineup(incident)
	if len(lineup) != 1 {
		t.Fatalf("want 1 lineup entry, got %d: %+v", len(lineup), lineup)
	}
	if lineup[0].Slot != 1 || lineup[0].PokemonId.Int64 != 100 || lineup[0].Form.Int64 != 1047 {
		t.Errorf("unexpected slot 1 entry: %+v", lineup[0])
	}

	// All slots known -> all three included (old OpenInvasion flow).
	full := &Incident{IncidentData: IncidentData{
		Slot1PokemonId: null.IntFrom(1), Slot2PokemonId: null.IntFrom(2), Slot3PokemonId: null.IntFrom(3),
	}}
	if got := incidentLineup(full); len(got) != 3 {
		t.Errorf("want 3 entries for a full lineup, got %d", len(got))
	}

	// No species known -> empty lineup.
	if got := incidentLineup(&Incident{}); len(got) != 0 {
		t.Errorf("want 0 entries for an empty lineup, got %d", len(got))
	}
}

// hyperpbWrapBattleState marshals in and returns a hyperpb-backed shim; the
// returned Shared must be Freed by the caller once done with the shim (and
// everything reachable from it).
func hyperpbWrapBattleState(t *testing.T, in *pogo.BattleStateOutProto) (pogoshim.BattleStateOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.BattleStateOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsBattleStateOutProto(msg.ProtoReflect()), shared
}

// TestUpdateFromBattleState_SetsSlot1 locks in Wave 3 Task 3 behavior:
// updateFromBattleState now reads BattleStateProto's two map fields
// (actors, pokemon) through pogoshim/manual.go's hand-written accessors --
// the generator emits no map support at all, so this is the one call site
// that exercises them end-to-end. Runs through both the std and hyperpb
// wraps.
func TestUpdateFromBattleState_SetsSlot1(t *testing.T) {
	build := func() *pogo.BattleStateOutProto {
		return &pogo.BattleStateOutProto{
			BattleState: &pogo.BattleStateProto{
				Actors: map[string]*pogo.BattleActorProto{
					"npc": {Id: "npc", Type: pogo.BattleActorProto_NPC, ActivePokemonId: 100, PokemonRoster: []uint64{100, 101}},
				},
				Pokemon: map[uint64]*pogo.BattlePokemonProto{
					100: {PokedexId: pogo.HoloPokemonId(147), Display: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(11)}},
					101: {PokedexId: pogo.HoloPokemonId(148)},
				},
			},
		}
	}

	check := func(name string, shim pogoshim.BattleStateOutProto) {
		incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}
		incident.updateFromBattleState(shim)

		if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 147 {
			t.Errorf("%s: slot1 = %+v, want 147", name, incident.Slot1PokemonId)
		}
		if !incident.Slot1Form.Valid || incident.Slot1Form.Int64 != 11 {
			t.Errorf("%s: slot1 form = %+v, want 11", name, incident.Slot1Form)
		}
		// slot 2 came from a revealed reserve (148).
		if !incident.Slot2PokemonId.Valid || incident.Slot2PokemonId.Int64 != 148 {
			t.Errorf("%s: slot2 = %+v, want 148", name, incident.Slot2PokemonId)
		}
		if !incident.Confirmed {
			t.Errorf("%s: expected Confirmed=true", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsBattleStateOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapBattleState(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// At the opening state the opponent's reserves carry PokedexId 0 (species hidden):
// those slots must stay NULL, not be written as pokemon id 0.
func TestUpdateFromBattleState_HiddenReserveLeftNull(t *testing.T) {
	build := func() *pogo.BattleStateOutProto {
		return &pogo.BattleStateOutProto{
			BattleState: &pogo.BattleStateProto{
				Actors: map[string]*pogo.BattleActorProto{
					"npc": {Id: "npc", Type: pogo.BattleActorProto_NPC, ActivePokemonId: 100, PokemonRoster: []uint64{100, 101, 102}},
				},
				Pokemon: map[uint64]*pogo.BattlePokemonProto{
					100: {PokedexId: pogo.HoloPokemonId(147)},
					101: {PokedexId: pogo.HoloPokemonId(0)}, // reserve, species not yet revealed
					102: {PokedexId: pogo.HoloPokemonId(0)},
				},
			},
		}
	}

	check := func(name string, shim pogoshim.BattleStateOutProto) {
		incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}
		incident.updateFromBattleState(shim)

		if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 147 {
			t.Errorf("%s: slot1 = %+v, want 147", name, incident.Slot1PokemonId)
		}
		if incident.Slot2PokemonId.Valid {
			t.Errorf("%s: slot2 should be NULL for a hidden reserve, got %+v", name, incident.Slot2PokemonId)
		}
		if incident.Slot3PokemonId.Valid {
			t.Errorf("%s: slot3 should be NULL for a hidden reserve, got %+v", name, incident.Slot3PokemonId)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsBattleStateOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapBattleState(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// TestUpdateFromBattleState_NoOpponent covers the "no opponent actor found"
// early return (only PLAYER-type actors present) -- must not panic on the
// zero-shim opponent for either engine, and must leave the incident
// unconfirmed.
func TestUpdateFromBattleState_NoOpponent(t *testing.T) {
	build := func() *pogo.BattleStateOutProto {
		return &pogo.BattleStateOutProto{
			BattleState: &pogo.BattleStateProto{
				Actors: map[string]*pogo.BattleActorProto{
					"player": {Id: "player", Type: pogo.BattleActorProto_PLAYER, ActivePokemonId: 1},
				},
			},
		}
	}

	check := func(name string, shim pogoshim.BattleStateOutProto) {
		incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}
		incident.updateFromBattleState(shim)
		if incident.Confirmed {
			t.Errorf("%s: expected Confirmed=false with no opponent actor", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsBattleStateOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapBattleState(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}
