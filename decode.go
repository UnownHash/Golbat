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

// decodeSocialActionWithRequest is "social"'s three-level nesting: Request
// (ProxyRequestProto) decodes via proxyReqEngine; INSIDE its closure, Data
// (ProxyResponseProto) decodes via proxyRespEngine -- both shims (and both
// arenas, under hyperpb) alive together for the status/action-type checks
// below. The response's own Payload bytes are opaque until the request's
// action type is known, so a THIRD decodeWithArena level -- one of
// friendDetailsEngine/searchPlayerOutEngine, chosen by that action type --
// happens one call further down in decodeGetFriendDetails/decodeSearchPlayer,
// all under the same "social" config method key (per-root handles, per Task
// 1's foundation commit). Only the Request+Data pair is shadow-verified
// (maybeShadowPair below); see shadowComparePair's engMethodSocial case for
// why the inner payload types are excluded.
func decodeSocialActionWithRequest(request []byte, payload []byte) string {
	maybeShadowPair(engMethodSocial, request, payload)
	res, err := decodeWithArena(engMethodSocial, proxyReqEngine, request,
		pogoshim.AsProxyRequestProto,
		func(proxyRequestProto pogoshim.ProxyRequestProto) string {
			innerRes, innerErr := decodeWithArena(engMethodSocial, proxyRespEngine, payload,
				pogoshim.AsProxyResponseProto,
				func(proxyResponseProto pogoshim.ProxyResponseProto) string {
					if proxyResponseProto.GetStatus() != pogo.ProxyResponseProto_COMPLETED && proxyResponseProto.GetStatus() != pogo.ProxyResponseProto_COMPLETED_AND_REASSIGNED {
						statsCollector.IncDecodeSocialActionWithRequest("error", "non_success")
						return fmt.Sprintf("unsuccessful proxyResponseProto response %d %s", int(proxyResponseProto.GetStatus()), proxyResponseProto.GetStatus())
					}

					switch pogo.InternalSocialAction(proxyRequestProto.GetAction()) {
					case pogo.InternalSocialAction_SOCIAL_ACTION_LIST_FRIEND_STATUS:
						statsCollector.IncDecodeSocialActionWithRequest("ok", "list_friend_status")
						return decodeGetFriendDetails(proxyResponseProto.GetPayload())
					case pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER:
						statsCollector.IncDecodeSocialActionWithRequest("ok", "search_player")
						return decodeSearchPlayer(proxyRequestProto, proxyResponseProto.GetPayload())
					}

					statsCollector.IncDecodeSocialActionWithRequest("ok", "unknown")
					return fmt.Sprintf("Did not process %s", pogo.InternalSocialAction(proxyRequestProto.GetAction()).String())
				})
			if innerErr != nil {
				log.Errorf("Failed to parse %s", innerErr)
				statsCollector.IncDecodeSocialActionWithRequest("error", "response_parse")
				return fmt.Sprintf("Failed to parse %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeSocialActionWithRequest("error", "request_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

// decodeGetFriendDetails is social's third level for
// SOCIAL_ACTION_LIST_FRIEND_STATUS: the response payload bytes decode as
// InternalGetFriendDetailsOutProto via friendDetailsEngine. Not
// shadow-verified on its own (see decodeSocialActionWithRequest's doc
// comment).
func decodeGetFriendDetails(payload []byte) string {
	res, err := decodeWithArena(engMethodSocial, friendDetailsEngine, payload,
		pogoshim.AsInternalGetFriendDetailsOutProto,
		func(getFriendDetailsOutProto pogoshim.InternalGetFriendDetailsOutProto) string {
			friends := getFriendDetailsOutProto.GetFriend()
			if getFriendDetailsOutProto.GetResult() != pogo.InternalGetFriendDetailsOutProto_SUCCESS || friends.Len() == 0 {
				statsCollector.IncDecodeGetFriendDetails("error", "non_success")
				return "unsuccessful get friends details"
			}

			failures := 0
			total := friends.Len()

			for friend := range friends.All() {
				player := friend.GetPlayer()

				// player.GetPublicData() gracefully degrades to a zero shim
				// if PublicData is absent -- the pre-shim code's direct
				// player.PublicData field access would nil-panic here if
				// player were a nil *pogo.InternalPlayerSummaryProto (never
				// observed on real SUCCESS payloads, same latent-panic-removal
				// class as every prior wave's shim conversions).
				updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.GetPublicData(), "", player.GetPlayerId())
				if updatePlayerError != nil {
					failures++
				}
			}

			statsCollector.IncDecodeGetFriendDetails("ok", "")
			return fmt.Sprintf("%d players decoded on %d", total-failures, total)
		})
	if err != nil {
		statsCollector.IncDecodeGetFriendDetails("error", "parse")
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
}

// decodeSearchPlayer is social's third level for SOCIAL_ACTION_SEARCH_PLAYER:
// the response payload decodes as InternalSearchPlayerOutProto via
// searchPlayerOutEngine, and -- nested inside that, a fourth arena, since the
// friend code lives in the ORIGINAL request's own payload bytes, not
// anywhere reachable from the response -- proxyRequestProto's Payload
// re-decodes as InternalSearchPlayerProto via searchPlayerReqEngine. Neither
// is shadow-verified on its own (see decodeSocialActionWithRequest's doc
// comment).
func decodeSearchPlayer(proxyRequestProto pogoshim.ProxyRequestProto, payload []byte) string {
	res, err := decodeWithArena(engMethodSocial, searchPlayerOutEngine, payload,
		pogoshim.AsInternalSearchPlayerOutProto,
		func(searchPlayerOutProto pogoshim.InternalSearchPlayerOutProto) string {
			if searchPlayerOutProto.GetResult() != pogo.InternalSearchPlayerOutProto_SUCCESS || !searchPlayerOutProto.HasPlayer() {
				statsCollector.IncDecodeSearchPlayer("error", "non_success")
				return "unsuccessful search player response"
			}

			// proxyRequestProto.GetPayload() is already a bytes.Clone'd copy
			// (pogoshim's BytesKind getter convention), so it's safe to reuse
			// even though it was originally read from the OUTER
			// decodeSocialActionWithRequest arena.
			innerRes, innerErr := decodeWithArena(engMethodSocial, searchPlayerReqEngine, proxyRequestProto.GetPayload(),
				pogoshim.AsInternalSearchPlayerProto,
				func(searchPlayerProto pogoshim.InternalSearchPlayerProto) string {
					if searchPlayerProto.GetFriendCode() == "" {
						statsCollector.IncDecodeSearchPlayer("error", "parse")
						// Replicates the pre-shim code's
						// fmt.Sprintf("Failed to parse %s", searchPlayerError)
						// in the branch where unmarshal succeeded but
						// FriendCode was empty -- searchPlayerError is nil
						// there, and Go's fmt renders a nil error via %s as
						// "%!s(<nil>)".
						var nilErr error
						return fmt.Sprintf("Failed to parse %s", nilErr)
					}

					player := searchPlayerOutProto.GetPlayer()
					updatePlayerError := decoder.UpdatePlayerRecordWithPlayerSummary(dbDetails, player, player.GetPublicData(), searchPlayerProto.GetFriendCode(), "")
					if updatePlayerError != nil {
						statsCollector.IncDecodeSearchPlayer("error", "update")
						return fmt.Sprintf("Failed update player %s", updatePlayerError)
					}

					statsCollector.IncDecodeSearchPlayer("ok", "")
					return "1 player decoded from SearchPlayerProto"
				})
			if innerErr != nil {
				statsCollector.IncDecodeSearchPlayer("error", "parse")
				return fmt.Sprintf("Failed to parse %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeSearchPlayer("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}
	return res
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

// decodeGetContestData is the first of Task 4's five request-optional
// request+data methods (contest_data, size_contest_entry, station_details,
// tappable, event_rsvps). Their shape is the mirror image of
// decodeOpenInvasion's mandatory-request template (see that function's doc
// comment): Data is the OUTER decode (always present on the wire) and
// Request is the INNER, OPTIONAL decode -- request can legitimately be nil,
// in which case a zero-value request shim stands in (IsZero()==true, every
// Get* chains to its zero default), matching the pre-shim code's
// always-non-nil-but-possibly-unpopulated *pogo.XxxProto pointer exactly.
// maybeShadowPair runs unconditionally (even with a nil request): decoding
// zero-length bytes is well-defined and identical across engines, so the
// composite digest still meaningfully verifies the Data half every time.
func decodeGetContestData(ctx context.Context, request []byte, data []byte) string {
	maybeShadowPair(engMethodContestData, request, data)
	res, err := decodeWithArena(engMethodContestData, contestDataEngine, data,
		pogoshim.AsGetContestDataOutProto,
		func(decodedContestData pogoshim.GetContestDataOutProto) string {
			if request == nil {
				return decoder.UpdatePokestopWithContestData(ctx, dbDetails, pogoshim.GetContestDataProto{}, decodedContestData)
			}
			innerRes, innerErr := decodeWithArena(engMethodContestData, contestDataReqEngine, request,
				pogoshim.AsGetContestDataProto,
				func(decodedRequest pogoshim.GetContestDataProto) string {
					return decoder.UpdatePokestopWithContestData(ctx, dbDetails, decodedRequest, decodedContestData)
				})
			if innerErr != nil {
				log.Errorf("Failed to parse GetContestDataProto %s", innerErr)
				return fmt.Sprintf("Failed to parse GetContestDataProto %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse GetContestDataOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetContestDataOutProto %s", err)
	}
	return res
}

// decodeGetPokemonSizeContestEntry follows decodeGetContestData's
// request-optional shape (see its doc comment). Note the error text below
// says "...OutProto..." for BOTH the outer AND inner parse failures -- that
// mismatched label is inherited byte-for-byte from the pre-shim code, which
// used the same (technically wrong, since the inner failure is actually the
// non-Out request proto) message for both.
func decodeGetPokemonSizeContestEntry(ctx context.Context, request []byte, data []byte) string {
	maybeShadowPair(engMethodSizeContestEntry, request, data)
	res, err := decodeWithArena(engMethodSizeContestEntry, sizeEntryEngine, data,
		pogoshim.AsGetPokemonSizeLeaderboardEntryOutProto,
		func(decodedEntry pogoshim.GetPokemonSizeLeaderboardEntryOutProto) string {
			if decodedEntry.GetStatus() != pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS {
				return fmt.Sprintf("Ignored GetPokemonSizeLeaderboardEntryOutProto non-success status %s", decodedEntry.GetStatus())
			}

			if request == nil {
				return decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dbDetails, pogoshim.GetPokemonSizeLeaderboardEntryProto{}, decodedEntry)
			}
			innerRes, innerErr := decodeWithArena(engMethodSizeContestEntry, sizeEntryReqEngine, request,
				pogoshim.AsGetPokemonSizeLeaderboardEntryProto,
				func(decodedRequest pogoshim.GetPokemonSizeLeaderboardEntryProto) string {
					return decoder.UpdatePokestopWithPokemonSizeContestEntry(ctx, dbDetails, decodedRequest, decodedEntry)
				})
			if innerErr != nil {
				log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", innerErr)
				return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetPokemonSizeLeaderboardEntryOutProto %s", err)
	}
	return res
}

// decodeGetStationDetails follows decodeGetContestData's request-optional
// shape (see its doc comment).
func decodeGetStationDetails(ctx context.Context, request []byte, data []byte) string {
	maybeShadowPair(engMethodStationDetails, request, data)
	res, err := decodeWithArena(engMethodStationDetails, stationDetailsEngine, data,
		pogoshim.AsGetStationedPokemonDetailsOutProto,
		func(decodedDetails pogoshim.GetStationedPokemonDetailsOutProto) string {
			process := func(decodedRequest pogoshim.GetStationedPokemonDetailsProto) string {
				if decodedDetails.GetResult() == pogo.GetStationedPokemonDetailsOutProto_STATION_NOT_FOUND {
					// station without stationed pokemon found, therefore we need to reset the columns
					return decoder.ResetStationedPokemonWithStationDetailsNotFound(ctx, dbDetails, decodedRequest)
				} else if decodedDetails.GetResult() != pogo.GetStationedPokemonDetailsOutProto_SUCCESS {
					return fmt.Sprintf("Ignored GetStationedPokemonDetailsOutProto non-success status %s", decodedDetails.GetResult())
				}
				return decoder.UpdateStationWithStationDetails(ctx, dbDetails, decodedRequest, decodedDetails)
			}
			if request == nil {
				return process(pogoshim.GetStationedPokemonDetailsProto{})
			}
			innerRes, innerErr := decodeWithArena(engMethodStationDetails, stationDetailsReqEngine, request,
				pogoshim.AsGetStationedPokemonDetailsProto, process)
			if innerErr != nil {
				log.Errorf("Failed to parse GetStationedPokemonDetailsProto %s", innerErr)
				return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsProto %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
		return fmt.Sprintf("Failed to parse GetStationedPokemonDetailsOutProto %s", err)
	}
	return res
}

// decodeTappable follows decodeGetContestData's request-optional shape (see
// its doc comment). Note the leading space in "result + \" \" + ...": when
// there's no encounter, result stays "" and the returned string still starts
// with a literal space -- preserved byte-for-byte from the pre-shim code.
func decodeTappable(ctx context.Context, request, data []byte, username string, timestampMs int64) string {
	maybeShadowPair(engMethodTappable, request, data)
	res, err := decodeWithArena(engMethodTappable, tappableEngine, data,
		pogoshim.AsProcessTappableOutProto,
		func(tappable pogoshim.ProcessTappableOutProto) string {
			process := func(tappableRequest pogoshim.ProcessTappableProto) string {
				if tappable.GetStatus() != pogo.ProcessTappableOutProto_SUCCESS {
					return fmt.Sprintf("Ignored ProcessTappableOutProto non-success status %s", tappable.GetStatus())
				}
				var result string
				if tappable.HasEncounter() {
					result = decoder.UpdatePokemonRecordWithTappableEncounter(ctx, dbDetails, tappableRequest, tappable.GetEncounter(), username, timestampMs)
				}
				return result + " " + decoder.UpdateTappable(ctx, dbDetails, tappableRequest, tappable, timestampMs)
			}
			if request == nil {
				return process(pogoshim.ProcessTappableProto{})
			}
			innerRes, innerErr := decodeWithArena(engMethodTappable, tappableReqEngine, request,
				pogoshim.AsProcessTappableProto, process)
			if innerErr != nil {
				log.Errorf("Failed to parse %s", innerErr)
				return fmt.Sprintf("Failed to parse ProcessTappableProto %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse ProcessTappableOutProto %s", err)
	}
	return res
}

// decodeGetEventRsvp follows decodeGetContestData's request-optional shape
// (see its doc comment). The RSVP oneof (EventDetails: Raid vs GmaxBattle)
// uses the generated Has/Get accessors directly -- no manual oneof-type
// handling needed, matching Wave 2 Task 5's precedent for message-typed
// oneof members.
func decodeGetEventRsvp(ctx context.Context, request []byte, data []byte) string {
	maybeShadowPair(engMethodEventRsvps, request, data)
	res, err := decodeWithArena(engMethodEventRsvps, rsvpEngine, data,
		pogoshim.AsGetEventRsvpsOutProto,
		func(rsvp pogoshim.GetEventRsvpsOutProto) string {
			process := func(rsvpRequest pogoshim.GetEventRsvpsProto) string {
				if rsvp.GetStatus() != pogo.GetEventRsvpsOutProto_SUCCESS {
					return fmt.Sprintf("Ignored GetEventRsvpsOutProto non-success status %s", rsvp.GetStatus())
				}

				switch {
				case rsvpRequest.HasRaid():
					return decoder.UpdateGymRecordWithRsvpProto(ctx, dbDetails, rsvpRequest.GetRaid(), rsvp)
				case rsvpRequest.HasGmaxBattle():
					return "Unsupported GmaxBattle Rsvp received"
				}

				return "Failed to parse GetEventRsvpsProto - unknown event type"
			}
			if request == nil {
				return process(pogoshim.GetEventRsvpsProto{})
			}
			innerRes, innerErr := decodeWithArena(engMethodEventRsvps, rsvpReqEngine, request,
				pogoshim.AsGetEventRsvpsProto, process)
			if innerErr != nil {
				log.Errorf("Failed to parse %s", innerErr)
				return fmt.Sprintf("Failed to parse GetEventRsvpsProto %s", innerErr)
			}
			return innerRes
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpsOutProto %s", err)
	}
	return res
}

// decodeGetEventRsvpCount reads only LocationId/MaybeCount/GoingCount -- no
// request proto at all -- but is migrated for uniformity with the rest of
// this wave. genericShadowEngine already wires engMethodEventRsvpCount to
// rsvpCountEngine (Task 1's foundation commit).
func decodeGetEventRsvpCount(ctx context.Context, data []byte) string {
	maybeShadow(engMethodEventRsvpCount, data)
	res, err := decodeWithArena(engMethodEventRsvpCount, rsvpCountEngine, data,
		pogoshim.AsGetEventRsvpCountOutProto,
		func(rsvp pogoshim.GetEventRsvpCountOutProto) string {
			if rsvp.GetStatus() != pogo.GetEventRsvpCountOutProto_SUCCESS {
				return fmt.Sprintf("Ignored GetEventRsvpCountOutProto non-success status %s", rsvp.GetStatus())
			}

			var clearLocations []string
			for rsvpDetails := range rsvp.GetRsvpDetails().All() {
				if rsvpDetails.GetMaybeCount() == 0 && rsvpDetails.GetGoingCount() == 0 {
					clearLocations = append(clearLocations, rsvpDetails.GetLocationId())
					decoder.ClearGymRsvp(ctx, dbDetails, rsvpDetails.GetLocationId())
				}
			}

			return "Cleared RSVP @ " + strings.Join(clearLocations, ", ")
		})
	if err != nil {
		log.Errorf("Failed to parse %s", err)
		return fmt.Sprintf("Failed to parse GetEventRsvpCountOutProto %s", err)
	}
	return res
}
