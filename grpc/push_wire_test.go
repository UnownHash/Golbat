package grpc

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestPushGatewayContentWireRoundTrip(t *testing.T) {
	in := &RawProtoRequest{
		Username: "acc",
		PushContents: []*PushGatewayContent{{
			MessageType: "raid_lobby_player_count",
			Payload:     []byte{1, 2, 3},
		}},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out RawProtoRequest
	if err := proto.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.PushContents) != 1 {
		t.Fatalf("round trip: expected 1 push content, got %d", len(out.PushContents))
	}
	pc := out.PushContents[0]
	if pc.MessageType != "raid_lobby_player_count" {
		t.Errorf("message_type round trip mismatch: %q", pc.MessageType)
	}
	if string(pc.Payload) != string([]byte{1, 2, 3}) {
		t.Errorf("payload round trip mismatch: %v", pc.Payload)
	}
}
