package decoder

import (
	"context"
	"fmt"
	"time"

	"golbat/db"
	"golbat/pogo"

	"github.com/jellydator/ttlcache/v3"
	log "github.com/sirupsen/logrus"
)

func UpdatePokemonRecordWithEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.EncounterOutProto, username string, timestamp int64) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := encounter.Pokemon.EncounterId

	// Remove from pending queue - encounter arrived so no need for delayed wild update
	if pokemonPendingQueue != nil {
		pokemonPendingQueue.Remove(encounterId)
	}

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	pokemon.updatePokemonFromEncounterProto(ctx, db, encounter, username, timestamp)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, timestamp/1000)
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	updateEncounterStats(pokemon)

	return fmt.Sprintf("%d %d Pokemon %d CP%d", encounter.Pokemon.EncounterId, encounterId, pokemon.PokemonId, encounter.Pokemon.Pokemon.Cp)
}

func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, encounter *pogo.DiskEncounterOutProto, username string) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := uint64(encounter.Pokemon.PokemonDisplay.DisplayId)

	pokemon, unlock, err := getPokemonRecordForUpdate(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	if pokemon == nil || pokemon.isNewRecord() {
		// No pokemon found - unlock not set when pokemon is nil
		if unlock != nil {
			unlock()
		}
		diskEncounterCache.Set(encounterId, encounter, ttlcache.DefaultTTL)
		return fmt.Sprintf("%d Disk encounter without previous GMO - Pokemon stored for later", encounterId)
	}
	defer unlock()

	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter, username)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	updateEncounterStats(pokemon)

	return fmt.Sprintf("%d Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}

func UpdatePokemonRecordWithTappableEncounter(ctx context.Context, db db.DbDetails, request *pogo.ProcessTappableProto, encounter *pogo.TappableEncounterProto, username string, timestampMs int64) string {
	encounterId := request.GetEncounterId()

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId)
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	pokemon.updatePokemonFromTappableEncounterProto(ctx, db, request, encounter, username, timestampMs)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	updateEncounterStats(pokemon)

	return fmt.Sprintf("%d Tappable Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}
