# Tappable Scan API — Design Spec

- **Date:** 2026-10-28
- **Status:** Design approved, not built
- **Branch:** `feat/tappable-scan-api`, stacked on `feat/tappable-reward-type` (PR #428)
- **Origin:** PR #428 comment by Petap0w: frontends should get tappables in an area from the API, not the `tappable` table.

## 1. Problem

The only tappable endpoint is `GET /api/tappable/id/{id}`. Frontends read the DB for area queries. During events tappables reach ~20% of the pokemon count (events last up to a week), so an area scan must be indexed, not a walk over `tappableCache`.

## 2. Scope

In: R-tree + lookup cache for tappables, `POST /api/tappable/scan` with DNF filters, startup preload, per-entry cache TTL from `expire_timestamp`.

Out (addable later without breaking): gRPC `ScanTappables`, filtering on the tappable `type` string.

## 3. Cache TTL

Today `tappableCache` uses a flat 60 min TTL with touch-on-hit (`decoder/main.go`), so expired tappables stay resident up to an hour and evictions don't track real expiry.

Change:
- `TouchOnHit: false`.
- `tappableCacheTTL(t, now)`: `expire_timestamp + 60s - now`, minimum 1 min; no `expire_timestamp` → 60 min.
- `saveTappableRecord`: every save `Set`s with that TTL (not `UpdateTTL`: the upsert re-caches a tappable evicted mid-save, see `OtterCache`). A clean save (DB-loaded, unchanged) with no lookup entry also sets the TTL and indexes it.

## 4. Index (`decoder/tappableRtree.go`)

Modelled on `pokemonRtree.go` / `fortRtree.go`, reusing the generic pieces.

```go
type TappableLookup struct {
	Lat, Lon        float64
	ExpireTimestamp int64 // 0 = unknown
	RewardType      int8  // index into lootRewardTypeNames, -1 = none
	ItemId          int16 // -1 = none
	RewardPokemonId int16 // -1 = none
	PokemonId       int16 // encounter species, -1 = none
}
```

- `tappableTree rtree.RTreeG[uint64]`, `tappableTreeMutex sync.RWMutex`, snapshot via `refreshTreeSnapshot` (≤1 s staleness, scans hold no lock).
- `tappableLookupCache *xsync.Map[uint64, TappableLookup]`.
- `lootRewardTypeNames`: the 15 `LootItemProto` oneof names in field-number order (pinned by `TestTappableRewardTypeNamesPinned`); name ↔ int8 index.
- `tappableTreeEvictor = newTreeEvictor[uint64]("tappable", 65536, treeEvictorBatchSize, flush)`.

Lifecycle — all runtime tree mutations go through the writer, as pokemon (savers holding entity locks never take `tappableTreeMutex`; at ~20% of pokemon volume the fort-style direct add would risk the same saver convoy):
- **Save** (`saveTappableRecord`, entity lock held): lookup missing → `EnqueueInsert` + lookup store (also self-heals an eviction race); position changed → `Enqueue(old)` + `EnqueueInsert(new)`; always refresh the lookup. Blocking sends, as the pokemon savers.
- **Eviction** (`tappableCache.OnEviction`, Expired/Deleted only): lock the tappable; if the cache holds a different instance for the id (re-cached), skip; else delete the lookup entry and `TryEnqueue` the tree delete (dispatcher must not block; a drop leaves a ghost point, skipped by scans since the lookup is gone). Deletes match (coords, id), so stale ones are no-ops.
- **Preload**: `tappablePreloadInsert` inserts directly (pre-traffic), as `pokemonRtreePreloadInsert`.

Memory at ~200k tappables: lookup ~8 MB plus tree nodes. Always on, no config flag. A size test pins `TappableLookup`.

## 5. API (`decoder/api_tappable_scan.go`, `POST /api/tappable/scan`)

Request `ApiTappableScan`:

```jsonc
{
  "min": {"lat": 47.0, "lon": 8.0},
  "max": {"lat": 47.5, "lon": 8.6},
  "limit": 0,          // 0 = server default; clamped by tuning.max_tappable_results
  "updated_after": 0,  // applied on the locked record, as fort scans
  "filters": [         // OR'd; omitted/null/empty = all tappables
    {"reward_type": ["item"], "item_id": [1301]},
    {"reward_type": ["pokemon_candy"], "reward_pokemon_id": [25]},
    {"pokemon_id": [25]}
  ]
}
```

Clause fields: `reward_type` ([]string), `item_id`, `reward_pokemon_id`, `pokemon_id` ([]int16). AND within a clause. Omitted/null list = no constraint; explicit empty list = matches nothing (fort DNF semantics). Unknown `reward_type` name → 400.

Response `ApiTappableScanResult`:

```jsonc
{"tappables": [/* ApiTappableResult */], "examined": 0, "skipped": 0, "total": 0, "limit_reached": false}
```

Flow (as `internalGetForts`):
1. Tree snapshot bbox search.
2. Load `TappableLookup`; missing → `skipped`.
3. Skip `ExpireTimestamp != 0 && <= now` (evicted within ~60 s anyway).
4. DNF match.
5. Collect unique ids up to the limit.
6. Per id: `PeekTappableRecord`, `updated_after` gate, `BuildTappableResult`.

Limit: new `tuning.max_tappable_results` (default 9000), `tappableScanLimit` helper mirroring `fortScanLimit`. Separate from `max_fort_results` so operators can cap tappables during events.

Registration: `routes_huma.go` next to `get-tappable`, `api_secret` security, `draftBadge`. No `fort_in_memory` gate.

## 6. Preload (`decoder/preload.go`)

`preloadTappables` in Phase 1 (independent of forts/spawnpoints), under the existing `preload` flag:
- `SELECT <tappable columns> FROM tappable WHERE expire_timestamp > ?` (now).
- Cache `Set` with `tappableCacheTTL`, then `tappablePreloadInsert`.
- Rows without `expire_timestamp` are not preloaded (live traffic refreshes them).
- Count logged with the other preload counts.

## 7. Tests

- `tappableCacheTTL`: verified, unverified, past expiry (1 min floor), no timestamp.
- Index: save inserts tree + lookup; move re-inserts; eviction deletes; re-cached instance skips delete; stale delete no-op; preload insert; missing-lookup self-heal.
- DNF: omitted vs empty lists, each field, OR across clauses, encounter-only clause, unknown `reward_type` → 400.
- Scan: bbox, expiry gate, limit + `limit_reached`, `updated_after`, dedupe.
- Route: registered, requires auth; route list in `huma_routes_test.go`.
- Size pin for `TappableLookup`.

## 8. Docs

`CLAUDE.md`: Entities table (tappable cache TTL), Spatial Indexes (tappable tree), Preload order, gRPC/API list note (HTTP only).
