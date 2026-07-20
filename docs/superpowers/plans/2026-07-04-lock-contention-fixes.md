# Lock-Contention Fixes Implementation Plan

> **CORRECTION (post-implementation review):** this plan's premise that
> ttlcache invokes eviction callbacks synchronously under the shard write
> lock is WRONG — `Cache.OnEviction` wraps each callback in `go fn(...)`,
> so callbacks run on their own goroutines, unsynchronized with the sweep,
> cache operations, or entity-lock holders. The production freeze was the
> global tree-mutex convoy formed by thousands of per-eviction goroutines
> (amplified by permanent copy-on-write from per-request tree copies), not
> shard-lock hold time. The batching/snapshot fixes below still target that
> convoy, but the eviction callbacks additionally need the race guards added
> in the post-review fix wave (entity-lock + re-cache check for pokemon;
> lookup-presence + fort-type guards for forts). Code comments are the
> source of truth; do not restore this plan's synchrony claims.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the multi-second Pokemon entity-lock freezes caused by TTL-cache expiry sweeps doing per-item global R-tree surgery under shard locks, and de-synchronize the post-restart expiry cohort.

**Architecture:** Cache eviction callbacks become O(1): they delete the lookup-cache entry inline (lock-free) and enqueue the R-tree delete onto a buffered channel drained by a worker that batches ~512 deletes per global-mutex acquisition. API scans stop calling `tree.Copy()` per request and instead share an `atomic.Pointer` snapshot refreshed at most once per second, so copy-on-write costs amortize. Pokemon cache TTLs get jitter (unverified) and a 1-minute clamp (past despawn) so restart cohorts can't detonate in one sweep. Two new tuning knobs: `cache_shards` and `raw_processing_concurrency`.

**Tech Stack:** Go 1.26, `github.com/tidwall/rtree`, `github.com/jellydator/ttlcache/v3` v3.4.0, `math/rand/v2`, koanf config.

**Background (read first):** `decoder/pokemonRtree.go`, `decoder/fortRtree.go`, `decoder/sharded_cache.go`, `decoder/main.go:95-180` (cache construction + eviction callbacks). The bug: ttlcache's `DeleteExpired` holds the shard write lock for its whole sweep and calls eviction callbacks synchronously under it; our callbacks take the global `pokemonTreeMutex`/`fortTreeMutex` per item. Savers call `pokemonCache.Set` and `addPokemonToTree` while holding the pokemon entity mutex, so sweep duration propagates into entity-lock hold time.

## Global Constraints

- Go 1.26; use `math/rand/v2` (never `math/rand`).
- No behavior change when new config keys are absent, except the documented TTL changes in Task 5.
- Every task must leave `go build ./...` and `go test ./decoder/... ./...` passing and code `gofmt`-clean.
- Follow existing naming: lowercase package-private functions, `koanf` snake_case tags.
- Tests in `decoder` share package globals (`pokemonTree`, caches); tests that touch globals must restore state (`defer`) and must not call `initDataCache`.
- Branch: all work on `perf/eviction-lock-contention` off current `main`.

---

### Task 1: Batched tree evictor

**Files:**
- Create: `decoder/rtree_evictor.go`
- Test: `decoder/rtree_evictor_test.go`

**Interfaces:**
- Produces: `newTreeEvictor[K comparable](name string, capacity, batchSize int, flush func([]treeEvictionEntry[K])) *treeEvictor[K]` with methods `Enqueue(id K, lat, lon float64)` and `Close()`; type `treeEvictionEntry[K comparable]{ id K; lat, lon float64 }`. Tasks 2–3 consume these.

- [ ] **Step 1: Create the branch**

```bash
git checkout -b perf/eviction-lock-contention main
```

- [ ] **Step 2: Write the failing test**

Create `decoder/rtree_evictor_test.go`:

```go
package decoder

import (
	"sync"
	"testing"
)

func TestTreeEvictorFlushesAllEntriesInBatches(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]treeEvictionEntry[uint64]

	e := newTreeEvictor[uint64]("test", 64, 4, func(entries []treeEvictionEntry[uint64]) {
		mu.Lock()
		batch := make([]treeEvictionEntry[uint64], len(entries))
		copy(batch, entries)
		flushed = append(flushed, batch)
		mu.Unlock()
	})

	for i := range uint64(10) {
		e.Enqueue(i, float64(i), -float64(i))
	}
	e.Close() // drains remaining entries and stops the worker

	mu.Lock()
	defer mu.Unlock()

	total := 0
	seen := map[uint64]bool{}
	for _, batch := range flushed {
		if len(batch) == 0 || len(batch) > 4 {
			t.Errorf("batch size %d outside (0, 4]", len(batch))
		}
		for _, entry := range batch {
			total++
			seen[entry.id] = true
			if entry.lat != float64(entry.id) || entry.lon != -float64(entry.id) {
				t.Errorf("entry %d has wrong coords (%f, %f)", entry.id, entry.lat, entry.lon)
			}
		}
	}
	if total != 10 || len(seen) != 10 {
		t.Errorf("expected 10 unique entries flushed, got %d (%d unique)", total, len(seen))
	}
}

func TestTreeEvictorCloseIsIdempotentAndEnqueueAfterCloseIsSafe(t *testing.T) {
	e := newTreeEvictor[uint64]("test", 4, 2, func([]treeEvictionEntry[uint64]) {})
	e.Enqueue(1, 0, 0)
	e.Close()
	e.Close()          // must not panic
	e.Enqueue(2, 0, 0) // must not panic or block
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./decoder/ -run TestTreeEvictor -count=1`
Expected: FAIL — `undefined: newTreeEvictor`

- [ ] **Step 4: Write the implementation**

Create `decoder/rtree_evictor.go`:

```go
package decoder

import (
	"sync"

	log "github.com/sirupsen/logrus"
)

// treeEvictionEntry is one deferred R-tree removal.
type treeEvictionEntry[K comparable] struct {
	id  K
	lat float64
	lon float64
}

// treeEvictor defers R-tree removals out of cache-eviction callbacks.
// ttlcache invokes eviction callbacks synchronously while holding the
// shard's write lock for the whole expiry sweep, so per-item work there
// must be O(1). Enqueue is called from that context; a single worker
// drains the channel and flushes removals in batches so the global tree
// mutex is taken once per batch instead of once per item.
type treeEvictor[K comparable] struct {
	name      string
	ch        chan treeEvictionEntry[K]
	flush     func([]treeEvictionEntry[K])
	batchSize int

	closeOnce sync.Once
	done      chan struct{}
}

func newTreeEvictor[K comparable](name string, capacity, batchSize int, flush func([]treeEvictionEntry[K])) *treeEvictor[K] {
	e := &treeEvictor[K]{
		name:      name,
		ch:        make(chan treeEvictionEntry[K], capacity),
		flush:     flush,
		batchSize: batchSize,
		done:      make(chan struct{}),
	}
	go e.run()
	return e
}

// Enqueue queues a removal. Blocks only if the channel is full, which
// restores (batched) backpressure rather than dropping the entry and
// leaking a ghost point in the tree.
func (e *treeEvictor[K]) Enqueue(id K, lat, lon float64) {
	defer func() {
		// Sending on a closed channel panics; Close is only used by
		// tests and shutdown, where losing the entry is acceptable.
		_ = recover()
	}()
	e.ch <- treeEvictionEntry[K]{id: id, lat: lat, lon: lon}
}

// Close stops the worker after draining queued entries. Test/shutdown use.
func (e *treeEvictor[K]) Close() {
	e.closeOnce.Do(func() { close(e.ch) })
	<-e.done
}

func (e *treeEvictor[K]) run() {
	defer close(e.done)
	buf := make([]treeEvictionEntry[K], 0, e.batchSize)
	for entry := range e.ch {
		buf = append(buf[:0], entry)
	drain:
		for len(buf) < e.batchSize {
			select {
			case next, ok := <-e.ch:
				if !ok {
					break drain
				}
				buf = append(buf, next)
			default:
				break drain
			}
		}
		e.flush(buf)
		if pending := len(e.ch); pending > cap(e.ch)/2 {
			log.Warnf("[TREE_EVICTOR] %s backlog %d/%d", e.name, pending, cap(e.ch))
		}
	}
	// Channel closed: flush anything the final receive loop left queued.
	// (range exits only when ch is closed AND empty, so nothing remains.)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./decoder/ -run TestTreeEvictor -count=1 -race`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add decoder/rtree_evictor.go decoder/rtree_evictor_test.go
git commit -m "perf: add batched tree evictor for deferred rtree removals"
```

---

### Task 2: Pokemon evictions through the evictor

**Files:**
- Modify: `decoder/pokemonRtree.go` (add evictor + flush func; change `initPokemonRtree` callback)
- Test: `decoder/pokemonRtree_evict_test.go`

**Interfaces:**
- Consumes: `newTreeEvictor`, `treeEvictionEntry` from Task 1.
- Produces: `pokemonTreeEvictor *treeEvictor[uint64]` (package var), `flushPokemonTreeEvictions(entries []treeEvictionEntry[uint64])`.

- [ ] **Step 1: Write the failing test**

Create `decoder/pokemonRtree_evict_test.go`:

```go
package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
)

func TestFlushPokemonTreeEvictionsRemovesPoints(t *testing.T) {
	// pokemonTree is a package global; count only the ids we add.
	ids := []uint64{910001, 910002, 910003}
	for _, id := range ids {
		p := &Pokemon{PokemonData: PokemonData{Id: Uint64Str(id), Lat: 1.5, Lon: 2.5, PokemonId: 1, Form: null.IntFrom(0)}}
		addPokemonToTree(p)
	}

	inTree := func(id uint64) bool {
		found := false
		pokemonTreeMutex.RLock()
		pokemonTree.Search([2]float64{2.5, 1.5}, [2]float64{2.5, 1.5}, func(_, _ [2]float64, v uint64) bool {
			if v == id {
				found = true
				return false
			}
			return true
		})
		pokemonTreeMutex.RUnlock()
		return found
	}

	for _, id := range ids {
		if !inTree(id) {
			t.Fatalf("setup: %d not in tree", id)
		}
	}

	flushPokemonTreeEvictions([]treeEvictionEntry[uint64]{
		{id: 910001, lat: 1.5, lon: 2.5},
		{id: 910002, lat: 1.5, lon: 2.5},
		{id: 910003, lat: 1.5, lon: 2.5},
	})

	for _, id := range ids {
		if inTree(id) {
			t.Errorf("%d still in tree after flush", id)
		}
	}
}
```

Note: `addPokemonToTree` only touches `pokemonTree`, which needs no init. If the test file fails to compile because `Uint64Str` has a different name, check `decoder/pokemon.go` for the `Id` field's type and construct accordingly — do not change the production type.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestFlushPokemonTreeEvictions -count=1`
Expected: FAIL — `undefined: flushPokemonTreeEvictions`

- [ ] **Step 3: Implement flush + evictor + new callback**

In `decoder/pokemonRtree.go`, add after the `var` block (around line 54):

```go
const (
	treeEvictorQueueSize = 131072
	treeEvictorBatchSize = 512
)

var pokemonTreeEvictor *treeEvictor[uint64]

// flushPokemonTreeEvictions removes a batch of evicted pokemon from the
// spatial index under a single tree-mutex acquisition. If a pokemon was
// re-added at the same coordinates between eviction and flush, the tree
// holds two identical entries and Delete removes one, leaving the fresh
// entry intact; a re-add at new coordinates is untouched by this delete.
func flushPokemonTreeEvictions(entries []treeEvictionEntry[uint64]) {
	pokemonTreeMutex.Lock()
	for _, e := range entries {
		pokemonTree.Delete([2]float64{e.lon, e.lat}, [2]float64{e.lon, e.lat}, e.id)
	}
	pokemonTreeMutex.Unlock()
}
```

In `initPokemonRtree()` (same file), create the evictor and replace the eviction callback body. Currently:

```go
	// Set up OnEviction callback on all shards
	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[uint64, *Pokemon]) {
		pokemon := v.Value()
		removePokemonFromTree(uint64(pokemon.Id), pokemon.Lat, pokemon.Lon)
		// Rely on the pokemon pvp lookup caches to remove themselves rather than trying to synchronise
	})
```

Replace with:

```go
	pokemonTreeEvictor = newTreeEvictor[uint64]("pokemon", treeEvictorQueueSize, treeEvictorBatchSize, flushPokemonTreeEvictions)

	// This callback runs synchronously inside ttlcache's expiry sweep,
	// which holds the shard's write lock for the WHOLE sweep — it must be
	// O(1). Lookup-cache removal is lock-free and happens inline so scans
	// stop seeing the pokemon immediately; the tree removal is deferred
	// to the batched evictor.
	pokemonCache.OnEviction(func(ctx context.Context, ev ttlcache.EvictionReason, v *ttlcache.Item[uint64, *Pokemon]) {
		pokemon := v.Value()
		pokemonId := uint64(pokemon.Id)
		if item, ok := pokemonLookupCache.LoadAndDelete(pokemonId); ok && item.PokemonLookup != nil {
			adjustPokemonFormCount(pokemonFormKey{item.PokemonLookup.PokemonId, item.PokemonLookup.Form}, -1)
		}
		pokemonTreeEvictor.Enqueue(pokemonId, pokemon.Lat, pokemon.Lon)
	})
```

`removePokemonFromTree` keeps its current body — direct callers (position changes in `savePokemonRecordAsAtTime`, manual clears) still use it for single, immediate removals.

- [ ] **Step 4: Run tests**

Run: `go test ./decoder/ -run 'TestFlushPokemonTreeEvictions|TestTreeEvictor' -count=1 -race && go build ./...`
Expected: PASS, clean build

- [ ] **Step 5: Commit**

```bash
git add decoder/pokemonRtree.go decoder/pokemonRtree_evict_test.go
git commit -m "perf: make pokemon cache eviction O(1), batch rtree removals"
```

---

### Task 3: Fort/station evictions through the evictor

**Files:**
- Modify: `decoder/fortRtree.go` (evictor + flush func, created in `initFortRtree`)
- Modify: `decoder/main.go:106-136` (three eviction callbacks)
- Test: `decoder/fortRtree_evict_test.go`

**Interfaces:**
- Consumes: `newTreeEvictor`, `treeEvictionEntry`, constants `treeEvictorQueueSize`/`treeEvictorBatchSize` from Tasks 1–2.
- Produces: `fortTreeEvictor *treeEvictor[string]`, `flushFortTreeEvictions(entries []treeEvictionEntry[string])`, `deferFortEviction(fortId string, lat, lon float64)`.

- [ ] **Step 1: Write the failing test**

Create `decoder/fortRtree_evict_test.go`:

```go
package decoder

import "testing"

func TestFlushFortTreeEvictionsRemovesPoints(t *testing.T) {
	ids := []string{"fort-evict-a", "fort-evict-b"}
	fortTreeMutex.Lock()
	for _, id := range ids {
		fortTree.Insert([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, id)
	}
	fortTreeMutex.Unlock()

	inTree := func(id string) bool {
		found := false
		fortTreeMutex.RLock()
		fortTree.Search([2]float64{9.5, 8.5}, [2]float64{9.5, 8.5}, func(_, _ [2]float64, v string) bool {
			if v == id {
				found = true
				return false
			}
			return true
		})
		fortTreeMutex.RUnlock()
		return found
	}

	for _, id := range ids {
		if !inTree(id) {
			t.Fatalf("setup: %s not in tree", id)
		}
	}

	flushFortTreeEvictions([]treeEvictionEntry[string]{
		{id: "fort-evict-a", lat: 8.5, lon: 9.5},
		{id: "fort-evict-b", lat: 8.5, lon: 9.5},
	})

	for _, id := range ids {
		if inTree(id) {
			t.Errorf("%s still in tree after flush", id)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestFlushFortTreeEvictions -count=1`
Expected: FAIL — `undefined: flushFortTreeEvictions`

- [ ] **Step 3: Implement**

In `decoder/fortRtree.go`, next to `evictFortFromTree` (line 261), add:

```go
var fortTreeEvictor *treeEvictor[string]

// flushFortTreeEvictions removes a batch of evicted forts from the spatial
// index under a single tree-mutex acquisition (same re-add reasoning as
// flushPokemonTreeEvictions).
func flushFortTreeEvictions(entries []treeEvictionEntry[string]) {
	fortTreeMutex.Lock()
	for _, e := range entries {
		fortTree.Delete([2]float64{e.lon, e.lat}, [2]float64{e.lon, e.lat}, e.id)
	}
	fortTreeMutex.Unlock()
}

// deferFortEviction is the O(1) eviction-callback path: lookup cache is
// cleared inline (lock-free) so scans skip the fort immediately, tree
// removal is batched.
func deferFortEviction(fortId string, lat, lon float64) {
	fortLookupCache.Delete(fortId)
	fortTreeEvictor.Enqueue(fortId, lat, lon)
}
```

In `initFortRtree()` (find it in `decoder/fortRtree.go`; it initializes `fortLookupCache`), add as the first line:

```go
	fortTreeEvictor = newTreeEvictor[string]("fort", treeEvictorQueueSize, treeEvictorBatchSize, flushFortTreeEvictions)
```

**Ordering check:** `decoder/main.go` registers the fort eviction callbacks (lines 106-136) *before* `initFortRtree()` runs (line 173). Callbacks cannot fire before `Start()`'s first sweep ≥ TTL later, so this is safe, but do not reorder init without checking.

In `decoder/main.go`, replace the three callback bodies' `evictFortFromTree(x.Id, x.Lat, x.Lon)` calls with `deferFortEviction(x.Id, x.Lat, x.Lon)`:

```go
	if config.Config.FortInMemory {
		pokestopCache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Pokestop]) {
			p := item.Value()
			deferFortEviction(p.Id, p.Lat, p.Lon)
		})
	}
```

(same one-line change inside the gym callback and the station callback; the station callback keeps its `clearStationBattleState(item.Key())` line unchanged).

`evictFortFromTree` keeps its current body for direct callers (`clearGymWithLock`, fort-type conversions etc. — check callers with `grep -n "evictFortFromTree" decoder/*.go` and leave them as-is).

- [ ] **Step 4: Run tests**

Run: `go test ./decoder/ -run 'TestFlushFortTreeEvictions' -count=1 -race && go build ./...`
Expected: PASS, clean build

- [ ] **Step 5: Commit**

```bash
git add decoder/fortRtree.go decoder/fortRtree_evict_test.go decoder/main.go
git commit -m "perf: make fort cache evictions O(1), batch rtree removals"
```

---

### Task 4: Shared scan snapshots instead of per-request tree copies

**Files:**
- Modify: `decoder/pokemonRtree.go` (snapshot helper)
- Modify: `decoder/fortRtree.go` (snapshot helper)
- Modify: `decoder/api_pokemon.go:83-85`, `decoder/api_pokemon_common.go:84-86`, `decoder/api_pokemon_scan_v1.go:170-172`, `decoder/weather_iv.go:56-58`, `decoder/api_fort.go:248-250`, `decoder/api_fort.go:437-439`
- Test: `decoder/rtree_snapshot_test.go`

**Interfaces:**
- Produces: `getPokemonTreeSnapshot() *rtree.RTreeG[uint64]`, `getFortTreeSnapshot() *rtree.RTreeG[string]`, `invalidateTreeSnapshots()` (test helper).

**Why:** every `Copy()` puts the live tree back into copy-on-write mode, so at high scan rates every insert/delete pays node cloning. One shared snapshot, refreshed at most once per second, amortizes that cost across all queries. Snapshots are read-only (`Search` only) so concurrent use is safe. ≤1s staleness is acceptable for map queries and for `ProactiveIVSwitch` (it re-verifies each candidate via lookup cache + a locked peek).

- [ ] **Step 1: Write the failing test**

Create `decoder/rtree_snapshot_test.go`:

```go
package decoder

import (
	"testing"
	"time"
)

func TestPokemonTreeSnapshotIsReusedWithinMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getPokemonTreeSnapshot()
	s2 := getPokemonTreeSnapshot()
	if s1 != s2 {
		t.Error("expected snapshot to be reused within max age")
	}
}

func TestPokemonTreeSnapshotRefreshesAfterMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getPokemonTreeSnapshot()
	// Backdate the stored snapshot instead of sleeping.
	if snap := pokemonTreeSnapshot.Load(); snap != nil {
		snap.createdAt = time.Now().Add(-2 * treeSnapshotMaxAge)
	}
	s2 := getPokemonTreeSnapshot()
	if s1 == s2 {
		t.Error("expected a fresh snapshot after max age")
	}
}

func TestFortTreeSnapshotIsReusedWithinMaxAge(t *testing.T) {
	invalidateTreeSnapshots()
	defer invalidateTreeSnapshots()

	s1 := getFortTreeSnapshot()
	s2 := getFortTreeSnapshot()
	if s1 != s2 {
		t.Error("expected fort snapshot to be reused within max age")
	}
}
```

Note: backdating `snap.createdAt` races nothing in tests (no concurrent scanners), and keeps the test instant.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TreeSnapshot -count=1`
Expected: FAIL — `undefined: invalidateTreeSnapshots` etc.

- [ ] **Step 3: Implement the helpers**

In `decoder/pokemonRtree.go` add (near the tree vars; add `"sync/atomic"` and `"time"` to imports if missing):

```go
// treeSnapshotMaxAge bounds scan-snapshot staleness. Scans re-verify hits
// against the lookup caches (and lock records for final results), so a
// slightly stale spatial index only costs a few extra skips/misses.
const treeSnapshotMaxAge = time.Second

type treeSnapshot[K comparable] struct {
	tree      rtree.RTreeG[K]
	createdAt time.Time
}

var pokemonTreeSnapshot atomic.Pointer[treeSnapshot[uint64]]

// getPokemonTreeSnapshot returns a read-only spatial index snapshot shared
// by all scans, refreshed at most every treeSnapshotMaxAge. This replaces
// per-request Copy(), which kept the live tree permanently copy-on-write.
// Callers must only call Search on the result.
func getPokemonTreeSnapshot() *rtree.RTreeG[uint64] {
	if snap := pokemonTreeSnapshot.Load(); snap != nil && time.Since(snap.createdAt) < treeSnapshotMaxAge {
		return &snap.tree
	}
	pokemonTreeMutex.RLock()
	tree := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()
	snap := &treeSnapshot[uint64]{tree: tree, createdAt: time.Now()}
	pokemonTreeSnapshot.Store(snap)
	return &snap.tree
}

// invalidateTreeSnapshots is a test helper.
func invalidateTreeSnapshots() {
	pokemonTreeSnapshot.Store(nil)
	fortTreeSnapshot.Store(nil)
}
```

In `decoder/fortRtree.go` add:

```go
var fortTreeSnapshot atomic.Pointer[treeSnapshot[string]]

// getFortTreeSnapshot: see getPokemonTreeSnapshot.
func getFortTreeSnapshot() *rtree.RTreeG[string] {
	if snap := fortTreeSnapshot.Load(); snap != nil && time.Since(snap.createdAt) < treeSnapshotMaxAge {
		return &snap.tree
	}
	fortTreeMutex.RLock()
	tree := fortTree.Copy()
	fortTreeMutex.RUnlock()
	snap := &treeSnapshot[string]{tree: tree, createdAt: time.Now()}
	fortTreeSnapshot.Store(snap)
	return &snap.tree
}
```

- [ ] **Step 4: Replace the six call sites**

Each currently reads (pokemon variant):

```go
	pokemonTreeMutex.RLock()
	pokemonTree2 := pokemonTree.Copy()
	pokemonTreeMutex.RUnlock()
```

Replace with:

```go
	pokemonTree2 := getPokemonTreeSnapshot()
```

Sites and their local variable names (keep the variable name, only change how it's produced; `pokemonTree2.Search(...)` works identically on the pointer):
1. `decoder/api_pokemon.go:83` — var `pokemonTree2`
2. `decoder/api_pokemon_common.go:84` — var `pokemonTree2` (the `lockedTime := time.Since(start)` line after it stays)
3. `decoder/api_pokemon_scan_v1.go:170` — var `pokemonTree2` (keep `lockedTime`)
4. `decoder/weather_iv.go:56-58` — var `pokemonTree2` (keep the `start`/`lockedTime` bookkeeping around it)
5. `decoder/api_fort.go:248` — var `fortTreeCopy` → `fortTreeCopy := getFortTreeSnapshot()` (keep `lockedTime`)
6. `decoder/api_fort.go:437` — same as 5

Also check `decoder/api_pokemon_common.go:89` `totalPokemon := pokemonTree2.Len()` — `Len()` on the shared snapshot is fine.

- [ ] **Step 5: Run tests and build**

Run: `go test ./decoder/ -run TreeSnapshot -count=1 -race && go build ./... && go test ./decoder/ -count=1`
Expected: PASS (full decoder suite guards the call-site edits)

- [ ] **Step 6: Commit**

```bash
git add decoder/pokemonRtree.go decoder/fortRtree.go decoder/rtree_snapshot_test.go decoder/api_pokemon.go decoder/api_pokemon_common.go decoder/api_pokemon_scan_v1.go decoder/weather_iv.go decoder/api_fort.go
git commit -m "perf: share 1s rtree snapshots across scans instead of per-request copies"
```

---

### Task 5: TTL jitter + past-despawn clamp + rehydration TTL

**Files:**
- Modify: `decoder/pokemon_decode.go:68-77` (`remainingDuration`)
- Modify: `decoder/pokemon_state.go:83-87` (`getPokemonRecordReadOnly` rehydration TTL)
- Test: `decoder/pokemon_ttl_test.go`

**Behavior changes (intentional, documented in commit):**
- Unverified pokemon: cache TTL was flat 60min (`ttlcache.DefaultTTL`); becomes 55–65min with per-pokemon random jitter, so restart-synchronized cohorts spread across a 10-minute window instead of one sweep.
- Verified pokemon at/past despawn (`timeLeft <= 60`): was a fresh 60 minutes; becomes 1 minute.
- DB-rehydrated pokemon (cache miss in non-memory-only mode): cached with `remainingDuration` instead of the flat default.

- [ ] **Step 1: Write the failing test**

Create `decoder/pokemon_ttl_test.go`:

```go
package decoder

import (
	"testing"
	"time"

	"github.com/guregu/null/v6"
)

func TestRemainingDurationVerifiedFutureDespawn(t *testing.T) {
	now := int64(1_000_000)
	p := &Pokemon{PokemonData: PokemonData{
		ExpireTimestampVerified: true,
		ExpireTimestamp:         null.IntFrom(now + 600),
	}}
	got := p.remainingDuration(now)
	if want := 660 * time.Second; got != want {
		t.Errorf("remainingDuration = %v, want %v", got, want)
	}
}

func TestRemainingDurationVerifiedPastDespawnClampsToOneMinute(t *testing.T) {
	now := int64(1_000_000)
	p := &Pokemon{PokemonData: PokemonData{
		ExpireTimestampVerified: true,
		ExpireTimestamp:         null.IntFrom(now - 300),
	}}
	if got := p.remainingDuration(now); got != time.Minute {
		t.Errorf("remainingDuration past despawn = %v, want 1m (was previously a fresh hour)", got)
	}
}

func TestRemainingDurationUnverifiedIsJittered(t *testing.T) {
	p := &Pokemon{PokemonData: PokemonData{ExpireTimestampVerified: false}}
	seen := map[time.Duration]bool{}
	for range 200 {
		d := p.remainingDuration(0)
		if d < pokemonUnverifiedTTL || d >= pokemonUnverifiedTTL+pokemonUnverifiedTTLJitter {
			t.Fatalf("jittered TTL %v outside [%v, %v)", d, pokemonUnverifiedTTL, pokemonUnverifiedTTL+pokemonUnverifiedTTLJitter)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Errorf("expected jitter to produce varied TTLs, got %d distinct values", len(seen))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestRemainingDuration -count=1`
Expected: FAIL — `undefined: pokemonUnverifiedTTL` (and the clamp test fails against current behavior)

- [ ] **Step 3: Implement**

In `decoder/pokemon_decode.go` replace `remainingDuration` (lines 68-77) with (add `"math/rand/v2"` import):

```go
const (
	// pokemonUnverifiedTTL (+ jitter) replaces the flat cache-default TTL
	// for pokemon without a verified despawn. The jitter prevents
	// restart-synchronized cohorts (preload + cold-cache ingest all
	// stamped in the same few minutes) from expiring in one giant
	// ttlcache sweep an hour later.
	pokemonUnverifiedTTL       = 55 * time.Minute
	pokemonUnverifiedTTLJitter = 10 * time.Minute
)

func (pokemon *Pokemon) remainingDuration(now int64) time.Duration {
	if pokemon.ExpireTimestampVerified {
		timeLeft := 60 + pokemon.ExpireTimestamp.ValueOrZero() - now
		if timeLeft > 60 {
			return time.Duration(timeLeft) * time.Second
		}
		// At/past despawn: keep briefly for late queries rather than
		// granting a fresh hour to a corpse.
		return time.Minute
	}
	return pokemonUnverifiedTTL + rand.N(pokemonUnverifiedTTLJitter)
}
```

Check the `ttlcache.DefaultTTL` import is still needed in that file after the edit (`grep -n ttlcache decoder/pokemon_decode.go`); drop the import only if now unused.

In `decoder/pokemon_state.go` `getPokemonRecordReadOnly`, the rehydration cache-insert (lines 83-87) currently:

```go
	existingPokemon, _ := pokemonCache.GetOrSetFunc(encounterId, func() *Pokemon {
		// Only called if key doesn't exist - our Pokemon wins
		pokemonRtreeUpdatePokemonOnGet(&dbPokemon)
		return &dbPokemon
	})
```

becomes:

```go
	existingPokemon, _ := pokemonCache.GetOrSetFunc(encounterId, func() *Pokemon {
		// Only called if key doesn't exist - our Pokemon wins
		pokemonRtreeUpdatePokemonOnGet(&dbPokemon)
		return &dbPokemon
	}, ttlcache.WithTTL[uint64, *Pokemon](dbPokemon.remainingDuration(time.Now().Unix())))
```

(add `"time"` / ttlcache imports if missing in that file — ttlcache is already imported).

- [ ] **Step 4: Run tests**

Run: `go test ./decoder/ -run TestRemainingDuration -count=1 && go test ./decoder/ -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add decoder/pokemon_decode.go decoder/pokemon_state.go decoder/pokemon_ttl_test.go
git commit -m "perf: jitter unverified pokemon TTLs, clamp past-despawn TTL to 1m

Unverified pokemon now expire 55-65min after last significant update
instead of exactly 60, so a restart's synchronized ingest cohort spreads
over a 10 minute window instead of detonating in a single expiry sweep.
Verified pokemon past despawn get 1 minute instead of a fresh hour, and
DB-rehydrated pokemon get a despawn-based TTL (from PR #277)."
```

---

### Task 6: Configurable cache shard count

**Files:**
- Modify: `config/config.go:133-145` (tuning struct)
- Modify: `decoder/main.go` (shard-count helper + 5 uses of `runtime.NumCPU()` in `NumShards:`)
- Test: `decoder/cache_shards_test.go`

**Interfaces:**
- Produces: config key `tuning.cache_shards` (0 = auto → `runtime.NumCPU()`), `cacheShardCount() int` in package decoder.

- [ ] **Step 1: Write the failing test**

Create `decoder/cache_shards_test.go`:

```go
package decoder

import (
	"runtime"
	"testing"

	"golbat/config"
)

func TestCacheShardCountDefaultsToNumCPU(t *testing.T) {
	old := config.Config.Tuning.CacheShards
	defer func() { config.Config.Tuning.CacheShards = old }()

	config.Config.Tuning.CacheShards = 0
	if got := cacheShardCount(); got != runtime.NumCPU() {
		t.Errorf("cacheShardCount() = %d, want NumCPU %d", got, runtime.NumCPU())
	}

	config.Config.Tuning.CacheShards = 64
	if got := cacheShardCount(); got != 64 {
		t.Errorf("cacheShardCount() = %d, want 64", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestCacheShardCount -count=1`
Expected: FAIL — `CacheShards` / `cacheShardCount` undefined

- [ ] **Step 3: Implement**

In `config/config.go`, add to the `tuning` struct:

```go
	CacheShards                    int     `koanf:"cache_shards"` // shards per in-memory entity cache; 0 = number of CPUs. Raise on very large instances to shrink per-shard expiry sweeps.
```

(No `config/reader.go` default needed — the zero value is the auto default.)

In `decoder/main.go`, add near the top of the cache-init function:

```go
// cacheShardCount resolves tuning.cache_shards; more shards mean smaller
// per-shard expiry sweeps and finer-grained write locking at the cost of
// more cleanup goroutines.
func cacheShardCount() int {
	if n := config.Config.Tuning.CacheShards; n > 0 {
		return n
	}
	return runtime.NumCPU()
}
```

Then replace all five `NumShards: runtime.NumCPU(),` occurrences (pokestop line 102, gym line 114, station line 126, spawnpoint line 159, pokemon line 167) with `NumShards: cacheShardCount(),`. Check `runtime` is still used elsewhere in the file; remove the import only if unused.

- [ ] **Step 4: Run tests**

Run: `go test ./decoder/ -run TestCacheShardCount -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add config/config.go decoder/main.go decoder/cache_shards_test.go
git commit -m "feat: tuning.cache_shards to size sharded entity caches"
```

---

### Task 7: Bounded raw-proto processing concurrency

**Files:**
- Modify: `config/config.go` (tuning struct)
- Create: `raw_limiter.go` (package main)
- Modify: `routes.go:351` and `grpc_server_raw.go:93` (wrap processing goroutines)
- Modify: `main.go` (call `initRawProcessingLimiter()` after config load, near `decoder.InitWriteBehindQueue` at main.go:213)
- Test: `raw_limiter_test.go` (package main)

**Interfaces:**
- Produces: config key `tuning.raw_processing_concurrency` (0 = auto `min(4×NumCPU, 96)`, -1 = unlimited), `initRawProcessingLimiter()`, `acquireRawProcessingSlot() (release func())`.

**Why:** every `/raw` POST and gRPC submission spawns an unbounded goroutine. During any stall, thousands pile into the entity-lock convoys and amplify it. A semaphore parks the excess *outside* the lock graph. This is a guardrail, not the fix.

- [ ] **Step 1: Write the failing test**

Create `raw_limiter_test.go`:

```go
package main

import (
	"testing"
	"time"

	"golbat/config"
)

func TestRawLimiterBoundsConcurrency(t *testing.T) {
	old := config.Config.Tuning.RawProcessingConcurrency
	defer func() {
		config.Config.Tuning.RawProcessingConcurrency = old
		rawProcessingSem = nil
	}()

	config.Config.Tuning.RawProcessingConcurrency = 2
	initRawProcessingLimiter()

	r1 := acquireRawProcessingSlot()
	r2 := acquireRawProcessingSlot()

	acquired := make(chan struct{})
	go func() {
		r3 := acquireRawProcessingSlot()
		close(acquired)
		r3()
	}()

	select {
	case <-acquired:
		t.Fatal("third slot acquired despite limit of 2")
	case <-time.After(50 * time.Millisecond):
	}

	r1()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("third slot not acquired after release")
	}
	r2()
}

func TestRawLimiterUnlimited(t *testing.T) {
	old := config.Config.Tuning.RawProcessingConcurrency
	defer func() {
		config.Config.Tuning.RawProcessingConcurrency = old
		rawProcessingSem = nil
	}()

	config.Config.Tuning.RawProcessingConcurrency = -1
	initRawProcessingLimiter()
	if rawProcessingSem != nil {
		t.Fatal("expected nil semaphore for unlimited config")
	}
	release := acquireRawProcessingSlot()
	release() // must not panic
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestRawLimiter -count=1`
Expected: FAIL — undefined identifiers

- [ ] **Step 3: Implement**

Add to the `tuning` struct in `config/config.go`:

```go
	RawProcessingConcurrency       int     `koanf:"raw_processing_concurrency"` // max concurrent raw-proto processing goroutines; 0 = auto (4x CPUs, capped at 96), -1 = unlimited
```

Create `raw_limiter.go`:

```go
package main

import (
	"runtime"

	"golbat/config"
)

// rawProcessingSem bounds concurrent raw-proto processing goroutines
// (HTTP /raw and gRPC). nil means unlimited. Excess submissions park here
// instead of piling into entity-lock convoys during a stall; ingestion
// endpoints still respond immediately.
var rawProcessingSem chan struct{}

func initRawProcessingLimiter() {
	n := config.Config.Tuning.RawProcessingConcurrency
	switch {
	case n < 0:
		rawProcessingSem = nil
		return
	case n == 0:
		n = min(4*runtime.NumCPU(), 96)
	}
	rawProcessingSem = make(chan struct{}, n)
}

// acquireRawProcessingSlot blocks until a processing slot is free and
// returns the release func. Always call the returned func exactly once.
func acquireRawProcessingSlot() func() {
	sem := rawProcessingSem
	if sem == nil {
		return func() {}
	}
	sem <- struct{}{}
	return func() { <-sem }
}
```

In `routes.go:351`, the processing goroutine currently starts:

```go
	// Process each proto in a packet in sequence, but in a go-routine
	go func() {
		timeout := 5 * time.Second
```

becomes:

```go
	// Process each proto in a packet in sequence, but in a go-routine
	go func() {
		release := acquireRawProcessingSlot()
		defer release()

		timeout := 5 * time.Second
```

In `grpc_server_raw.go:93`, same insertion at the top of its `go func() {`.

In `main.go`, after config load and before the server starts accepting traffic (next to `decoder.InitWriteBehindQueue(ctx, dbDetails)` around line 213), add:

```go
	initRawProcessingLimiter()
```

- [ ] **Step 4: Run tests**

Run: `go test . -run TestRawLimiter -count=1 -race && go build ./... && go test ./... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add config/config.go raw_limiter.go raw_limiter_test.go routes.go grpc_server_raw.go main.go
git commit -m "feat: tuning.raw_processing_concurrency bounds raw decode goroutines"
```

---

### Task 8: Full verification pass

- [ ] **Step 1: Full build, vet, race tests**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -count=1 -race
```
Expected: no gofmt output, vet clean, all tests pass.

- [ ] **Step 2: Eyeball the diff for stray changes**

```bash
git diff main --stat
```
Expected: only the files named in Tasks 1–7.
