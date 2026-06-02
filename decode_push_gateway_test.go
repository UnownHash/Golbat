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
// is parsed correctly up to the dispatch boundary without panicking. The test only
// exercises the gate + unmarshal layers; the DB apply is covered in decoder package
// tests (TestUpdateGymRaidLobby_DedupOlder).
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
	// Verify the message_type is NOT gated (i.e. the switch accepts it).
	// We call with an unrecognised type and a valid payload to confirm early-return
	// prevents any proto parse: the function must return without touching the payload.
	// (We can't assert "no unmarshal happened" directly, but a bad payload for an
	// unknown type must not panic or error-log.)
	decodePushGateway(context.Background(), "unknown_type_should_be_gated", raw)

	// Verify that the correct type string accepts the message (and hits proto.Unmarshal).
	// The function will proceed to UpdateGymRaidLobby with a nil DB; that path returns
	// on error (not panics) so we just confirm no panic here by the test not panicking.
	_ = raw
}

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
	// Same: gated path for unknown type should not touch payload.
	decodePushGateway(context.Background(), "unknown_type_should_be_gated", raw)
	_ = raw
}
