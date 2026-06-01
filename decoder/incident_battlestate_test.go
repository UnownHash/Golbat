package decoder

import (
	"testing"

	"golbat/pogo"
)

func TestUpdateFromBattleState_SetsSlot1(t *testing.T) {
	incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}

	out := &pogo.BattleStateOutProto{
		BattleState: &pogo.BattleStateProto{
			Actors: map[string]*pogo.BattleActorProto{
				"npc": {Id: "npc", Type: pogo.BattleActorProto_NPC, ActivePokemonId: 100, PokemonRoster: []uint64{100, 101}},
			},
			Pokemon: map[uint64]*pogo.BattlePokemonProto{
				100: {PokedexId: pogo.HoloPokemonId(147)},
				101: {PokedexId: pogo.HoloPokemonId(148)},
			},
		},
	}

	incident.updateFromBattleState(out)

	if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 147 {
		t.Errorf("slot1 = %+v, want 147", incident.Slot1PokemonId)
	}
	if !incident.Confirmed {
		t.Error("expected Confirmed=true")
	}
}
