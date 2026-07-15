package decoder

import (
	"sync"
	"testing"

	"github.com/guregu/null/v6"
)

func TestQuestConditions_Aggregate(t *testing.T) {
	initQuestConditions()
	k := []questConditionKey{{RewardType: 2, ItemId: 1, Title: "catch_x", Target: 3}}
	adjustQuestConditions(k, +1)
	adjustQuestConditions(k, +1)
	got := GetAvailableQuestConditions()
	if len(got) != 1 || got[0].Count != 2 || got[0].Title != "catch_x" {
		t.Fatalf("want 1 entry count=2, got %+v", got)
	}
	adjustQuestConditions(k, -2)
	if len(GetAvailableQuestConditions()) != 0 {
		t.Fatal("entry should be removed at count 0")
	}
}

// newQuestStop builds a pokestop carrying a no-AR quest slot (the primary
// Quest* fields) with the given title/target.
func newQuestStop(id, title string, target int64) *Pokestop {
	return &Pokestop{PokestopData: PokestopData{
		Id:                id,
		Lat:               1.0,
		Lon:               2.0,
		QuestRewardType:   null.IntFrom(2),
		QuestItemId:       null.IntFrom(1),
		QuestRewardAmount: null.IntFrom(5),
		QuestTitle:        null.StringFrom(title),
		QuestTarget:       null.IntFrom(target),
	}}
}

// findCondition returns the result matching title+target, or false.
func findCondition(results []ApiQuestConditionResult, title string, target int32) (ApiQuestConditionResult, bool) {
	for _, r := range results {
		if r.Title == title && r.Target == target {
			return r, true
		}
	}
	return ApiQuestConditionResult{}, false
}

// TestQuestConditions_Lifecycle exercises the full reconcile path through the
// real hook sites: load(+1) -> quest change(reconcile) -> evict(-1) nets to
// empty, and a two-fort aggregate collapses onto one entry with count 2.
func TestQuestConditions_Lifecycle(t *testing.T) {
	initQuestConditions()

	stop := newQuestStop("quest-life-1", "catch_x", 3)

	// Load / first lookup -> +1.
	updatePokestopLookup(stop)
	if got := GetAvailableQuestConditions(); len(got) != 1 || got[0].Count != 1 || got[0].Title != "catch_x" {
		t.Fatalf("after load: want 1 entry count=1 title=catch_x, got %+v", got)
	}

	// Quest change on the same fort -> old key removed, new key added, still one entry.
	stop.QuestTitle = null.StringFrom("catch_y")
	stop.QuestTarget = null.IntFrom(7)
	updatePokestopLookup(stop)
	got := GetAvailableQuestConditions()
	if len(got) != 1 {
		t.Fatalf("after change: want exactly 1 entry, got %+v", got)
	}
	if r, ok := findCondition(got, "catch_y", 7); !ok || r.Count != 1 {
		t.Fatalf("after change: want single catch_y/target=7 count=1, got %+v", got)
	}
	if _, ok := findCondition(got, "catch_x", 3); ok {
		t.Fatalf("after change: stale catch_x/target=3 should be gone, got %+v", got)
	}

	// A second fort offering the identical (now catch_y) option -> count 2.
	stop2 := newQuestStop("quest-life-2", "catch_y", 7)
	updatePokestopLookup(stop2)
	got = GetAvailableQuestConditions()
	if r, ok := findCondition(got, "catch_y", 7); !ok || r.Count != 2 {
		t.Fatalf("after second fort: want catch_y/target=7 count=2, got %+v", got)
	}

	// Evict fort 1 -> back to count 1.
	deferFortEviction(POKESTOP, stop.Id, stop.Lat, stop.Lon)
	got = GetAvailableQuestConditions()
	if r, ok := findCondition(got, "catch_y", 7); !ok || r.Count != 1 {
		t.Fatalf("after evict fort1: want catch_y/target=7 count=1, got %+v", got)
	}

	// Evict fort 2 -> empty, and its tracker entry gone.
	deferFortEviction(POKESTOP, stop2.Id, stop2.Lat, stop2.Lon)
	if got := GetAvailableQuestConditions(); len(got) != 0 {
		t.Fatalf("after evict fort2: want 0 entries, got %+v", got)
	}
	if _, ok := questFortKeys.Load(stop2.Id); ok {
		t.Fatalf("after evict fort2: tracker entry should be gone")
	}
}

// TestQuestConditions_DeleteAndConversion covers the two non-eviction removal
// paths: a deleted pokestop (via fortRtreeUpdatePokestopOnSave's Deleted branch)
// and a pokestop that converts to a gym (its stale contribution must be dropped
// when the old pokestop finally evicts).
func TestQuestConditions_DeleteAndConversion(t *testing.T) {
	initQuestConditions()

	// Deletion path.
	del := newQuestStop("quest-del-1", "spin_x", 4)
	updatePokestopLookup(del)
	if len(GetAvailableQuestConditions()) != 1 {
		t.Fatalf("delete setup: want 1 entry")
	}
	del.Deleted = true
	fortRtreeUpdatePokestopOnSave(del)
	if got := GetAvailableQuestConditions(); len(got) != 0 {
		t.Fatalf("after delete: want 0 entries, got %+v", got)
	}

	// Conversion path: fort is a pokestop with a quest, then its FortLookup
	// entry is overwritten by a gym. The pokestop's contribution lingers until
	// its own eviction fires (FortType mismatch), which must still drop it.
	conv := newQuestStop("quest-conv-1", "battle_x", 2)
	updatePokestopLookup(conv)
	if len(GetAvailableQuestConditions()) != 1 {
		t.Fatalf("conversion setup: want 1 entry")
	}
	gym := &Gym{GymData: GymData{Id: conv.Id, Lat: conv.Lat, Lon: conv.Lon}}
	updateGymLookup(gym) // overwrites FortLookup[id] to GYM; count still stale here
	// Stale pokestop eviction: FortLookup is now GYM (mismatch) but the quest
	// contribution must still be removed.
	deferFortEviction(POKESTOP, conv.Id, conv.Lat, conv.Lon)
	if got := GetAvailableQuestConditions(); len(got) != 0 {
		t.Fatalf("after conversion+evict: want 0 entries, got %+v", got)
	}
	if _, ok := questFortKeys.Load(conv.Id); ok {
		t.Fatalf("after conversion+evict: tracker entry should be gone")
	}
}

// TestQuestConditions_ConcurrentReconcile hammers a single fort with concurrent
// reconciles (load/save) and evictions, then quiesces with a lone eviction. The
// telescoping invariant must leave the aggregate empty with no leaked or
// negative counts. Run under -race to validate map/thread safety.
func TestQuestConditions_ConcurrentReconcile(t *testing.T) {
	initQuestConditions()

	const goroutines = 16
	const iterations = 500
	stop := newQuestStop("quest-concurrent-1", "concurrent_x", 9)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				updatePokestopLookup(stop)
				deferFortEviction(POKESTOP, stop.Id, stop.Lat, stop.Lon)
			}
		}()
	}
	wg.Wait()

	// Quiesce: the last operation on the fort is an eviction, so it must net to
	// empty regardless of the interleavings above.
	deferFortEviction(POKESTOP, stop.Id, stop.Lat, stop.Lon)

	if got := GetAvailableQuestConditions(); len(got) != 0 {
		t.Fatalf("after concurrent storm + final evict: want 0 entries, got %+v", got)
	}
	if _, ok := questFortKeys.Load(stop.Id); ok {
		t.Fatalf("after concurrent storm + final evict: tracker entry should be gone")
	}

	// Belt and suspenders: no key anywhere should hold a non-positive or leaked
	// count (GetAvailableQuestConditions already filters <=0, so any surviving
	// entry is a leak).
	questConditionCount.Range(func(k questConditionKey, count int64) bool {
		t.Fatalf("leaked count entry %+v = %d", k, count)
		return false
	})
}
