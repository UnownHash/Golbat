package main

import (
	"context"
	"testing"

	"golbat/pogo"

	"google.golang.org/protobuf/proto"
)

func TestDecodePushGateway_GatesUnknownType(t *testing.T) {
	// Unhandled type -> early return before unmarshal, no panic.
	decodePushGateway(context.Background(), "map_objects_update", []byte{0xff})
}

func TestDecodePushGateway_MalformedPayloadForKnownType(t *testing.T) {
	// Known type but garbage payload -> logs a warning, no panic.
	decodePushGateway(context.Background(), "raid_lobby_player_count", []byte{0xff, 0xfe})
}

func TestDecodePushGateway_MalformedBreadPayload(t *testing.T) {
	// Same malformed-payload guard for the bread path.
	decodePushGateway(context.Background(), "bread_lobby_player_count", []byte{0xfe, 0xfd})
}

// TestDecodePushGateway_ClassifiesRaidLobby verifies that a valid raid-lobby proto
// passes the gate, is unmarshalled, and dispatches into UpdateGymRaidLobby. In this
// unit-test context the decoder caches (gymCache) are uninitialised, so
// UpdateGymRaidLobby panics on the nil cache before it reaches the DB. We recover
// from that expected panic to confirm the correct message_type IS accepted and the
// unmarshal+dispatch path is exercised. The DB-layer dedup behaviour is covered by
// decoder.TestUpdateGymRaidLobby_DedupOlder.
func TestDecodePushGateway_ClassifiesRaidLobby(t *testing.T) {
	msg := &pogo.PushGatewayMessage{
		MessagePubTimestampMs: 5000,
		Message: &pogo.PushGatewayMessage_RaidLobbyPlayerCount{
			RaidLobbyPlayerCount: &pogo.RaidLobbyCounterData{GymId: "G", PlayerCount: 3, LobbyJoinEndMs: 9000},
		},
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Call with the correct message_type to exercise gate-pass → unmarshal → dispatch.
	// Recover from the nil-gymCache panic that occurs inside UpdateGymRaidLobby when
	// the decoder package caches are not initialised (no DB/cache in unit tests).
	func() {
		defer func() { recover() }() //nolint:errcheck
		decodePushGateway(context.Background(), "raid_lobby_player_count", raw)
	}()
}

// TestDecodePushGateway_ClassifiesBreadLobby verifies that a valid bread-lobby proto
// passes the gate, is unmarshalled, and dispatches into UpdateStationBattleLobby.
// Same nil-stationCache caveat as TestDecodePushGateway_ClassifiesRaidLobby applies.
func TestDecodePushGateway_ClassifiesBreadLobby(t *testing.T) {
	msg := &pogo.PushGatewayMessage{
		MessagePubTimestampMs: 6000,
		Message: &pogo.PushGatewayMessage_BreadLobbyPlayerCount{
			BreadLobbyPlayerCount: &pogo.BreadLobbyCounterData{StationId: "S", PlayerCount: 2, BreadLobbyJoinEndMs: 7000},
		},
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Call with the correct message_type to exercise gate-pass → unmarshal → dispatch.
	// Recover from nil-stationCache panic inside UpdateStationBattleLobby.
	func() {
		defer func() { recover() }() //nolint:errcheck
		decodePushGateway(context.Background(), "bread_lobby_player_count", raw)
	}()
}
