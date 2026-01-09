package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeTappable(ctx context.Context, pogoProto PogoProto) (bool, string) {
	scanParameters := pogoProto.GetScanParameters()
	if !scanParameters.ProcessTappables {
		return true, "Tappable processing disabled"
	}

	tappable, err := DecodeResponseProto[pogo.ProcessTappableOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return true, fmt.Sprintf("Failed to parse ProcessTappableOutProto %s", err)
	}

	tappableRequest, err := DecodeRequestProto[pogo.ProcessTappableProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return true, fmt.Sprintf("Failed to parse ProcessTappableProto %s", err)
	}

	if tappable.Status != pogo.ProcessTappableOutProto_SUCCESS {
		return true, fmt.Sprintf("Ignored ProcessTappableOutProto non-success status %s", tappable.Status)
	}
	var result string
	if encounter := tappable.GetEncounter(); encounter != nil {
		result = decoder.UpdatePokemonRecordWithTappableEncounter(ctx, dec.dbDetails, tappableRequest, encounter, pogoProto.GetAccount(), pogoProto.GetTimestampMs())
	}
	return true, result + " " + decoder.UpdateTappable(ctx, dec.dbDetails, tappableRequest, tappable, pogoProto.GetTimestampMs())
}
