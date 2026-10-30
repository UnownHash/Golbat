# Tappable Scan API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `POST /api/tappable/scan` returns tappables in a bounding box, filtered by DNF clauses, served from an in-memory R-tree.

**Architecture:** A tappable R-tree + lock-free lookup cache mirror `pokemonRtree.go`; all runtime tree mutations go through a `treeEvictor[uint64]` writer. `tappableCache` gets per-entry TTL from `expire_timestamp` so evictions track real expiry and drive tree deletes. Unexpired tappables are preloaded at startup.

**Tech Stack:** Go, `github.com/tidwall/rtree`, `github.com/puzpuzpuz/xsync/v4`, Huma v2, otter v2 (via `ottercache`).

**Spec:** `docs/superpowers/specs/2026-10-28-tappable-scan-api-design.md`

## Global Constraints

- Build/test with `-tags go_json`: `go test -tags go_json ./decoder/` and `go test -tags go_json .`.
- Branch `feat/tappable-scan-api`, stacked on `feat/tappable-reward-type`. Do not push.
- Savers holding a tappable lock never take `tappableTreeMutex`; only the writer and `refreshTreeSnapshot` do. Preload is the only direct insert.
- Eviction callbacks never block: tree deletes from them use `TryEnqueue`.
- DNF semantics as fort scans: omitted/null list = no constraint, explicit empty list = matches nothing; clauses OR'd, fields AND'd.
- `tuning.max_tappable_results` default `9000`.
- Lookup sentinel `-1` = absent for `RewardType`, `ItemId`, `RewardPokemonId`, `PokemonId`; `ExpireTimestamp` `0` = unknown.
- Comments and docs terse and factual.
- Additions beyond the spec: `limits.max_tappable_results` in `/api/status` (Task 3), `api.md` section (Task 6), `tappable` in `golbat_worker_backlog` (Task 2).

## Review Focus

1. Unverified tappable re-seen (expire extended by `setUnknownTimestamp`) must not be evicted at its old expiry → Task 1 `TestTappableSaveExtendsCacheTTL`.
2. `reward_type` names are case-sensitive proto names; `"Item"` or `"candy"` → 400, not an empty result → Task 3 `TestTappableScanUnknownRewardType`.
3. Inverted box (`min` > `max`) returns an empty list with 200, as fort scans → Task 3 `TestTappableScanInvertedBox`.
4. Negative `limit` behaves as 0 (server default) → Task 3 `TestTappableScanLimit`.
5. An encounter-only clause (`pokemon_id`) must not match reward tappables, and a reward clause must not match encounters → Task 3 `TestTappableDnfMatch` cases `encounter clause vs reward tappable`, `reward clause vs encounter tappable`.

---

### Task 1: Per-entry tappable cache TTL

**Files:**
- Modify: `decoder/main.go` (tappableCache config, ~line 205)
- Modify: `decoder/tappable_state.go` (`saveTappableRecord`)
- Create: `decoder/tappable_ttl.go`
- Test: `decoder/tappable_ttl_test.go`

**Interfaces:**
- Produces:
  - `func tappableCacheTTL(t *Tappable, now int64) time.Duration`
  - `var tappableCacheUpdateTTL = func(id uint64, ttl time.Duration) { tappableCache.UpdateTTL(id, ttl) }` (test seam)
  - `Tappable.cachedExpire int64` (`db:"-"`): expire value the current cache TTL was computed from

- [ ] **Step 1: Write failing tests** in `decoder/tappable_ttl_test.go`:
  - `TestTappableCacheTTL` table, `now = 1_700_000_000`:
    - expire `now+300` → `360s`
    - expire `now-600` → `60s` (1 min floor)
    - expire `now+30` → `90s`
    - no expire (`null.Int{}`) → `60*time.Minute`
  - `TestTappableSaveExtendsCacheTTL`: swap `tappableCacheUpdateTTL` for a recorder; new tappable id 9001 with `ExpireTimestamp = now+120`, `saveTappableRecord` (recorder not called, entry cached); set `ExpireTimestamp = now+1200`, save → recorder called once with id 9001 and `1260s ± 2s`.
  - `TestTappableSaveNoTTLUpdateWhenExpireUnchanged`: second save with the same expire (dirty via `SetType`) → recorder not called.

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run 'TestTappableCacheTTL|TestTappableSave'` → FAIL (undefined `tappableCacheTTL`).

- [ ] **Step 3: Implement**
  - `tappableCacheTTL`: `expire + 60 - now` seconds, floor `time.Minute`; invalid expire → `60*time.Minute`.
  - `decoder/main.go`: `TouchOnHit: false` for `tappableCache`.
  - `saveTappableRecord`: new record → `tappableCache.Set(id, t, tappableCacheTTL(t, now))`; existing record with `ExpireTimestamp.Int64 != cachedExpire` → `tappableCacheUpdateTTL(id, tappableCacheTTL(t, now))`. Set `cachedExpire = ExpireTimestamp.Int64` in both cases.
  - `cachedExpire` grows `Tappable`: update the size pin in `decoder/entity_sizes_test.go` (264 → 272, class 288 unchanged) with a one-line comment.

- [ ] **Step 4: Run** the Step 2 command and `go test -tags go_json ./decoder/ -run 'Tappable|Sizes'` → PASS.

- [ ] **Step 5: Commit** `feat: tappable cache TTL follows expire_timestamp`

### Task 2: Tappable R-tree, lookup cache, writer, eviction

**Files:**
- Create: `decoder/tappableRtree.go`
- Modify: `decoder/main.go` (call `initTappableRtree()` after `initFortRtree()`)
- Modify: `decoder/tappable_state.go` (`saveTappableRecord` calls the save hook)
- Test: `decoder/tappableRtree_test.go`

**Interfaces:**
- Consumes: `lootItemTypeOneof` (`decoder/tappable_decode.go`), `newTreeEvictor`, `flushTreeEvictions`, `refreshTreeSnapshot`, `treeEvictorBatchSize`.
- Produces:
  - `type TappableLookup struct { Lat, Lon float64; ExpireTimestamp int64; RewardType int8; ItemId, RewardPokemonId, PokemonId int16 }`
  - `var lootRewardTypeNames []string` — built from `lootItemTypeOneof.Fields()` in order.
  - `func lootRewardTypeIndex(name string) (int8, bool)`
  - `var tappableLookupCache *xsync.Map[uint64, TappableLookup]`
  - `func getTappableTreeSnapshot() *rtree.RTreeG[uint64]`
  - `func tappableRtreeUpdateOnSave(t *Tappable)` — caller holds the lock
  - `func tappablePreloadInsert(t *Tappable)` — direct tree insert + lookup
  - `func handleTappableEviction(t *Tappable)`
  - `func buildTappableLookup(t *Tappable) TappableLookup`

- [ ] **Step 1: Write failing tests** in `decoder/tappableRtree_test.go`. Each test uses unique ids and calls a helper `drainTappableTree(t)` that waits until `tappableTreeEvictor.QueueLen() == 0` then forces a snapshot refresh (reset `tappableTreeSnapshot` to nil).
  - `TestTappableLookupSize`: `unsafe.Sizeof(TappableLookup{}) == 32`.
  - `TestLootRewardTypeIndex`: `"item"` → 0, `"pokemon_candy"` → 3, `"temp_evo_pokemon_branch"` → 14, `"nope"` → `(0,false)`, `len(lootRewardTypeNames) == 15`.
  - `TestBuildTappableLookup`: candy tappable (`RewardType "pokemon_candy"`, `RewardPokemonId 25`, expire set) → `{RewardType:3, ItemId:-1, RewardPokemonId:25, PokemonId:-1, ExpireTimestamp: expire}`; encounter tappable (`Encounter 25`, no reward) → `RewardType:-1, PokemonId:25`; no expire → `ExpireTimestamp:0`.
  - `TestTappableSaveInsertsTree`: `tappableRtreeUpdateOnSave` on a new tappable → lookup present, tree search at its point returns the id.
  - `TestTappableSaveMoveReinserts`: save at A, move to B, save → search at A empty, at B returns id.
  - `TestTappableEvictionDeletes`: save, then `handleTappableEviction` with the tappable not in `tappableCache` → lookup gone, tree empty at point.
  - `TestTappableEvictionSkipsRecached`: tappable present in `tappableCache` (different `*Tappable` for the same id) → lookup and tree point remain.
  - `TestTappableSaveSelfHealsMissingLookup`: delete the lookup entry by hand after a save, save again → lookup back, single tree point (search returns the id once after dedupe).
  - `TestTappablePreloadInsert`: lookup + tree point present without the evictor (queue length 0 throughout).

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run 'Tappable(Lookup|Save|Eviction|Preload)|LootRewardType|BuildTappableLookup'` → FAIL (undefined).

- [ ] **Step 3: Implement** `decoder/tappableRtree.go`:
  - Globals: `tappableTree rtree.RTreeG[uint64]`, `tappableTreeMutex sync.RWMutex`, `tappableTreeSnapshot atomic.Pointer[treeSnapshot[uint64]]`, `tappableTreeEvictor *treeEvictor[uint64]`, `tappableLookupCache`.
  - `initTappableRtree()`: evictor `newTreeEvictor[uint64]("tappable", 65536, treeEvictorBatchSize, flushTappableTreeEvictions)`, lookup map, then `tappableCache.OnEviction(func(_ uint64, t *Tappable, _ ottercache.EvictionReason) { handleTappableEviction(t) })`.
  - `tappableRtreeUpdateOnSave`: old lookup missing → `EnqueueInsert`; coords changed → `Enqueue(old)` + `EnqueueInsert(new)`; always `Store(buildTappableLookup(t))`.
  - `handleTappableEviction`: lock (`"cacheEviction"`); `tappableCache.Has(id)` → return; `LoadAndDelete` the lookup; if found, `TryEnqueue(id, lookup.Lat, lookup.Lon)`.
  - Add `tappableTreeEvictor` to `StartWorkerBacklogReporter` (`decoder/stats.go:269`) alongside the pokemon/fort evictors.
  - `decoder/tappable_state.go`: `saveTappableRecord` calls `tappableRtreeUpdateOnSave(tappable)` after `ClearDirty()`.

- [ ] **Step 4: Run** the Step 2 command, then `go test -tags go_json ./decoder/` → PASS.

- [ ] **Step 5: Commit** `feat: tappable R-tree and lookup cache`

### Task 3: Scan endpoint with DNF filters

**Files:**
- Create: `decoder/api_tappable_scan.go`
- Modify: `config/config.go` (`Tuning.MaxTappableResults int \`koanf:"max_tappable_results"\``), `config/reader.go` (default `9000`), `config.toml.example` (line after `max_fort_results`)
- Modify: `decoder/api_status.go` (`Limits.MaxTappableResults`, json `max_tappable_results`)
- Test: `decoder/api_tappable_scan_test.go`

**Interfaces:**
- Consumes: Task 2 lookup cache, snapshot, `lootRewardTypeIndex`; `PeekTappableRecord`, `BuildTappableResult`, `updatedStrictlyAfter`, `ApiLatLon`.
- Produces:
  - `type ApiTappableDnfFilter struct { RewardType []string \`json:"reward_type"\`; ItemId, RewardPokemonId, PokemonId []int16 }` (json `item_id`, `reward_pokemon_id`, `pokemon_id`; all `required:"false"`)
  - `type ApiTappableScan struct { Min, Max ApiLatLon; Limit int; UpdatedAfter int64; DnfFilters []ApiTappableDnfFilter \`json:"filters"\` }`
  - `type ApiTappableScanResult struct { Tappables []*ApiTappableResult; Examined, Skipped, Total int; LimitReached bool }` (json `tappables`, `examined`, `skipped`, `total`, `limit_reached`)
  - `func TappableScanEndpoint(p ApiTappableScan) (*ApiTappableScanResult, error)` — error only for invalid `reward_type`, message `unknown reward_type "<name>"`
  - `func tappableScanLimit(limit int) int`

- [ ] **Step 1: Write failing tests** in `decoder/api_tappable_scan_test.go` (seed via Task 2's `tappablePreloadInsert` plus `tappableCache.Set`; set `config.Config.Tuning.MaxTappableResults = 100` with restore):
  - `TestTappableDnfMatch` table on `isTappableDnfMatch(&lookup, &compiledFilter, now)`: no fields → match; `reward_type ["item"]` vs item lookup → match, vs candy → no; `item_id [1301]` vs 1301 → match, vs 1 → no; empty `item_id []` → no; `reward_pokemon_id [25]` vs candy 25 → match; `encounter clause vs reward tappable` (`pokemon_id [25]` vs candy-25 lookup, `PokemonId -1`) → no; `reward clause vs encounter tappable` (`reward_type ["pokemon_candy"]` vs encounter) → no; two fields AND'd, one failing → no.
  - `TestTappableScanBox`: 3 tappables, 2 inside → 2 results, `examined == 2`, `total == 3`.
  - `TestTappableScanOrClauses`: rare candy, pikachu candy, stardust; filters `[{reward_type:[item], item_id:[1301]}, {reward_pokemon_id:[25]}]` → 2 results.
  - `TestTappableScanExpiryGate`: `ExpireTimestamp = now-1` → excluded; `0` → included.
  - `TestTappableScanLimit`: 5 matches, `limit 2` → 2 results, `limit_reached true`; `limit -1` → 5 results (server default 100), `limit_reached false`.
  - `TestTappableScanUpdatedAfter`: `Updated` 100 and 200, `updated_after 150` → only the 200 one; `examined == 2`.
  - `TestTappableScanUnknownRewardType`: `reward_type ["Item"]` → error containing `unknown reward_type "Item"`; same for `"candy"`.
  - `TestTappableScanInvertedBox`: `min` > `max` → 0 results, nil error.

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run 'TappableDnf|TappableScan'` → FAIL.

- [ ] **Step 3: Implement**
  - Compile filters once per request: `type tappableDnfCompiled struct { rewardType, itemId, rewardPokemonId, pokemonId []int16; hasRewardType, hasItemId, hasRewardPokemonId, hasPokemonId bool }` — `has*` = list non-nil (preserves omitted vs empty). Unknown `reward_type` → return the error before scanning.
  - `isTappableDnfMatch(l *TappableLookup, f *tappableDnfCompiled, now int64) bool` uses `slices.Contains`.
  - Scan follows `internalGetForts` (`decoder/api_fort.go:370`): snapshot search, lookup load (missing → skipped), expiry gate (`ExpireTimestamp != 0 && <= now` → skip), DNF, `seen` dedupe, stop at `tappableScanLimit`. Log line prefix `TappableScan`.
  - Results: per id `PeekTappableRecord(id, "API.TappableScan")`, `updatedStrictlyAfter(t.Updated, p.UpdatedAfter)`, `BuildTappableResult`, unlock.
  - `LimitReached: len(keys) >= tappableScanLimit(p.Limit)`.
  - `tappableScanLimit`: same shape as `fortScanLimit` with `MaxTappableResults`.
  - Status: `status.Limits.MaxTappableResults = config.Config.Tuning.MaxTappableResults`; extend the status test that asserts limits (grep `max_fort_results` in `*_test.go`).

- [ ] **Step 4: Run** the Step 2 command, then `go test -tags go_json ./decoder/ ./config/` → PASS.

- [ ] **Step 5: Commit** `feat: tappable scan with DNF filters`

### Task 4: Route registration

**Files:**
- Modify: `routes_huma.go` (`registerTier3Routes`, after `get-tappable`)
- Test: `huma_routes_test.go`

**Interfaces:**
- Consumes: `decoder.TappableScanEndpoint`, `decoder.ApiTappableScan`, `decoder.ApiTappableScanResult`.

- [ ] **Step 1: Write failing tests** in `huma_routes_test.go`:
  - In `TestTier3RoutesRegisterInSpec` `want`: `{"post", "/api/tappable/scan"}`.
  - In `TestTier3ReadEndpoints`: `t.Run("tappable/scan empty area is 200")` — POST `{"min":{"lat":1,"lon":1},"max":{"lat":1.001,"lon":1.001}}` → 200, body `tappables` is `[]`.
  - `t.Run("tappable/scan unknown reward_type is 400")` — filters `[{"reward_type":["Item"]}]` → 400.
  - Spec check: the operation for `/api/tappable/scan` post has a non-empty `security` list.

- [ ] **Step 2: Run** `go test -tags go_json . -run 'Tier3'` → FAIL.

- [ ] **Step 3: Implement** in `routes_huma.go`: input `struct{ Body decoder.ApiTappableScan }`, output `struct{ Body decoder.ApiTappableScanResult }`; operation `OperationID "scan-tappables"`, `POST /api/tappable/scan`, summary `Search tappables in a bounding box (DNF filters)`, `Tags ["Tappable"]`, `Security` as `get-tappable`, `DefaultStatus 200`, `draftBadge(&op)`. Endpoint error → `huma.Error400BadRequest(err.Error())`.

- [ ] **Step 4: Run** the Step 2 command and `go test -tags go_json .` → PASS.

- [ ] **Step 5: Commit** `feat: POST /api/tappable/scan route`

### Task 5: Preload

**Files:**
- Modify: `decoder/preload.go` (Phase 1 goroutine, final log line)
- Modify: `decoder/tappable_state.go` (extract `tappableSelectColumns` const from the SELECT in `loadTappableFromDatabase`)
- Test: `decoder/tappable_preload_test.go`

**Interfaces:**
- Consumes: `tappableCacheTTL` (Task 1), `tappablePreloadInsert` (Task 2).
- Produces: `func preloadTappables(dbDetails db.DbDetails) int32`; `func preloadTappableRow(t *Tappable, now int64) bool` (cache set + insert; false when skipped).

- [ ] **Step 1: Write failing tests** on `preloadTappableRow` (no DB):
  - expire `now+300` → true, `tappableCache.Has(id)`, lookup present.
  - expire `now-1` → false, not cached.
  - no expire → false, not cached.

- [ ] **Step 2: Run** `go test -tags go_json ./decoder/ -run TappablePreload` → FAIL.

- [ ] **Step 3: Implement**
  - `const tappableSelectColumns` used by `loadTappableFromDatabase` and the preload query.
  - `preloadTappables`: `SELECT ` + `tappableSelectColumns` + ` FROM tappable WHERE expire_timestamp > ?`, worker pool shaped like `preloadSpawnpoints` (`decoder/preload.go:321`), each row → `preloadTappableRow`; count via `atomic.AddInt32`.
  - `Preload`: add a 5th Phase 1 goroutine (`wg.Add(5)`), add `%d tappables` to the final `Preload: loaded ...` line.

- [ ] **Step 4: Run** the Step 2 command and `go test -tags go_json ./decoder/` → PASS.

- [ ] **Step 5: Commit** `feat: preload unexpired tappables`

### Task 6: Docs

**Files:**
- Modify: `CLAUDE.md`, `api.md`

- [ ] **Step 1: Edit `CLAUDE.md`**
  - Entities table: Tappable cache type `TTL` → `Otter (per-entry TTL)`.
  - Caching → Configuration: tappable TTL = `expire_timestamp` + 60 s (1 min floor), 60 min without expire, no touch-on-hit.
  - Spatial Indexes: `### Tappable R-tree (tappableRtree.go)` — lookup fields, writer-only mutations, eviction guard, DNF fields, `max_tappable_results`.
  - Preload list: tappables (Phase 1, unexpired only).
  - gRPC API paragraph: tappable scan is HTTP only.

- [ ] **Step 2: Edit `api.md`**: under Tappable Endpoints add `### POST /api/tappable/scan` (request/response per spec §5, filter semantics, 400 on unknown `reward_type`, list of the 15 names); add `max_tappable_results` to the status limits description.

- [ ] **Step 3: Verify** `go build -tags go_json ./... && go vet -tags go_json ./... && go test -tags go_json ./... && ~/go/bin/staticcheck -tags go_json ./decoder/ | grep -v U1000` → builds, all PASS, no new staticcheck lines.

- [ ] **Step 4: Commit** `docs: tappable scan API`
