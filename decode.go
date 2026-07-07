package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golbat/decoder"
	"golbat/pogo"
	"golbat/pogoshim"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func decode(ctx context.Context, method int, protoData *ProtoData) {
	getMethodName := func(method int, trimString bool) string {
		if val, ok := pogo.Method_name[int32(method)]; ok {
			if trimString && strings.HasPrefix(val, "METHOD_") {
				return strings.TrimPrefix(val, "METHOD_")
			}
			return val
		}
		return fmt.Sprintf("#%d", method)
	}

	if method != int(pogo.InternalPlatformClientAction_INTERNAL_PROXY_SOCIAL_ACTION) && protoData.Level < 30 {
		statsCollector.IncDecodeMethods("error", "low_level", getMethodName(method, true))
		log.Debugf("Insufficient Level %d Did not process hook type %s", protoData.Level, pogo.Method(method))
		return
	}

	processed := false
	ignore := false
	start := time.Now()
	result := ""

	switch pogo.Method(method) {
	case pogo.Method_METHOD_START_INCIDENT:
		result = decodeStartIncident(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_INVASION_OPEN_COMBAT_SESSION:
		if protoData.Request != nil {
			result = decodeOpenInvasion(ctx, protoData.Request, protoData.Data)
			processed = true
		}
	case pogo.Method_METHOD_FORT_DETAILS:
		result = decodeFortDetails(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_MAP_OBJECTS:
		result = decodeGMO(ctx, protoData, getScanParameters(protoData))
		processed = true
	case pogo.Method_METHOD_GYM_GET_INFO:
		result = decodeGetGymInfo(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_ENCOUNTER:
		if getScanParameters(protoData).ProcessPokemon {
			result = decodeEncounter(ctx, protoData.Data, protoData.Account, protoData.TimestampMs)
		}
		processed = true
	case pogo.Method_METHOD_DISK_ENCOUNTER:
		result = decodeDiskEncounter(ctx, protoData.Data, protoData.Account)
		processed = true
	case pogo.Method_METHOD_FORT_SEARCH:
		result = decodeQuest(ctx, protoData.Data, protoData.HaveAr)
		processed = true
	case pogo.Method_METHOD_GET_PLAYER:
		ignore = true
	case pogo.Method_METHOD_GET_HOLOHOLO_INVENTORY:
		ignore = true
	case pogo.Method_METHOD_CREATE_COMBAT_CHALLENGE:
		ignore = true
	case pogo.Method(pogo.InternalPlatformClientAction_INTERNAL_PROXY_SOCIAL_ACTION):
		if protoData.Request != nil {
			result = decodeSocialActionWithRequest(protoData.Request, protoData.Data)
			processed = true
		}
	case pogo.Method_METHOD_GET_MAP_FORTS:
		result = decodeGetMapForts(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_ROUTES:
		result = decodeGetRoutes(ctx, protoData.Data)
		processed = true
	case pogo.Method_METHOD_GET_CONTEST_DATA:
		if getScanParameters(protoData).ProcessPokestops {
			// Request helps, but can be decoded without it
			result = decodeGetContestData(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_GET_POKEMON_SIZE_CONTEST_ENTRY:
		// Request is essential to decode this
		if protoData.Request != nil {
			if getScanParameters(protoData).ProcessPokestops {
				result = decodeGetPokemonSizeContestEntry(ctx, protoData.Request, protoData.Data)
			}
			processed = true
		}
	case pogo.Method_METHOD_GET_STATION_DETAILS:
		if getScanParameters(protoData).ProcessStations {
			// Request is essential to decode this
			result = decodeGetStationDetails(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_PROCESS_TAPPABLE:
		if getScanParameters(protoData).ProcessTappables {
			// Request is essential to decode this
			result = decodeTappable(ctx, protoData.Request, protoData.Data, protoData.Account, protoData.TimestampMs)
		}
		processed = true
	case pogo.Method_METHOD_GET_EVENT_RSVPS:
		if getScanParameters(protoData).ProcessGyms {
			result = decodeGetEventRsvp(ctx, protoData.Request, protoData.Data)
		}
		processed = true
	case pogo.Method_METHOD_GET_EVENT_RSVP_COUNT:
		if getScanParameters(protoData).ProcessGyms {
			result = decodeGetEventRsvpCount(ctx, protoData.Data)
		}
		processed = true
	default:
		log.Debugf("Did not know hook type %s", pogo.Method(method))
	}
	if !ignore {
		elapsed := time.Since(start)
		if processed {
			statsCollector.IncDecodeMethods("ok", "", getMethodName(method, true))
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, result)
		} else {
			log.Debugf("%s/%s %s - %s - %s", protoData.Uuid, protoData.Account, pogo.Method(method), elapsed, "**Did not process**")
			statsCollector.IncDecodeMethods("unprocessed", "", getMethodName(method, true))
		}
	}
}

func getScanParameters(protoData *ProtoData) decoder.ScanParameters {
	return decoder.FindScanConfiguration(protoData.ScanContext, protoData.Lat, protoData.Lon)
}

func decodeQuest(ctx context.Context, sDec []byte, haveAr *bool) string {
	if haveAr == nil {
		statsCollector.IncDecodeQuest("error", "missing_ar_info")
		log.Infoln("Cannot determine AR quest - ignoring")
		// We should either assume AR quest, or trace inventory like RDM probably
		return "No AR quest info"
	}
	maybeShadow(engMethodQuest, sDec)
	res, err := decodeWithArena(engMethodQuest, questEngine, sDec,
		pogoshim.AsFortSearchOutProto,
		func(decodedQuest pogoshim.FortSearchOutProto) string {
			if decodedQuest.GetResult() != pogo.FortSearchOutProto_SUCCESS {
				statsCollector.IncDecodeQuest("error", "non_success")
				res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedQuest.GetResult(),
					pogo.FortSearchOutProto_Result_name[int32(decodedQuest.GetResult())])
				return res
			}

			return decoder.UpdatePokestopWithQuest(ctx, dbDetails, decodedQuest, *haveAr)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeQuest("error", "parse")
		return "Parse failure"
	}
	return res
}

func decodeSocialActionWithRequest(request []byte, payload []byte) string {
	var proxyRequestProto pogo.ProxyRequestProto

	if err := proto.Unmarshal(request, &proxyRequestProto); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeSocialActionWithRequest("error", "request_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	var proxyResponseProto pogo.ProxyResponseProto

	if err := proto.Unmarshal(payload, &proxyResponseProto); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeSocialActionWithRequest("error", "response_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.Status != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
		statsCollector.IncDecodeSocialActionWithRequest("error", "non_success")
		return fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.Status), proxyResponseProto.Status)
	}

	switch pogo.InternalSocialAction(proxyRequestProto.GetAction()) {
	case pogo.InternalSocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
		statsCollector.IncDecodeSocialActionWithRequest("ok", "list_friend_status")
		return decodeGetFriendDetails(proxyResponseProto.Payload)
	case pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
		statsCollector.IncDecodeSocialActionWithRequest("ok", "search_player")
		return decodeSearchPlayer(&proxyRequestProto, proxyResponseProto.Payload)

	}

	statsCollector.IncDecodeSocialActionWithRequest("ok", "unknown")
	return fmt.Sprintf("Did not process %s", pogo.InternalSocialAction(proxyRequestProto.GetAction()).String())
}

func decodeGetFriendDetails(payload []byte) string {
	var getFriendDetailsOutProto pogo.InternalGetFriendDetailsOutProto
	getFriendDetailsError := proto.Unmarshal(payload, &getFriendDetailsOutProto)

	if getFriendDetailsError != nil {
		statsCollector.IncDecodeGetFriendDetails("error", "parse")
		log.Errorf("Failed to parse %s", getFriendDetailsError)
		return fmt.Sprintf("Failed to parse %s", getFriendDetailsError)
	}

	if getFriendDetailsOutProto.GetResult() != pogo.InternalGetFriendDetailsOutProto_SUCCESS || getFriendDetailsOutProto.GetFriend() == nil {
		statsCollector.IncDecodeGetFriendDetails("error", "non_success")
		return "unsuccessful get friends details"
	}

	failures := 0

	for _, friend := range getFriendDetailsOutProto.GetFriend() {
		player := friend.GetPlayer()

		updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.PublicData, "", player.GetPlayerId())
		if updatePlayerError != nil {
			failures++
		}
	}

	statsCollector.IncDecodeGetFriendDetails("ok", "")
	return fmt.Sprintf("%d players decoded on %d", len(getFriendDetailsOutProto.GetFriend())-failures, len(getFriendDetailsOutProto.GetFriend()))
}

func decodeSearchPlayer(proxyRequestProto *pogo.ProxyRequestProto, payload []byte) string {
	var searchPlayerOutProto pogo.InternalSearchPlayerOutProto
	searchPlayerOutError := proto.Unmarshal(payload, &searchPlayerOutProto)

	if searchPlayerOutError != nil {
		log.Errorf("Failed to parse %s", searchPlayerOutError)
		statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerOutError)
	}

	if searchPlayerOutProto.GetResult() != pogo.InternalSearchPlayerOutProto_SUCCESS || searchPlayerOutProto.GetPlayer() == nil {
		statsCollector.IncDecodeSearchPlayer("error", "non_success")
		return "unsuccessful search player response"
	}

	var searchPlayerProto pogo.InternalSearchPlayerProto
	searchPlayerError := proto.Unmarshal(proxyRequestProto.GetPayload(), &searchPlayerProto)

	if searchPlayerError != nil || searchPlayerProto.GetFriendCode() == "" {
		statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", searchPlayerError)
	}

	player := searchPlayerOutProto.GetPlayer()
	updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.PublicData, searchPlayerProto.GetFriendCode(), "")
	if updatePlayerError != nil {
		statsCollector.IncDecodeSearchPlayer("error", "update")
		return fmt.Sprintf("Failed update player %s", updatePlayerError)
	}

	statsCollector.IncDecodeSearchPlayer("ok", "")
	return "1 player decoded from SearchPlayerProto"
}

func decodeFortDetails(ctx context.Context, sDec []byte) string {
	maybeShadow(engMethodFortDetails, sDec)
	res, err := decodeWithArena(engMethodFortDetails, fortDetailsEngine, sDec,
		pogoshim.AsFortDetailsOutProto,
		func(decodedFort pogoshim.FortDetailsOutProto) string {
			switch decodedFort.GetFortType() {
			case pogo.FortType_CHECKPOINT:
				statsCollector.IncDecodeFortDetails("ok", "pokestop")
				return decoder.UpdatePokestopRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
			case pogo.FortType_GYM:
				statsCollector.IncDecodeFortDetails("ok", "gym")
				return decoder.UpdateGymRecordWithFortDetailsOutProto(ctx, dbDetails, decodedFort)
			}

			statsCollector.IncDecodeFortDetails("ok", "unknown")
			return "Unknown fort type"
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeFortDetails("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeGetMapForts(ctx context.Context, sDec []byte) string {
	maybeShadow(engMethodGetMapForts, sDec)
	res, err := decodeWithArena(engMethodGetMapForts, mapFortsEngine, sDec,
		pogoshim.AsGetMapFortsOutProto,
		func(decodedMapForts pogoshim.GetMapFortsOutProto) string {
			if decodedMapForts.GetStatus() != pogo.GetMapFortsOutProto_SUCCESS {
				statsCollector.IncDecodeGetMapForts("error", "non_success")
				res := fmt.Sprintf(`GetMapFortsOutProto: Ignored non-success value %d:%s`, decodedMapForts.GetStatus(),
					pogo.GetMapFortsOutProto_Status_name[int32(decodedMapForts.GetStatus())])
				return res
			}

			statsCollector.IncDecodeGetMapForts("ok", "")
			var outputString string
			processedForts := 0

			for fort := range decodedMapForts.GetFort().All() {
				status, output := decoder.UpdateFortRecordWithGetMapFortsOutProto(ctx, dbDetails, fort)
				if status {
					processedForts += 1
					outputString += output + ", "
				}
			}

			if processedForts > 0 {
				return fmt.Sprintf("Updated %d forts: %s", processedForts, outputString)
			}
			return "No forts updated"
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeGetMapForts("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeGetRoutes(ctx context.Context, sDec []byte) string {
	maybeShadow(engMethodRoutes, sDec)
	res, err := decodeWithArena(engMethodRoutes, routesEngine, sDec,
		pogoshim.AsGetRoutesOutProto,
		func(getRoutesOutProto pogoshim.GetRoutesOutProto) string {
			if getRoutesOutProto.GetStatus() != pogo.GetRoutesOutProto_SUCCESS {
				return fmt.Sprintf("GetRoutesOutProto: Ignored non-success value %d:%s", getRoutesOutProto.GetStatus(), getRoutesOutProto.GetStatus().String())
			}

			decodeSuccesses := map[string]bool{}
			decodeErrors := map[string]bool{}

			cells := getRoutesOutProto.GetRouteMapCell()
			for cell := range cells.All() {
				for route := range cell.GetRoute().All() {
					// TODO we need to check the repeated field, for now access last element.
					// Len()>0 guard (absent in the pre-shim direct-index version, which
					// would have panicked on a genuinely empty list) degrades safely
					// instead, matching every other getter-chain in this migration.
					statuses := route.GetRouteSubmissionStatus()
					if statuses.Len() > 0 {
						last := statuses.At(statuses.Len() - 1)
						if last.GetStatus() != pogo.RouteSubmissionStatus_PUBLISHED {
							log.Warnf("Non published Route found in GetRoutesOutProto, status: %s", last.GetStatus().String())
							continue
						}
					}
					decodeError := decoder.UpdateRouteRecordWithSharedRouteProto(ctx, dbDetails, route)
					if decodeError != nil {
						if !decodeErrors[route.GetId()] {
							decodeErrors[route.GetId()] = true
						}
						log.Errorf("Failed to decode route %s", decodeError)
					} else if !decodeSuccesses[route.GetId()] {
						decodeSuccesses[route.GetId()] = true
					}
				}
			}

			return fmt.Sprintf(
				"Decoded %d routes, failed to decode %d routes, from %d cells",
				len(decodeSuccesses),
				len(decodeErrors),
				cells.Len(),
			)
		})
	if err != nil {
		return fmt.Sprintf("failed to decode GetRoutesOutProto %s", err)
	}
	return res
}

func decodeGetGymInfo(ctx context.Context, sDec []byte) string {
	maybeShadow(engMethodGymInfo, sDec)
	res, err := decodeWithArena(engMethodGymInfo, gymInfoEngine, sDec,
		pogoshim.AsGymGetInfoOutProto,
		func(decodedGymInfo pogoshim.GymGetInfoOutProto) string {
			if decodedGymInfo.GetResult() != pogo.GymGetInfoOutProto_SUCCESS {
				statsCollector.IncDecodeGetGymInfo("error", "non_success")
				res := fmt.Sprintf(`GymGetInfoOutProto: Ignored non-success value %d:%s`, decodedGymInfo.GetResult(),
					pogo.GymGetInfoOutProto_Result_name[int32(decodedGymInfo.GetResult())])
				return res
			}

			statsCollector.IncDecodeGetGymInfo("ok", "")
			return decoder.UpdateGymRecordWithGymInfoProto(ctx, dbDetails, decodedGymInfo)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeGetGymInfo("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeEncounter(ctx context.Context, sDec []byte, username string, timestampMs int64) string {
	maybeShadow(engMethodEncounter, sDec)
	res, err := decodeWithArena(engMethodEncounter, encounterEngine, sDec,
		pogoshim.AsEncounterOutProto,
		func(enc pogoshim.EncounterOutProto) string {
			if enc.GetStatus() != pogo.EncounterOutProto_ENCOUNTER_SUCCESS {
				statsCollector.IncDecodeEncounter("error", "non_success")
				res := fmt.Sprintf(`EncounterOutProto: Ignored non-success value %d:%s`, enc.GetStatus(),
					pogo.EncounterOutProto_Status_name[int32(enc.GetStatus())])
				return res
			}

			statsCollector.IncDecodeEncounter("ok", "")
			return decoder.UpdatePokemonRecordWithEncounterProto(ctx, dbDetails, enc, username, timestampMs)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeDiskEncounter(ctx context.Context, sDec []byte, username string) string {
	maybeShadow(engMethodDiskEncounter, sDec)
	res, err := decodeWithArena(engMethodDiskEncounter, diskEncounterEngine, sDec,
		pogoshim.AsDiskEncounterOutProto,
		func(enc pogoshim.DiskEncounterOutProto) string {
			if enc.GetResult() != pogo.DiskEncounterOutProto_SUCCESS {
				statsCollector.IncDecodeDiskEncounter("error", "non_success")
				res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, enc.GetResult(),
					pogo.DiskEncounterOutProto_Result_name[int32(enc.GetResult())])
				return res
			}

			statsCollector.IncDecodeDiskEncounter("ok", "")
			return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, enc, sDec, username)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeDiskEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeStartIncident(ctx context.Context, sDec []byte) string {
	maybeShadow(engMethodStartIncident, sDec)
	res, err := decodeWithArena(engMethodStartIncident, startIncidentEngine, sDec,
		pogoshim.AsStartIncidentOutProto,
		func(decodedIncident pogoshim.StartIncidentOutProto) string {
			if decodedIncident.GetStatus() != pogo.StartIncidentOutProto_SUCCESS {
				statsCollector.IncDecodeStartIncident("error", "non_success")
				res := fmt.Sprintf(`GiovanniOutProto: Ignored non-success value %d:%s`, decodedIncident.GetStatus(),
					pogo.StartIncidentOutProto_Status_name[int32(decodedIncident.GetStatus())])
				return res
			}

			statsCollector.IncDecodeStartIncident("ok", "")
			return decoder.ConfirmIncident(ctx, dbDetails, decodedIncident)
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeStartIncident("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

// decodeOpenInvasion is the template for every "request+data" method in this
// wave (Task 4 copies this shape for contest_data/size_contest_entry/
// station_details/tappable/event_rsvps): the Request proto is decoded via
// its own decodeWithArena handle, and the Data proto is decoded via ITS OWN
// handle INSIDE the Request's process closure. Both shims (and both arenas
// backing them, when running hyperpb) are therefore alive together for the
// UpdateIncidentLineup call, which needs fields from both -- and both arenas
// are freed only once decodeOpenInvasion returns, since the inner
// decodeWithArena call (and everything under it) completes before the outer
// one's closure returns.
func decodeOpenInvasion(ctx context.Context, request []byte, payload []byte) string {
	maybeShadowPair(engMethodOpenInvasion, request, payload)
	res, err := decodeWithArena(engMethodOpenInvasion, openInvasionReqEngine, request,
		pogoshim.AsOpenInvasionCombatSessionProto,
		func(decodeOpenInvasionRequest pogoshim.OpenInvasionCombatSessionProto) string {
			if !decodeOpenInvasionRequest.HasIncidentLookup() {
				return "Invalid OpenInvasionCombatSessionProto received"
			}

			innerRes, innerErr := decodeWithArena(engMethodOpenInvasion, openInvasionEngine, payload,
				pogoshim.AsOpenInvasionCombatSessionOutProto,
				func(decodedOpenInvasionResponse pogoshim.OpenInvasionCombatSessionOutProto) string {
					if decodedOpenInvasionResponse.GetStatus() != pogo.InvasionStatus_SUCCESS {
						statsCollector.IncDecodeOpenInvasion("error", "non_success")
						res := fmt.Sprintf(`InvasionLineupOutProto: Ignored non-success value %d:%s`, decodedOpenInvasionResponse.GetStatus(),
							pogo.InvasionStatus_Status_name[int32(decodedOpenInvasionResponse.GetStatus())])
						return res
					}

					statsCollector.IncDecodeOpenInvasion("ok", "")
					return decoder.UpdateIncidentLineup(ctx, dbDetails, decodeOpenInvasionRequest, decodedOpenInvasionResponse)
				})
			if innerErr != nil {
				log.Errorf("Failed to parse %s", innerErr)
				statsCollector.IncDecodeOpenInvasion("error", "parse")
				return fmt.Sprintf("Failed to parse %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeOpenInvasion("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func decodeGMO(ctx context.Context, protoData *ProtoData, scanParameters decoder.ScanParameters) string {
	maybeShadow(engMethodGmo, protoData.Data)
	res, err := decodeWithArena(engMethodGmo, gmoEngine, protoData.Data,
		pogoshim.AsGetMapObjectsOutProto,
		func(gmo pogoshim.GetMapObjectsOutProto) string {
			if gmo.GetStatus() != pogo.GetMapObjectsOutProto_SUCCESS {
				statsCollector.IncDecodeGMO("error", "non_success")
				res := fmt.Sprintf(`GetMapObjectsOutProto: Ignored non-success value %d:%s`, gmo.GetStatus(),
					pogo.GetMapObjectsOutProto_Status_name[int32(gmo.GetStatus())])
				return res
			}

			var newForts []decoder.RawFortData
			var newStations []decoder.RawStationData
			var newWildPokemon []decoder.RawWildPokemonData
			var newNearbyPokemon []decoder.RawNearbyPokemonData
			var newMapPokemon []decoder.RawMapPokemonData
			var newMapCells []uint64

			// track forts per cell for memory-based cleanup (every map cell gets an
			// entry, so empty fort lists are seen as "no forts" by the tracker)
			cellForts := make(map[uint64]*decoder.FortTrackerGMOContents)

			cells := gmo.GetMapCell()
			if cells.Len() == 0 {
				return "Skipping GetMapObjectsOutProto: No map cells found"
			}
			// Hoisted into a plain int64 now, while cells is still backed by a
			// live arena. cells (and everything under it) is arena-allocated by
			// the hyperpb engine and is only valid for the lifetime of this
			// process closure; decodeHyperpb frees/pools the arena once this
			// closure returns. ProactiveIVSwitch runs on goroutines that can
			// outlive the closure, so it must never retain a reference into
			// cells directly (see capture below).
			firstCellAsOfTimeMs := cells.At(0).GetAsOfTimeMs()
			for cell := range cells.All() {
				cellForts[cell.GetS2CellId()] = &decoder.FortTrackerGMOContents{
					Pokestops: make([]string, 0),
					Gyms:      make([]string, 0),
					Timestamp: cell.GetAsOfTimeMs(),
				}

				if isCellNotEmpty(cell) {
					newMapCells = append(newMapCells, cell.GetS2CellId())
				}

				for fort := range cell.GetFort().All() {
					newForts = append(newForts, decoder.RawFortData{Cell: cell.GetS2CellId(), Data: fort, Timestamp: cell.GetAsOfTimeMs()})

					// track fort by type for memory-based cleanup (only if tracker enabled)
					if cf, ok := cellForts[cell.GetS2CellId()]; ok {
						switch fort.GetFortType() {
						case pogo.FortType_GYM:
							cf.Gyms = append(cf.Gyms, fort.GetFortId())
						case pogo.FortType_CHECKPOINT:
							cf.Pokestops = append(cf.Pokestops, fort.GetFortId())
						}
					}

					if fort.HasActivePokemon() {
						newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{Cell: cell.GetS2CellId(), Data: fort.GetActivePokemon(), Timestamp: cell.GetAsOfTimeMs()})
					}
				}
				for mon := range cell.GetWildPokemon().All() {
					newWildPokemon = append(newWildPokemon, decoder.RawWildPokemonData{Cell: cell.GetS2CellId(), Data: mon, Timestamp: cell.GetAsOfTimeMs()})
				}
				for mon := range cell.GetNearbyPokemon().All() {
					newNearbyPokemon = append(newNearbyPokemon, decoder.RawNearbyPokemonData{Cell: cell.GetS2CellId(), Data: mon, Timestamp: cell.GetAsOfTimeMs()})
				}
				for station := range cell.GetStations().All() {
					newStations = append(newStations, decoder.RawStationData{Cell: cell.GetS2CellId(), Data: station})
				}
			}

			var newClientWeather []pogoshim.ClientWeatherProto
			for w := range gmo.GetClientWeather().All() {
				newClientWeather = append(newClientWeather, w)
			}

			if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
				decoder.UpdateFortBatch(ctx, dbDetails, scanParameters, newForts)
			}
			var weatherUpdates []decoder.WeatherUpdate
			if scanParameters.ProcessWeather {
				weatherUpdates = decoder.UpdateClientWeatherBatch(ctx, dbDetails, newClientWeather, firstCellAsOfTimeMs, protoData.Account)
			}
			if scanParameters.ProcessPokemon {
				decoder.UpdatePokemonBatch(ctx, dbDetails, scanParameters, newWildPokemon, newNearbyPokemon, newMapPokemon, newClientWeather, protoData.Account)
				if scanParameters.ProcessWeather && scanParameters.ProactiveIVSwitching {
					// Only plain values (weatherUpdate, firstCellAsOfTimeMs/1000) are
					// captured here — never cells or any arena-backed pogoshim type.
					// These goroutines are throttled by ProactiveIVSwitchSem, not by
					// this closure's lifetime, and can still be running long after
					// decodeHyperpb has returned the arena to its pool.
					asOfTimeSec := firstCellAsOfTimeMs / 1000
					for _, weatherUpdate := range weatherUpdates {
						go func(weatherUpdate decoder.WeatherUpdate) {
							decoder.ProactiveIVSwitchSem <- true
							defer func() { <-decoder.ProactiveIVSwitchSem }()
							decoder.ProactiveIVSwitch(ctx, dbDetails, weatherUpdate, scanParameters.ProactiveIVSwitchingToDB, asOfTimeSec)
						}(weatherUpdate)
					}
				}
			}
			if scanParameters.ProcessStations {
				decoder.UpdateStationBatch(ctx, dbDetails, scanParameters, newStations)
			}

			if scanParameters.ProcessCells {
				decoder.UpdateClientMapS2CellBatch(ctx, dbDetails, newMapCells)
			}

			if scanParameters.ProcessGyms || scanParameters.ProcessPokestops {
				decoder.CheckRemovedForts(ctx, dbDetails, cellForts)
			}

			newFortsLen := len(newForts)
			newStationsLen := len(newStations)
			newWildPokemonLen := len(newWildPokemon)
			newNearbyPokemonLen := len(newNearbyPokemon)
			newMapPokemonLen := len(newMapPokemon)
			newClientWeatherLen := len(newClientWeather)
			newMapCellsLen := len(newMapCells)

			statsCollector.IncDecodeGMO("ok", "")
			statsCollector.AddDecodeGMOType("fort", float64(newFortsLen))
			statsCollector.AddDecodeGMOType("station", float64(newStationsLen))
			statsCollector.AddDecodeGMOType("wild_pokemon", float64(newWildPokemonLen))
			statsCollector.AddDecodeGMOType("nearby_pokemon", float64(newNearbyPokemonLen))
			statsCollector.AddDecodeGMOType("map_pokemon", float64(newMapPokemonLen))
			statsCollector.AddDecodeGMOType("weather", float64(newClientWeatherLen))
			statsCollector.AddDecodeGMOType("cell", float64(newMapCellsLen))

			return fmt.Sprintf("%d cells containing %d forts %d stations %d mon %d nearby", newMapCellsLen, newFortsLen, newStationsLen, newWildPokemonLen, newNearbyPokemonLen)
		})
	if err != nil {
		statsCollector.IncDecodeGMO("error", "parse")
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

func isCellNotEmpty(cell pogoshim.ClientMapCellProto) bool {
	return cell.GetStations().Len() > 0 || cell.GetFort().Len() > 0 || cell.GetWildPokemon().Len() > 0 || cell.GetNearbyPokemon().Len() > 0 || cell.GetCatchablePokemon().Len() > 0
}

func decodeGetContestData(ctx context.Context, request []byte, data []byte) string {
	var decodedContestData pogo.GetContestDataOutProto
	if err := proto.Unmarshal(data, &decodedContestData); err != nil {
		log.Errorf("Failed to parse GetContestDataOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetContestDataOutProto %s", err)
	}

	var decodedContestDataRequest pogo.GetContestDataProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedContestDataRequest); err != nil {
			log.Errorf("Failed to parse GetContestDataProto %s", err)
			return fmt.Sprintf("Failed to parse GetContestDataProto %s", err)
		}
	}
	return decoder.UpdatePokestopWithContestData(ctx, dbDetails, &decodedContestDataRequest, &decodedContestData)
}

func decodeGetPokemonSizeContestEntry(ctx context.Context, request []byte, data []byte) string {
	var decodedPokemonSizeContestEntry pogo.GetPokemonSizeLeaderboardEntryOutProto
	if err := proto.Unmarshal(data, &decodedPokemonSizeContestEntry); err != nil {
		log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
	}

	if decodedPokemonSizeContestEntry.Status != pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetPokemonSizeLeaderboardEntryOutProto non-success status %s", decodedPokemonSizeContestEntry.Status)
	}

	var decodedPokemonSizeContestEntryRequest pogo.GetPokemonSizeLeaderboardEntryProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedPokemonSizeContestEntryRequest); err != nil {
			log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
			return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		}
	}

	return decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dbDetails, &decodedPokemonSizeContestEntryRequest, &decodedPokemonSizeContestEntry)
}

func decodeGetStationDetails(ctx context.Context, request []byte, data []byte) string {
	var decodedGetStationDetails pogo.GetStationedPokemonDetailsOutProto
	if err := proto.Unmarshal(data, &decodedGetStationDetails); err != nil {
		log.Errorf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
	}

	var decodedGetStationDetailsRequest pogo.GetStationedPokemonDetailsProto
	if request != nil {
		if err := proto.Unmarshal(request, &decodedGetStationDetailsRequest); err != nil {
			log.Errorf("Failed to parse GetStationedPokemonDetailsProto %s", err)
			return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsProto %s", err)
		}
	}

	if decodedGetStationDetails.Result == pogo.GetStationedPokemonDetailsOutProto_STATION_NOT_FOUND {
		// station without stationed pokemon found, therefore we need to reset the columns
		return decoder.ResetStationedPokemonWithStationDetailsNotFound(ctx, dbDetails, &decodedGetStationDetailsRequest)
	} else if decodedGetStationDetails.Result != pogo.GetStationedPokemonDetailsOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetStationedPokemonDetailsOutProto non-success status %s", decodedGetStationDetails.Result)
	}

	return decoder.UpdateStationWithStationDetails(ctx, dbDetails, &decodedGetStationDetailsRequest, &decodedGetStationDetails)
}

func decodeTappable(ctx context.Context, request, data []byte, username string, timestampMs int64) string {
	var tappable pogo.ProcessTappableOutProto
	if err := proto.Unmarshal(data, &tappable); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse ProcessTappableOutProto %s", err)
	}

	var tappableRequest pogo.ProcessTappableProto
	if request != nil {
		if err := proto.Unmarshal(request, &tappableRequest); err != nil {
			log.Errorf("Failed to parse %s", err)
			return fmt.Sprintf("Failed to parse ProcessTappableProto %s", err)
		}
	}

	if tappable.Status != pogo.ProcessTappableOutProto_SUCCESS {
		return fmt.Sprintf("Ignored ProcessTappableOutProto non-success status %s", tappable.Status)
	}
	var result string
	if encounter := tappable.GetEncounter(); encounter != nil {
		result = decoder.UpdatePokemonRecordWithTappableEncounter(ctx, dbDetails, &tappableRequest, encounter, username, timestampMs)
	}
	return result + " " + decoder.UpdateTappable(ctx, dbDetails, &tappableRequest, &tappable, timestampMs)
}

func decodeGetEventRsvp(ctx context.Context, request []byte, data []byte) string {
	var rsvp pogo.GetEventRsvpsOutProto
	if err := proto.Unmarshal(data, &rsvp); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpsOutProto %s", err)
	}

	var rsvpRequest pogo.GetEventRsvpsProto
	if request != nil {
		if err := proto.Unmarshal(request, &rsvpRequest); err != nil {
			log.Errorf("Failed to parse %s", err)
			return fmt.Sprintf("Failed to parse GetEventRsvpsProto %s", err)
		}
	}

	if rsvp.Status != pogo.GetEventRsvpsOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetEventRsvpsOutProto non-success status %s", rsvp.Status)
	}

	switch op := rsvpRequest.EventDetails.(type) {
	case *pogo.GetEventRsvpsProto_Raid:
		return decoder.UpdateGymRecordWithRsvpProto(ctx, dbDetails, op.Raid, &rsvp)
	case *pogo.GetEventRsvpsProto_GmaxBattle:
		return "Unsupported GmaxBattle Rsvp received"
	}

	return "Failed to parse GetEventRsvpsProto - unknown event type"
}

func decodeGetEventRsvpCount(ctx context.Context, data []byte) string {
	var rsvp pogo.GetEventRsvpCountOutProto
	if err := proto.Unmarshal(data, &rsvp); err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpCountOutProto %s", err)
	}

	if rsvp.Status != pogo.GetEventRsvpCountOutProto_SUCCESS {
		return fmt.Sprintf("Ignored GetEventRsvpCountOutProto non-success status %s", rsvp.Status)
	}

	var clearLocations []string
	for _, rsvpDetails := range rsvp.RsvpDetails {
		if rsvpDetails.MaybeCount == 0 && rsvpDetails.GoingCount == 0 {
			clearLocations = append(clearLocations, rsvpDetails.LocationId)
			decoder.ClearGymRsvp(ctx, dbDetails, rsvpDetails.LocationId)
		}
	}

	return "Cleared RSVP @ " + strings.Join(clearLocations, ", ")
}
