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
	// slot 2 came from a revealed reserve (148).
	if !incident.Slot2PokemonId.Valid || incident.Slot2PokemonId.Int64 != 148 {
		t.Errorf("slot2 = %+v, want 148", incident.Slot2PokemonId)
	}
	if !incident.Confirmed {
		t.Error("expected Confirmed=true")
	}
}

// At the opening state the opponent's reserves carry PokedexId 0 (species hidden):
// those slots must stay NULL, not be written as pokemon id 0.
func TestUpdateFromBattleState_HiddenReserveLeftNull(t *testing.T) {
	incident := &Incident{IncidentData: IncidentData{Id: "-9", PokestopId: "F"}, newRecord: true}

	out := &pogo.BattleStateOutProto{
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

	incident.updateFromBattleState(out)

	if !incident.Slot1PokemonId.Valid || incident.Slot1PokemonId.Int64 != 147 {
		t.Errorf("slot1 = %+v, want 147", incident.Slot1PokemonId)
	}
	if incident.Slot2PokemonId.Valid {
		t.Errorf("slot2 should be NULL for a hidden reserve, got %+v", incident.Slot2PokemonId)
	}
	if incident.Slot3PokemonId.Valid {
		t.Errorf("slot3 should be NULL for a hidden reserve, got %+v", incident.Slot3PokemonId)
	}
}
