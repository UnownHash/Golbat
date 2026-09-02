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
- [Despawn Timing Fixes](#despawn-timing-fixes)
- [Cross-Golbat Lookup](#cross-golbat-lookup)

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
  rpc GetPokemon(GetPokemonRequest) returns (GetPokemonResponse);
}
```

`Search` and `SearchV3` mirror the HTTP v2/v3 scan endpoints. `GetPokemon` is
the batched gRPC counterpart of `GET /api/pokemon/id/{encounter_id}`: it is
how one Golbat instance asks another about pokemon by encounter id, used by
[Cross-Golbat Lookup](#cross-golbat-lookup) below. It answers only from
pokemon the receiving instance already holds in its own in-memory cache — a
miss returns nothing, there is no database fallback — and authenticates the
same way as `Search`.

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

---

## Despawn Timing Fixes

Two fixes to despawn-timing and webhook delivery. Both are independent of the
peer-lookup feature below and apply to every instance — there is nothing to
configure and no `[[golbat_peer]]` entry is required to get them.

### Despawn wraparound clamp

A verified expiry is derived from a spawnpoint's `despawn_sec` (a
second-of-hour) plus the current second-of-hour, wrapping forward into the
next hour when that second has already passed within this one. For a pokemon
whose first-seen time is already known, Golbat now checks whether that wrap
would give it more than an hour of total lifetime — a spawn's maximum. If so,
the wrap was spurious and the phantom hour is subtracted back out. Clamped
wraps increment the `golbat_despawn_wrap_clamped_total` counter (see
[Metrics](#metrics) below).

### Webhook fires on expiry verification change

`createPokemonWebhooks` now also fires when a pokemon's
`expire_timestamp_verified` flips, in addition to its existing triggers (new
record, species, weather, CP). Previously a pokemon that gained or lost a
TTH-verified despawn time without also changing species/weather/CP emitted no
webhook, so `disappear_time_verified` and `disappear_time` could go stale on
the receiving end. This fires from ordinary local TTH verification and from
the despawn retirement described below — it does not require a peer.

---

## Cross-Golbat Lookup

An instance may ask configured peers about pokemon it has seen but cannot
fully describe — missing IVs, an unverified expiry, or IVs about to be
discarded by a weather-boost change. This is useful where instances overlap:
it avoids spending a duplicate Encounter and propagates TTH-verified despawn
timing between them.

### Configuration

Configure one entry per peer:

```toml
[[golbat_peer]]
address = "10.0.0.2:50051"
api_secret = "shared-secret"
timeout_ms = 500
```

Configuring a peer is what enables the feature; there is no separate flag.
`address` is required. `api_secret` should match that peer's own `api_secret`
— it is sent as the `authorization` gRPC metadata value, checked the same way
`GetPokemon` checks any other caller. `timeout_ms` defaults to 500ms if
omitted or zero; peers are expected on the same LAN, and a lookup is an
optimisation, so timing out costs nothing but today's behaviour.

### How it works

Lookups are best-effort. Enqueueing never blocks the decode path. Queued
questions are dispatched in batches — a 50ms window or 500 questions per
peer, whichever comes first — and each distinct `(encounter_id, pokemon_id,
form, weather)` question is asked at most once per hour, via a dedup cache.
A timeout or a negative answer costs nothing: the instance falls back to its
normal behaviour.

A question is asked when a sighting is missing IVs, has an unverified expiry
on a known spawn point, or is about to have its IVs discarded because a
weather-boost transition found no matching buffered scan — in that last case
the question is about the boost state being switched *to*, asked just before
the stale IVs are cleared.

A peer answers only from what it already holds in memory — never from its
database, and never by forwarding the question to its own peers, so a lookup
cannot loop between instances. Before answering from a cached pokemon it
confirms that record still describes the same species, form and weather the
question named: encounter IDs are reused when the game server mutates a
spawn, and IVs are rolled per weather-boost state, so a record held under a
different boost state describes a different roll and is not a valid answer to
a question about this one.

A peer that has never seen the pokemon can still answer the expiry half of
the question from its own spawnpoint table, when the question carries a spawn
point ID and that peer knows its despawn second — overlapping instances
routinely differ in which pokemon they have seen while sharing which spawn
points they know. Such an answer carries a verified expiry and nothing else;
the asking side does not need stats to act on it.

### Answers are advisory

A peer's `despawn_sec` is written to the shared spawnpoint record only when
this instance has none — local TTH always wins, and a later local TTH
observation can still overwrite a peer-sourced value. A peer's IVs and level
are adopted; its CP is not — CP is always recomputed locally, since it also
depends on this instance's own game master and weather data. PVP rankings are
never exchanged at all: each instance computes its own from IVs and level,
since rankings depend on the answering instance's own league configuration.

The accuracy of a peer's despawn answers is only as good as that peer's own
TTH data; Golbat has no way to assess a given peer's accuracy.

### Contradicted despawn_sec is retired

Whenever a verified expiry is derived from a spawnpoint's `despawn_sec` and
the pokemon being looked at is still alive past that computed expiry, the
`despawn_sec` is proven wrong — no encounter lives longer than about an hour,
so a live sighting mapping to an already-past expiry cannot be explained by a
correct value. Golbat clears (retires) the spawnpoint's `despawn_sec` in the
background so the next sighting re-derives it from a fresh TTH; the
pokemon's own expiry reverts to an unverified estimate, the same as for an
unknown spawnpoint.

This check runs for every `despawn_sec` reaching it, not only ones a peer
wrote — a locally-learned value can also be contradicted, typically by clock
skew beyond its small tolerance margin. **This behaviour applies whether or
not any peer is configured.**

A peer's despawn answer that local-truth-wins rejects outright (because this
instance already had its own `despawn_sec`) is never tested and can never
trigger a retire — only a value that actually reached storage can later be
proven wrong.

### Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `golbat_peer_lookup_dropped_total` | Counter | Peer lookup candidates dropped because the outbound queue was full. |
| `golbat_despawn_wrap_clamped_total` | Counter | Verified despawns whose wraparound implied an impossible (>1 hour) lifetime and were clamped. Independent of peer configuration — see [Despawn Timing Fixes](#despawn-timing-fixes). |
| `golbat_despawn_clear_dropped_total` | Counter | Contradicted-`despawn_sec` clears dropped because the correction queue was full. |
| `golbat_despawn_retired_total` | Counter | `despawn_sec` values cleared because a live sighting contradicted them. |
| `golbat_worker_backlog{worker="despawn_correction"}` | Gauge | Depth of the queue feeding the despawn-retirement worker. One series of the pre-existing `golbat_worker_backlog` gauge, which also tracks other background queues. |
| `golbat_worker_backlog{worker="peer_lookup"}` | Gauge | Depth of the outbound lookup queue. Watch alongside `golbat_peer_lookup_dropped_total`: sustained backlog is what precedes drops. Absent unless a peer is configured. |
