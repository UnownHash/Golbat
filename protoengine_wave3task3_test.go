package main

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"golbat/config"
	"golbat/pogo"
	"golbat/pogoshim"
)

// The tests in this file mirror TestDecodeWithArenaFortDetails's shape
// (protoengine_test.go, Task 1) for the five engine handles Wave 3 Task 3
// wires into live decode.go/decode_nebula.go call sites: mapFortsEngine,
// routesEngine, startIncidentEngine, battleStateEngine, and the
// openInvasionReqEngine/openInvasionEngine pair. Each proves decodeWithArena
// produces the same result via both std and hyperpb, and that a malformed
// payload surfaces an error without ever calling process -- independent of
// any DB-touching decoder.* entry point (this package has no DB test
// harness; see the Wave 3 Task 2 report's precedent for testing at this
// layer instead).

func TestDecodeWithArenaGetMapForts(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodGetMapForts: engine}

			in := &pogo.GetMapFortsOutProto{
				Status: pogo.GetMapFortsOutProto_SUCCESS,
				Fort:   []*pogo.GetMapFortsOutProto_FortProto{{Id: "FORT1", Name: "Test"}},
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var gotId string
			_, err = decodeWithArena(engMethodGetMapForts, mapFortsEngine, raw, pogoshim.AsGetMapFortsOutProto,
				func(g pogoshim.GetMapFortsOutProto) string {
					for f := range g.GetFort().All() {
						gotId = f.GetId()
					}
					return "ok"
				})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotId != "FORT1" {
				t.Fatalf("got fort id %q, want %q", gotId, "FORT1")
			}

			if _, err := decodeWithArena(engMethodGetMapForts, mapFortsEngine, malformedPayload, pogoshim.AsGetMapFortsOutProto,
				func(pogoshim.GetMapFortsOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed payload")
			}
		})
	}
}

func TestDecodeWithArenaRoutes(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodRoutes: engine}

			in := &pogo.GetRoutesOutProto{
				Status: pogo.GetRoutesOutProto_SUCCESS,
				RouteMapCell: []*pogo.ClientRouteMapCellProto{
					{Route: []*pogo.SharedRouteProto{{Id: "ROUTE1"}}},
				},
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var gotId string
			_, err = decodeWithArena(engMethodRoutes, routesEngine, raw, pogoshim.AsGetRoutesOutProto,
				func(g pogoshim.GetRoutesOutProto) string {
					for cell := range g.GetRouteMapCell().All() {
						for route := range cell.GetRoute().All() {
							gotId = route.GetId()
						}
					}
					return "ok"
				})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotId != "ROUTE1" {
				t.Fatalf("got route id %q, want %q", gotId, "ROUTE1")
			}

			if _, err := decodeWithArena(engMethodRoutes, routesEngine, malformedPayload, pogoshim.AsGetRoutesOutProto,
				func(pogoshim.GetRoutesOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed payload")
			}
		})
	}
}

func TestDecodeWithArenaStartIncident(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodStartIncident: engine}

			in := &pogo.StartIncidentOutProto{
				Status:   pogo.StartIncidentOutProto_SUCCESS,
				Incident: &pogo.ClientIncidentProto{IncidentId: "INC1", FortId: "FORT1"},
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			got, err := decodeWithArena(engMethodStartIncident, startIncidentEngine, raw, pogoshim.AsStartIncidentOutProto,
				func(s pogoshim.StartIncidentOutProto) string { return s.GetIncident().GetIncidentId() })
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "INC1" {
				t.Fatalf("got %q want %q", got, "INC1")
			}

			if _, err := decodeWithArena(engMethodStartIncident, startIncidentEngine, malformedPayload, pogoshim.AsStartIncidentOutProto,
				func(pogoshim.StartIncidentOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed payload")
			}
		})
	}
}

func TestDecodeWithArenaBattleState(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodNebulaBattleState: engine}

			in := &pogo.BattleStateOutProto{
				BattleState: &pogo.BattleStateProto{
					Actors: map[string]*pogo.BattleActorProto{
						"npc": {Id: "npc", Type: pogo.BattleActorProto_NPC, ActivePokemonId: 7},
					},
				},
			}
			raw, err := proto.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var gotActive uint64
			_, err = decodeWithArena(engMethodNebulaBattleState, battleStateEngine, raw, pogoshim.AsBattleStateOutProto,
				func(b pogoshim.BattleStateOutProto) string {
					for a := range b.GetBattleState().GetActors().All() {
						gotActive = a.GetActivePokemonId()
					}
					return "ok"
				})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotActive != 7 {
				t.Fatalf("got active pokemon id %d, want 7", gotActive)
			}

			if _, err := decodeWithArena(engMethodNebulaBattleState, battleStateEngine, malformedPayload, pogoshim.AsBattleStateOutProto,
				func(pogoshim.BattleStateOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed payload")
			}
		})
	}
}

// TestDecodeWithArenaOpenInvasionPair covers the request+data pair
// (openInvasionReqEngine, openInvasionEngine) that decodeOpenInvasion nests,
// proving each handle independently decodes correctly via both engines.
func TestDecodeWithArenaOpenInvasionPair(t *testing.T) {
	saved := config.Config.ProtoEngine.Overrides
	defer func() { config.Config.ProtoEngine.Overrides = saved }()

	for _, engine := range []string{"std", "hyperpb"} {
		t.Run(engine, func(t *testing.T) {
			config.Config.ProtoEngine.Overrides = map[string]string{engMethodOpenInvasion: engine}

			reqIn := &pogo.OpenInvasionCombatSessionProto{
				IncidentLookup: &pogo.IncidentLookupProto{IncidentId: "INC1", FortId: "FORT1"},
			}
			reqRaw, err := proto.Marshal(reqIn)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			gotReq, err := decodeWithArena(engMethodOpenInvasion, openInvasionReqEngine, reqRaw, pogoshim.AsOpenInvasionCombatSessionProto,
				func(r pogoshim.OpenInvasionCombatSessionProto) string { return r.GetIncidentLookup().GetIncidentId() })
			if err != nil {
				t.Fatalf("unexpected request error: %v", err)
			}
			if gotReq != "INC1" {
				t.Fatalf("got request %q want %q", gotReq, "INC1")
			}

			dataIn := &pogo.OpenInvasionCombatSessionOutProto{Status: pogo.InvasionStatus_SUCCESS, Combat: &pogo.CombatProto{CombatId: "C1"}}
			dataRaw, err := proto.Marshal(dataIn)
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			gotData, err := decodeWithArena(engMethodOpenInvasion, openInvasionEngine, dataRaw, pogoshim.AsOpenInvasionCombatSessionOutProto,
				func(d pogoshim.OpenInvasionCombatSessionOutProto) string { return d.GetCombat().GetCombatId() })
			if err != nil {
				t.Fatalf("unexpected data error: %v", err)
			}
			if gotData != "C1" {
				t.Fatalf("got data %q want %q", gotData, "C1")
			}

			if _, err := decodeWithArena(engMethodOpenInvasion, openInvasionReqEngine, malformedPayload, pogoshim.AsOpenInvasionCombatSessionProto,
				func(pogoshim.OpenInvasionCombatSessionProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed request payload")
			}
			if _, err := decodeWithArena(engMethodOpenInvasion, openInvasionEngine, malformedPayload, pogoshim.AsOpenInvasionCombatSessionOutProto,
				func(pogoshim.OpenInvasionCombatSessionOutProto) string { return "" }); err == nil {
				t.Fatal("expected error for malformed data payload")
			}
		})
	}
}
