package decoder

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func UpdateFortBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, p []RawFortData) {
	// Logic is:
	// 1. Filter out pokestops that are unchanged (last modified time)
	// 2. Fetch current stops from database
	// 3. Generate batch of inserts as needed (with on duplicate saveGymRecord)

	//var stopsToModify []string

	for _, fort := range p {
		fortId := fort.Data.FortId
		if fort.Data.FortType == pogo.FortType_CHECKPOINT && scanParameters.ProcessPokestops {
			pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fortId)
			if err != nil {
				log.Errorf("getOrCreatePokestopRecord: %s", err)
				continue
			}

			pokestop.updatePokestopFromFort(fort.Data, fort.Cell, fort.Timestamp/1000)

			// If this is a new pokestop, check if it was converted from a gym and copy shared fields
			if pokestop.IsNewRecord() {
				gym, gymUnlock, _ := GetGymRecordReadOnly(ctx, db, fortId)
				if gym != nil {
					pokestop.copySharedFieldsFrom(gym)
					gymUnlock()
				}
			}

			savePokestopRecord(ctx, db, pokestop)
			unlock()

			incidents := fort.Data.PokestopDisplays
			if incidents == nil && fort.Data.PokestopDisplay != nil {
				incidents = []*pogo.PokestopIncidentDisplayProto{fort.Data.PokestopDisplay}
			}

			if incidents != nil {
				for _, incidentProto := range incidents {
					incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentProto.IncidentId, fortId)
					if err != nil {
						log.Errorf("getOrCreateIncidentRecord: %s", err)
						continue
					}
					incident.updateFromPokestopIncidentDisplay(incidentProto)
					saveIncidentRecord(ctx, db, incident)
					unlock()
				}
			}
		}

		if fort.Data.FortType == pogo.FortType_GYM && scanParameters.ProcessGyms {
			gym, gymUnlock, err := getOrCreateGymRecord(ctx, db, fortId)
			if err != nil {
				log.Errorf("getOrCreateGymRecord: %s", err)
				continue
			}

			gym.updateGymFromFort(fort.Data, fort.Cell)

			// If this is a new gym, check if it was converted from a pokestop and copy shared fields
			if gym.IsNewRecord() {
				pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, fortId)
				if pokestop != nil {
					gym.copySharedFieldsFrom(pokestop)
					unlock()
				}
			}

			saveGymRecord(ctx, db, gym)
			gymUnlock()
		}
	}
}

func UpdateStationBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, p []RawStationData) {
	for _, stationProto := range p {
		stationId := stationProto.Data.Id
		station, unlock, err := getOrCreateStationRecord(ctx, db, stationId)
		if err != nil {
			log.Errorf("getOrCreateStationRecord: %s", err)
			continue
		}
		station.updateFromStationProto(stationProto.Data, stationProto.Cell)
		saveStationRecord(ctx, db, station)
		unlock()
	}
}

func UpdatePokemonBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, wildPokemonList []RawWildPokemonData, nearbyPokemonList []RawNearbyPokemonData, mapPokemonList []RawMapPokemonData, weather []*pogo.ClientWeatherProto, username string) {
	weatherLookup := make(map[int64]pogo.GameplayWeatherProto_WeatherCondition)
	for _, weatherProto := range weather {
		weatherLookup[weatherProto.S2CellId] = weatherProto.GameplayWeather.GameplayCondition
	}

	for _, wild := range wildPokemonList {
		encounterId := wild.Data.EncounterId

		// spawnpointUpdateFromWild doesn't need Pokemon lock
		spawnpointUpdateFromWild(ctx, db, wild.Data, wild.Timestamp)

		if scanParameters.ProcessWild {
			// Use read-only getter - we're only checking if update is needed, then queuing
			pokemon, unlock, err := getPokemonRecordReadOnly(ctx, db, encounterId)
			if err != nil {
				log.Errorf("getPokemonRecordReadOnly: %s", err)
				continue
			}

			updateTime := wild.Timestamp / 1000
			shouldQueue := pokemon == nil || pokemon.wildSignificantUpdate(wild.Data, updateTime)

			if unlock != nil {
				unlock()
			}

			if shouldQueue {
				// The sweeper will process it after timeout if no encounter arrives
				pending := &PendingPokemon{
					EncounterId:   encounterId,
					WildPokemon:   wild.Data,
					CellId:        int64(wild.Cell),
					TimestampMs:   wild.Timestamp,
					UpdateTime:    updateTime,
					WeatherLookup: weatherLookup,
					Username:      username,
				}
				pokemonPendingQueue.AddPending(pending)
			}
		}
	}

	if scanParameters.ProcessNearby {
		for _, nearby := range nearbyPokemonList {
			encounterId := nearby.Data.EncounterId

			pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId)
			if err != nil {
				log.Printf("getOrCreatePokemonRecord: %s", err)
				continue
			}

			updateTime := nearby.Timestamp / 1000
			if pokemon.isNewRecord() || pokemon.nearbySignificantUpdate(nearby.Data, updateTime) {
				pokemon.updateFromNearby(ctx, db, nearby.Data, int64(nearby.Cell), weatherLookup, nearby.Timestamp, username)
				savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, nearby.Timestamp/1000)
			}

			unlock()
		}
	}

	for _, mapPokemon := range mapPokemonList {
		encounterId := mapPokemon.Data.EncounterId

		pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId)
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
			continue
		}

		pokemon.updateFromMap(ctx, db, mapPokemon.Data, int64(mapPokemon.Cell), weatherLookup, mapPokemon.Timestamp, username)
		storedDiskEncounter := diskEncounterCache.Get(encounterId)
		if storedDiskEncounter != nil {
			diskEncounter := storedDiskEncounter.Value()
			diskEncounterCache.Delete(encounterId)
			pokemon.updatePokemonFromDiskEncounterProto(ctx, db, diskEncounter, username)
			//log.Infof("Processed stored disk encounter")
		}
		savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, mapPokemon.Timestamp/1000)

		unlock()
	}
}

func UpdateClientWeatherBatch(ctx context.Context, db db.DbDetails, p []*pogo.ClientWeatherProto, timestampMs int64, account string) (updates []WeatherUpdate) {
	hourKey := timestampMs / time.Hour.Milliseconds()
	for _, weatherProto := range p {
		weather, unlock, err := getOrCreateWeatherRecord(ctx, db, weatherProto.S2CellId)
		if err != nil {
			log.Printf("getOrCreateWeatherRecord: %s", err)
			continue
		}

		if weather.newRecord || timestampMs >= weather.UpdatedMs {
			state := getWeatherConsensusState(weatherProto.S2CellId, hourKey)
			if state != nil {
				publish, publishProto := state.applyObservation(hourKey, account, weatherProto)
				if publish {
					if publishProto == nil {
						publishProto = weatherProto
					}
					weather.UpdatedMs = timestampMs
					weather.updateWeatherFromClientWeatherProto(publishProto)
					saveWeatherRecord(ctx, db, weather)
					if weather.oldValues.GameplayCondition != weather.GameplayCondition {
						updates = append(updates, WeatherUpdate{
							S2CellId:   publishProto.S2CellId,
							NewWeather: int32(publishProto.GetGameplayWeather().GetGameplayCondition()),
						})
					}
				}
			}
		}

		unlock()
	}
	return updates
}

func UpdateClientMapS2CellBatch(ctx context.Context, db db.DbDetails, cellIds []uint64) {
	saveS2CellRecords(ctx, db, cellIds)
}
