package decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/pogo"
)

func ResetStationedPokemonWithStationDetailsNotFound(ctx context.Context, db db.DbDetails, request *pogo.GetStationedPokemonDetailsProto) string {
	stationId := request.StationId

	fortId, ok := ParseFortId(stationId)
	if !ok {
		log.Errorf("ResetStationedPokemonWithStationDetailsNotFound: unparseable station id %q", stationId)
		return fmt.Sprintf("Invalid station id %s", stationId)
	}

	station, unlock, err := getStationRecordForUpdate(ctx, db, fortId, "ResetStationedPokemon")
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

func UpdateStationWithStationDetails(ctx context.Context, db db.DbDetails, request *pogo.GetStationedPokemonDetailsProto, stationDetails *pogo.GetStationedPokemonDetailsOutProto) string {
	stationId := request.StationId

	fortId, ok := ParseFortId(stationId)
	if !ok {
		log.Errorf("UpdateStationWithStationDetails: unparseable station id %q", stationId)
		return fmt.Sprintf("Invalid station id %s", stationId)
	}

	station, unlock, err := getStationRecordForUpdate(ctx, db, fortId, "UpdateStationWithDetails")
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
