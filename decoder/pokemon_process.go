package decoder

import (
	"context"
	"fmt"
	"time"

	"golbat/db"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"

	"github.com/guregu/null/v6"
)

func UpdatePokemonRecordWithEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.EncounterOutProto, username string, timestamp int64) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := encounter.Pokemon.EncounterId

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId, "UpdatePokemonFromEncounter")
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	pokemon.updatePokemonFromEncounterProto(ctx, db, encounter, username, timestamp)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, timestamp/1000)
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	enqueuePokemonStatsEvent(pokemonStatsEvent{snap: pokemon.statsSnapshot(), encounter: true})

	return fmt.Sprintf("%d %d Pokemon %d CP%d", encounter.Pokemon.EncounterId, encounterId, pokemon.PokemonId, encounter.Pokemon.Pokemon.Cp)
}

func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, request *pogo.DiskEncounterProto, encounter *pogo.DiskEncounterOutProto, username string) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}
	if encounter.Pokemon.PokemonDisplay == nil {
		log.Warnf("[POKEMON] Disk encounter %d without PokemonDisplay - ignored", request.EncounterId)
		return "Disk encounter without display"
	}

	encounterId := uint64(request.EncounterId)
	if encounterId == 0 {
		return "Disk encounter request without encounter id"
	}
	if displayId := uint64(encounter.Pokemon.GetPokemonDisplay().GetDisplayId()); displayId != 0 && displayId != encounterId {
		log.Warnf("[POKEMON] Disk encounter id mismatch: request %d display %d", encounterId, displayId)
	}

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId, "UpdatePokemonFromDiskEncounter")
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	if pokemon.isNewRecord() {
		// Placement happens exactly once, at record creation, by whichever
		// proto arrives first — here from the disk encounter request. A
		// later GMO contributes the verified despawn time.
		pokemon.SetPokestopId(null.StringFrom(request.FortId))
		pokemon.SetLat(request.GymLatDegrees)
		pokemon.SetLon(request.GymLngDegrees)
		pokemon.SetExpireTimestamp(null.IntFrom(time.Now().Unix() + lureSpawnLifetimeSeconds))
		pokemon.SetExpireTimestampVerified(false)
	}

	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter, username)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	enqueuePokemonStatsEvent(pokemonStatsEvent{snap: pokemon.statsSnapshot(), encounter: true})

	return fmt.Sprintf("%d Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}

func UpdatePokemonRecordWithTappableEncounter(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, encounter *pogo.TappableEncounterProto, username string, timestampMs int64) string {
	encounterId := request.GetEncounterId()

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId, "UpdatePokemonFromTappableEncounter")
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	pokemon.updatePokemonFromTappableEncounterProto(ctx, db, request, encounter, username, timestampMs)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	enqueuePokemonStatsEvent(pokemonStatsEvent{snap: pokemon.statsSnapshot(), encounter: true})

	return fmt.Sprintf("%d Tappable Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}
