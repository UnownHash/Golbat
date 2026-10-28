package decoder

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/tidwall/rtree"

	"golbat/ottercache"
)

// TappableLookup holds the fields tappable scans filter on. -1 = absent;
// ExpireTimestamp 0 = unknown.
type TappableLookup struct {
	Lat             float64
	Lon             float64
	ExpireTimestamp int64
	RewardType      int8 // index into lootRewardTypeNames
	ItemId          int16
	RewardPokemonId int16
	PokemonId       int16 // encounter species
}

// lootRewardTypeNames are the LootItemProto oneof field names (reward_type
// values) in field-number order.
var lootRewardTypeNames, lootRewardTypeIndexes = func() ([]string, map[string]int8) {
	fields := lootItemTypeOneof.Fields()
	names := make([]string, fields.Len())
	indexes := make(map[string]int8, fields.Len())
	for i := range names {
		names[i] = string(fields.Get(i).Name())
		indexes[names[i]] = int8(i)
	}
	return names, indexes
}()

func lootRewardTypeIndex(name string) (int8, bool) {
	i, ok := lootRewardTypeIndexes[name]
	return i, ok
}

var tappableLookupCache *xsync.Map[uint64, TappableLookup]
var tappableTreeMutex sync.RWMutex
var tappableTree rtree.RTreeG[uint64]
var tappableTreeSnapshot atomic.Pointer[treeSnapshot[uint64]]
var tappableTreeEvictor *treeEvictor[uint64]

// getTappableTreeSnapshot: see refreshTreeSnapshot.
func getTappableTreeSnapshot() *rtree.RTreeG[uint64] {
	return refreshTreeSnapshot(&tappableTreeSnapshot, &tappableTreeMutex, &tappableTree)
}

func initTappableRtree() {
	tappableTreeEvictor = newTreeEvictor[uint64]("tappable", 65536, treeEvictorBatchSize, flushTappableTreeEvictions)
	tappableLookupCache = xsync.NewMap[uint64, TappableLookup]()
	tappableCache.OnEviction(func(_ uint64, t *Tappable, _ ottercache.EvictionReason) {
		handleTappableEviction(t)
	})
}

// flushTappableTreeEvictions: see flushTreeEvictions.
func flushTappableTreeEvictions(entries []treeEvictionEntry[uint64]) {
	flushTreeEvictions(&tappableTreeMutex, &tappableTree, entries)
}

// tappableLookupInt16 saturates at MaxInt16 so a present value never reads as -1 (absent).
func tappableLookupInt16(n null.Int) int16 {
	switch {
	case !n.Valid || n.Int64 < 0:
		return -1
	case n.Int64 > math.MaxInt16:
		return math.MaxInt16
	}
	return int16(n.Int64)
}

func buildTappableLookup(t *Tappable) TappableLookup {
	rewardType := int8(-1)
	if t.RewardType.Valid {
		if i, ok := lootRewardTypeIndex(t.RewardType.String); ok {
			rewardType = i
		}
	}
	return TappableLookup{
		Lat:             t.Lat,
		Lon:             t.Lon,
		ExpireTimestamp: t.ExpireTimestamp.ValueOrZero(),
		RewardType:      rewardType,
		ItemId:          tappableLookupInt16(t.ItemId),
		RewardPokemonId: tappableLookupInt16(t.RewardPokemonId),
		PokemonId:       tappableLookupInt16(t.Encounter),
	}
}

// tappableRtreeUpdateOnSave refreshes the lookup and queues tree mutations
// through the writer. Caller holds the tappable lock. A missing lookup (new
// tappable, or evicted and re-cached) re-inserts the point.
func tappableRtreeUpdateOnSave(t *Tappable) {
	old, inMap := tappableLookupCache.Load(t.Id)
	if !inMap {
		tappableTreeEvictor.EnqueueInsert(t.Id, t.Lat, t.Lon)
	} else if old.Lat != t.Lat || old.Lon != t.Lon {
		tappableTreeEvictor.Enqueue(t.Id, old.Lat, old.Lon)
		tappableTreeEvictor.EnqueueInsert(t.Id, t.Lat, t.Lon)
	}
	tappableLookupCache.Store(t.Id, buildTappableLookup(t))
}

// tappablePreloadInsert inserts directly; preload runs before traffic.
func tappablePreloadInsert(t *Tappable) {
	if _, inMap := tappableLookupCache.Load(t.Id); inMap {
		return
	}
	tappableTreeMutex.Lock()
	tappableTree.Insert([2]float64{t.Lon, t.Lat}, [2]float64{t.Lon, t.Lat}, t.Id)
	tappableTreeMutex.Unlock()
	tappableLookupCache.Store(t.Id, buildTappableLookup(t))
}

// handleTappableEviction drops an evicted tappable's lookup and queues its
// tree delete. Runs on the cache's eviction dispatcher: never blocks, and
// skips a tappable re-cached since the eviction fired.
func handleTappableEviction(t *Tappable) {
	t.Lock("cacheEviction")
	defer t.Unlock()
	if tappableCache.Has(t.Id) {
		return
	}
	if l, ok := tappableLookupCache.LoadAndDelete(t.Id); ok {
		tappableTreeEvictor.TryEnqueue(t.Id, l.Lat, l.Lon)
	}
}
