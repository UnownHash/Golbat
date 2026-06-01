package decoder

import (
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
)

func (incident *Incident) updateFromPokestopIncidentDisplay(pokestopDisplay *pogo.PokestopIncidentDisplayProto) {
	incident.SetId(pokestopDisplay.IncidentId)
	incident.SetStartTime(int64(pokestopDisplay.IncidentStartMs / 1000))
	incident.SetExpirationTime(int64(pokestopDisplay.IncidentExpirationMs / 1000))
	incident.SetDisplayType(int16(pokestopDisplay.IncidentDisplayType))
	if (incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) || incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE)) && incident.Confirmed {
		log.Debugf("Incident has already been confirmed as a decoy: %s", incident.Id)
		return
	}
	characterDisplay := pokestopDisplay.GetCharacterDisplay()
	if characterDisplay != nil {
		// team := pokestopDisplay.Open
		incident.SetStyle(int16(characterDisplay.Style))
		incident.SetCharacter(int16(characterDisplay.Character))
	} else {
		incident.SetStyle(0)
		incident.SetCharacter(0)
	}
}

func (incident *Incident) updateFromOpenInvasionCombatSessionOut(protoRes *pogo.OpenInvasionCombatSessionOutProto) {
	incident.SetSlot1PokemonId(null.NewInt(int64(protoRes.Combat.Opponent.ActivePokemon.PokedexId.Number()), true))
	incident.SetSlot1Form(null.NewInt(int64(protoRes.Combat.Opponent.ActivePokemon.PokemonDisplay.Form.Number()), true))
	for i, pokemon := range protoRes.Combat.Opponent.ReservePokemon {
		switch i {
		case 0:
			incident.SetSlot2PokemonId(null.NewInt(int64(pokemon.PokedexId.Number()), true))
			incident.SetSlot2Form(null.NewInt(int64(pokemon.PokemonDisplay.Form.Number()), true))
		case 1:
			incident.SetSlot3PokemonId(null.NewInt(int64(pokemon.PokedexId.Number()), true))
			incident.SetSlot3Form(null.NewInt(int64(pokemon.PokemonDisplay.Form.Number()), true))
		}
	}
	incident.SetConfirmed(true)
}

func (incident *Incident) updateFromStartIncidentOut(proto *pogo.StartIncidentOutProto) {
	incident.SetCharacter(int16(proto.GetIncident().GetStep()[0].GetPokestopDialogue().GetDialogueLine()[0].GetCharacter()))
	if incident.Character == int16(pogo.EnumWrapper_CHARACTER_GIOVANNI) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE) {
		incident.SetConfirmed(true)
	}
	incident.SetStartTime(int64(proto.Incident.GetCompletionDisplay().GetIncidentStartMs() / 1000))
	incident.SetExpirationTime(int64(proto.Incident.GetCompletionDisplay().GetIncidentExpirationMs() / 1000))
}

// updateFromBattleState fills the lineup slots from a Nebula get-state response.
// Opponent actor selection matches NPC (type=4) or NPC_BOSS (type=5) — provisional,
// pending validation against a real captured get-state payload.
func (incident *Incident) updateFromBattleState(out *pogo.BattleStateOutProto) {
	state := out.GetBattleState()
	if state == nil {
		return
	}

	// Identify the opponent actor (NPC or NPC_BOSS). Verbose debug for payload capture.
	var opponent *pogo.BattleActorProto
	for _, a := range state.GetActors() {
		log.Debugf("Nebula battlestate actor id=%s type=%s team=%s active=%d roster=%v",
			a.GetId(), a.GetType(), a.GetTeam(), a.GetActivePokemonId(), a.GetPokemonRoster())
		// NOTE: opponent-actor selection is provisional, pending validation against a real
		// captured get-state payload (we have none yet).
		if a.GetType() == pogo.BattleActorProto_NPC || a.GetType() == pogo.BattleActorProto_NPC_BOSS {
			opponent = a
		}
	}
	if opponent == nil {
		log.Warnf("Nebula battlestate: no opponent actor found")
		return
	}

	pokemon := state.GetPokemon()
	setSlot := func(slot int, id uint64) {
		bp := pokemon[id]
		if bp == nil {
			return
		}
		pokedex := null.NewInt(int64(bp.GetPokedexId().Number()), true)
		switch slot {
		case 1:
			incident.SetSlot1PokemonId(pokedex)
		case 2:
			incident.SetSlot2PokemonId(pokedex)
		case 3:
			incident.SetSlot3PokemonId(pokedex)
		}
	}

	// Slot 1 = active; slots 2/3 = next roster entries (skipping the active id).
	setSlot(1, opponent.GetActivePokemonId())
	slot := 2
	for _, id := range opponent.GetPokemonRoster() {
		if id == opponent.GetActivePokemonId() {
			continue
		}
		if slot > 3 {
			break
		}
		setSlot(slot, id)
		slot++
	}

	incident.SetConfirmed(true)
}
