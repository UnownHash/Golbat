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
		fortId, ok := ParseFortId(fort.Data.FortId)
		if !ok {
			FortIdParseDrops.Report(func(dropped int64) {
				log.Errorf("UpdateFortBatch: dropped %d unparseable fort id(s) in the last second (most recently %q)", dropped, fort.Data.FortId)
			})
			continue
		}
		if fort.Data.FortType == pogo.FortType_CHECKPOINT && scanParameters.ProcessPokestops {
			pokestop, unlock, err := getOrCreatePokestopRecord(ctx, db, fortId, "UpdateFortBatch")
			if err != nil {
				log.Errorf("getOrCreatePokestopRecord: %s", err)
				continue
			}

			pokestop.updatePokestopFromFort(fortId, fort.Data, fort.Cell, fort.Timestamp/1000)
			isNewRecord := pokestop.IsNewRecord()

			savePokestopRecord(ctx, db, pokestop)
			unlock()

			// If this was a new pokestop, check if it was converted from a gym and copy shared fields.
			// To avoid deadlock, we do this after releasing the pokestop lock.
			if isNewRecord && DoesGymExist(ctx, db, fortId) {
				// Get shared fields from gym (with gym lock only)
				gym, gymUnlock, _ := GetGymRecordReadOnly(ctx, db, fortId, "UpdateFortBatch.pokestopSharedFields")
				if gym != nil {
					sharedFields := gym.GetSharedFields()
					gymUnlock()

					// Re-acquire pokestop lock to apply shared fields
					pokestop, unlock, err = getPokestopRecordForUpdate(ctx, db, fortId, "UpdateFortBatch.sharedFields")
					if err != nil {
						log.Errorf("getPokestopRecordForUpdate (shared fields): %s", err)
					} else if pokestop != nil {
						pokestop.ApplySharedFields(sharedFields)
						savePokestopRecord(ctx, db, pokestop)
						unlock()
					}
				}
			}

			incidents := fort.Data.PokestopDisplays
			if incidents == nil && fort.Data.PokestopDisplay != nil {
				incidents = []*pogo.PokestopIncidentDisplayProto{fort.Data.PokestopDisplay}
			}

			for _, incidentProto := range incidents {
				if incidentProto.IncidentId == "" {
					continue
				}
				incident, unlock, err := getOrCreateIncidentRecord(ctx, db, incidentProto.IncidentId, fortId, "UpdateFortBatch")
				if err != nil {
					log.Errorf("getOrCreateIncidentRecord: %s", err)
					continue
				}
				incident.updateFromPokestopIncidentDisplay(incidentProto)
				saveIncidentRecord(ctx, db, incident)
				unlock()
			}
		}

		if fort.Data.FortType == pogo.FortType_GYM && scanParameters.ProcessGyms {
			gym, gymUnlock, err := getOrCreateGymRecord(ctx, db, fortId, "UpdateFortBatch")
			if err != nil {
				log.Errorf("getOrCreateGymRecord: %s", err)
				continue
			}

			gym.updateGymFromFort(fortId, fort.Data, fort.Cell, fort.Timestamp)
			isNewRecord := gym.IsNewRecord()

			saveGymRecord(ctx, db, gym)
			gymUnlock()

			// If this was a new gym, check if it was converted from a pokestop and copy shared fields.
			// To avoid deadlock, we do this after releasing the gym lock.
			if isNewRecord && DoesPokestopExist(ctx, db, fortId) {
				// Get shared fields from pokestop (with pokestop lock only)
				pokestop, unlock, _ := getPokestopRecordReadOnly(ctx, db, fortId, "UpdateFortBatch.gymSharedFields")
				if pokestop != nil {
					sharedFields := pokestop.GetSharedFields()
					unlock()

					// Re-acquire gym lock to apply shared fields
					gym, gymUnlock, err = getGymRecordForUpdate(ctx, db, fortId, "UpdateFortBatch.sharedFields")
					if err != nil {
						log.Errorf("getGymRecordForUpdate (shared fields): %s", err)
					} else if gym != nil {
						gym.ApplySharedFields(sharedFields)
						saveGymRecord(ctx, db, gym)
						gymUnlock()
					}
				}
			}
		}
	}
}

func UpdateStationBatch(ctx context.Context, db db.DbDetails, scanParameters ScanParameters, p []RawStationData) {
	for _, stationProto := range p {
		stationId, ok := ParseFortId(stationProto.Data.Id)
		if !ok {
			FortIdParseDrops.Report(func(dropped int64) {
				log.Errorf("UpdateStationBatch: dropped %d unparseable station id(s) in the last second (most recently %q)", dropped, stationProto.Data.Id)
			})
			continue
		}
		station, unlock, err := getOrCreateStationRecord(ctx, db, stationId, "UpdateStationBatch")
		if err != nil {
			log.Errorf("getOrCreateStationRecord: %s", err)
			continue
		}
		station.updateFromStationProto(stationId, stationProto.Data, stationProto.Cell)
		syncStationBattlesFromProto(station, stationProto.Data.BattleDetails)
		saveStationRecord(ctx, db, station)
		unlock()
	}
}

// retryMissingDerivedFields gives a pokemon's derived fields one more chance,
// on a sighting the significant-update gate above has decided not to
// re-decode: CP, and the moves and size derivation fills.
//
// Each is filled only when absent, and the only things that fill them are the
// sighting updates. So a field that failed to fill once never gets another
// chance on its own — the pokemon spends the rest of its life showing IVs with
// nothing beside them, long after whatever caused it is gone. Anything
// transient does that: ohbem not yet built when the first sighting landed, a
// masterfile briefly unreadable or not yet carrying the species or its move
// pools.
//
// Both sighting paths need this. nearbySignificantUpdate skips a pokemon at a
// pokestop when nothing about it moved, so without this a nearby pokemon's
// fields would never be retried at all.
//
// The cost is a few bool reads per skipped sighting when nothing is missing,
// and nothing at all with derive_iv off, where a pokemon that was never
// encountered has no IVs to derive from.
//
// Only on the skipped-update branch: a sighting that could not be placed
// leaves the record untouched, and saving it here would be the save
// updateFromWild and updateFromNearby just declined.
func retryMissingDerivedFields(ctx context.Context, db db.DbDetails, pokemon *Pokemon, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, updateTime int64, username string) {
	// A Ditto never reaches here — its stored species (132) never matches the
	// disguise a GMO reports, so the gate always re-decodes it.
	if pokemon.AtkIv.Valid && (!pokemon.Move1.Valid || !pokemon.Move2.Valid || !pokemon.Size.Valid) {
		pokemon.deriveSizeAndMovesIfAbsent()
	}
	if !pokemon.Cp.Valid {
		pokemon.recomputeCpIfNeeded(ctx, db, weather)
	}
	if pokemon.IsDirty() {
		savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, updateTime, username)
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
			pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId, "UpdatePokemonBatch.wild")
			if err != nil {
				log.Errorf("getOrCreatePokemonRecord: %s", err)
				continue
			}

			updateTime := wild.Timestamp / 1000
			if pokemon.isNewRecord() || pokemon.wildSignificantUpdate(wild.Data, updateTime) {
				if pokemon.updateFromWild(ctx, db, wild.Data, int64(wild.Cell), weatherLookup, wild.Timestamp, username) {
					savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, updateTime, username)
				}
			} else {
				retryMissingDerivedFields(ctx, db, pokemon, weatherLookup, updateTime, username)
			}
			unlock()
		}
	}

	// The nearby list is already filtered by the scan rules at extraction
	// (decodeGMO): empty when nearby pokemon are off, and without fort-less
	// cell pokemon unless nearby_cell_pokemon is on.
	for _, nearby := range nearbyPokemonList {
		pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, nearby.Data.EncounterId, "UpdatePokemonBatch.nearby")
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
			continue
		}

		updateTime := nearby.Timestamp / 1000
		if pokemon.isNewRecord() || pokemon.nearbySignificantUpdate(nearby.Data, updateTime) {
			if pokemon.updateFromNearby(ctx, db, nearby.Data, int64(nearby.Cell), weatherLookup, nearby.Timestamp, username) {
				savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, updateTime, username)
			}
		} else {
			retryMissingDerivedFields(ctx, db, pokemon, weatherLookup, updateTime, username)
		}

		unlock()
	}

	for _, mapPokemon := range mapPokemonList {
		pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, mapPokemon.Data.EncounterId, "UpdatePokemonBatch.map")
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
			continue
		}

		if pokemon.updateFromMap(ctx, db, mapPokemon, weatherLookup, username) {
			savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, mapPokemon.Timestamp/1000, username)
		}
		unlock()
	}
}

func UpdateClientWeatherBatch(ctx context.Context, db db.DbDetails, p []*pogo.ClientWeatherProto, timestampMs int64, account string) (updates []WeatherUpdate) {
	hourKey := timestampMs / time.Hour.Milliseconds()
	for _, weatherProto := range p {
		weather, unlock, err := getOrCreateWeatherRecord(ctx, db, weatherProto.S2CellId, "UpdateClientWeatherBatch")
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
