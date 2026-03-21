# Golbat Architecture Guide

## Overview

Golbat is a high-performance Go backend that receives raw protobuf data from Pokemon GO game clients, decodes it, maintains an in-memory cache of all game entities, persists changes to MySQL via write-behind queues, dispatches webhooks, and serves a REST/gRPC API for querying entities with spatial and attribute-based filters.

## Project Layout

```
main.go              — HTTP/gRPC server setup, route registration
routes.go            — HTTP route handlers (raw ingest, API endpoints)
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
  fort_tracker.go    — In-memory fort lifecycle tracking via S2 cells
  tracked_mutex.go   — Lock contention instrumentation
  writebehind/       — Write-behind queue implementation
  writebehind_batch.go — Queue initialization and batch flush SQL
webhooks/
  webhook.go         — Webhook types, config parsing, HTTP dispatch
  sender.go          — Thread-safe message batching and sending
config/              — TOML config parsing
db/                  — Database connection details, query helpers
geo/                 — Geofence loading, R-tree matching, S2 lookup
```

## Raw Message Processing

### Ingest Endpoints

Messages arrive via two paths:

1. **HTTP POST `/raw`** (`routes.go`): Accepts JSON with base64-encoded protobuf payloads. Supports both Pogodroid format (array of `{payload, type}`) and standard format (object with `contents[]`, `username`, `trainerlvl`, etc.). Returns 201 immediately; processing happens in a background goroutine with a 5s timeout.

2. **gRPC `SubmitRawProto`** (`grpc_server_raw.go`): Accepts `RawProtoRequest` with binary proto payloads in `Contents[]`. Same async processing pattern.

Both paths normalize into `ProtoData` structs and call `decode()`.

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

### ShardedCache

High-contention entities (pokestop, gym, station, spawnpoint, pokemon) use `ShardedCache[K, V]` — a generic wrapper over multiple `ttlcache.Cache` instances. Keys are distributed across `runtime.NumCPU()` shards via FNV-1a hashing (strings) or identity (integers), reducing lock contention on the underlying cache maps.

### TTL Cache

Lower-contention entities (incident, weather, route, tappable, player, s2cell) use a single `ttlcache.Cache` instance.

### Configuration

- **Fort caches**: 60-minute TTL normally, 25 hours when `config.Config.FortInMemory` is enabled (keeps forts resident for R-tree operations).
- **Pokemon cache**: 60-minute TTL with `DisableTouchOnHit = true` — TTL counts from creation, not last access, ensuring pokemon expire after their despawn time regardless of query frequency.
- **All other caches**: 60-minute TTL (weather consensus: 2 hours).

### Eviction Callbacks

Fort and pokemon caches register eviction callbacks that clean up the corresponding R-tree entries when a cache item expires. This maintains consistency between the cache and spatial index.

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
- Added to tree when pokemon is first saved or loaded from DB on cache miss (`pokemonRtreeUpdatePokemonOnGet`)
- Updated on every save via `updatePokemonLookup()` which also recalculates PVP rankings
- Removed via cache eviction callback when pokemon TTL expires

### Fort R-tree (`fortRtree.go`)

**FortLookup** stores a union of fields across all fort types: type indicator, location, power-up level, AR eligibility, plus type-specific fields:
- **Gym**: team, slots, raid level/pokemon/timestamps
- **Pokestop**: lure, quest rewards (both AR and non-AR), incident data, contest data
- **Station**: battle level/pokemon/timestamps

Enabled by `config.Config.FortInMemory`. Fort cache TTL is extended to 25 hours to keep entries resident.

**Incident data** on FortLookup is updated separately via `updatePokestopIncidentLookup()` because incidents load after pokestops during preload, and incident updates come through a different code path than pokestop updates.

### Scanning and DNF Filters

#### Pokemon Scan

Three API versions exist (V1/V2/V3), all following the same pattern:

1. Copy the R-tree (read lock) for thread-safe traversal
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

1. Copy fort R-tree
2. Bounding-box search for fort IDs
3. Load `FortLookup` from lookup cache
4. Apply `isFortDnfMatch()` which checks fort type, then type-specific fields:
   - **Gym**: raid level, raid pokemon, raid expiry timestamp
   - **Pokestop**: quest rewards (unified AR/non-AR matching), incidents, lures, contests
   - **Station**: battle level, battle pokemon, battle expiry
5. Lock and load full entity records for matched IDs

The `FortCombinedScanEndpoint` scans all three fort types in one pass and splits results by type.

## Preload

On startup, `Preload()` bulk-loads entities from the database into caches. Order matters when `FortInMemory` is enabled:

1. Pokestops → populates cache + fort R-tree
2. Gyms → populates cache + fort R-tree
3. Stations → populates cache + fort R-tree
4. Incidents → **must load after pokestops** because `updatePokestopIncidentLookup` needs existing fort R-tree entries
5. Pokemon → populates cache + pokemon R-tree

Fort tracker is initialized from the preloaded fort data.
