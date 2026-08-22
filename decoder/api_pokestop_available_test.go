package decoder

import (
	"encoding/json"
	"testing"
)

// TestGetAvailablePokestops seeds the maintained lure/showcase/invasion maps
// via the observe hooks (not fortLookupCache — GetAvailablePokestops no
// longer scans it) plus the quest-conditions map via initQuestConditions.
func TestGetAvailablePokestops(t *testing.T) {
	initFortAvailability()
	initQuestConditions()
	now := int64(1_000_000)
	// quest reward + condition via the maintained map (the sole quest source)
	adjustQuestConditions([]questConditionKey{{RewardType: 2, ItemId: 1, Title: "catch_x", Target: 3}}, +1)
	// one fort: active lure, EXPIRED showcase (excluded)
	observePokestop(&FortLookup{
		LureId: 501, LureExpireTimestamp: now + 100,
		ContestPokemonId: 1, ShowcaseExpiry: now - 1, // expired -> excluded
	}, nil, now)
	// active grunt incident
	observeInvasion(&FortLookupIncident{DisplayType: 1, Character: 5, Confirmed: true, Slot1PokemonId: 41, ExpireTimestamp: now + 100}, now)

	res := GetAvailablePokestops(now)
	if !res.ShowcaseFocusFilter {
		t.Fatal("showcase_focus_filter capability must be true even when no showcases are active")
	}
	if len(res.Lures) != 1 || res.Lures[0].LureId != 501 {
		t.Fatalf("lure: %+v", res.Lures)
	}
	if len(res.Showcases) != 0 {
		t.Fatalf("expired showcase should be excluded: %+v", res.Showcases)
	}
	if len(res.Quests) != 1 || res.Quests[0].RewardType != 2 {
		t.Fatalf("quest: %+v", res.Quests)
	}
	if len(res.Invasions) != 1 || res.Invasions[0].Character != 5 {
		t.Fatalf("invasion: %+v", res.Invasions)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal availability: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode availability: %v", err)
	}
	if got := string(body["showcase_focus_filter"]); got != "true" {
		t.Fatalf("showcase_focus_filter wire value = %s, want true", got)
	}
}
