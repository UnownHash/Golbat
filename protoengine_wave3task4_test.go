package main

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"golbat/config"
	"golbat/pogo"
	"golbat/pogoshim"
)

// The tests in this file mirror TestDecodeWithArenaFortDetails's shape
// (protoengine_test.go, Task 1) for the engine handles Wave 3 Task 4 wires
// into live decode.go call sites: the five request+data pairs
// (contest_data, size_contest_entry, station_details, tappable,
// event_rsvps), the single-proto event_rsvp_count, and social's five
// per-root handles (proxyReqEngine/proxyRespEngine/friendDetailsEngine/
// searchPlayerOutEngine/searchPlayerReqEngine). Each proves decodeWithArena
// produces the same result via both std and hyperpb, and that a malformed
// payload surfaces an error without ever calling process.

func TestDecodeWithArenaContestDataPair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodContestData: engine}

			reqRaw, err := proto.Marshal(&pogo.GetContestDataProto{FortId: "FORT1"})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodContestData, contestDataReqEngine, reqRaw, pogoshim.AsGetContestDataProto,
				func(r pogoshim.GetContestDataProto) string { return r.GetFortId() })
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "FORT1" {
				t.Fatalf("got request %q want %q", gotReq, "FORT1")
			}

			dataRaw, err := proto.Marshal(&pogo.GetContestDataOutProto{
				Status:          pogo.GetContestDataOutProto_SUCCESS,
				ContestIncident: &pogo.ClientContestIncidentProto{Contests: []*pogo.ContestProto{{ContestId: "C1"}}},
			})
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodContestData, contestDataEngine, dataRaw, pogoshim.AsGetContestDataOutProto,
				func(d pogoshim.GetContestDataOutProto) string {
					return d.GetContestIncident().GetContests().At(0).GetContestId()
				})
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "C1" {
				t.Fatalf("got data %q want %q", gotData, "C1")
			}

			if _, err := decodeWithArena(engMethodContestData, contestDataReqEngine, malformedPayload, pogoshim.AsGetContestDataProto,
				func(pogoshim.GetContestDataProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodContestData, contestDataEngine, malformedPayload, pogoshim.AsGetContestDataOutProto,
				func(pogoshim.GetContestDataOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}

func TestDecodeWithArenaSizeContestEntryPair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodSizeContestEntry: engine}

			reqRaw, err := proto.Marshal(&pogo.GetPokemonSizeLeaderboardEntryProto{ContestId: "C1-1"})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodSizeContestEntry, sizeEntryReqEngine, reqRaw, pogoshim.AsGetPokemonSizeLeaderboardEntryProto,
				func(r pogoshim.GetPokemonSizeLeaderboardEntryProto) string { return r.GetContestId() })
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "C1-1" {
				t.Fatalf("got request %q want %q", gotReq, "C1-1")
			}

			dataRaw, err := proto.Marshal(&pogo.GetPokemonSizeLeaderboardEntryOutProto{
				Status:         pogo.GetPokemonSizeLeaderboardEntryOutProto_SUCCESS,
				ContestEntries: []*pogo.ContestEntryProto{{Rank: 1, PokedexId: pogo.HoloPokemonId_BULBASAUR}},
			})
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodSizeContestEntry, sizeEntryEngine, dataRaw, pogoshim.AsGetPokemonSizeLeaderboardEntryOutProto,
				func(d pogoshim.GetPokemonSizeLeaderboardEntryOutProto) string {
					if d.GetContestEntries().At(0).GetPokedexId() == pogo.HoloPokemonId_BULBASAUR {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "ok" {
				t.Fatalf("got data %q want ok", gotData)
			}

			if _, err := decodeWithArena(engMethodSizeContestEntry, sizeEntryReqEngine, malformedPayload, pogoshim.AsGetPokemonSizeLeaderboardEntryProto,
				func(pogoshim.GetPokemonSizeLeaderboardEntryProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodSizeContestEntry, sizeEntryEngine, malformedPayload, pogoshim.AsGetPokemonSizeLeaderboardEntryOutProto,
				func(pogoshim.GetPokemonSizeLeaderboardEntryOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}

func TestDecodeWithArenaStationDetailsPair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodStationDetails: engine}

			reqRaw, err := proto.Marshal(&pogo.GetStationedPokemonDetailsProto{StationId: "STATION1"})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodStationDetails, stationDetailsReqEngine, reqRaw, pogoshim.AsGetStationedPokemonDetailsProto,
				func(r pogoshim.GetStationedPokemonDetailsProto) string { return r.GetStationId() })
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "STATION1" {
				t.Fatalf("got request %q want %q", gotReq, "STATION1")
			}

			dataRaw, err := proto.Marshal(&pogo.GetStationedPokemonDetailsOutProto{
				Result:                   pogo.GetStationedPokemonDetailsOutProto_SUCCESS,
				TotalNumStationedPokemon: 3,
			})
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodStationDetails, stationDetailsEngine, dataRaw, pogoshim.AsGetStationedPokemonDetailsOutProto,
				func(d pogoshim.GetStationedPokemonDetailsOutProto) string {
					if d.GetTotalNumStationedPokemon() == 3 {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "ok" {
				t.Fatalf("got data %q want ok", gotData)
			}

			if _, err := decodeWithArena(engMethodStationDetails, stationDetailsReqEngine, malformedPayload, pogoshim.AsGetStationedPokemonDetailsProto,
				func(pogoshim.GetStationedPokemonDetailsProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodStationDetails, stationDetailsEngine, malformedPayload, pogoshim.AsGetStationedPokemonDetailsOutProto,
				func(pogoshim.GetStationedPokemonDetailsOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}

func TestDecodeWithArenaTappablePair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodTappable: engine}

			reqRaw, err := proto.Marshal(&pogo.ProcessTappableProto{EncounterId: 42})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodTappable, tappableReqEngine, reqRaw, pogoshim.AsProcessTappableProto,
				func(r pogoshim.ProcessTappableProto) string {
					if r.GetEncounterId() == 42 {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "ok" {
				t.Fatalf("got request %q want ok", gotReq)
			}

			dataRaw, err := proto.Marshal(&pogo.ProcessTappableOutProto{
				Status: pogo.ProcessTappableOutProto_SUCCESS,
				Reward: []*pogo.LootProto{{LootItem: []*pogo.LootItemProto{{
					Type:  &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_POKE_BALL},
					Count: 5,
				}}}},
			})
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodTappable, tappableEngine, dataRaw, pogoshim.AsProcessTappableOutProto,
				func(d pogoshim.ProcessTappableOutProto) string {
					item := d.GetReward().At(0).GetLootItem().At(0)
					if item.HasItem() && item.GetItem() == pogo.Item_ITEM_POKE_BALL && item.GetCount() == 5 {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "ok" {
				t.Fatalf("got data %q want ok", gotData)
			}

			if _, err := decodeWithArena(engMethodTappable, tappableReqEngine, malformedPayload, pogoshim.AsProcessTappableProto,
				func(pogoshim.ProcessTappableProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodTappable, tappableEngine, malformedPayload, pogoshim.AsProcessTappableOutProto,
				func(pogoshim.ProcessTappableOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}

func TestDecodeWithArenaEventRsvpsPair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodEventRsvps: engine}

			reqRaw, err := proto.Marshal(&pogo.GetEventRsvpsProto{
				EventDetails: &pogo.GetEventRsvpsProto_Raid{Raid: &pogo.RaidDetails{FortId: "FORT1"}},
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodEventRsvps, rsvpReqEngine, reqRaw, pogoshim.AsGetEventRsvpsProto,
				func(r pogoshim.GetEventRsvpsProto) string {
					if r.HasRaid() {
						return r.GetRaid().GetFortId()
					}
					return "no-raid"
				})
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "FORT1" {
				t.Fatalf("got request %q want %q", gotReq, "FORT1")
			}

			dataRaw, err := proto.Marshal(&pogo.GetEventRsvpsOutProto{
				Status:        pogo.GetEventRsvpsOutProto_SUCCESS,
				RsvpTimeslots: []*pogo.EventRsvpTimeslotProto{{TimeSlot: 111, GoingCount: 2}},
			})
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodEventRsvps, rsvpEngine, dataRaw, pogoshim.AsGetEventRsvpsOutProto,
				func(d pogoshim.GetEventRsvpsOutProto) string {
					ts := d.GetRsvpTimeslots().At(0)
					if ts.GetTimeSlot() == 111 && ts.GetGoingCount() == 2 {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "ok" {
				t.Fatalf("got data %q want ok", gotData)
			}

			if _, err := decodeWithArena(engMethodEventRsvps, rsvpReqEngine, malformedPayload, pogoshim.AsGetEventRsvpsProto,
				func(pogoshim.GetEventRsvpsProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodEventRsvps, rsvpEngine, malformedPayload, pogoshim.AsGetEventRsvpsOutProto,
				func(pogoshim.GetEventRsvpsOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}

func TestDecodeWithArenaEventRsvpCount(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodEventRsvpCount: engine}

			in := &pogo.GetEventRsvpCountOutProto{
				Status:      pogo.GetEventRsvpCountOutProto_SUCCESS,
				RsvpDetails: []*pogo.RsvpCountDetails{{LocationId: "LOC1"}},
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			got, err := decodeWithArena(engMethodEventRsvpCount, rsvpCountEngine, raw, pogoshim.AsGetEventRsvpCountOutProto,
				func(r pogoshim.GetEventRsvpCountOutProto) string { return r.GetRsvpDetails().At(0).GetLocationId() })
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "LOC1" {
				t.Fatalf("got %q want %q", got, "LOC1")
			}

			if _, err := decodeWithArena(engMethodEventRsvpCount, rsvpCountEngine, malformedPayload, pogoshim.AsGetEventRsvpCountOutProto,
				func(pogoshim.GetEventRsvpCountOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed payload")
			}
		})
	}
}

// TestDecodeWithArenaSocialHandles covers social's five per-root handles:
// the Request/Response pair plus the three inner-payload types
// (friendDetailsEngine, searchPlayerOutEngine, searchPlayerReqEngine).
func TestDecodeWithArenaSocialHandles(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodSocial: engine}

			reqRaw, err := proto.Marshal(&pogo.ProxyRequestProto{Action: uint32(pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER), Payload: []byte("payload-bytes")})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodSocial, proxyReqEngine, reqRaw, pogoshim.AsProxyRequestProto,
				func(r pogoshim.ProxyRequestProto) string {
					if pogo.InternalSocialAction(r.GetAction()) == pogo.InternalSocialAction_SOCIAL_ACTION_SEARCH_PLAYER {
						return "ok"
					}
					return "mismatch"
				})
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "ok" {
				t.Fatalf("got request %q want ok", gotReq)
			}

			respRaw, err := proto.Marshal(&pogo.ProxyResponseProto{Status: pogo.ProxyResponseProto_COMPLETED, Payload: []byte("inner")})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			gotResp, err := decodeWithArena(engMethodSocial, proxyRespEngine, respRaw, pogoshim.AsProxyResponseProto,
				func(r pogoshim.ProxyResponseProto) string { return string(r.GetPayload()) })
			if err != nil {
				t.Fatalf("unexpected response error: %v", err)
			}
			if gotResp != "inner" {
				t.Fatalf("got response %q want %q", gotResp, "inner")
			}

			friendRaw, err := proto.Marshal(&pogo.InternalGetFriendDetailsOutProto{
				Result: pogo.InternalGetFriendDetailsOutProto_SUCCESS,
				Friend: []*pogo.InternalFriendDetailsProto{{Player: &pogo.InternalPlayerSummaryProto{Codename: "Ash"}}},
			})
			if err != nil {
				t.Fatalf("marshal friend details: %v", err)
			}
			gotFriend, err := decodeWithArena(engMethodSocial, friendDetailsEngine, friendRaw, pogoshim.AsInternalGetFriendDetailsOutProto,
				func(d pogoshim.InternalGetFriendDetailsOutProto) string {
					var name string
					for f := range d.GetFriend().All() {
						name = f.GetPlayer().GetCodename()
					}
					return name
				})
			if err != nil {
				t.Fatalf("unexpected friend details error: %v", err)
			}
			if gotFriend != "Ash" {
				t.Fatalf("got friend %q want %q", gotFriend, "Ash")
			}

			searchOutRaw, err := proto.Marshal(&pogo.InternalSearchPlayerOutProto{
				Result: pogo.InternalSearchPlayerOutProto_SUCCESS,
				Player: &pogo.InternalPlayerSummaryProto{Codename: "Misty"},
			})
			if err != nil {
				t.Fatalf("marshal search player out: %v", err)
			}
			gotSearchOut, err := decodeWithArena(engMethodSocial, searchPlayerOutEngine, searchOutRaw, pogoshim.AsInternalSearchPlayerOutProto,
				func(d pogoshim.InternalSearchPlayerOutProto) string { return d.GetPlayer().GetCodename() })
			if err != nil {
				t.Fatalf("unexpected search player out error: %v", err)
			}
			if gotSearchOut != "Misty" {
				t.Fatalf("got search player out %q want %q", gotSearchOut, "Misty")
			}

			searchReqRaw, err := proto.Marshal(&pogo.InternalSearchPlayerProto{FriendCode: "1234"})
			if err != nil {
				t.Fatalf("marshal search player request: %v", err)
			}
			gotSearchReq, err := decodeWithArena(engMethodSocial, searchPlayerReqEngine, searchReqRaw, pogoshim.AsInternalSearchPlayerProto,
				func(r pogoshim.InternalSearchPlayerProto) string { return r.GetFriendCode() })
			if err != nil {
				t.Fatalf("unexpected search player request error: %v", err)
			}
			if gotSearchReq != "1234" {
				t.Fatalf("got search player request %q want %q", gotSearchReq, "1234")
			}

			for _, h := range []struct {
				name string
				eng  *protoEngineHandle
			}{
				{"proxyReqEngine", proxyReqEngine},
				{"proxyRespEngine", proxyRespEngine},
				{"friendDetailsEngine", friendDetailsEngine},
				{"searchPlayerOutEngine", searchPlayerOutEngine},
				{"searchPlayerReqEngine", searchPlayerReqEngine},
			} {
				if _, err := decodeWithArena(engMethodSocial, h.eng, malformedPayload, pogoshim.AsProxyRequestProto,
					func(pogoshim.ProxyRequestProto) string { return "" }); err == nil {
					t.Fatalf("%s: expected error for malformed payload", h.name)
				}
			}
		})
	}
}
