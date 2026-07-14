# Golbat Architecture Guide

## Overview

Golbat is a high-performance Go backend that receives raw protobuf data from Pokemon GO game clients, decodes it, maintains an in-memory cache of all game entities, persists changes to MySQL via write-behind queues, dispatches webhooks, and serves a REST/gRPC API for querying entities with spatial and attribute-based filters.

## Project Layout

```
main.go              — HTTP/gRPC server setup, route registration
routes.go            — HTTP route handlers (raw ingest, API endpoints)
raw_limiter.go       — Bounded raw-processing concurrency (semaphore + shed)
decode.go            — Proto method dispatcher, GMO decoder
grpc_server_raw.go   — gRPC raw proto receiver
decoder/
  main.go            — Cache/queue initialization, raw data types
  sharded_cache.go   — Generic sharded TTL cache
  scanarea.go        — ScanParameters and scan rule matching
  gmo_decode.go      — Batch processors (forts, pokemon, weather, stations)
  <entity>.go        — Struct definition, setters with dirty tracking
  <entity>_state.go  — CRUD: load/save/webhooks, get*Record* functions
  <entity>_decode.go — Proto → entity field mapping
  <entity>_process.go — High-level proto processing (FortDetails, encounters, etc.)
  api_<entity>.go    — API result structs, scan endpoints, DNF filters
  pokemonRtree.go    — Pokemon spatial index + lookup cache
  fortRtree.go       — Fort spatial index + lookup cache
  fort_tracker.go    — In-memory fort lifecycle tracking via S2 cells (async worker)
  rtree_evictor.go   — Ordered tree-mutation worker (batched inserts/deletes)
  stats.go           — Stats aggregation worker, area stats, geofence reload
  db_timing.go       — timedDbQuery alias for under-lock DB loads
  tracked_mutex.go   — Lock contention instrumentation
  writebehind/       — Write-behind queue implementation
  writebehind_batch.go — Queue initialization and batch flush SQL
webhooks/
  webhook.go         — Webhook types, config parsing, HTTP dispatch
  sender.go          — Thread-safe message batching and sending
config/              — TOML config parsing
db/                  — Database connection details, query helpers; timing.go
                       wraps DB calls with [DB_SLOW] logging + duration histogram
geo/                 — Geofence loading, R-tree matching; geofence_compiled.go
                       (cached-bounds point-in-polygon), s2_lookup.go (S2 cell
                       fast path for area matching)
util/                — Small shared helpers (DropReporter, etc.)
```

## Raw Message Processing

### Ingest Endpoints

Messages arrive via two paths:

1. **HTTP POST `/raw`** (`routes.go`): Accepts JSON with base64-encoded protobuf payloads. Supports both Pogodroid format (array of `{payload, type}`) and standard format (object with `contents[]`, `username`, `trainerlvl`, etc.). Returns 201 immediately; processing happens in a background goroutine with a 5s timeout.

2. **gRPC `SubmitRawProto`** (`grpc_server_raw.go`): Accepts `RawProtoRequest` with binary proto payloads in `Contents[]`. Same async processing pattern.

Both paths normalize into `ProtoData` structs and call `decode()`.

Processing concurrency is bounded (`raw_limiter.go`): a semaphore of
`tuning.raw_processing_concurrency` slots (default min(4×CPU, 96)) with a
bounded parked queue (`raw_processing_queue_factor` × slots, default 32×).
When the queue is full, packets are shed with aggregated once-per-second
logging and a `golbat_raw_packets_shed_total` counter — bounded loss under
overload instead of unbounded goroutine pileup on internal locks.

### Dispatch (`decode.go`)

`decode()` switches on `pogo.Method` to route each proto type:

- `METHOD_GET_MAP_OBJECTS` → `decodeGMO()` — the primary data source
- `METHOD_FORT_DETAILS` → individual fort/gym detail updates
- `METHOD_GYM_GET_INFO` → gym defender/detail updates
- `METHOD_ENCOUNTER` / `METHOD_DISK_ENCOUNTER` → pokemon encounter data
- `METHOD_FORT_SEARCH` → quest rewards
- `METHOD_GET_MAP_FORTS` → bulk fort name/image data
- Plus ~15 other method types (invasions, routes, tappables, weather, etc.)

Level 30+ is required for most methods to ensure data quality.

### GetMapObjects (GMO) Processing

`decodeGMO()` is the main data pipeline. It parses `GetMapObjectsOutProto` and extracts:

- **Forts** (pokestops + gyms) → `UpdateFortBatch()`
- **Wild/Nearby/Map Pokemon** → `UpdatePokemonBatch()`
- **Weather** → `UpdateClientWeatherBatch()` → triggers proactive IV switching
- **Stations** → `UpdateStationBatch()`
- **S2 Cells** → cell timestamp tracking
- **Fort removal detection** → `CheckRemovedForts()`

Processing is gated by `ScanParameters` — a set of boolean flags resolved from config scan rules based on the request's `scan_context` and geographic location. This allows different scan areas to process different entity types.

### Proto decode engines

Golbat supports two protobuf decoders selected per method via `[proto_engine]`
config. See `docs/adding-a-proto.md` for the full pipeline (accessor
generation → engine registration → decode wiring → shadow coverage → tests)
to follow when adding a new method or proto.

**Standard decoder** (`"std"`): protobuf-go, the original stable decoder.

**Hyperpb decoder** (`"hyperpb"`): High-performance arena-allocated decoder via `buf.build/go/hyperpb`, wrapped by generated `pogoshim` accessors. Generated by `cmd/pogoshimgen` via `scripts/genshim.sh` after `vbase.pb.go` updates. Arena-allocated message access is faster (cache-friendly layout, reduced allocations) but requires careful lifetime management:
- Shim accessors are generated as thin wrappers. They never escape `decodeWithArena()` — no storage in caches, channels, or goroutines; data is copied on demand.
- Payload bytes are owned by the arena and freed when `decodeWithArena()` returns. Getters that need persistence copy into independently-allocated structures (strings/bytes are cloned out of the arena; repeated strings via `StringList`).
- The `pogoshim` generated file is several hundred KB and treated as generated code (spot-check only on review). `pogoshim/manual.go` hand-writes the one construct the generator doesn't emit — `map<K,V>` fields — following the generator's own conventions; see that file's header before extending it.

**Coverage**: as of Wave 3, every client-proto method Golbat decodes runs
through the proto engine — GMO, encounters, fort details, gym info, quests,
map forts, routes, incidents, invasions, battle state, contests, size-
leaderboard entries, station details, tappables, event RSVPs, and social
(proxy request/response, friend details, player search). The one exception is
the push-gateway lobby-count path (`decode_push_gateway.go`): it extracts a
handful of scalars and nothing else, so it stays on plain `proto.Unmarshal`
unconditionally — there's no `*pogo.X`/`pogoshim.X` retention hazard to design
around, and std is already as cheap as that low-volume message type needs
(see the doc comment on `decodePushGateway`).

**Engine selection** (`[proto_engine]` config) resolves per method in this order (see `engineFor` in `protoengine.go`):
1. A legacy explicit key (`gmo`, `encounter`, `disk_encounter`), if non-empty — wins outright, preserving pre-Wave-3 config compatibility.
2. `[proto_engine.overrides].<method>`, if present and non-empty — one entry per method key (see `protoengine.go`'s `engMethod*` constants and `engineSpecs` table for the full list).
3. `default` (config default: `"hyperpb"`).

An unrecognized value (anything other than `"std"`, `"hyperpb"`, or `""`) logs a one-time `[PROTO_ENGINE]` warning at startup and falls back to running on `"std"`.

- **Rollback**: set the method to `"std"` via its legacy key (gmo/encounter/disk_encounter) or an `[proto_engine.overrides]` entry, then restart — no rebuild needed.

**Shadow verification** (config `shadow_sample_rate`, default 0.01):
- Sampled dual-decode: both engines process a subset of packets, compare decoded field digests.
- Mismatch → `golbat_proto_shadow_total{result="mismatch"}` incremented, `[PROTO_SHADOW]` log at ERROR.
- Set to 0 to disable; useful for validating engine parity after updates.

**Go compiler PGO** (`default.pgo`, build-time):
- `default.pgo`, when present, is automatically applied by Go (>= 1.21) when building the main package via the default `-pgo=auto` build flag. It is captured via `make pgo-capture` (see below) and should be committed to the repo once a production profile exists — not yet present on this branch.

**Hyperpb runtime PGO** (config `proto_engine.pgo`, default `true`):
- Runtime warmup (`proto_engine.pgo`, default on): the first 256 packets per method (or 10 minutes, whichever comes first) record a live-traffic profile and the parser tables are recompiled from it (~4% decode win). This was briefly disabled after a hyperpb Recompile bug duplicated repeated-string elements (bufbuild/hyperpb-go#39), now fixed upstream (PR #40) and verified against our protos; `TestHyperpbRecompileRepeatedStringNoDuplication` guards against a regression.
- Refresh periodically: `GOLBAT_URL=https://host:9001 GOLBAT_SECRET=... make pgo-capture` on a production instance captures a profile and commits it.

## Entity Model

### Struct Pattern

Every entity follows the same pattern:

```go
type PokestopData struct {    // Copyable data fields with db tags
    Id   string  `db:"id"`
    Name null.String `db:"name"`
    // ... all persisted columns
}

type Pokestop struct {
    mu TrackedMutex[string] `db:"-"`  // Entity-level mutex
    PokestopData                       // Embedded — copied for queue snapshots
    dirty     bool     `db:"-"`        // Needs DB write
    newRecord bool     `db:"-"`        // INSERT vs UPDATE
    oldValues PokestopOldValues `db:"-"` // Snapshot for webhook comparison
}
```

- **Setter methods** (`SetName`, `SetLat`, etc.) track dirty state and optionally log field changes when `dbDebugEnabled` is true.
- **`snapshotOldValues()`** captures current field values before modifications, used later for webhook change detection.
- The embedded `Data` struct can be copied by value for the write-behind queue without copying the mutex or internal state.

### Entities

| Entity | Cache Key | Cache Type | Queue Key | ID Type |
|--------|-----------|------------|-----------|---------|
| Pokestop | string (fort ID) | Sharded | string | string |
| Gym | string (fort ID) | Sharded | string | string |
| Pokemon | uint64 (encounter ID) | Sharded | uint64 | uint64 |
| Station | string (station ID) | Sharded | string | string |
| Spawnpoint | int64 (spawn ID) | Sharded | int64 | int64 |
| Incident | string (incident ID) | TTL | string | string |
| Weather | int64 (S2 cell ID) | TTL | — | int64 |
| Route | string (route ID) | TTL | string | string |
| Tappable | uint64 (encounter ID) | TTL | uint64 | uint64 |

## Caching

### OtterCache (hot entities)

High-contention entities (pokestop, gym, station, spawnpoint, pokemon — plus the encounter stats cache) use `OtterCache[K, V]` (`ottercache/cache.go`), a hardened adapter over otter v2 (Caffeine-style: lock-free reads, hierarchical timing-wheel expiry). The adapter bakes in two non-negotiable behaviors: eviction events are re-dispatched to a single bounded-queue dispatcher goroutine (otter's default is a goroutine per event, and raw inline delivery could deadlock handlers that take entity locks), and only Expired/Deleted causes reach handlers (otter fires Replacement on overwriting live entries, which Golbat does routinely — a Replacement event reaching the eviction guards would enqueue bogus tree deletes). Per-entry TTLs ride on the value (otter has no per-call TTL); touch-on-hit is a per-cache flag choosing `ExpiryAccessingFunc` (forts, spawnpoints — reads reset the entry's own TTL, ~free via the timer wheel) vs `ExpiryWritingFunc` (pokemon — TTL encodes despawn time, must never extend on read). No sharding: the table is internally concurrent (`tuning.cache_shards` is obsolete).

**Cache construction must happen after config load.** `decoder.InitDataCache()` (idempotent) is called from `main()` once config is read — fort TTLs and eviction-callback registration depend on config, so nothing in package `init()` may build caches or read `config.Config`. Test binaries construct caches via `init_test.go` files.

### Singleton caches

Lower-contention entities (incident, weather, route, tappable, player, s2cell, device) use unsharded `OtterCache` instances too — the whole codebase is on one cache model (ttlcache is no longer a dependency).

### Configuration

- **Fort caches**: jittered per-entry TTL (`fortCacheEntryTTL`): ~60–70 min normally, 25–27 h when `config.Config.FortInMemory` is enabled (keeps forts resident for R-tree operations). Jitter spreads a restart cohort's expiry so downstream work (tree deletes, tracker events, DB reloads) arrives as a stream, not a burst (with otter there is no reader-blocking sweep; jitter survives as burst smoothing).
- **Pokemon cache**: per-entry TTL from `remainingDuration` with `DisableTouchOnHit = true` — verified despawns get despawn time + 60 s (clamped to 1 minute once at/past despawn), unverified get 55–65 min with per-pokemon jitter.
- **All other caches**: 60-minute TTL (weather consensus: 2 hours).

### Eviction Callbacks and the Tree Writer

Fort and pokemon caches register eviction callbacks that clean up the lookup-cache entry inline and hand the R-tree mutation to an ordered batching worker (`decoder/rtree_evictor.go`). Important facts about this path:

- Eviction callbacks arrive on each cache's single dispatcher goroutine (see `ottercache/cache.go`) — bounded, but still NOT synchronized with cache operations or entity-lock holders. The pokemon callback therefore takes the entity lock and skips cleanup if the pokemon was re-cached; the fort callback skips when the lookup entry is already gone (deleted fort) or owned by a different fort type (pokestop↔gym conversion).
- **All runtime pokemon tree mutations go through the writer** — new-record inserts, position moves (delete+insert pairs), rehydration inserts, the eviction-race self-heal, and eviction deletes. Savers holding entity locks never touch `pokemonTreeMutex` (production dumps once showed 90+ savers convoyed there); only the worker and the ~1/s scan-snapshot refresh acquire it. Enqueue order is apply order; deletes match on (coords, id) so stale duplicates are no-ops; the rtree is a multiset so duplicate inserts self-correct. Startup preload uses direct inserts (pre-traffic) to avoid flooding the queue. Fort tree: deletes are queued, adds remain direct (low churn).
- Mutations apply in batches (~512 per tree-mutex acquisition), so the tree may briefly hold a ghost point (harmless — scans consult the lookup cache) or a duplicate point for a re-added id (scan paths dedupe matched ids).
- A save that finds its lookup entry missing re-queues the tree insert (`savePokemonRecordAsAtTime`), self-healing the eviction/re-add race.

## Locking Model

### Entity-Level Mutex

Each entity instance has its own mutex (`TrackedMutex`). All access to entity fields goes through `get*Record*` functions that return `(entity, unlockFunc, error)`. The caller MUST call the returned unlock function.

### Record Access Patterns

Four access patterns, from lightest to heaviest:

1. **`Peek*Record`** — Cache-only lookup, no DB fallback. Returns locked entity or nil. Used for read-only API queries where missing data is acceptable.

2. **`get*RecordReadOnly`** — Cache lookup with DB fallback on miss. Acquires lock but does NOT snapshot old values. Used for read-only access that needs complete data.

3. **`get*RecordForUpdate`** — Calls ReadOnly internally, then snapshots old values for webhook comparison. Used when modifying an existing entity.

4. **`getOrCreate*Record`** — Atomically creates a new cache entry if absent (via `GetOrSetFunc`), then locks and loads from DB if marked as new record. Always snapshots. Used when the entity may not exist yet.

### Atomic Cache Population

`GetOrSetFunc` ensures only one goroutine creates a given cache entry. If two goroutines race to create the same entity, one wins and the other gets the winner's instance. Both then lock the same mutex, serializing their updates.

### Lock Ordering

**Never hold two entity locks simultaneously.** When multiple entities must be accessed (e.g., pokestop-to-gym conversion copying shared fields), release the first lock before acquiring the second. The pattern is: lock A → copy needed data → unlock A → lock B → apply data → unlock B.

If you must reason about lock priority (e.g., choosing which to acquire first), use this ordering by dependency:

```
1. Pokestop / Gym      (peers — never lock both at once)
2. Station
3. Incident             (references Pokestop for lat/lon/name)
4. Pokemon              (references Pokestop, Spawnpoint)
5. Spawnpoint
6. Weather
7. Route
8. Tappable
```

In practice most code only locks a single entity. The cases where two interact:
- **Incident/Pokemon save** → briefly locks Pokestop to copy lat/lon/name, then releases
- **Fort type conversion** → copies shared fields from one fort type to the other with release-between

## Write-Behind Queues

### Architecture

Each entity type has a `TypedQueue[K, T]` that batches and coalesces writes:

1. **Enqueue**: Takes a snapshot of the entity's Data struct (value copy) and adds it to a pending map keyed by entity ID. If the same entity is enqueued again before flushing, the entry is updated in-place (coalescing).

2. **Dispatch**: A processing loop checks for entries whose `ReadyAt` time has passed, moves them to a batch buffer.

3. **Flush**: When the batch reaches `BatchSize` (default 50) or `BatchTimeout` (default 100ms) elapses, the batch is flushed via a bulk `INSERT ... ON DUPLICATE KEY UPDATE` SQL statement.

4. **Concurrency**: All queues share a `SharedLimiter` that caps total concurrent DB writers (default 50). This prevents overwhelming the database connection pool.

5. **Deadlock retry**: MySQL deadlock errors (1213) trigger up to 3 retries with exponential backoff.

### Pokemon Delay

Wild and nearby pokemon use a 30-second write delay (`wildPokemonDelay`). When a wild pokemon is first seen in a GMO, it's enqueued with `delay = 30s`, giving time for an encounter request to arrive with IV/CP/level data. If an encounter arrives within the window, the queue entry is updated in-place with the richer data. Encounter-sourced pokemon write immediately (delay = 0).

### Database

**Golbat targets MariaDB.** MySQL may work but is not tested. SQL syntax, migrations, and batch upsert queries are written for MariaDB compatibility.

#### Connection Split

`DbDetails` holds two connection pools:
- **`PokemonDb`**: Dedicated pool for the pokemon table (highest write volume).
- **`GeneralDb`**: Everything else (forts, incidents, weather, routes, etc.).

## Decode-Path Workers

Work that would otherwise serialize the 96 decode goroutines on a global
lock runs on dedicated single-worker pipelines fed by channels:

| Worker | Channel | Full-channel behavior |
|--------|---------|----------------------|
| Tree writer (`rtree_evictor.go`) | 262144 | Blocking send (tree mutations must not be lost) |
| Stats aggregation (`stats.go`) | 262144 | **Drop + count** (stats are loss-tolerant) |
| Fort tracker (`fort_tracker.go`) | 8192 | **Drop + count** (staleness needs an hour of absence) |

**Invariant: never add a blocking send from the decode path to a worker
whose throughput can fall below the event rate.** A saturated worker with
blocking producers produced a production fill-drain limit cycle (all
decoders frozen at the enqueue for 3–5s every ~30s). Loss-tolerant paths
drop and count (`util.DropReporter` aggregates to one log line/second;
stats drops and cache eviction-event drops have Prometheus counters —
the latter is the one loss with no self-heal, leaking lookup entries
until restart; tree-delete and fort-tracker drops are log-only because
scans/rescans self-heal them). The stats worker drains in
batches of 512 and takes `pokemonStatsLock` once per batch; geofence
reloads clear the stats maps via an in-band barrier event so queued
events with pre-reload area names drain into the old maps.

Worker queue depths are exported every 10s as
`golbat_worker_backlog{worker}` and warn at >50% capacity.

## Webhooks

### Types

| Config String | Webhook Type | Payload Type |
|---------------|-------------|--------------|
| `pokemon_iv` | PokemonIV | `pokemon` |
| `pokemon_no_iv` | PokemonNoIV | `pokemon` |
| `pokemon` | Both IV types | `pokemon` |
| `gym` | GymDetails | `gym_details` |
| `raid` | Raid | `raid` |
| `quest` | Quest | `quest` |
| `pokestop` | Pokestop | `pokestop` |
| `invasion` | Invasion | `invasion` |
| `weather` | Weather | `weather` |
| `fort_update` | FortUpdate | `fort_update` |
| `max_battle` | MaxBattle | `max_battle` |

### Dispatch Flow

1. After saving an entity, the save function calls webhook creation functions (e.g., `createPokemonWebhooks`, `createGymFortWebhooks`).
2. These build a webhook payload struct and call `webhooksSender.AddMessage(type, payload, areas)`.
3. The sender accumulates messages in typed collections.
4. Every 1 second, `Flush()` sends all accumulated messages to each configured webhook endpoint as a batched JSON POST.
5. Messages are filtered by area — a webhook configured for specific areas only receives messages from matching geofences.

### Fort Change Webhooks

Fort updates (new/edit/removal) go through `CreateFortWebHooks(old, new, change)`:
- **NEW**: Sends new fort data. Triggered when `newRecord` is true.
- **EDIT**: Compares old vs new for name, description, image URL (path-only comparison), and location (with float tolerance). Only sends if actual changes detected.
- **REMOVAL**: Sends old fort data. Triggered by fort tracker stale detection.

The `oldValues` for EDIT comparison come from `snapshotOldValues()` called at lock acquisition time.

## In-Memory Fort Tracking

### Purpose

The fort tracker detects when forts are removed from the game or converted between types (pokestop ↔ gym), using S2 cell-level tracking of which forts exist.

### Data Model

```
FortTracker
  ├── cells: map[uint64]*FortTrackerCellState    // S2 cell → {lastSeen, pokestops set, gyms set}
  └── forts: map[string]*FortTrackerLastSeen      // fort ID → {cellId, lastSeen, isGym}
```

### Detection Flow

1. Each GMO response contains S2 cell IDs and the forts within them.
2. `CheckRemovedForts()` calls `ProcessCellUpdate()` for each cell.
3. For each cell, forts in the previous state but NOT in the current GMO are candidates for removal.
4. If a fort has been missing for longer than `staleThreshold` (default 1 hour), it's marked as stale and deleted via `clearGymWithLock`/`clearPokestopWithLock`.
5. If a pokestop ID appears in the gym set (or vice versa), it's detected as a type conversion — the old type is marked deleted (but not removed from the tracker).

### API

- `GET /api/fort-tracker/cell/:cell_id` — returns forts in an S2 cell
- `GET /api/fort-tracker/forts/:fort_id` — returns a fort's cell and last-seen timestamp

## Spatial Indexes (R-trees)

### Two-Level Architecture

Both pokemon and fort spatial queries use the same two-level pattern to avoid holding entity locks during search:

1. **R-tree** (`rtree.RTreeG`): Maps `[lon, lat]` points to entity IDs. Provides fast bounding-box searches.
2. **Lookup cache** (`xsync.MapOf`): Maps entity IDs to lightweight structs containing only the fields needed for filter matching. Lock-free concurrent reads.

This separation means a scan of 100,000 pokemon in a bounding box only touches the R-tree and lookup cache — no entity mutexes are acquired until the final step of building API results for the matched subset.

### Pokemon R-tree (`pokemonRtree.go`)

**PokemonLookup** stores: PokemonId, Form, Weather, IVs (Atk/Def/Sta), Level, CP, Gender, Size, Iv percentage, HasEncounterValues flag. Uses `-1` sentinel for missing nullable values.

**PokemonPvpLookup** stores: best PVP rank in Little/Great/Ultra leagues.

**Lifecycle**:
- Tree insert queued through the tree writer when first saved or loaded from DB on cache miss (`pokemonRtreeUpdatePokemonOnGet`); preload inserts directly (`pokemonRtreePreloadInsert`)
- Lookup entry updated synchronously on every save via `updatePokemonLookup()` which also recalculates PVP rankings
- Tree delete queued via cache eviction callback when pokemon TTL expires

### Fort R-tree (`fortRtree.go`)

**FortLookup** stores a union of fields across all fort types: type indicator, location, power-up level, AR eligibility, plus type-specific fields:
- **Gym**: team, slots, raid level/pokemon/timestamps
- **Pokestop**: lure, quest rewards (both AR and non-AR), incident data, contest data
- **Station**: battle level/pokemon/timestamps

Enabled by `config.Config.FortInMemory`. Fort cache TTL is extended to 25 hours to keep entries resident.

**Incident data** on FortLookup is updated separately via `updatePokestopIncidentLookup()` because incidents load after pokestops during preload, and incident updates come through a different code path than pokestop updates.

**Scaling caveat (fort scans are low-traffic today; this is the pre-scoped lever if that changes):** FortLookup's value layout is already right — flat scalars by value, one fetch per candidate, no pointer chain (the design the pokemon lookup converged to in the de-pointer change). The weakness at pokemon-like scan volumes would be **string keys**: ~35-byte fort IDs are hashed per Load, key-compared per bucket walk, and stored as entries in the R-tree itself (pointer-laden tree nodes; ~2M string objects in every GC mark). The fix, when a profile demands it: intern fort IDs to dense integers at save/delete and key both the tree and the lookup map by the intern ID — one change removes the hashing, the compares, and the tree/GC pointer load together.

### Scanning and DNF Filters

#### Pokemon Scan

Three API versions exist (V1/V2/V3), all following the same pattern:

1. Take the shared read-only tree snapshot (`refreshTreeSnapshot`, refreshed at most once per second — scans never copy the tree per request and hold no lock while searching; hits are re-verified against the live lookup cache, so ≤1s staleness only affects candidate discovery)
2. `rtree.Search(minLon, minLat, maxLon, maxLat)` to get candidate pokemon IDs
3. For each ID, load `PokemonLookup` + `PokemonPvpLookup` from lookup cache
4. Apply DNF filter matching
5. Collect matching IDs up to a configurable limit
6. For matched IDs, call `peekPokemonRecordReadOnly()` to lock and build full API results

**DNF (Disjunctive Normal Form) Filters**: An array of filter clauses OR'd together. Each clause has AND'd conditions (IV range, level range, CP range, pokemon ID + form, PVP ranking, gender, size). A pokemon matches if ANY clause fully matches.

**Filter lookup optimization**: Filters are pre-indexed by `{pokemonId, form}` key. For each pokemon, the system tries:
1. Exact `{pokemonId, form}` match
2. Wildcard form: `{pokemonId, -1}`
3. Global catch-all: `{-1, -1}`

This avoids iterating all filters for every pokemon.

#### Fort Scan

`internalGetForts()` and `internalGetFortsCombined()` follow the same pattern:

1. Take the shared fort tree snapshot (same 1s-refresh mechanism as pokemon)
2. Bounding-box search for fort IDs
3. Load `FortLookup` from lookup cache
4. Apply `isFortDnfMatch()` which checks fort type, then type-specific fields:
   - **Gym**: raid level, raid pokemon, raid expiry timestamp
   - **Pokestop**: quest rewards (unified AR/non-AR matching), incidents, lures, contests
   - **Station**: battle level, battle pokemon, battle expiry
5. Lock and load full entity records for matched IDs

The `FortCombinedScanEndpoint` scans all three fort types in one pass and splits results by type.

## Geofence Matching

Area attribution (stats, webhook filtering) matches points against Koji /
file geofences via two layers:

1. **S2 cell fast path** (`geo/s2_lookup.go`, `tuning.s2_cell_lookup`):
   fences are pre-classified into interior cells (stored at coarse levels
   10–15, looked up via parent walk) and edge cells. Interior hit → area
   names with no polygon math; edge cell or miss → fall through to layer 2.
   Loops are `Normalize()`d (CW GeoJSON rings would otherwise cover the
   planet); hole rings exclude or edge the cells they touch. The lookup is
   built **asynchronously** (takes ~1 min for large projects) — during a
   build/reload window the fast path is nil and everything uses layer 2.
2. **Compiled polygons** (`geo/geofence_compiled.go`): exact
   point-in-polygon with cached ring bounds (~5× faster than orb's
   `planar.RingContains`, which recomputes bounds per call). Differentially
   tested against orb.

Reloads (`ReadGeofences`) are serialized under a mutex, skipped entirely
when the fetched content hash is unchanged, and stale async S2 builds are
discarded via a generation counter with publication under `s2PublishMu`.
A Koji outage at boot degrades (no area matching until a successful
reload) instead of failing startup.

## Preload

On startup, `Preload()` bulk-loads entities from the database into caches. Order matters when `FortInMemory` is enabled:

1. Pokestops → populates cache + fort R-tree
2. Gyms → populates cache + fort R-tree
3. Stations → populates cache + fort R-tree
4. Incidents → **must load after pokestops** because `updatePokestopIncidentLookup` needs existing fort R-tree entries
5. Pokemon → populates cache + pokemon R-tree

Fort tracker is initialized from the preloaded fort data.
