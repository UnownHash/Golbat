package decoder

import (
	"github.com/guregu/null/v6"
	log "github.com/sirupsen/logrus"

	"golbat/pogo"
	"golbat/pogoshim"
)

// updateFromPokestopIncidentDisplay reads the character_display oneof member
// via pokestopDisplay.GetCharacterDisplay(). This is emitted by pogoshimgen
// like any other message-kind field getter (Has+Get+IsValid) because the
// generator iterates MessageDescriptor.Fields() without special-casing oneof
// membership - protoreflect.Message.Get on a oneof field that is unset, or
// set to a different member, returns an invalid/zero value exactly as it
// does for a plain optional message field. Verified for both std and hyperpb
// wraps in TestUpdateFromPokestopIncidentDisplayOneofShim (fort_shim_test.go).
func (incident *Incident) updateFromPokestopIncidentDisplay(pokestopDisplay pogoshim.PokestopIncidentDisplayProto) {
	incident.SetId(pokestopDisplay.GetIncidentId())
	incident.SetStartTime(pokestopDisplay.GetIncidentStartMs() / 1000)
	incident.SetExpirationTime(pokestopDisplay.GetIncidentExpirationMs() / 1000)
	incident.SetDisplayType(int16(pokestopDisplay.GetIncidentDisplayType()))
	if (incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) || incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE)) && incident.Confirmed {
		log.Debugf("Incident has already been confirmed as a decoy: %s", incident.Id)
		return
	}
	if pokestopDisplay.HasCharacterDisplay() {
		characterDisplay := pokestopDisplay.GetCharacterDisplay()
		incident.SetStyle(int16(characterDisplay.GetStyle()))
		incident.SetCharacter(int16(characterDisplay.GetCharacter()))
	} else {
		incident.SetStyle(0)
		incident.SetCharacter(0)
	}
}

func (incident *Incident) updateFromOpenInvasionCombatSessionOut(protoRes pogoshim.OpenInvasionCombatSessionOutProto) {
	opponent := protoRes.GetCombat().GetOpponent()
	active := opponent.GetActivePokemon()
	incident.SetSlot1PokemonId(null.NewInt(int64(active.GetPokedexId().Number()), true))
	incident.SetSlot1Form(null.NewInt(int64(active.GetPokemonDisplay().GetForm().Number()), true))

	reserve := opponent.GetReservePokemon()
	for i := 0; i < reserve.Len(); i++ {
		pokemon := reserve.At(i)
		switch i {
		case 0:
			incident.SetSlot2PokemonId(null.NewInt(int64(pokemon.GetPokedexId().Number()), true))
			incident.SetSlot2Form(null.NewInt(int64(pokemon.GetPokemonDisplay().GetForm().Number()), true))
		case 1:
			incident.SetSlot3PokemonId(null.NewInt(int64(pokemon.GetPokedexId().Number()), true))
			incident.SetSlot3Form(null.NewInt(int64(pokemon.GetPokemonDisplay().GetForm().Number()), true))
		}
	}
	incident.SetConfirmed(true)
}

// updateFromStartIncidentOut reads the first dialogue line's character from
// the first step, matching the pre-shim code's unconditional
// Step[0].GetPokestopDialogue().GetDialogueLine()[0] index chain. That chain
// panicked on an empty Step or DialogueLine list (never observed in
// practice -- the game always includes at least one PokestopDialogue step
// with one line for a confirmable incident); the Len()>0 guards below make
// the same access safe instead of matching that latent panic, consistent
// with every other getter-chain in this migration.
func (incident *Incident) updateFromStartIncidentOut(proto pogoshim.StartIncidentOutProto) {
	incidentInfo := proto.GetIncident()
	if steps := incidentInfo.GetStep(); steps.Len() > 0 {
		if lines := steps.At(0).GetPokestopDialogue().GetDialogueLine(); lines.Len() > 0 {
			incident.SetCharacter(int16(lines.At(0).GetCharacter()))
		}
	}
	if incident.Character == int16(pogo.EnumWrapper_CHARACTER_GIOVANNI) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_MALE) ||
		incident.Character == int16(pogo.EnumWrapper_CHARACTER_DECOY_GRUNT_FEMALE) {
		incident.SetConfirmed(true)
	}
	incident.SetStartTime(incidentInfo.GetCompletionDisplay().GetIncidentStartMs() / 1000)
	incident.SetExpirationTime(incidentInfo.GetCompletionDisplay().GetIncidentExpirationMs() / 1000)
}

// updateFromBattleState fills the lineup slots from a Nebula get-state response.
// The opponent is the NPC (grunt) / NPC_BOSS (leader/Giovanni) actor; the player
// actor is ignored. Validated against production get-state payloads: at the
// opening state only the opponent's ACTIVE pokemon has its species revealed, so
// slot 1 is reliably populated while the reserves (slots 2/3) carry PokedexId 0
// until the battle progresses — those are left NULL rather than written as 0.
func (incident *Incident) updateFromBattleState(out pogoshim.BattleStateOutProto) {
	state := out.GetBattleState()
	if state.IsZero() {
		return
	}

	// Identify the opponent actor (NPC or NPC_BOSS). Verbose debug for payload
	// capture. actors/pokemon are BattleStateProto's two map fields -- pogoshim's
	// generator has no map support (see pogoshim/manual.go), so both are
	// read through hand-written accessors there.
	var opponent pogoshim.BattleActorProto
	for a := range state.GetActors().All() {
		roster := a.GetPokemonRoster()
		rosterIds := make([]uint64, roster.Len())
		for i := 0; i < roster.Len(); i++ {
			rosterIds[i] = roster.At(i).Uint()
		}
		log.Debugf("Nebula battlestate actor id=%s type=%s team=%s active=%d roster=%v",
			a.GetId(), a.GetType(), a.GetTeam(), a.GetActivePokemonId(), rosterIds)
		if a.GetType() == pogo.BattleActorProto_NPC || a.GetType() == pogo.BattleActorProto_NPC_BOSS {
			opponent = a
		}
	}
	if opponent.IsZero() {
		log.Warnf("Nebula battlestate: no opponent actor found")
		return
	}

	pokemon := state.GetPokemon()
	setSlot := func(slot int, id uint64) {
		bp := pokemon.Get(id)
		if bp.IsZero() {
			return
		}
		pokedexId := int64(bp.GetPokedexId().Number())
		if pokedexId == 0 {
			// Species not revealed yet (e.g. reserve pokemon at the opening state):
			// leave the slot NULL rather than writing pokemon id 0.
			return
		}
		pokedex := null.NewInt(pokedexId, true)
		// Form is 0 (FORM_UNSET) for default-form pokemon, which is a valid value.
		form := null.NewInt(int64(bp.GetDisplay().GetForm().Number()), true)
		switch slot {
		case 1:
			incident.SetSlot1PokemonId(pokedex)
			incident.SetSlot1Form(form)
		case 2:
			incident.SetSlot2PokemonId(pokedex)
			incident.SetSlot2Form(form)
		case 3:
			incident.SetSlot3PokemonId(pokedex)
			incident.SetSlot3Form(form)
		}
	}

	// Slot 1 = active; slots 2/3 = next roster entries (skipping the active id).
	setSlot(1, opponent.GetActivePokemonId())
	slot := 2
	roster := opponent.GetPokemonRoster()
	for i := 0; i < roster.Len(); i++ {
		id := roster.At(i).Uint()
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

	log.Debugf("Nebula lineup incident=%s slot1=%s/%s slot2=%s/%s slot3=%s/%s (pokemon/form)",
		incident.Id,
		FormatNull(incident.Slot1PokemonId), FormatNull(incident.Slot1Form),
		FormatNull(incident.Slot2PokemonId), FormatNull(incident.Slot2Form),
		FormatNull(incident.Slot3PokemonId), FormatNull(incident.Slot3Form))
}
