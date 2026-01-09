package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeStartIncident(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedIncident, err := DecodeResponseProto[pogo.StartIncidentOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeStartIncident("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedIncident.Status != pogo.StartIncidentOutProto_SUCCESS {
		dec.statsCollector.IncDecodeStartIncident("error", "non_success")
		res := fmt.Sprintf(`GiovanniOutProto: Ignored non-success value %d:%s`, decodedIncident.Status,
			pogo.StartIncidentOutProto_Status_name[int32(decodedIncident.Status)])
		return true, res
	}

	dec.statsCollector.IncDecodeStartIncident("ok", "")
	return true, decoder.ConfirmIncident(ctx, dec.dbDetails, decodedIncident)
}

func (dec *ProtoDecoder) decodeOpenInvasion(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodeOpenInvasionRequest, err := DecodeRequestProto[pogo.OpenInvasionCombatSessionProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeOpenInvasion("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}
	if decodeOpenInvasionRequest.IncidentLookup == nil {
		return true, "Invalid OpenInvasionCombatSessionProto received"
	}

	decodedOpenInvasionResponse, err := DecodeResponseProto[pogo.OpenInvasionCombatSessionOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeOpenInvasion("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedOpenInvasionResponse.Status != pogo.InvasionStatus_SUCCESS {
		dec.statsCollector.IncDecodeOpenInvasion("error", "non_success")
		res := fmt.Sprintf(`InvasionLineupOutProto: Ignored non-success value %d:%s`, decodedOpenInvasionResponse.Status,
			pogo.InvasionStatus_Status_name[int32(decodedOpenInvasionResponse.Status)])
		return true, res
	}

	dec.statsCollector.IncDecodeOpenInvasion("ok", "")
	return true, decoder.UpdateIncidentLineup(ctx, dec.dbDetails, decodeOpenInvasionRequest, decodedOpenInvasionResponse)
}
