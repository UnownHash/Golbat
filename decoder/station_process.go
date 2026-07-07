package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogoshim"
)

func ResetStationedPokemonWithStationDetailsNotFound(ctx context.Context, db db.DbDetails, request pogoshim.GetStationedPokemonDetailsProto) string {
	stationId := request.GetStationId()

	station, unlock, err := getStationRecordForUpdate(ctx, db, stationId, "ResetStationedPokemon")
	if err != nil {
		log.Printf("Get station %s", err)
		return "Error getting station"
	}

	if station == nil {
		log.Infof("Stationed pokemon details for station %s not found", stationId)
		return fmt.Sprintf("Stationed pokemon details for station %s not found", stationId)
	}
	defer unlock()

	station.resetStationedPokemonFromStationDetailsNotFound()
	saveStationRecord(ctx, db, station)
	return fmt.Sprintf("StationedPokemonDetails %s", stationId)
}

func UpdateStationWithStationDetails(ctx context.Context, db db.DbDetails, request pogoshim.GetStationedPokemonDetailsProto, stationDetails pogoshim.GetStationedPokemonDetailsOutProto) string {
	stationId := request.GetStationId()

	station, unlock, err := getStationRecordForUpdate(ctx, db, stationId, "UpdateStationWithDetails")
	if err != nil {
		log.Printf("Get station %s", err)
		return "Error getting station"
	}

	if station == nil {
		log.Infof("Stationed pokemon details for station %s not found", stationId)
		return fmt.Sprintf("Stationed pokemon details for station %s not found", stationId)
	}
	defer unlock()

	station.updateFromGetStationedPokemonDetailsOutProto(stationDetails)
	saveStationRecord(ctx, db, station)
	return fmt.Sprintf("StationedPokemonDetails %s", stationId)
}
