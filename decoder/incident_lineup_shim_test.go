package decoder

import (
	"testing"

	"buf.build/go/hyperpb"
	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/pogoshim"
)

// hyperpbWrapOpenInvasionOut marshals in and returns a hyperpb-backed shim;
// the returned Shared must be Freed by the caller once done with the shim
// (and everything reachable from it). Mirrors the established
// hyperpbWrap<Root> convention (quest_shim_test.go, incident_battlestate_test.go).
func hyperpbWrapOpenInvasionOut(t *testing.T, in *pogo.OpenInvasionCombatSessionOutProto) (pogoshim.OpenInvasionCombatSessionOutProto, *hyperpb.Shared) {
	t.Helper()
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ty := hyperpb.CompileMessageDescriptor((*pogo.OpenInvasionCombatSessionOutProto)(nil).ProtoReflect().Descriptor())
	shared := new(hyperpb.Shared)
	msg := shared.NewMessage(ty)
	if err := msg.Unmarshal(raw); err != nil {
		shared.Free()
		t.Fatal(err)
	}
	return pogoshim.AsOpenInvasionCombatSessionOutProto(msg.ProtoReflect()), shared
}

// TestUpdateFromOpenInvasionCombatSessionOutShim locks in Wave 3 Task 3
// behavior: updateFromOpenInvasionCombatSessionOut's un-guarded
// protoRes.Combat.Opponent.ActivePokemon... field-chain (nil-panics on any
// std proto that doesn't fully populate Combat/Opponent/ActivePokemon)
// becomes a getter chain that degrades to zero values instead, with
// identical output for a fully-populated payload across both engines.
func TestUpdateFromOpenInvasionCombatSessionOutShim(t *testing.T) {
	build := func() *pogo.OpenInvasionCombatSessionOutProto {
		return &pogo.OpenInvasionCombatSessionOutProto{
			Status: pogo.InvasionStatus_SUCCESS,
			Combat: &pogo.CombatProto{
				Opponent: &pogo.CombatProto_CombatPlayerProto{
					ActivePokemon: &pogo.CombatProto_CombatPokemonProto{
						PokedexId:      pogo.HoloPokemonId(1),
						PokemonDisplay: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(0)},
					},
					ReservePokemon: []*pogo.CombatProto_CombatPokemonProto{
						{PokedexId: pogo.HoloPokemonId(2), PokemonDisplay: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(5)}},
						{PokedexId: pogo.HoloPokemonId(3), PokemonDisplay: &pogo.PokemonDisplayProto{Form: pogo.PokemonDisplayProto_Form(7)}},
						// A third reserve entry: the pre-shim code's switch only
						// handles case 0/1, so this must be silently ignored --
						// exactly like the original `for i, pokemon := range` loop
						// with no `case 2` branch.
						{PokedexId: pogo.HoloPokemonId(4)},
					},
				},
			},
		}
	}

	check := func(name string, shim pogoshim.OpenInvasionCombatSessionOutProto) {
		incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}
		incident.updateFromOpenInvasionCombatSessionOut(shim)

		if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 1 {
			t.Errorf("%s: slot1 = %+v, want 1", name, incident.Slot1PokemonId)
		}
		if !incident.Slot1Form.Valid || incident.Slot1Form.Int64 != 0 {
			t.Errorf("%s: slot1 form = %+v, want 0 (valid)", name, incident.Slot1Form)
		}
		if !incident.Slot2PokemonId.Valid || incident.Slot2PokemonId.Int64 != 2 {
			t.Errorf("%s: slot2 = %+v, want 2", name, incident.Slot2PokemonId)
		}
		if !incident.Slot2Form.Valid || incident.Slot2Form.Int64 != 5 {
			t.Errorf("%s: slot2 form = %+v, want 5", name, incident.Slot2Form)
		}
		if !incident.Slot3PokemonId.Valid || incident.Slot3PokemonId.Int64 != 3 {
			t.Errorf("%s: slot3 = %+v, want 3", name, incident.Slot3PokemonId)
		}
		if !incident.Slot3Form.Valid || incident.Slot3Form.Int64 != 7 {
			t.Errorf("%s: slot3 form = %+v, want 7", name, incident.Slot3Form)
		}
		if !incident.Confirmed {
			t.Errorf("%s: expected Confirmed=true", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsOpenInvasionCombatSessionOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapOpenInvasionOut(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}

// TestUpdateFromOpenInvasionCombatSessionOutShim_Empty locks in the
// nil-safety improvement itself: an OpenInvasionCombatSessionOutProto with
// no Combat/Opponent/ActivePokemon at all (which the pre-shim direct
// field-chain access -- protoRes.Combat.Opponent.ActivePokemon.PokedexId --
// would have nil-panicked on) must degrade to zero values instead of
// panicking, for both engines. Note this matches the pre-shim code's own
// unconditional null.NewInt(..., true): slot 1 is always marked VALID with
// whatever PokedexId.Number() resolves to (0 here), never left NULL -- there
// is no "hidden species" zero-check on this path the way
// updateFromBattleState has one.
func TestUpdateFromOpenInvasionCombatSessionOutShim_Empty(t *testing.T) {
	build := func() *pogo.OpenInvasionCombatSessionOutProto {
		return &pogo.OpenInvasionCombatSessionOutProto{Status: pogo.InvasionStatus_SUCCESS}
	}

	check := func(name string, shim pogoshim.OpenInvasionCombatSessionOutProto) {
		incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}
		incident.updateFromOpenInvasionCombatSessionOut(shim)

		if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 0 {
			t.Errorf("%s: slot1 = %+v, want valid/0", name, incident.Slot1PokemonId)
		}
		if incident.Slot2PokemonId.Valid {
			t.Errorf("%s: slot2 should be unset (no reserves), got %+v", name, incident.Slot2PokemonId)
		}
		if !incident.Confirmed {
			t.Errorf("%s: expected Confirmed=true even with an empty lineup", name)
		}
	}

	stdIn := build()
	check("std", pogoshim.AsOpenInvasionCombatSessionOutProto(stdIn.ProtoReflect()))

	hyperShim, shared := hyperpbWrapOpenInvasionOut(t, build())
	defer shared.Free()
	check("hyperpb", hyperShim)
}
