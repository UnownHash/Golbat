package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeQuest(ctx context.Context, pogoProto PogoProto) (bool, string) {
	haveAr := pogoProto.GetHaveAr()
	if haveAr == nil {
		dec.statsCollector.IncDecodeQuest("error", "missing_ar_info")
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return true, "No AR quest info"
	}
	decodedQuest, err := DecodeResponseProto[pogo.FortSearchOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeQuest("error", "parse")
		return true, "Parse failure"
	}

	if decodedQuest.Result != pogo.FortSearchOutProto_SUCCESS {
		dec.statsCollector.IncDecodeQuest("error", "non_success")
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedQuest.Result,
			pogo.FortSearchOutProto_Result_name[int32(decodedQuest.Result)])
		return true, res
	}

	return true, decoder.UpdatePokestopWithQuest(ctx, dec.dbDetails, decodedQuest, *haveAr)
}

func (dec *ProtoDecoder) decodeFortDetails(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedFort, err := DecodeResponseProto[pogo.FortDetailsOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeFortDetails("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	switch decodedFort.FortType {
	case pogo.FortType_CHECKPOINT:
		dec.statsCollector.IncDecodeFortDetails("ok", "pokestop")
		return true, decoder.UpdatePokestopRecordWithFortDetailsOutProto(ctx, dec.dbDetails, decodedFort)
	case pogo.FortType_GYM:
		dec.statsCollector.IncDecodeFortDetails("ok", "gym")
		return true, decoder.UpdateGymRecordWithFortDetailsOutProto(ctx, dec.dbDetails, decodedFort)
	}

	dec.statsCollector.IncDecodeFortDetails("ok", "unknown")
	return true, "Unknown fort type"
}

func (dec *ProtoDecoder) decodeGetMapForts(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedMapForts, err := DecodeResponseProto[pogo.GetMapFortsOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeGetMapForts("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedMapForts.Status != pogo.GetMapFortsOutProto_SUCCESS {
		dec.statsCollector.IncDecodeGetMapForts("error", "non_success")
		res := fmt.Sprintf(`GetMapFortsOutProto: Ignored non-success value %d:%s`, decodedMapForts.Status,
			pogo.GetMapFortsOutProto_Status_name[int32(decodedMapForts.Status)])
		return true, res
	}

	dec.statsCollector.IncDecodeGetMapForts("ok", "")
	var outputString string
	processedForts := 0

	for _, fort := range decodedMapForts.Fort {
		status, output := decoder.UpdateFortRecordWithGetMapFortsOutProto(ctx, dec.dbDetails, fort)
		if status {
			processedForts += 1
			outputString += output + ", "
		}
	}

	if processedForts > 0 {
		return true, fmt.Sprintf("Updated %d forts: %s", processedForts, outputString)
	}
	return true, "No forts updated"
}

func (dec *ProtoDecoder) decodeGetGymInfo(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedGymInfo, err := DecodeResponseProto[pogo.GymGetInfoOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeGetGymInfo("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedGymInfo.Result != pogo.GymGetInfoOutProto_SUCCESS {
		dec.statsCollector.IncDecodeGetGymInfo("error", "non_success")
		res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedGymInfo.Result,
			pogo.GymGetInfoOutProto_Result_name[int32(decodedGymInfo.Result)])
		return true, res
	}

	dec.statsCollector.IncDecodeGetGymInfo("ok", "")
	return true, decoder.UpdateGymRecordWithGymInfoProto(ctx, dec.dbDetails, decodedGymInfo)
}

func (dec *ProtoDecoder) decodeGetEventRsvp(ctx context.Context, pogoProto PogoProto) (bool, string) {
	scanParameters := pogoProto.GetScanParameters()
	if !scanParameters.ProcessGyms {
		return true, "Gym processing disabled"
	}

	rsvp, err := DecodeResponseProto[pogo.GetEventRsvpsOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return true, fmt.Sprintf("Failed to parse GetEventRsvpsOutProto %s", err)
	}

	rsvpRequest, err := DecodeRequestProto[pogo.GetEventRsvpsProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return true, fmt.Sprintf("Failed to parse GetEventRsvpsProto %s", err)
	}

	if rsvp.Status != pogo.GetEventRsvpsOutProto_SUCCESS {
		return true, fmt.Sprintf("Ignored GetEventRsvpsOutProto non-success status %s", rsvp.Status)
	}

	switch op := rsvpRequest.EventDetails.(type) {
	case *pogo.GetEventRsvpsProto_Raid:
		return true, decoder.UpdateGymRecordWithRsvpProto(ctx, dec.dbDetails, op.Raid, rsvp)
	case *pogo.GetEventRsvpsProto_GmaxBattle:
		return true, "Unsupported GmaxBattle Rsvp received"
	}

	return true, "Failed to parse GetEventRsvpsProto - unknown event type"
}

func (dec *ProtoDecoder) decodeGetEventRsvpCount(ctx context.Context, pogoProto PogoProto) (bool, string) {
	scanParameters := pogoProto.GetScanParameters()
	if !scanParameters.ProcessGyms {
		return true, "Gym processing disabled"
	}

	rsvp, err := DecodeResponseProto[pogo.GetEventRsvpCountOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return true, fmt.Sprintf("Failed to parse GetEventRsvpCountOutProto %s", err)
	}

	if rsvp.Status != pogo.GetEventRsvpCountOutProto_SUCCESS {
		return true, fmt.Sprintf("Ignored GetEventRsvpCountOutProto non-success status %s", rsvp.Status)
	}

	var clearLocations []string
	for _, rsvpDetails := range rsvp.RsvpDetails {
		if rsvpDetails.MaybeCount == 0 && rsvpDetails.GoingCount == 0 {
			clearLocations = append(clearLocations, rsvpDetails.LocationId)
			decoder.ClearGymRsvp(ctx, dec.dbDetails, rsvpDetails.LocationId)
		}
	}

	return true, "Cleared RSVP @ " + fmt.Sprint(clearLocations)
}
