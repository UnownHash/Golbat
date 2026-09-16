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
