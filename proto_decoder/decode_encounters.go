package proto_decoder

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"golbat/decoder"
	"golbat/pogo"
)

func (dec *ProtoDecoder) decodeEncounter(ctx context.Context, pogoProto PogoProto) (bool, string) {
	scanParameters := pogoProto.GetScanParameters()
	if !scanParameters.ProcessPokemon {
		return true, "Pokemon processing disabled"
	}

	decodedEncounterInfo, err := DecodeResponseProto[pogo.EncounterOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeEncounter("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Status != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
		dec.statsCollector.IncDecodeEncounter("error", "non_success")
		res := fmt.Sprintf(`EncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Status,
			pogo.EncounterOutProto_Status_name[int32(decodedEncounterInfo.Status)])
		return true, res
	}

	dec.statsCollector.IncDecodeEncounter("ok", "")
	return true, decoder.UpdatePokemonRecordWithEncounterProto(ctx, dec.dbDetails, decodedEncounterInfo, pogoProto.GetAccount(), pogoProto.GetTimestampMs())
}

func (dec *ProtoDecoder) decodeDiskEncounter(ctx context.Context, pogoProto PogoProto) (bool, string) {
	decodedEncounterInfo, err := DecodeResponseProto[pogo.DiskEncounterOutProto](pogoProto)
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		dec.statsCollector.IncDecodeDiskEncounter("error", "parse")
		return true, fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		dec.statsCollector.IncDecodeDiskEncounter("error", "non_success")
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return true, res
	}

	dec.statsCollector.IncDecodeDiskEncounter("ok", "")
	return true, decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dec.dbDetails, decodedEncounterInfo, pogoProto.GetAccount())
}
