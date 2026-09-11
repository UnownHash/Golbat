package decoder

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"golbat/config"

	"github.com/UnownHash/gohbem"
	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/tidwall/rtree"

	"golbat/ottercache"
)

// PokemonLookupCacheItem holds the scan-filter data INLINE BY VALUE.
// The scan path loads one of these per candidate (15-20M/s in production
// profiles, 14% of CPU); with pointer fields each candidate cost up to
// three dependent DRAM misses (map node -> lookup -> pvp) and the map
// carried 2 heap objects per entry (20M pointees, 1.4x this structure's
// GC mark cost — see cachebench/lookup_bench_test.go: inline layout is
// 3.2x faster at production concurrency). Validity flags replace the nil
// checks.
type PokemonLookupCacheItem struct {
	PokemonLookup    PokemonLookup
	PokemonPvpLookup PokemonPvpLookup
	HasLookup        bool
	HasPvp           bool
}

// PokemonLookup is the scan-filter view of a pokemon. Its nullable fields are
// signed and use -1 for "no value" rather than carrying a validity flag each,
// which is what keeps the whole struct inside PokemonLookupCacheItem above.
//
// Every field here is built by lookupInt8/lookupInt16/lookupForm/lookupIv,
// never by a bare conversion: the stored fields are clamped at their column's
// ceiling, and converting that ceiling into one of these slots lands on -1 or
// on some other negative number. See lookupInt8's doc comment. Form's -1 is
// not even an absence marker — it is the wildcard key in the DNF filter index
// — so it has its own helper.
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

	// Eviction callbacks arrive on the OtterCache dispatcher goroutine —
	// async relative to updaters holding the entity lock, so this races
	// concurrent saves; the cleanup itself is serialized in
	// handlePokemonEviction.
	pokemonCache.OnEviction(func(_ uint64, pokemon *Pokemon, _ ottercache.EvictionReason) {
		handlePokemonEviction(pokemon)
	})
}

// handlePokemonEviction removes an evicted pokemon from the lookup cache
// (inline, lock-free — scans stop seeing it immediately) and defers its
// tree removal to the batched evictor. It runs on the cache's eviction
// dispatcher goroutine, so it takes the entity lock to serialize against
// updaters: if
// a save re-cached the pokemon after the eviction fired, the lookup and
// tree entries are current and must be left alone — cleaning them would
// make a live, cached pokemon invisible to every scan.
func handlePokemonEviction(pokemon *Pokemon) {
	pokemonId := uint64(pokemon.Id)
	pokemon.Lock("cacheEviction")
	defer pokemon.Unlock()

	if pokemonCache.Has(pokemonId) {
		// Re-cached (same pokemon re-saved, or a successor record created)
		// — its owner maintains the lookup/tree entries now.
		return
	}
	if item, ok := pokemonLookupCache.LoadAndDelete(pokemonId); ok && item.HasLookup {
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

// lookupInt8 and lookupInt16 read a narrowed nullable field into
// PokemonLookup's signed slot, which uses -1 as its own "no value" sentinel
// (see PokemonLookup's fields) instead of a separate validity flag.
//
// They saturate at the slot's own maximum instead of converting, and that is
// the whole point of them. The stored fields are clamped at their *column's*
// ceiling — 255 for a tinyint, 65535 for a smallint — so a bare int8(255) or
// int16(65535) is exactly -1: a value that is present becomes one every scan
// filter reads as absent, silently dropping the pokemon from filtered scans.
// The hazard is wider than that one collision, too. Every stored value from
// 128 (or 32768) up converts to some negative number and so fails every
// `>= Min` comparison in the DNF filters, not just the ceiling.
//
// Saturating keeps the value on the correct side of every range comparison —
// a garbage level reads as "very high" rather than "absent" or "negative" —
// and cannot reach the sentinel. The int8/int16 ceilings sit far above every
// real reading (levels top out near 51, genders at 3, sizes at 5, CP near
// 6000), so the saturation is only reachable on the same out-of-range inputs
// the storage clamp already exists for.
//
// Deliberately no metric here. golbat_field_clamped_total's home is the
// setter that stores the value (see clampUint's doc comment): this is a
// representation choice in a derived cache, not a second storage event, and
// counting it would add a second count per sighting for the same fact.
func lookupInt8[T ~uint8 | ~uint16 | ~uint32](n null.Value[T]) int8 {
	if !n.Valid {
		return -1
	}
	if uint64(n.V) > math.MaxInt8 {
		return math.MaxInt8
	}
	return int8(n.V)
}

func lookupInt16[T ~uint8 | ~uint16 | ~uint32](n null.Value[T]) int16 {
	if !n.Valid {
		return -1
	}
	if uint64(n.V) > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(n.V)
}

// lookupForm reads the stored form into PokemonLookup.Form. Unlike the two
// helpers above it has no absent sentinel — an absent form is 0, matching the
// column — but it must still never produce -1, for a different reason: -1 is
// the *wildcard-form* key in the DNF filter index (api_pokemon_common.go
// falls back to {pokemonId, -1} and then {-1, -1}). A stored 65535 converted
// to -1 would therefore not read as "no form"; it would match the catch-all
// filter set for that pokemon id, and adjustPokemonFormCount would file it
// under the wildcard key. Saturating at MaxInt16 gives it a form key of its
// own that no filter fallback and no real form id can collide with.
func lookupForm(n null.Value[uint16]) int16 {
	if !n.Valid {
		return 0
	}
	if n.V > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(n.V)
}

// lookupIv reads the stored IV percentage into PokemonLookup.Iv. iv is
// float(5,2) unsigned, so the column holds up to 999.99: rows written before
// clampIv capped the per-stat IVs at 15 can carry a percentage well above
// 127 (a single stat stored at the tinyint's 255 gives iv = 566.67), and a
// bare int8 of that is negative — 384 converts to -128, the sentinel's
// neighbourhood, and 200 to -56. Floor first, then saturate, so the DNF
// filters read "very high" rather than "absent".
func lookupIv(n null.Value[float32]) int8 {
	if !n.Valid {
		return -1
	}
	switch v := math.Floor(float64(n.V)); {
	case v > math.MaxInt8:
		return math.MaxInt8
	case v > 0:
		return int8(v)
	default:
		// Zero, negative, or NaN. None are reachable from an unsigned
		// float(5,2) column, but a float-to-int conversion is undefined in
		// the Go spec when the value is out of the target's range, so
		// nothing is converted unless it is known to be in range.
		return 0
	}
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
	if existed && pokemonLookupCacheItem.HasLookup {
		oldKey = pokemonFormKey{pokemonLookupCacheItem.PokemonLookup.PokemonId, pokemonLookupCacheItem.PokemonLookup.Form}
	}

	pokemonLookupCacheItem.HasLookup = true
	pokemonLookupCacheItem.PokemonLookup = PokemonLookup{
		PokemonId:          pokemon.PokemonId,
		HasEncounterValues: pokemon.AtkIv.Valid || len(pokemon.GolbatInternal) > 0 || len(pokemon.scanHistory) > 0,
		Weather:            lookupInt8(pokemon.Weather),
		Atk:                lookupInt8(pokemon.AtkIv),
		Def:                lookupInt8(pokemon.DefIv),
		Sta:                lookupInt8(pokemon.StaIv),
		Level:              lookupInt8(pokemon.Level),
		Gender:             lookupInt8(pokemon.Gender),
		Cp:                 lookupInt16(pokemon.Cp),
		Iv:                 lookupIv(pokemon.Iv),
		Size:               lookupInt8(pokemon.Size),
	}
	if !pokemon.IsDitto {
		pokemonLookupCacheItem.PokemonLookup.Form = lookupForm(pokemon.Form)
	}

	if changePvp {
		if pvp, ok := calculatePokemonPvpLookup(pokemon, pvpResults); ok {
			pokemonLookupCacheItem.PokemonPvpLookup = pvp
			pokemonLookupCacheItem.HasPvp = true
		} else {
			pokemonLookupCacheItem.PokemonPvpLookup = PokemonPvpLookup{}
			pokemonLookupCacheItem.HasPvp = false
		}
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

func calculatePokemonPvpLookup(pokemon *Pokemon, pvpResults map[string][]gohbem.PokemonEntry) (PokemonPvpLookup, bool) {
	if pvpResults == nil {
		return PokemonPvpLookup{}, false
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

	return PokemonPvpLookup{
		Little: bestValue("little"),
		Great:  bestValue("great"),
		Ultra:  bestValue("ultra"),
	}, true
}

func addPokemonToTree(pokemon *Pokemon) {
	pokemonId := uint64(pokemon.Id)

	pokemonTreeMutex.Lock()
	pokemonTree.Insert([2]float64{pokemon.Lon, pokemon.Lat}, [2]float64{pokemon.Lon, pokemon.Lat}, pokemonId)
	pokemonTreeMutex.Unlock()
}
