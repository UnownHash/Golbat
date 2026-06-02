package main

import (
	"context"

	"golbat/decoder"
	"golbat/pogo"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// decodePushGateway classifies a push-gateway message by message_type, unmarshals
// the PushGatewayMessage, and dispatches to the appropriate decoder update path.
// Unknown message types are gated before unmarshal to avoid unnecessary work.
func decodePushGateway(ctx context.Context, messageType string, payload []byte) {
	switch messageType {
	case "raid_lobby_player_count", "bread_lobby_player_count":
		// handled below
	default:
		return // gate before unmarshal
	}

	var msg pogo.PushGatewayMessage
	if err := proto.Unmarshal(payload, &msg); err != nil {
		log.Warnf("PushGateway: failed to parse %s: %v", messageType, err)
		return
	}
	switch m := msg.GetMessage().(type) {
	case *pogo.PushGatewayMessage_RaidLobbyPlayerCount:
		d := m.RaidLobbyPlayerCount
		log.Infof("PushGateway: received raid_lobby gym=%s players=%d joinEnd=%d", d.GetGymId(), d.GetPlayerCount(), d.GetLobbyJoinEndMs())
		decoder.UpdateGymRaidLobby(ctx, dbDetails, d.GetGymId(), d.GetPlayerCount(), d.GetLobbyJoinEndMs())
	case *pogo.PushGatewayMessage_BreadLobbyPlayerCount:
		d := m.BreadLobbyPlayerCount
		log.Infof("PushGateway: received bread_lobby station=%s players=%d joinEnd=%d", d.GetStationId(), d.GetPlayerCount(), d.GetBreadLobbyJoinEndMs())
		decoder.UpdateStationBattleLobby(ctx, dbDetails, d.GetStationId(), d.GetPlayerCount(), d.GetBreadLobbyJoinEndMs())
	}
}
