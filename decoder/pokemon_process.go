package decoder

import (
	"context"
	"fmt"
	"time"

	"golbat/db"
	"golbat/pogoshim"

	log "github.com/sirupsen/logrus"

	"golbat/ottercache"
)

func UpdatePokemonRecordWithEncounterProto(ctx context.Context, db db.DbDetails, encounter pogoshim.EncounterOutProto, username string, timestamp int64) string {
	if !encounter.HasPokemon() {
		return "No encounter"
	}

	encounterId := encounter.GetPokemon().GetEncounterId()

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

	return fmt.Sprintf("%d %d Pokemon %d CP%d", encounter.GetPokemon().GetEncounterId(), encounterId, pokemon.PokemonId, encounter.GetPokemon().GetPokemon().GetCp())
}

// UpdatePokemonRecordWithDiskEncounterProto processes a decoded disk
// encounter. payload carries the same raw DISK_ENCOUNTER bytes that
// decoded to encounter: on the no-previous-GMO fallback path, the raw
// bytes (not the shim, which is only valid for the lifetime of the arena
// backing decodeWithArena's process closure) are cached for replay once
// the corresponding map pokemon shows up in a later GMO.
func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, encounter pogoshim.DiskEncounterOutProto, payload []byte, username string) string {
	if !encounter.HasPokemon() {
		return "No encounter"
	}

	encounterId := uint64(encounter.GetPokemon().GetPokemonDisplay().GetDisplayId())

	pokemon, unlock, err := getPokemonRecordForUpdate(ctx, db, encounterId, "UpdatePokemonFromDiskEncounter")
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}

	if pokemon == nil || pokemon.isNewRecord() {
		// No pokemon found - unlock not set when pokemon is nil
		if unlock != nil {
			unlock()
		}
		diskEncounterCache.Set(encounterId, payload, ottercache.DefaultTTL)
		return fmt.Sprintf("%d Disk encounter without previous GMO - Pokemon stored for later", encounterId)
	}
	defer unlock()

	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter, username)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	enqueuePokemonStatsEvent(pokemonStatsEvent{snap: pokemon.statsSnapshot(), encounter: true})

	return fmt.Sprintf("%d Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.GetPokemon().GetCp())
}

func UpdatePokemonRecordWithTappableEncounter(ctx context.Context, db db.DbDetails, request pogoshim.ProcessTappableProto, encounter pogoshim.TappableEncounterProto, username string, timestampMs int64) string {
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

	return fmt.Sprintf("%d Tappable Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.GetPokemon().GetCp())
}
