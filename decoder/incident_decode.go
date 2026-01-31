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
		if i == 0 {
			incident.SetSlot2PokemonId(null.NewInt(int64(pokemon.PokedexId.Number()), true))
			incident.SetSlot2Form(null.NewInt(int64(pokemon.PokemonDisplay.Form.Number()), true))
		} else if i == 1 {
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
