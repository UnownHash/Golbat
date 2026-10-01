# Golbat API Documentation

Golbat provides both HTTP REST and gRPC APIs for querying Pokemon GO data.

## Table of Contents

- [Authentication](#authentication)
- [Health Check](#health-check)
- [Raw Data Ingestion](#raw-data-ingestion)
- [Pokemon Endpoints](#pokemon-endpoints)
- [Pokestop Endpoints](#pokestop-endpoints)
- [Gym Endpoints](#gym-endpoints)
- [Quest Endpoints](#quest-endpoints)
- [Tappable Endpoints](#tappable-endpoints)
- [Device Endpoints](#device-endpoints)
- [Debug Endpoints](#debug-endpoints)
- [gRPC API](#grpc-api)
- [Data Structures](#data-structures)
- [Derived Stats and Despawn Times](#derived-stats-and-despawn-times)

---

## Authentication

### API Authentication

All `/api/*` endpoints require authentication via the `X-Golbat-Secret` header.

```
X-Golbat-Secret: your_api_secret
```

The secret is configured via `api_secret` in the configuration file.

### Raw Endpoint Authentication

The `/raw` endpoint optionally supports Bearer token authentication:

```
Authorization: Bearer your_raw_bearer_token
```

This is only enforced if `raw_bearer` is configured.

---

## Health Check

### GET /health

Unrestricted health check endpoint for monitoring.

**Authentication:** Not required

**Response:**
```json
{
  "status": "ok"
}
```

### GET /api/health

Authenticated health check endpoint.

**Authentication:** Required

**Response:**
```json
{
  "status": "ok"
}
```

---

## Raw Data Ingestion

### POST /raw

Accept raw protobuf data from scanning clients.

**Authentication:** Bearer token (optional, if configured)

**Request Body:**
```json
{
  "uuid": "device_uuid",
  "username": "account_name",
  "trainerlvl": 30,
  "scan_context": "context_string",
  "lat_target": 40.7128,
  "lon_target": -74.0060,
  "timestamp_ms": 1234567890,
  "have_ar": true,
  "contents": [
    {
      "payload": "base64_encoded_proto",
      "type": 1,
      "request": "optional_request_proto"
    }
  ]
}
```

**Response:** HTTP 201 Created (async processing)

**Notes:**
- Multiple provider formats supported (Pogodroid, standard format)
- Processing timeout: 5s normal, 30s if `extended_timeout` enabled
- Content can use `data` or `payload` for the proto data
- Content can use `method` or `type` for the method number

---

## Pokemon Endpoints

### GET /api/pokemon/id/:pokemon_id

Retrieve a single pokemon by encounter ID.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| pokemon_id | uint64 | path | Pokemon encounter ID |

**Response:** [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 200: Pokemon found
- 404: Pokemon not found

---

### GET /api/pokemon/available

List all available pokemon species with counts.

**Authentication:** Required

**Response:**
```json
[
  {
    "id": 1,
    "form": 0,
    "count": 42
  }
]
```

---

### POST /api/pokemon/scan

Query pokemon in a geographic area with filters (v1 - legacy).

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"lat": 40.7, "lon": -74.0},
  "max": {"lat": 40.8, "lon": -73.9},
  "center": {"lat": 40.75, "lon": -73.95},
  "limit": 500,
  "global": {
    "iv": [0, 100],
    "atk_iv": [0, 15],
    "def_iv": [0, 15],
    "sta_iv": [0, 15],
    "level": [1, 50],
    "cp": [0, 3000],
    "gender": 1,
    "additional": {
      "include_everything": false,
      "include_hundoiv": true,
      "include_zeroiv": false,
      "include_xxs": true,
      "include_xxl": false
    },
    "pvp": {
      "little": [1, 100],
      "great": [1, 100],
      "ultra": [1, 100]
    }
  },
  "filters": {
    "1-0": {}
  }
}
```

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

---

### POST /api/pokemon/v2/scan

Query pokemon with DNF (Disjunctive Normal Form) filters - more efficient filtering.

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"lat": 40.7, "lon": -74.0},
  "max": {"lat": 40.8, "lon": -73.9},
  "limit": 500,
  "filters": [
    {
      "pokemon": [{"id": 1, "form": 0}],
      "iv": {"min": 90, "max": 100},
      "atk_iv": {"min": 10, "max": 15},
      "def_iv": {"min": 10, "max": 15},
      "sta_iv": {"min": 10, "max": 15},
      "level": {"min": 30, "max": 50},
      "cp": {"min": 2000, "max": 3000},
      "gender": {"min": 0, "max": 2},
      "size": {"min": 0, "max": 5},
      "pvp_little": {"min": 1, "max": 100},
      "pvp_great": {"min": 1, "max": 100},
      "pvp_ultra": {"min": 1, "max": 100}
    }
  ]
}
```

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

---

### POST /api/pokemon/v3/scan

Query pokemon with advanced DNF filters, returns metadata about scan.

**Authentication:** Required

**Request Body:** Same as v2, with gender as array

**Response:**
```json
{
  "pokemon": [],
  "examined": 1000,
  "skipped": 50,
  "total": 1050
}
```

---

### POST /api/pokemon/search

Advanced search using center point and distance.

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"lat": 40.7, "lon": -74.0},
  "max": {"lat": 40.8, "lon": -73.9},
  "center": {"lat": 40.75, "lon": -73.95},
  "limit": 500,
  "searchIds": [1, 4, 7]
}
```

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 200: Success
- 400: Bad Request (validation failed)

---

## Pokestop Endpoints

### GET /api/pokestop/id/:fort_id

Retrieve a single pokestop by fort ID.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| fort_id | string | path | Pokestop fort ID |

**Response:** [ApiPokestopResult](#apipokestopresult)

**Status Codes:**
- 200: Pokestop found
- 404: Pokestop not found

---

### POST /api/pokestop-positions

Get coordinates of all pokestops within a geofence.

**Authentication:** Required

**Request Body:** GeoJSON Feature or Geometry (Polygon, MultiPolygon, or a GeometryCollection of polygons), or Golbat Geofence format
```json
{
  "fence": [
    {"lat": 40.7, "lon": -74.0},
    {"lat": 40.8, "lon": -74.0},
    {"lat": 40.8, "lon": -73.9},
    {"lat": 40.7, "lon": -73.9}
  ]
}
```

**Response:**
```json
[
  {
    "id": "fort_id",
    "latitude": 40.7128,
    "longitude": -74.0060
  }
]
```

---

## Gym Endpoints

### GET /api/gym/id/:gym_id

Retrieve a single gym by gym ID.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| gym_id | string | path | Gym ID |

**Response:** [ApiGymResult](#apigymresult)

**Status Codes:**
- 200: Gym found
- 404: Gym not found

---

### POST /api/gym/query

Get multiple gyms by IDs.

**Authentication:** Required

**Request Body:**
```json
{
  "ids": ["gym_id1", "gym_id2"]
}
```
Or as an array:
```json
["gym_id1", "gym_id2"]
```

**Response:** Array of [ApiGymResult](#apigymresult)

**Limits:**
- Maximum 500 IDs per request
- Duplicates are filtered

**Status Codes:**
- 200: Success
- 413: Request Entity Too Large (exceeds 500 IDs)

---

### POST /api/gym/search

Advanced gym search with filters.

**Authentication:** Required

**Request Body:**
```json
{
  "filters": [
    {
      "name": "central park",
      "description": "playground",
      "location_distance": {
        "location": {"lat": 40.7829, "lon": -73.9654},
        "distance": 500
      },
      "bbox": {
        "min_lon": -74.0,
        "min_lat": 40.7,
        "max_lon": -73.9,
        "max_lat": 40.8
      }
    }
  ],
  "limit": 100
}
```

**Response:** Array of [ApiGymResult](#apigymresult)

**Limits:**
- Default limit: 500
- Max limit: 10,000
- Max distance: 500,000 meters

**Status Codes:**
- 200: Success
- 400: Bad Request (invalid filters)
- 504: Gateway Timeout

---

## Quest Endpoints

### POST /api/quest-status

Get quest statistics for a geofence area.

**Authentication:** Required

**Request Body:** GeoJSON Feature or Geometry (Polygon, MultiPolygon, or a GeometryCollection of polygons), or Golbat Geofence format

**Response:**
```json
{
  "ar_quests": 50,
  "no_ar_quests": 100,
  "total": 200
}
```

---

### POST /api/clear-quests
### DELETE /api/clear-quests

Clear all quests within a geofence area.

**Authentication:** Required

**Request Body:** GeoJSON Feature or Geometry (Polygon, MultiPolygon, or a GeometryCollection of polygons), or Golbat Geofence format

**Response:**
```json
{
  "status": "ok"
}
```

---

### POST /api/reload-geojson
### GET /api/reload-geojson

Reload geofence boundaries and clear stats.

**Authentication:** Required

**Response:**
```json
{
  "status": "ok"
}
```

---

## Tappable Endpoints

### GET /api/tappable/id/:tappable_id

Retrieve a tappable (invasions, research, etc.).

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| tappable_id | uint64 | path | Tappable ID |

**Response:** [ApiTappableResult](#apitappableresult)

**Status Codes:**
- 200: Tappable found
- 400: Invalid ID
- 404: Tappable not found

---

## Device Endpoints

### GET /api/devices/all

Get information about all connected/known devices.

**Authentication:** Required

**Response:**
```json
{
  "devices": [
    {
      "uuid": "device_uuid",
      "lat": 40.7128,
      "lon": -74.0060,
      "last_scan": 1234567890
    }
  ]
}
```

---

## Debug Endpoints

These endpoints are only available if `tuning.profile_routes` is enabled in configuration.

**Authentication:** Required

| Endpoint | Description |
|----------|-------------|
| GET /debug/pprof/cmdline | Command line arguments |
| GET /debug/pprof/heap | Heap memory profile |
| GET /debug/pprof/block | Block profile |
| GET /debug/pprof/mutex | Mutex profile |
| GET /debug/pprof/trace | Execution trace |
| GET /debug/pprof/profile | CPU profile |
| GET /debug/pprof/symbol | Symbol lookup |

---

## gRPC API

Golbat serves a gRPC API on `grpc_port` (the same listener as raw ingest). The
schema is `grpc/api.proto`, package `golbat_api`, service `GolbatApi`. Server
reflection is enabled, so `grpcurl` and `ghz` work without the proto files.

**Message sizes:** fort scans can return up to `tuning.max_fort_results`
results (default 9000) with JSON passthrough blobs attached, and easily
exceed 4 MB. grpc-go clients default `MaxCallRecvMsgSize` to 4 MB and fail
with `RESOURCE_EXHAUSTED` on a large response — raise it
(`grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20))`, `grpcurl
-max-msg-sz 67108864`, ghz `--max-recv-message-size`), or lower `limit`. The
server imposes no send-side cap.

### Authentication

The API uses the existing `api_secret` from the config file — the same value
the HTTP `X-Golbat-Secret` header carries. Send it as gRPC metadata:

```
x-golbat-secret: your_api_secret
```

`authorization: your_api_secret` and `authorization: Bearer your_api_secret`
are also accepted. A missing or wrong secret fails with `UNAUTHENTICATED`. An
empty `api_secret` disables the check, as for HTTP. `raw_bearer` is only ever
checked by `RawProto.SubmitRawProto`.

Server reflection is unauthenticated (it exposes only the schema, not data),
so `grpc_port` should not be reachable from the public internet; the API
methods themselves are always gated by `api_secret`.

### Service

| RPC | HTTP counterpart |
|-----|------------------|
| `ScanPokemon(PokemonScanRequest) → PokemonScanResponse` | `POST /api/pokemon/v3/scan` |
| `GetPokemon(GetPokemonRequest) → GetPokemonResponse` | `GET /api/pokemon/id/{pokemon_id}`, batched; misses are omitted; capped at `tuning.max_pokemon_results` ids per call (`INVALID_ARGUMENT` above it) |
| `ScanGyms(FortScanRequest) → GymScanResponse` | `POST /api/gym/scan` |
| `ScanPokestops(FortScanRequest) → PokestopScanResponse` | `POST /api/pokestop/scan` |
| `ScanStations(FortScanRequest) → StationScanResponse` | `POST /api/station/scan` |
| `ScanForts(FortCombinedScanRequest) → FortScanResponse` | `POST /api/fort/scan` — each type group takes its own `limit` (0 = server default); the response carries `gyms_stats` / `pokestops_stats` / `stations_stats` (`examined`, `limit_reached`) per type, and the top-level `limit_reached` is true when any type or the overall `limit` was reached |

Every message mirrors the JSON request or response it is named after, field
for field, with proto field names equal to the JSON keys. The scans run the
same spatial index, DNF matching and record build as the HTTP endpoints; only
the serialisation differs.

### Differences from the JSON API

- **Encounter ids are numeric** (`uint64`), not decimal strings.
- **64-bit ids are `jstype = JS_STRING`** (`Pokemon.id`, `spawn_id`, `cell_id`,
  `GetPokemonRequest.encounter_ids`, the fort `cell_id`s and
  `StationBattle.bread_battle_seed`), so JavaScript/TypeScript generators emit
  them as strings. Other languages are unaffected.
- **Pokemon `filters`** keep JSON semantics: an empty list matches nothing;
  one clause with no conditions (`{}`) matches every pokemon. Fort scans
  differ (as in JSON): an empty `filters` list matches every fort of the type.
- **`IntRange`**: an unset `min` is 0 and an unset `max` is 32767 (no upper
  bound). In JSON an omitted `max` is 0. When timing gRPC against HTTP, send
  both bounds (or neither) — a filter with only `min` selects `min..32767`
  over gRPC but matches nothing over HTTP, so the two responses would differ
  in size.
- **Repeated list filters** (team ids, raid levels, quest reward types, ...):
  an empty list means no constraint. proto3 cannot distinguish an empty list
  from an absent one, so the JSON "explicitly empty list matches nothing" case
  does not exist here.
- **`DnfId.pokemon_id`** is the field name in both pokemon and fort filters
  (JSON pokemon filters use `id`).
- **Stored JSON blobs** (`defenders_json`, `guarding_pokemon_display_json`,
  `rsvps_json`, `quest_conditions_json`, `quest_rewards_json`,
  `alternative_quest_*_json`, `showcase_focus_json`, `showcase_rankings_json`,
  `stationed_pokemon_json`) are the stored JSON text verbatim, exactly as the
  HTTP API emits them. Unset means JSON `null`.
- **PVP rankings** are structured (`PvpRankings` with `PvpEntry` lists per
  league) rather than a JSON object.
- **`updated_after`** (unix seconds) on the pokemon scan and every fort scan
  returns only entities with `updated > updated_after`. It is applied when the
  response is built, after the spatial scan, DNF matching and the result
  limit, so `examined`, `skipped`, `total` and `limit_reached` describe the
  scan and a page may come back short or empty with `limit_reached` true.
  Entities that expire, are deleted, or stop matching the filters simply
  disappear from later responses; poll with a broad filter and reconcile
  locally, and refresh fully now and then. `updated` has one-second
  resolution, so pass `max(updated) - 1` from the previous response and
  expect the boundary second to be re-delivered. Same semantics on the JSON
  v3 pokemon scan and the fort scans; advertised as `filters.updated_after`
  on `/api/status`.
- **Errors are gRPC status codes**: `UNAUTHENTICATED` (secret),
  `FAILED_PRECONDITION` (fort scans without `fort_in_memory`, HTTP 503),
  `INVALID_ARGUMENT` (`min` or `max` missing, or more than
  `max_pokemon_results` ids in `GetPokemon`).

### Example

```bash
grpcurl -plaintext \
  -H 'x-golbat-secret: your_api_secret' \
  -d '{"min":{"lat":51.4,"lon":-0.2},"max":{"lat":51.6,"lon":0.0},"limit":100,
       "filters":[{"iv":{"min":90}}]}' \
  localhost:50001 golbat_api.GolbatApi/ScanPokemon
```

**Benchmarking:** the gzip compressor is installed server-side; a client that
negotiates gzip receives compressed protobuf, so comparing it against an
uncompressed HTTP response overstates the gRPC win. Compare like with like —
disable compression on the client, or compress both paths.

---

## Data Structures

### Location

```json
{
  "lat": 40.7128,
  "lon": -74.0060
}
```

### Bounding Box (Bbox)

```json
{
  "min_lon": -74.0,
  "min_lat": 40.7,
  "max_lon": -73.9,
  "max_lat": 40.8
}
```

### ApiPokemonResult

```json
{
  "id": "encounter_id",
  "pokestop_id": "fort_id_or_null",
  "spawn_id": 123456789,
  "lat": 40.7128,
  "lon": -74.0060,
  "weight": 5.5,
  "size": 2,
  "height": 0.8,
  "expire_timestamp": 1234567890,
  "updated": 1234567800,
  "pokemon_id": 1,
  "move_1": 100,
  "move_2": 200,
  "gender": 1,
  "cp": 500,
  "atk_iv": 15,
  "def_iv": 15,
  "sta_iv": 15,
  "iv": 100.0,
  "form": 0,
  "level": 30,
  "weather": 1,
  "costume": 0,
  "first_seen_timestamp": 1234567000,
  "changed": 1234567800,
  "cell_id": 123456789,
  "expire_timestamp_verified": true,
  "display_pokemon_id": 1,
  "is_ditto": false,
  "seen_type": "encounter",
  "shiny": false,
  "username": "trainer_name",
  "capture_1": 0.5,
  "capture_2": 0.6,
  "capture_3": 0.7,
  "pvp": {},
  "is_event": 0
}
```

### ApiPokestopResult

```json
{
  "id": "fort_id",
  "lat": 40.7128,
  "lon": -74.0060,
  "name": "Pokestop Name",
  "url": "image_url",
  "lure_expire_timestamp": 1234567890,
  "last_modified_timestamp": 1234567800,
  "updated": 1234567800,
  "enabled": true,
  "quest_type": 1,
  "quest_timestamp": 1234567800,
  "quest_target": 3,
  "quest_conditions": "json_conditions",
  "quest_rewards": "json_rewards",
  "quest_template": "template_string",
  "quest_title": "Quest Title",
  "quest_expiry": 1234667800,
  "cell_id": 123456789,
  "deleted": false,
  "lure_id": 501,
  "first_seen_timestamp": 1234567000,
  "sponsor_id": 1,
  "partner_id": "partner_code",
  "ar_scan_eligible": 1,
  "power_up_level": 1,
  "power_up_points": 100,
  "power_up_end_timestamp": 1234567890,
  "alternative_quest_type": null,
  "alternative_quest_timestamp": null,
  "alternative_quest_target": null,
  "alternative_quest_conditions": null,
  "alternative_quest_rewards": null,
  "alternative_quest_template": null,
  "alternative_quest_title": null,
  "alternative_quest_expiry": null,
  "description": "Pokestop description",
  "showcase_focus": "focus_pokemon",
  "showcase_pokemon_id": 1,
  "showcase_pokemon_form_id": 0,
  "showcase_pokemon_type_id": 1,
  "showcase_ranking_standard": 1,
  "showcase_expiry": 1234567890,
  "showcase_rankings": "json_rankings"
}
```

### ApiGymResult

```json
{
  "id": "gym_id",
  "lat": 40.7128,
  "lon": -74.0060,
  "name": "Gym Name",
  "url": "image_url",
  "last_modified_timestamp": 1234567800,
  "raid_end_timestamp": 1234567890,
  "raid_spawn_timestamp": 1234567800,
  "raid_battle_timestamp": 1234567850,
  "updated": 1234567800,
  "raid_pokemon_id": 1,
  "guarding_pokemon_id": 25,
  "guarding_pokemon_display": "display_string",
  "available_slots": 3,
  "team_id": 1,
  "raid_level": 3,
  "enabled": 1,
  "ex_raid_eligible": 1,
  "in_battle": 0,
  "raid_pokemon_move_1": 100,
  "raid_pokemon_move_2": 200,
  "raid_pokemon_form": 0,
  "raid_pokemon_alignment": 1,
  "raid_pokemon_cp": 30000,
  "raid_is_exclusive": 0,
  "cell_id": 123456789,
  "deleted": false,
  "total_cp": 150000,
  "first_seen_timestamp": 1234567000,
  "raid_pokemon_gender": 1,
  "sponsor_id": 1,
  "partner_id": "partner_code",
  "raid_pokemon_costume": 0,
  "raid_pokemon_evolution": 0,
  "ar_scan_eligible": 1,
  "power_up_level": 1,
  "power_up_points": 100,
  "power_up_end_timestamp": 1234567890,
  "description": "Gym description",
  "defenders": "json_defenders",
  "rsvps": "json_rsvps"
}
```

### ApiTappableResult

```json
{
  "id": 1234567890,
  "lat": 40.7128,
  "lon": -74.0060,
  "fort_id": "gym_or_pokestop_id",
  "spawn_id": 987654321,
  "type": "invasion",
  "pokemon_id": 1,
  "item_id": 1,
  "count": 1,
  "expire_timestamp": 1234567890,
  "expire_timestamp_verified": true,
  "updated": 1234567800
}
```

---

## Derived Stats and Despawn Times

Golbat can compute a Pokemon's IVs, level, size, moves, despawn time and
Ditto identity directly from data every sighting already carries — the
encounter id and the spawn id — instead of waiting for a real encounter or
a verified despawn second.

Three switches control it, all on by default and all there to turn a
derivation off if the game changes the algorithm before Golbat is updated:

| Setting | Controls |
|---|---|
| `derive_iv` | IVs, level, size and moves |
| `derive_despawn` | despawn times at spawnpoints whose despawn has not been observed; off, they get a 20-minute guess |
| `ditto_derivation` | marking a sighting as a Ditto (see "Ditto derivation" below) |

### What is derived, and when

- **IVs, level, size and moves**, on all three sighting paths that can
  produce or update a record without an actual encounter: `updateFromWild`
  (GMO wild pokemon), `updateFromNearby` (GMO nearby pokemon), and
  `updateFromMap` (GMO lure pokemon at a fort) — all in
  `decoder/pokemon_decode.go`. Each calls `deriveIvIfAbsent`, which keeps
  IVs already stored (filling only an absent size or move) and otherwise
  fills `atk_iv`/`def_iv`/`sta_iv`/`iv`, `level`, `size` and any absent move from
  `derive/encounter.For`, using the pokemon's own encounter id and the boost
  state it was seen under (`pokemon.Weather`, as set by this sighting's
  display proto). For a known Ditto the boost state is the Ditto's own — it
  is boosted only when the reported condition is partly cloudy — not its
  disguise's. The values written are bit-for-bit what a real encounter would
  have produced for that id and boost state.
- **Despawn time**, from the spawn id alone, via `derive/despawn.NextOccurrence`
  (applied through `applyDerivedDespawn` in `decoder/pokemon_decode.go`)
  whenever no verified despawn second is available for the spawnpoint. See
  "Storage" below for what counts as available.
- **Repopulation instead of blanking on a weather flip.** When a wild/nearby
  pokemon's weather-boost state changes between sightings and Golbat has no
  scan on file for the new state, `repopulateIv` re-derives IVs, level
  and size for the new state from the encounter id rather than clearing
  them, so a pokemon does not go from "known" back to "unknown" merely
  because the weather changed between two sightings.

### Size classes

Size is a `nextDouble()` draw classified by fixed, strict cut points: below
`0.004` is XXS, below `0.029` XS, below `0.971` M, below `0.996` XL, and
anything else XXL. Size events shift these cut points for the species they
apply to, and derivation does not model events, so a size derived during
one can be a class off until a real encounter reports the actual size.

### Ditto derivation

The game decides whether a spawn is a Ditto from the same encounter-id
generator that produces its IVs: the third value it draws, read as a Java
`nextFloat()`, is compared against `0.02f`. A sighting is marked as a Ditto
when that value is strictly below the threshold — `nextFloat() < 0.02f`,
equivalently `next(24) < 335545`, since `0.02f` is 0.0199999996 — **and**
its displayed species and form are in the Ditto pool. The code calls this
value the Ditto *roll*; it has nothing to do with weather rerolls.

The pool lists the species and forms a Ditto can disguise as. Golbat uses
the pool published at `https://uxie.unownhash.com/`, fetched at startup and
every 6 hours and cached under `cache/ditto-pool.json`, with a copy built
into the binary as a fallback. Its maintainers decide what goes in it. An
operator who needs something different sets `ditto_pool` in config, which
replaces the published pool entirely.

The threshold is fixed rather than configurable. If the game changes it,
Golbat will be patched; until then the derivation can be turned off with
`ditto_derivation = false`. Event effects are not modelled: a real
encounter always reports the game's actual values, and those replace
anything derived.

A real encounter can still establish or clear Ditto identity on its own
(`detectDitto`), including from a single encounter whose level or IVs are
impossible for the boost state the game reports. A marking made by
derivation is never overridden by derivation, but it is by that evidence.
The first encounter of a marked Pokemon also clears the mark when its stats
follow the disguise's weather boost rather than a Ditto's — possible
whenever the two differ, as in rain over a disguise rain boosts. A Pokemon
that has been encountered is never marked by derivation afterwards.

**What is measured and what is assumed.** The rule itself — the third draw,
`nextFloat() < 0.02f` — is the game's as far as it has been observed: it
was confirmed in game for three disguise species, and matched Golbat's own
cross-boost detection on all 3,797 encounters that detection could resolve.
The largest roll measured on a confirmed Ditto is 0.0199940. A later week of
encounters (September 2026) resolved 6 Dittos, largest roll 0.0171, and 56
non-Dittos of pool species, smallest roll 0.0353. The exact cutoff between
0.0199940 and 0.02 has not been observed, and neither has the game's rate
during a Ditto event.

### What is not derived, and why

**Strong Pokemon are excluded from all of the above, entirely.** Golbat
treats a strong Pokemon's IVs as unaffected by weather — see
`repopulateIv`'s `isBoostedMatches || isStrong` branch and `detectDitto`'s
requirement that a strong scan's IVs stay identical across boost states.
The derivation in `derive/encounter.For` implements the ordinary
boosted-floor mechanic instead, which does not apply to strong Pokemon.
Deriving for one anyway would write IVs that look exactly like real ones
but are wrong. So a strong Pokemon's IVs come only from a real encounter:
they are absent until one arrives, and cleared on a weather flip when no
scan is stored. The same applies to Ditto derivation: whether
a strong Pokemon can be a Ditto is not known either way, so a strong
Pokemon is never marked as one by derivation.

### `seen_type` says whether the stats were observed

Derivation never upgrades `seen_type`: a Pokemon whose stats came from
`deriveIvIfAbsent` keeps the `seen_type` its sighting gave it (`wild`,
`nearby_stop`, `nearby_cell` or `lure_wild`) until a real encounter
arrives. When a weather change replaces an encounter's stats with derived
ones for the new weather, an `encounter` or `lure_encounter` record goes back
to `wild` or `lure_wild`, as it does when the change clears them. So
`encounter` and `lure_encounter` mean the stats on the row are the ones an
encounter reported.

A consumer that wants to know "do we have usable stats for this Pokemon"
should check IV validity (e.g. whether `atk_iv`/`iv` is non-null); one that
wants to know whether they were observed should read `seen_type`.

### Derived values have the shape of observed ones

A derived IV, level, size or move is written exactly as an encounter's would
be, so every existing consumer — webhooks, PVP rankings, the stats/IV
counters — uses it without any change on their end. Beyond `seen_type`, no
column records where a value came from, so a wrong derivation cannot be
picked out after the fact. That is why the exclusions above are
conservative, and why the derivation is checked continuously against every
real encounter — see "Metrics" below.

### Storage

A **spawnpoint** is a place. Every Pokemon that spawns there despawns at the
same second of each hour, and the spawnpoint row stores that second as
`despawn_sec`. A **Pokemon** has one despawn instant of its own, stored as
`expire_timestamp`, with `expire_timestamp_verified` saying whether it was
seen or worked out. Derivation treats the two differently.

| | Spawnpoint `despawn_sec` | Pokemon `expire_timestamp` |
|---|---|---|
| What it is | Second of the hour, recurring, for the place | One instant, for one Pokemon |
| Written from | A wild sighting showing the time till hidden — nothing else | That spawnpoint's `despawn_sec` if known; otherwise derived from the spawn id |
| Ever derived? | **Never** | Yes, with `expire_timestamp_verified` false |
| Absent means | Not yet observed | — |

`despawn_sec` is an absolute second of the hour (`unix % 3600`), independent
of any timezone. Derivation does not otherwise touch spawnpoint rows or
their `last_seen`.

**How a derived expiry behaves.** It is exact for spawnpoints whose Pokemon
live half an hour (roughly 94%) and half an hour early for the rest. A
Pokemon still listed after its derived expiry is accepted again by the next
sighting and given the next half hour, so a 60-minute Pokemon corrects
itself without anything being written to its spawnpoint. A sighting up to
5 s past a derived expiry counts as that same despawn, since the derived
second can differ from the game's by a moment. Once a sighting shows the
real time till hidden, the spawnpoint gets its `despawn_sec` and every later
Pokemon there gets a verified expiry. Tappables at a spawnpoint with no
`despawn_sec` derive their expiry the same way.

### Metrics

Six Prometheus counters (namespace `golbat`, so exported as
`golbat_<name>`) back this feature, declared in
`stats_collector/prometheus.go`:

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `golbat_derivation_checked_total` | Counter | `path` | Real encounters whose raw observed stats were compared against a fresh encounter-id derivation. `path` is the sighting's `seen_type` at the time of the encounter (e.g. `encounter`, `lure_encounter`). Excludes strong Pokemon. |
| `golbat_derivation_disagreed_total` | Counter | `path` | Checked encounters whose observed stats did *not* match the derivation. Should sit at approximately zero; a rising rate means the derivation has stopped matching the game. |
| `golbat_derivation_boost_branch_divergence_total` | Counter | `path` | Checked encounters whose observed stats matched the *other* weather-boost branch — what a Ditto's do when its boost state differs from its disguise's. Not counted as a disagreement. |
| `golbat_derivation_move_mismatch_total` | Counter | `path` | Checked encounters whose observed moves differ from the derived ones, where the observed move is in the pool the derivation draws from. Also counted in `derivation_disagreed_total`. |
| `golbat_despawn_phase_diverged_total` | Counter | — | TTH sightings whose despawn instant is more than **2 s** away, modulo the half hour, from the phase derived from their spawnpoint id. See the baseline notes below. |
| `golbat_despawn_wrap_clamped_total` | Counter | — | Verified despawns whose `+3600` wraparound implied an impossible (>1 hour) lifetime and were clamped back to a *different* expiry than the record already held. A clamp that merely re-confirms the stored value is deliberately not counted — a record re-decoded after its despawn (a Ditto passes the wild significant-update check on every GMO) re-runs this path, and counting those re-confirmations would give the counter a meaningless baseline. |

**Expected baseline for `golbat_despawn_phase_diverged_total`:** not
zero. The comparison carries a ±2 s tolerance — the same tolerance
`SetDespawnSec` applies to this very quantity, because a spawnpoint's
despawn second is not reported to the second, and because both sides of
the comparison truncate sub-second precision away. A TTH reading still
lands further out than that from time to time, so a small steady trickle
is normal. What the counter is for is the *rate*: a step change, or a rate
climbing toward the TTH sighting rate, means the phase algorithm has
stopped matching the game.

A sighting a minute or more from its derived phase is a different matter:
while the algorithm matches the game it does not happen. Each one is logged,
at most one line per second, whether or not metrics are collected:

```
[DESPAWN] 1 sighting(s) in the last second despawned far from the derived despawn time (most recently spawnpoint 47b973e0595: second 384 of the half hour, derived 1784, 400s off). ...
```

An occasional line is noise; a steady stream means despawn derivation no
longer matches the game, and `derive_despawn = false` turns it off until
Golbat is updated.

Both checks compare the absolute unix second, `x mod 1800`, so they hold in
every timezone, including 45-minute offsets.

---

## Configuration Reference

| Key | Description |
|-----|-------------|
| `api_secret` | API authentication token (header: `X-Golbat-Secret`) |
| `raw_bearer` | Bearer token for raw endpoint (header: `Authorization: Bearer`) |
| `port` | HTTP server port |
| `grpc_port` | gRPC server port |
| `tuning.extended_timeout` | Enable 30s timeout for raw processing |
| `tuning.profile_routes` | Enable pprof debug endpoints |
| `tuning.max_pokemon_results` | Max pokemon returned per query |
| `tuning.max_pokemon_distance` | Max distance between min/max points in searches |
