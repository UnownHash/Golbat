package decoder

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"golbat/config"

	"github.com/UnownHash/gohbem"
	"github.com/guregu/null/v6"
	"github.com/jellydator/ttlcache/v3"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/tidwall/rtree"
)

type PokemonLookupCacheItem struct {
	PokemonLookup    *PokemonLookup
	PokemonPvpLookup *PokemonPvpLookup
}

type PokemonLookup struct {
	PokemonId          int16
	Form               int16
	HasEncounterValues bool
	Weather            int8
	Atk                int8
	Def                int8
	Sta                int8
	Level              int8
	Cp                 int16
	Gender             int8
	Xxs                bool
	Xxl                bool
	Iv                 int8
	Size               int8
}

type PokemonPvpLookup struct {
	Little int16
	Great  int16
	Ultra  int16
}

type pokemonFormKey struct {
	pokemonId int16
	form      int16
}

var pokemonLookupCache *xsync.Map[uint64, PokemonLookupCacheItem]
var pokemonFormCount *xsync.Map[pokemonFormKey, int64]
var pokemonTreeMutex sync.RWMutex
var pokemonTree rtree.RTreeG[uint64]

// treeSnapshotMaxAge bounds scan-snapshot staleness. Scans re-verify hits
// against the lookup caches (and lock records for final results), so a
// slightly stale spatial index only costs a few extra skips/misses.
const treeSnapshotMaxAge = time.Second

type treeSnapshot[K comparable] struct {
	tree      rtree.RTreeG[K]
	createdAt time.Time
}

var pokemonTreeSnapshot atomic.Pointer[treeSnapshot[uint64]]

// refreshTreeSnapshot returns a read-only spatial index snapshot shared by
// all scans, refreshed at most every treeSnapshotMaxAge. This replaces
// per-request Copy(), which kept the live tree permanently copy-on-write.
// Double-checked under the lock so a burst of scans arriving at expiry
// produces one Copy(), not one per caller. Copy() mutates the source tree's
// COW stamp — full lock required. The result is shared by concurrent
// goroutines: only read-only operations (Search, Nearby, Len) are safe on
// it — never Copy, Insert, Delete, or Replace.
func refreshTreeSnapshot[K comparable](snapPtr *atomic.Pointer[treeSnapshot[K]], mu *sync.RWMutex, tree *rtree.RTreeG[K]) *rtree.RTreeG[K] {
	if snap := snapPtr.Load(); snap != nil && time.Since(snap.createdAt) < treeSnapshotMaxAge {
		return &snap.tree
	}
	mu.Lock()
	if snap := snapPtr.Load(); snap != nil && time.Since(snap.createdAt) < treeSnapshotMaxAge {
		mu.Unlock()
		return &snap.tree
	}
	snap := &treeSnapshot[K]{tree: *tree.Copy(), createdAt: time.Now()}
	snapPtr.Store(snap)
	mu.Unlock()
	return &snap.tree
}

func getPokemonTreeSnapshot() *rtree.RTreeG[uint64] {
	return refreshTreeSnapshot(&pokemonTreeSnapshot, &pokemonTreeMutex, &pokemonTree)
}

const (
	treeEvictorQueueSize = 262144
	treeEvictorBatchSize = 512
)

var pokemonTreeEvictor *treeEvictor[uint64]

// flushTreeEvictions applies a batch of tree mutations, in enqueue order,
// under a single tree-mutex acquisition. Deletes match on (coords, id), so
// a stale duplicate delete (e.g. eviction racing a position move) finds
// nothing and is harmless; duplicate identical inserts leave a second
// point that the next delete pairs off against (rtree is a multiset).
func flushTreeEvictions[K comparable](mu *sync.RWMutex, tree *rtree.RTreeG[K], entries []treeEvictionEntry[K]) {
	mu.Lock()
	for _, e := range entries {
		point := [2]float64{e.lon, e.lat}
		if e.op == treeOpInsert {
			tree.Insert(point, point, e.id)
		} else {
			tree.Delete(point, point, e.id)
		}
	}
	mu.Unlock()
}

func flushPokemonTreeEvictions(entries []treeEvictionEntry[uint64]) {
	flushTreeEvictions(&pokemonTreeMutex, &pokemonTree, entries)
}

func adjustPokemonFormCount(key pokemonFormKey, delta int64) {
	pokemonFormCount.Compute(key, func(oldValue int64, loaded bool) (int64, xsync.ComputeOp) {
		newValue := oldValue + delta
		if newValue <= 0 {
			return 0, xsync.DeleteOp // delete entry when count reaches zero
		}
		return newValue, xsync.UpdateOp
	})
}

func initPokemonRtree() {
	pokemonLookupCache = xsync.NewMap[uint64, PokemonLookupCacheItem]()
	pokemonFormCount = xsync.NewMap[pokemonFormKey, int64]()

	pokemonTreeEvictor = newTreeEvictor[uint64]("pokemon", treeEvictorQueueSize, treeEvictorBatchSize, flushPokemonTreeEvictions)

	// ttlcache runs each eviction callback on its own goroutine (see
	// Cache.OnEviction: the registered fn is wrapped in `go fn(...)`), so
	// this races concurrent updaters holding the entity lock — the cleanup
	// itself is serialized in handlePokemonEviction.
	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[uint64, *Pokemon]) {
		handlePokemonEviction(v.Value())
	})
}

// handlePokemonEviction removes an evicted pokemon from the lookup cache
// (inline, lock-free — scans stop seeing it immediately) and defers its
// tree removal to the batched evictor. It runs on ttlcache's per-eviction
// goroutine, so it takes the entity lock to serialize against updaters: if
// a save re-cached the pokemon after the eviction fired, the lookup and
// tree entries are current and must be left alone — cleaning them would
// make a live, cached pokemon invisible to every scan.
func handlePokemonEviction(pokemon *Pokemon) {
	pokemonId := uint64(pokemon.Id)
	pokemon.Lock("cacheEviction")
	defer pokemon.Unlock()

	if pokemonCache.Get(pokemonId) != nil {
		// Re-cached (same pokemon re-saved, or a successor record created)
		// — its owner maintains the lookup/tree entries now.
		return
	}
	if item, ok := pokemonLookupCache.LoadAndDelete(pokemonId); ok && item.PokemonLookup != nil {
		adjustPokemonFormCount(pokemonFormKey{item.PokemonLookup.PokemonId, item.PokemonLookup.Form}, -1)
	}
	// Non-blocking: eviction callbacks are one goroutine per item and this
	// one holds the entity lock — see treeEvictor.Enqueue for the incident
	// a blocking send here caused.
	pokemonTreeEvictor.TryEnqueue(pokemonId, pokemon.Lat, pokemon.Lon)
}

// queuePokemonTreeInsert / queuePokemonTreeRemove are the runtime-path tree
// mutations: ordered through the single tree worker so savers (which hold
// entity locks) never contend on the tree mutex. Preload and tests use the
// direct add/remove functions below.
func queuePokemonTreeInsert(pokemon *Pokemon) {
	pokemonTreeEvictor.EnqueueInsert(uint64(pokemon.Id), pokemon.Lat, pokemon.Lon)
}

func queuePokemonTreeRemove(pokemonId uint64, lat, lon float64) {
	pokemonTreeEvictor.Enqueue(pokemonId, lat, lon)
}

func pokemonRtreeUpdatePokemonOnGet(pokemon *Pokemon) {
	pokemonId := uint64(pokemon.Id)

	_, inMap := pokemonLookupCache.Load(pokemonId)

	if !inMap {
		queuePokemonTreeInsert(pokemon)
		// this pokemon won't be available for pvp searches
		updatePokemonLookup(pokemon, false, nil)
	}
}

// pokemonRtreePreloadInsert is the startup-preload variant: inserts
// directly instead of through the tree worker. Preload runs before traffic
// (no contention to avoid) and its parallel workers would otherwise flood
// the writer channel — and a full channel blocks enqueuers, which at
// runtime includes savers holding entity locks.
func pokemonRtreePreloadInsert(pokemon *Pokemon) {
	if _, inMap := pokemonLookupCache.Load(uint64(pokemon.Id)); !inMap {
		addPokemonToTree(pokemon)
		updatePokemonLookup(pokemon, false, nil)
	}
}

func valueOrMinus1(n null.Int) int {
	if n.Valid {
		return int(n.Int64)
	}
	return -1
}

// updatePokemonLookup refreshes the scan lookup entry and reports whether
// one already existed — false means an eviction removed it (and the tree
// point) while the caller held the entity lock, so the caller must restore
// the tree point.
func updatePokemonLookup(pokemon *Pokemon, changePvp bool, pvpResults map[string][]gohbem.PokemonEntry) bool {
	pokemonId := uint64(pokemon.Id)

	pokemonLookupCacheItem, existed := pokemonLookupCache.Load(pokemonId)

	// Track old form key so we can adjust counts
	var oldKey pokemonFormKey
	if existed && pokemonLookupCacheItem.PokemonLookup != nil {
		oldKey = pokemonFormKey{pokemonLookupCacheItem.PokemonLookup.PokemonId, pokemonLookupCacheItem.PokemonLookup.Form}
	}

	pokemonLookupCacheItem.PokemonLookup = &PokemonLookup{
		PokemonId:          pokemon.PokemonId,
		HasEncounterValues: pokemon.AtkIv.Valid || len(pokemon.GolbatInternal) > 0 || len(pokemon.internal.ScanHistory) > 0,
		Weather:            int8(valueOrMinus1(pokemon.Weather)),
		Atk:                int8(valueOrMinus1(pokemon.AtkIv)),
		Def:                int8(valueOrMinus1(pokemon.DefIv)),
		Sta:                int8(valueOrMinus1(pokemon.StaIv)),
		Level:              int8(valueOrMinus1(pokemon.Level)),
		Gender:             int8(valueOrMinus1(pokemon.Gender)),
		Cp:                 int16(valueOrMinus1(pokemon.Cp)),
		Iv: func() int8 {
			if pokemon.Iv.Valid {
				return int8(math.Floor(pokemon.Iv.Float64))
			}
			return -1
		}(),
		Size: int8(valueOrMinus1(pokemon.Size)),
	}
	if !pokemon.IsDitto {
		pokemonLookupCacheItem.PokemonLookup.Form = int16(pokemon.Form.ValueOrZero())
	}

	if changePvp {
		pokemonLookupCacheItem.PokemonPvpLookup = calculatePokemonPvpLookup(pokemon, pvpResults)
	}

	pokemonLookupCache.Store(pokemonId, pokemonLookupCacheItem)

	// Update form counts
	newKey := pokemonFormKey{pokemonLookupCacheItem.PokemonLookup.PokemonId, pokemonLookupCacheItem.PokemonLookup.Form}
	if existed && oldKey != newKey {
		adjustPokemonFormCount(oldKey, -1)
	}
	if !existed || oldKey != newKey {
		adjustPokemonFormCount(newKey, 1)
	}

	return existed
}

func calculatePokemonPvpLookup(pokemon *Pokemon, pvpResults map[string][]gohbem.PokemonEntry) *PokemonPvpLookup {
	if pvpResults == nil {
		return nil
	}

	pvpStore := make(map[string]int16)
	for key, value := range pvpResults {
		var best int16 = 4096 // worst possible rank
		// This code actually calculates best in a level cap, which is no longer strictly necessary
		// But will leave in this form to allow easy change to per-cap again later

		for _, levelCap := range config.Config.Pvp.LevelCaps {
			for _, entry := range value {
				// we don't exclude mega evolutions yet
				if (int(entry.Cap) == levelCap || (entry.Capped && int(entry.Cap) <= levelCap)) &&
					entry.Rank < best {
					best = entry.Rank
				}
			}
		}
		if best != 4096 {
			pvpStore[key] = best
		}
	}

	bestValue := func(leagueKey string) int16 {
		if value, ok := pvpStore[leagueKey]; ok {
			return value
		}
		return 4096
	}

	return &PokemonPvpLookup{
		Little: bestValue("little"),
		Great:  bestValue("great"),
		Ultra:  bestValue("ultra"),
	}
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonId := uint64(pokemon.Id)

	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	pokemonTreeMutex.Unlock()
}
