package main

import (
	"context"
	"time"

	"golbat/decoder"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// decodeNebula routes on the typed context (the proto `oneof context` case,
// mapped onto NebulaData) and then on the endpoint.
func decodeNebula(ctx context.Context, endpoint string, nd *NebulaData) string {
	start := time.Now()
	result := ""
	switch {
	case nd.Invasion != nil:
		switch endpoint {
		case "get-state":
			result = decodeNebulaInvasionState(ctx, nd.Invasion.FortId, nd.Invasion.IncidentId, nd.Data)
		case "get-time", "send-player-event":
			result = "ignored (not needed for lineup)"
		default:
			result = "unknown endpoint"
		}
		log.Debugf("Nebula invasion/%s %s - %s - %s", endpoint, nd.BattleId, time.Since(start), result)
	default:
		log.Warnf("Nebula: no recognised context (endpoint %s, battle %s)", endpoint, nd.BattleId)
		result = "no context"
	}
	return result
}

func decodeNebulaInvasionState(ctx context.Context, fortId, incidentId string, payload []byte) string {
	var out pogo.BattleStateOutProto
	if err := proto.Unmarshal(payload, &out); err != nil {
		return "failed to parse BattleStateOutProto"
	}
	return decoder.UpdateIncidentLineupFromBattleState(ctx, dbDetails, fortId, incidentId, &out)
}
