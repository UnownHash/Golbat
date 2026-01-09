package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeGetStationDetails(ctx context.Context, pogoProto PogoProto) (bool, string) {
	scanParameters := pogoProto.GetScanParameters()
	if !scanParameters.ProcessStations {
		return true, "Station processing disabled"
	}

	decodedGetStationDetails, err := DecodeResponseProto[pogo.GetStationedPokemonDetailsOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
	}

	decodedGetStationDetailsRequest, err := DecodeRequestProto[pogo.GetStationedPokemonDetailsProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse GetStationedPokemonDetailsProto %s", err)
		return true, fmt.Sprintf("Failed to parse GetStationedPokemonDetailsProto %s", err)
	}

	if decodedGetStationDetails.Result == pogo.GetStationedPokemonDetailsOutProto_STATION_NOT_FOUND {
		// station without stationed pokemon found, therefore we need to reset the columns
		return true, decoder.ResetStationedPokemonWithStationDetailsNotFound(ctx, dec.dbDetails, decodedGetStationDetailsRequest)
	} else if decodedGetStationDetails.Result != pogo.GetStationedPokemonDetailsOutProto_SUCCESS {
		return true, fmt.Sprintf("Ignored GetStationedPokemonDetailsOutProto non-success status %s", decodedGetStationDetails.Result)
	}

	return true, decoder.UpdateStationWithStationDetails(ctx, dec.dbDetails, decodedGetStationDetailsRequest, decodedGetStationDetails)
}
