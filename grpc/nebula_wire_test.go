package grpc

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestNebulaContentWireRoundTrip(t *testing.T) {
	in := &RawProtoRequest{
		Username: "acc",
		NebulaContents: []*NebulaContent{{
			Endpoint:        "get-state",
			ResponsePayload: []byte{9, 8, 7},
			RequestPayload:  []byte{1},
			Context:         &NebulaContent_Invasion{Invasion: &InvasionContext{FortId: "F", IncidentId: "-9"}},
			BattleId:        "B1",
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
	if len(out.NebulaContents) != 1 || out.NebulaContents[0].Endpoint != "get-state" {
		t.Fatalf("round trip mismatch: %+v", out.NebulaContents)
	}
	inv := out.NebulaContents[0].GetInvasion()
	if inv == nil || inv.FortId != "F" || inv.IncidentId != "-9" {
		t.Fatalf("invasion context round trip mismatch: %+v", out.NebulaContents[0].GetContext())
	}
}
