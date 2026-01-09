package proto_decoder

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"golbat/db"
	"golbat/device_tracker"
	"golbat/pogo"
	"golbat/raw_decoder"
	"golbat/stats_collector"
)

type decodeProtoMethod struct {
	Decode   func(*ProtoDecoder, context.Context, PogoProto) (bool, string)
	MinLevel int
}

var decodeMethods = map[int]*decodeProtoMethod{
	int(pogo.Method_METHOD_START_INCIDENT):                              &decodeProtoMethod{(*ProtoDecoder).decodeStartIncident, 30},
	int(pogo.Method_METHOD_INVASION_OPEN_COMBAT_SESSION):                &decodeProtoMethod{(*ProtoDecoder).decodeOpenInvasion, 30},
	int(pogo.Method_METHOD_FORT_DETAILS):                                &decodeProtoMethod{(*ProtoDecoder).decodeFortDetails, 30},
	int(pogo.Method_METHOD_GET_MAP_OBJECTS):                             &decodeProtoMethod{(*ProtoDecoder).decodeGMO, 30},
	int(pogo.Method_METHOD_GYM_GET_INFO):                                &decodeProtoMethod{(*ProtoDecoder).decodeGetGymInfo, 30},
	int(pogo.Method_METHOD_ENCOUNTER):                                   &decodeProtoMethod{(*ProtoDecoder).decodeEncounter, 30},
	int(pogo.Method_METHOD_DISK_ENCOUNTER):                              &decodeProtoMethod{(*ProtoDecoder).decodeDiskEncounter, 30},
	int(pogo.Method_METHOD_FORT_SEARCH):                                 &decodeProtoMethod{(*ProtoDecoder).decodeQuest, 10},
	int(pogo.InternalPlatformClientAction_INTERNAL_PROXY_SOCIAL_ACTION): &decodeProtoMethod{(*ProtoDecoder).decodeSocialActionWithRequest, 0},
	int(pogo.Method_METHOD_GET_MAP_FORTS):                               &decodeProtoMethod{(*ProtoDecoder).decodeGetMapForts, 10},
	int(pogo.Method_METHOD_GET_ROUTES):                                  &decodeProtoMethod{(*ProtoDecoder).decodeGetRoutes, 30},
	int(pogo.Method_METHOD_GET_CONTEST_DATA):                            &decodeProtoMethod{(*ProtoDecoder).decodeGetContestData, 10},
	int(pogo.Method_METHOD_GET_POKEMON_SIZE_CONTEST_ENTRY):              &decodeProtoMethod{(*ProtoDecoder).decodeGetPokemonSizeContestEntry, 10},
	int(pogo.Method_METHOD_GET_STATION_DETAILS):                         &decodeProtoMethod{(*ProtoDecoder).decodeGetStationDetails, 10},
	int(pogo.Method_METHOD_PROCESS_TAPPABLE):                            &decodeProtoMethod{(*ProtoDecoder).decodeTappable, 30},
	int(pogo.Method_METHOD_GET_EVENT_RSVPS):                             &decodeProtoMethod{(*ProtoDecoder).decodeGetEventRsvp, 10},
	int(pogo.Method_METHOD_GET_EVENT_RSVP_COUNT):                        &decodeProtoMethod{(*ProtoDecoder).decodeGetEventRsvpCount, 10},
	// ignores
	int(pogo.Method_METHOD_GET_PLAYER):              nil,
	int(pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY):  nil,
	int(pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE): nil,
}

type ProtoDecoder struct {
	decodeTimeout  time.Duration
	dbDetails      db.DbDetails
	statsCollector stats_collector.StatsCollector
	deviceTracker  *device_tracker.DeviceTracker
}

func (dec *ProtoDecoder) decode(ctx context.Context, pogoProto raw_decoder.PogoProto) {
	processed := false
	ignore := false
	start := time.Now()
	result := ""

	method := pogoProto.GetMethod()
	decodeMethod, ok := decodeMethods[method]
	if ok {
		if decodeMethod == nil {
			// completely ignore
			return
		}
		if level := pogoProto.GetLevel(); level < decodeMethod.MinLevel {
			dec.statsCollector.IncDecodeMethods("error", "low_level", getMethodName(method, true))
			log.Debugf("Insufficient Level %d Did not process hook type %d(%s)", level, method, pogo.Method(method))
			return
		}
		processed, result = decodeMethod.Decode(dec, ctx, pogoProto)
	} else {
		log.Debugf("Did not know hook type %d(%s)", method, pogo.Method(method))
	}

	if !ignore {
		statResult := "ok"
		if !processed {
			result = "**Did not process**"
			statResult = "unprocessed"
		}
		dec.statsCollector.IncDecodeMethods(statResult, "", getMethodName(method, true))
		log.Debugf("%s/%s %s - %s - %s",
			pogoProto.GetDeviceId(), pogoProto.GetAccount(), pogo.Method(method),
			time.Since(start), result,
		)
	}
}

func (dec *ProtoDecoder) decodeGroup(ctx context.Context, protoGroup raw_decoder.PogoProtoGroup) {
	for _, proto := range protoGroup {
		// provide independent cancellation contexts for each proto decode
		ctx, cancel := context.WithTimeout(ctx, dec.decodeTimeout)
		dec.decode(ctx, proto)
		cancel()
	}
}

func (dec *ProtoDecoder) DecodeGroup(ctx context.Context, protoGroup raw_decoder.PogoProtoGroup) {
	l := len(protoGroup)
	if l == 0 {
		return
	}
	go dec.decodeGroup(ctx, protoGroup)
	lastProto := protoGroup[l-1]
	dec.deviceTracker.UpdateDeviceLocation(lastProto.GetDeviceId(), lastProto.GetLocation(), lastProto.GetScanContext())
}

func NewProtoDecoder(decodeTimeout time.Duration, dbDetails db.DbDetails, statsCollector stats_collector.StatsCollector, deviceTracker *device_tracker.DeviceTracker) *ProtoDecoder {
	return &ProtoDecoder{
		decodeTimeout:  decodeTimeout,
		dbDetails:      dbDetails,
		statsCollector: statsCollector,
		deviceTracker:  deviceTracker,
	}
}
