package proto_decoder

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeGMO(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedGmo, err := DecodeResponseProto[pogo.GetMapObjectsOutProto](pogoProto)
	if err != nil {
		dec.statsCollector.IncDecodeGMO("error", "parse")
		log.Errorf("Failed to parse %s", err)
	}

	if decodedGmo.Status != pogo.GetMapObjectsOutProto_SUCCESS {
		dec.statsCollector.IncDecodeGMO("error", "non_success")
		res := fmt.Sprintf(`GetMapObjectsOutProto: Ignored non-success value %d:%s`, decodedGmo.Status,
			pogo.GetMapObjectsOutProto_Status_name[int32(decodedGmo.Status)])
		return true, res
	}

	var newForts []decoder.RawFortData
	var newStations []decoder.RawStationData
	var newWildPokemon []decoder.RawWildPokemonData
	var newNearbyPokemon []decoder.RawNearbyPokemonData
	var newMapPokemon []decoder.RawMapPokemonData
	var newMapCells []uint64
	var cellsToBeCleaned []uint64

	// track forts per cell for memory-based cleanup (only if tracker enabled)
	cellForts := make(map[uint64]*decoder.CellFortsData)

	if len(decodedGmo.MapCell) == 0 {
		return true, "Skipping GetMapObjectsOutProto: No map cells found"
	}
	for _, mapCell := range decodedGmo.MapCell {
		if isCellNotEmpty(mapCell) {
			newMapCells = append(newMapCells, mapCell.S2CellId)
			if cellContainsForts(mapCell) {
				cellsToBeCleaned = append(cellsToBeCleaned, mapCell.S2CellId)
				// initialize cell forts tracking (only if tracker enabled)
				cellForts[mapCell.S2CellId] = &decoder.CellFortsData{
					Pokestops: make([]string, 0),
					Gyms:      make([]string, 0),
					Timestamp: mapCell.AsOfTimeMs,
				}
			}
		}
		for _, fort := range mapCell.Fort {
			newForts = append(newForts, decoder.RawFortData{Cell: mapCell.S2CellId, Data: fort, Timestamp: mapCell.AsOfTimeMs})

			// track fort by type for memory-based cleanup (only if tracker enabled)
			if cf, ok := cellForts[mapCell.S2CellId]; ok {
				switch fort.FortType {
				case pogo.FortType_GYM:
					cf.Gyms = append(cf.Gyms, fort.FortId)
				case pogo.FortType_CHECKPOINT:
					cf.Pokestops = append(cf.Pokestops, fort.FortId)
				}
			}

			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: mapCell.S2CellId, Data: fort.ActivePokemon, Timestamp: mapCell.AsOfTimeMs})
			}
		}
		for _, mon := range mapCell.WildPokemon {
			newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: mapCell.AsOfTimeMs})
		}
		for _, mon := range mapCell.NearbyPokemon {
			newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: mapCell.S2CellId, Data: mon, Timestamp: mapCell.AsOfTimeMs})
		}
		for _, station := range mapCell.Stations {
			newStations = append(newStations, decoder.RawStationData{Cell: mapCell.S2CellId, Data: station})
		}
	}

	scanParameters := pogoProto.GetScanParameters()
	if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
		decoder.UpdateFortBatch(ctx, dec.dbDetails, scanParameters, newForts)
	}
	var weatherUpdates []decoder.WeatherUpdate
	if scanParameters.ProcessWeather {
		weatherUpdates = decoder.UpdateClientWeatherBatch(ctx, dec.dbDetails, decodedGmo.ClientWeather, decodedGmo.MapCell[0].AsOfTimeMs)
	}
	if scanParameters.ProcessPokemon {
		decoder.UpdatePokemonBatch(ctx, dec.dbDetails, scanParameters, newWildPokemon, newNearbyPokemon, newMapPokemon, decodedGmo.ClientWeather, pogoProto.GetAccount())
		if scanParameters.ProcessWeather && scanParameters.ProactiveIVSwitching {
			for _, weatherUpdate := range weatherUpdates {
				go func(weatherUpdate decoder.WeatherUpdate) {
					decoder.ProactiveIVSwitchSem <- true
					defer func() { <-decoder.ProactiveIVSwitchSem }()
					decoder.ProactiveIVSwitch(ctx, dec.dbDetails, weatherUpdate, scanParameters.ProactiveIVSwitchingToDB, decodedGmo.MapCell[0].AsOfTimeMs/1000)
				}(weatherUpdate)
			}
		}
	}
	if scanParameters.ProcessStations {
		decoder.UpdateStationBatch(ctx, dec.dbDetails, scanParameters, newStations)
	}

	if scanParameters.ProcessCells {
		decoder.UpdateClientMapS2CellBatch(ctx, dec.dbDetails, newMapCells)
	}

	if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			decoder.CheckRemovedForts(ctx, dec.dbDetails, cellsToBeCleaned, cellForts)
		}()
	}

	newFortsLen := len(newForts)
	newStationsLen := len(newStations)
	newWildPokemonLen := len(newWildPokemon)
	newNearbyPokemonLen := len(newNearbyPokemon)
	newMapPokemonLen := len(newMapPokemon)
	newClientWeatherLen := len(decodedGmo.ClientWeather)
	newMapCellsLen := len(newMapCells)

	dec.statsCollector.IncDecodeGMO("ok", "")
	dec.statsCollector.AddDecodeGMOType("fort", float64(newFortsLen))
	dec.statsCollector.AddDecodeGMOType("station", float64(newStationsLen))
	dec.statsCollector.AddDecodeGMOType("wild_pokemon", float64(newWildPokemonLen))
	dec.statsCollector.AddDecodeGMOType("nearby_pokemon", float64(newNearbyPokemonLen))
	dec.statsCollector.AddDecodeGMOType("map_pokemon", float64(newMapPokemonLen))
	dec.statsCollector.AddDecodeGMOType("weather", float64(newClientWeatherLen))
	dec.statsCollector.AddDecodeGMOType("cell", float64(newMapCellsLen))

	return true, fmt.Sprintf("%d cells containing %d forts %d stations %d mon %d nearby", newMapCellsLen, newFortsLen, newStationsLen, newWildPokemonLen, newNearbyPokemonLen)
}
