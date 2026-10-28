package decoder

import (
	"testing"
	"time"
	"unsafe"

	"github.com/guregu/null/v6"
)

// tappableTreeCount counts tree points for id at (lat, lon).
func tappableTreeCount(id uint64, lat, lon float64) int {
	n := 0
	tappableTreeMutex.RLock()
	tappableTree.Search([2]float64{lon, lat}, [2]float64{lon, lat}, func(_, _ [2]float64, v uint64) bool {
		if v == id {
			n++
		}
		return true
	})
	tappableTreeMutex.RUnlock()
	return n
}

// waitTappableTree waits for the tree writer to apply queued mutations until cond holds.
func waitTappableTree(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newIndexedTappable(id uint64, lat, lon float64) *Tappable {
	return &Tappable{TappableData: TappableData{
		Id: id, Lat: lat, Lon: lon,
		RewardType: null.StringFrom("item"), ItemId: null.IntFrom(1301),
		ExpireTimestamp: null.IntFrom(time.Now().Unix() + 600),
	}}
}

func TestTappableLookupSize(t *testing.T) {
	if got := unsafe.Sizeof(TappableLookup{}); got != 32 {
		t.Errorf("unsafe.Sizeof(TappableLookup{}) = %d, want 32", got)
	}
}

func TestLootRewardTypeIndex(t *testing.T) {
	if len(lootRewardTypeNames) != 15 {
		t.Fatalf("len(lootRewardTypeNames) = %d, want 15", len(lootRewardTypeNames))
	}
	for name, want := range map[string]int8{"item": 0, "pokemon_candy": 3, "temp_evo_pokemon_branch": 14} {
		if got, ok := lootRewardTypeIndex(name); !ok || got != want {
			t.Errorf("lootRewardTypeIndex(%q) = %d, %v; want %d, true", name, got, ok, want)
		}
	}
	if got, ok := lootRewardTypeIndex("nope"); ok || got != 0 {
		t.Errorf("lootRewardTypeIndex(nope) = %d, %v; want 0, false", got, ok)
	}
}

func TestBuildTappableLookup(t *testing.T) {
	candy := &Tappable{TappableData: TappableData{
		Lat: 1, Lon: 2, RewardType: null.StringFrom("pokemon_candy"), RewardPokemonId: null.IntFrom(25),
		ExpireTimestamp: null.IntFrom(1700000000),
	}}
	if got, want := buildTappableLookup(candy), (TappableLookup{Lat: 1, Lon: 2, ExpireTimestamp: 1700000000, RewardType: 3, ItemId: -1, RewardPokemonId: 25, PokemonId: -1}); got != want {
		t.Errorf("candy lookup = %+v, want %+v", got, want)
	}
	encounter := &Tappable{TappableData: TappableData{Encounter: null.IntFrom(25)}}
	if got, want := buildTappableLookup(encounter), (TappableLookup{ExpireTimestamp: 0, RewardType: -1, ItemId: -1, RewardPokemonId: -1, PokemonId: 25}); got != want {
		t.Errorf("encounter lookup = %+v, want %+v", got, want)
	}
}

func TestTappableSaveInsertsTree(t *testing.T) {
	ta := newIndexedTappable(920001, 10.1, 20.1)
	tappableRtreeUpdateOnSave(ta)
	if _, ok := tappableLookupCache.Load(920001); !ok {
		t.Fatal("lookup missing after save")
	}
	waitTappableTree(t, "insert", func() bool { return tappableTreeCount(920001, 10.1, 20.1) == 1 })
}

func TestTappableSaveMoveReinserts(t *testing.T) {
	ta := newIndexedTappable(920002, 10.2, 20.2)
	tappableRtreeUpdateOnSave(ta)
	ta.Lat, ta.Lon = 10.3, 20.3
	tappableRtreeUpdateOnSave(ta)
	waitTappableTree(t, "move", func() bool {
		return tappableTreeCount(920002, 10.2, 20.2) == 0 && tappableTreeCount(920002, 10.3, 20.3) == 1
	})
	if l, _ := tappableLookupCache.Load(920002); l.Lat != 10.3 || l.Lon != 20.3 {
		t.Errorf("lookup at %v,%v, want 10.3,20.3", l.Lat, l.Lon)
	}
}

func TestTappableEvictionDeletes(t *testing.T) {
	ta := newIndexedTappable(920003, 10.4, 20.4)
	tappableRtreeUpdateOnSave(ta)
	waitTappableTree(t, "insert", func() bool { return tappableTreeCount(920003, 10.4, 20.4) == 1 })

	handleTappableEviction(ta)
	if _, ok := tappableLookupCache.Load(920003); ok {
		t.Error("lookup still present after eviction")
	}
	waitTappableTree(t, "delete", func() bool { return tappableTreeCount(920003, 10.4, 20.4) == 0 })
}

func TestTappableEvictionSkipsRecached(t *testing.T) {
	ta := newIndexedTappable(920004, 10.5, 20.5)
	tappableRtreeUpdateOnSave(ta)
	waitTappableTree(t, "insert", func() bool { return tappableTreeCount(920004, 10.5, 20.5) == 1 })

	successor := newIndexedTappable(920004, 10.5, 20.5)
	tappableCache.Set(920004, successor, time.Minute)
	t.Cleanup(func() { tappableCache.Delete(920004) })

	handleTappableEviction(ta)
	if _, ok := tappableLookupCache.Load(920004); !ok {
		t.Error("lookup removed for a re-cached tappable")
	}
	time.Sleep(50 * time.Millisecond)
	if n := tappableTreeCount(920004, 10.5, 20.5); n != 1 {
		t.Errorf("tree points = %d, want 1", n)
	}
}

func TestTappableSaveSelfHealsMissingLookup(t *testing.T) {
	ta := newIndexedTappable(920005, 10.6, 20.6)
	tappableRtreeUpdateOnSave(ta)
	waitTappableTree(t, "insert", func() bool { return tappableTreeCount(920005, 10.6, 20.6) == 1 })

	tappableLookupCache.Delete(920005)
	tappableRtreeUpdateOnSave(ta)
	if _, ok := tappableLookupCache.Load(920005); !ok {
		t.Fatal("lookup not restored")
	}
	// the multiset tree may briefly hold a duplicate; scans dedupe
	waitTappableTree(t, "re-insert", func() bool { return tappableTreeCount(920005, 10.6, 20.6) >= 1 })
}

func TestTappablePreloadInsert(t *testing.T) {
	ta := newIndexedTappable(920006, 10.7, 20.7)
	tappablePreloadInsert(ta)
	if _, ok := tappableLookupCache.Load(920006); !ok {
		t.Fatal("lookup missing after preload insert")
	}
	if n := tappableTreeCount(920006, 10.7, 20.7); n != 1 {
		t.Errorf("tree points = %d immediately after preload insert, want 1", n)
	}
}
