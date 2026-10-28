package main

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"golbat/pogo"
	"golbat/stats_collector"
)

// Without a request (or with encounter id 0) a tappable has no id, location or
// type; it must not be stored or sent as tappable 0 at (0,0).
func TestDecodeTappableRequiresRequest(t *testing.T) {
	statsCollector = stats_collector.NewNoopStatsCollector()
	out, err := proto.Marshal(&pogo.ProcessTappableOutProto{
		Status: pogo.ProcessTappableOutProto_SUCCESS,
		Reward: []*pogo.LootProto{{LootItem: []*pogo.LootItemProto{{Type: &pogo.LootItemProto_Item{Item: pogo.Item_ITEM_RARE_CANDY}, Count: 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	noId, err := proto.Marshal(&pogo.ProcessTappableProto{TappableTypeId: "TAPPABLE_TYPE_BREAKFAST"})
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string][]byte{"nil request": nil, "encounter id 0": noId} {
		if res := decodeTappable(context.Background(), req, out, "tester", 1700000000000); !strings.Contains(res, "without request") {
			t.Errorf("%s: decodeTappable = %q, want ignored-without-request message", name, res)
		}
	}
}
