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

**Request Body:** GeoJSON Feature, Geometry, or Golbat Geofence format
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

**Request Body:** GeoJSON Feature, Geometry, or Golbat Geofence format

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

**Request Body:** GeoJSON Feature, Geometry, or Golbat Geofence format

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

Golbat also provides a gRPC API running on a separate port (configured via `grpc_port`).

### Authentication

Use the `authorization` metadata header with the API secret.

### Pokemon Service

```protobuf
service Pokemon {
  rpc Search(PokemonScanRequest) returns (PokemonScanResponse);
  rpc SearchV3(PokemonScanRequestV3) returns (PokemonScanResponseV3);
}
```

The gRPC endpoints mirror the HTTP v2/v3 scan endpoints.

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
