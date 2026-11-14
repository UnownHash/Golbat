# Golbat API Documentation

Golbat provides both HTTP REST and gRPC APIs for querying Pokemon GO data.

## Table of Contents

- [Authentication](#authentication)
- [Conventions](#conventions)
- [Health, Version and Status](#health-version-and-status)
- [Raw Data Ingestion](#raw-data-ingestion)
- [Pokemon Endpoints](#pokemon-endpoints)
- [Fort Scans](#fort-scans)
- [Fort Availability](#fort-availability)
- [Pokestop Endpoints](#pokestop-endpoints)
- [Gym Endpoints](#gym-endpoints)
- [Station Endpoints](#station-endpoints)
- [Quest Endpoints](#quest-endpoints)
- [Tappable Endpoints](#tappable-endpoints)
- [Device Endpoints](#device-endpoints)
- [Fort Tracker Endpoints](#fort-tracker-endpoints)
- [Admin Endpoints](#admin-endpoints)
- [Debug Endpoints](#debug-endpoints)
- [gRPC API](#grpc-api)
- [Data Structures](#data-structures)
- [Derived Stats and Despawn Times](#derived-stats-and-despawn-times)
- [Configuration Reference](#configuration-reference)

---

## Authentication

### API Authentication

All `/api/*` endpoints and the `/debug/pprof/*` endpoints require
authentication via the `X-Golbat-Secret` header.

```
X-Golbat-Secret: your_api_secret
```

The secret is configured via `api_secret` in the configuration file. An empty
`api_secret` disables the check. A missing or wrong secret is a 401: a
`application/problem+json` body on the endpoints listed in the OpenAPI
document, plain text `Unauthorised` on the others (`/api/health`,
`/api/pokemon/scan`, `/debug/pprof/*`).

### Raw Endpoint Authentication

The `/raw` endpoint optionally supports Bearer token authentication:

```
Authorization: Bearer your_raw_bearer_token
```

This is only enforced if `raw_bearer` is configured. A request with a wrong or
missing token is logged and dropped; the response is an empty `200`, not a
401. For the gRPC `SubmitRawProto` see [gRPC raw ingest](#grpc-raw-ingest).

---

## Conventions

**OpenAPI.** With `api_docs = true` (the default) Golbat serves interactive
docs at `/docs`, the OpenAPI document at `/openapi.json` and the JSON schemas
under `/schemas`, all without the api secret. They cover every `/api/*`
endpoint except `/api/health` and the v1 `/api/pokemon/scan`; `/raw`,
`/health`, `/version` and `/debug/pprof/*` are not in it either.

**Request validation.** Endpoints in the OpenAPI document validate the request
body against its schema before the handler runs. Unknown fields, wrong types
and out-of-range values are rejected with `422` and an
`application/problem+json` body listing the offending properties. Other errors
on those endpoints use the same body shape.

**Success status.** Several endpoints answer `202 Accepted` rather than `200`
for a successful synchronous response: the pokemon v1/v2/v3 scans, search,
by-id and available; the pokestop, gym, station and tappable by-id reads;
`/api/pokestop-positions`; `/api/clear-quests`; and `/api/reload-geojson`.
The status codes listed below are the ones each endpoint actually returns.

**Endpoints marked Draft** in the OpenAPI document (the fort scans, fort
availability, and tappable scan/available) have no stable public contract yet
and may change.

**Bounding boxes.** Scan requests take `min` and `max` corners as
`{"lat": .., "lon": ..}`. The longer `{"latitude": .., "longitude": ..}`
spelling is also accepted (and is the only one the v1 pokemon scan takes).

---

## Health, Version and Status

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

### GET /version

Build information, read from the Go build info at startup.

**Authentication:** Not required

**Response:**
```json
{
  "revision": "c4e4702...",
  "modified": false,
  "build_time": "2026-11-01T12:00:00Z",
  "go_version": "go1.26.0"
}
```

`revision`, `modified` and `build_time` come from the VCS stamp; they are empty
(`""`/`false`) for a binary built without one.

### GET /api/status

Which optional features are enabled, the server's effective result caps, and
which optional filter and response fields this build supports. Map consumers
such as Diadem use it to detect capabilities and clamp their request limits
instead of probing gated endpoints.

**Authentication:** Required. Not gated on `fort_in_memory`.

**Response:**
```json
{
  "features": {
    "fort_in_memory": true,
    "tappable_scan": true
  },
  "limits": {
    "max_pokemon_results": 3000,
    "max_fort_results": 9000,
    "max_tappable_results": 9000
  },
  "filters": {
    "showcase_focus": true,
    "battle_available": true,
    "updated_after": true,
    "quest_temp_evolution_id": true
  }
}
```

**Stable contract.** The keys above are consumed by Diadem
(ccev/diadem#174) and are kept: they are never renamed, retyped or removed,
only added to. A consumer treats an absent key as `false`, which is what an
older Golbat that predates the key reports.

| Key | Meaning |
|-----|---------|
| `features.fort_in_memory` | The in-memory fort index is on (`fort_in_memory` config). Without it the [fort scans](#fort-scans) and [fort availability](#fort-availability) endpoints return 503, and pokestop by-id reads carry no `invasions`. |
| `features.tappable_scan` | `/api/tappable/scan` and `/api/tappable/available` exist. Always true on builds that have the key. |
| `limits.max_pokemon_results` | `tuning.max_pokemon_results`: the cap on pokemon scan/search results per request. |
| `limits.max_fort_results` | `tuning.max_fort_results`: the cap on fort scan results per request (and per type group on `/api/fort/scan`). |
| `limits.max_tappable_results` | `tuning.max_tappable_results`: the cap on tappable scan results per request. |
| `filters.showcase_focus` | Pokestop scans honour `contest_focus` Buddy selectors. |
| `filters.battle_available` | Station scans honour `battle_available`. |
| `filters.updated_after` | The pokemon v3 scan and every fort scan honour `updated_after`. |
| `filters.quest_temp_evolution_id` | Quest entries on `/api/pokestop/available` and `/api/fort/available` carry `temp_evolution_id`. |

New optional capabilities get a new `filters` (or `features`) flag here rather
than a flag on the endpoint they affect.

**Status Codes:**
- 200: OK

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
  "have_ar": true,
  "contents": [
    {
      "payload": "base64_encoded_response_proto",
      "type": 106,
      "request": "base64_encoded_request_proto",
      "have_ar": true
    }
  ],
  "nebula_contents": [
    {
      "endpoint": "get-state",
      "payload": "base64_encoded_response_proto",
      "request": "base64_encoded_request_proto",
      "battle_id": "battle_id",
      "context": {"invasion": {"fort_id": "fort_id", "incident_id": "incident_id"}}
    }
  ],
  "push_contents": [
    {
      "message_type": "raid_lobby_player_count",
      "payload": "base64_encoded_PushGatewayMessage"
    }
  ]
}
```

| Field | Notes |
|-------|-------|
| `uuid` | Device id. With non-zero `lat_target`/`lon_target` it updates the device's entry in [`/api/devices/all`](#get-apidevicesall). |
| `username` | Account name, recorded on the decoded entities. |
| `trainerlvl` | Trainer level; defaults to 30 when absent. Every `contents` method except `PROXY_SOCIAL_ACTION` is skipped below level 30. |
| `scan_context` | Selects the scan rules (`ScanParameters`) applied to this request. |
| `lat_target`, `lon_target` | Where the device is scanning. |
| `have_ar` | Default AR eligibility for every `contents` entry. Decides whether a quest is stored as the AR (`quest_*`) or non-AR (`alternative_quest_*`) quest. |
| `timestamp_ms` | Accepted but has no effect: the JSON number never passes the integer type check, so the receive time is used. (The gRPC `timestamp` field does take effect.) |
| `contents[]` | Game protos. The payload is read from `payload` or `data`, the method number from `type` or `method` (a `pogo.Method` value). `request` is the base64 request proto, needed by methods whose response alone is not enough. A per-entry `have_ar` overrides the top-level one. Entries missing a payload or method are skipped. |
| `nebula_contents[]` | Nebula (battle backend) calls. Only the `invasion` context is processed: endpoint `get-state` (a `BattleStateOutProto`) updates the incident's lineup; `get-time` and `send-player-event` are accepted and ignored. Other endpoints or contexts are logged and dropped. Not level-gated. |
| `push_contents[]` | Push-gateway messages. `message_type` is the `PushGatewayMessage` oneof case and `payload` the whole marshalled `pogo.PushGatewayMessage`, base64. `raid_lobby_player_count` updates a gym's raid lobby count and join deadline; `bread_lobby_player_count` does the same for a station's max battle lobby. Other types are ignored without being parsed. Not level-gated. |

A body needs at least one of `contents`, `nebula_contents` or
`push_contents`.

**Pogodroid format.** When the request carries an `Origin` header, the body is
instead a JSON array of `{"payload": "<base64>", "type": <method>, "lat": ..,
"lng": .., "quests_held": [..]}`. The `Origin` value becomes the device uuid,
the account name is `Pogodroid`, the level is 30, the first non-zero
`lat`/`lng` is the target, and `have_ar` is derived per entry from whether
`quests_held` contains the AR scan quest type.

**Response:**
- 201 Created: accepted; processing happens asynchronously.
- 422 Unprocessable Entity: the body could not be decoded (empty body).

**Notes:**
- The body is read up to 5 MiB; a larger body is cut off and fails to decode.
- Each proto gets its own processing timeout: 5 s, or 30 s with
  `tuning.extended_timeout`.
- Processing concurrency is bounded (`tuning.raw_processing_concurrency`,
  `tuning.raw_processing_queue_factor`). Under overload an accepted request
  can be shed after the 201; shed requests are counted in
  `golbat_raw_packets_shed_total`.

### gRPC raw ingest

`RawProto.SubmitRawProto` (`grpc/raw_receiver.proto`, package `raw_receiver`)
on `grpc_port` is the binary counterpart of `/raw`:

| Field | Equivalent in `/raw` |
|-------|----------------------|
| `device_id` | `uuid` |
| `username` | `username` |
| `trainer_level` | `trainerlvl` — but unset means 0, not 30, so it must be sent for anything to be decoded |
| `lat_target`, `lon_target` | same |
| `timestamp` | receive time override in unix ms (takes effect when > 0) |
| `have_ar` | same |
| `scan_context` | same |
| `contents[]` (`response_payload`, `request_payload`, `method`, `have_ar`) | `contents[]`, with raw bytes instead of base64 |
| `nebula_contents[]` (`endpoint`, `response_payload`, `request_payload`, `invasion` context, `battle_id`) | `nebula_contents[]` |
| `push_contents[]` (`message_type`, `payload`) | `push_contents[]`, with raw bytes |

When `raw_bearer` is set, the `authorization` metadata value must equal it
exactly (no `Bearer ` prefix). A failed check is not a gRPC error: the call
returns OK with `message: "Incorrect authorisation received"` and the data is
dropped. A successful call returns `message: "Processed"`; processing is
asynchronous, as for `/raw`.

---

## Pokemon Endpoints

### GET /api/pokemon/id/:pokemon_id

Retrieve a single pokemon by encounter ID, from the in-memory cache only (no
database fallback).

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| pokemon_id | uint64 | path | Pokemon encounter ID |

**Response:** [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 202: Pokemon found
- 404: Pokemon not cached
- 422: ID is not an unsigned integer

---

### GET /api/pokemon/available

Each distinct pokemon id/form in the in-memory pokemon index, with how many
cached pokemon have it.

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

Unordered. `null` (not `[]`) when no pokemon are cached. Counts include
cached pokemon that have despawned but not yet been evicted.

**Status Codes:**
- 202: OK

---

### POST /api/pokemon/scan

Query pokemon in a geographic area with filters (v1 - legacy).

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"latitude": 40.7, "longitude": -74.0},
  "max": {"latitude": 40.8, "longitude": -73.9},
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

- `min`/`max` take `latitude`/`longitude` keys only; `lat`/`lon` are not
  recognised here and leave the corner at 0,0. `center` is accepted and ignored.
- `filters` is keyed by `"<pokemon_id>-<form>"`. A pokemon matches when the
  `global` filter or the filter under its own key matches.
- Within one filter, a pokemon matches when all of the set range conditions
  (`iv`, `atk_iv`, `def_iv`, `sta_iv`, `level`, `cp`, and a non-zero `gender`)
  hold, **or** any set `pvp` league range holds, **or** any enabled
  `additional` flag holds. A filter with no range conditions matches only via
  `pvp` or `additional`. Every range is a two-element `[min, max]` array; any
  other length fails the whole request with an empty `500`.
- Expired pokemon are not filtered out.
- `limit`: 0 = server default; clamped by `tuning.max_pokemon_results`.

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 202: OK
- 400: Body is not valid JSON for this shape
- 500: A range that is not a two-element array

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

- `limit`: 0 = server default; clamped by `tuning.max_pokemon_results`.
- Pokemon past their `expire_timestamp` are left out of the response.

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 202: OK
- 422: Request body fails validation

#### Filter semantics (v2, v3 and gRPC)

The `filters` list is **not** a flat OR over every clause. Clauses are grouped
by the pokemon/form keys they list, and each pokemon is matched against exactly
one group — the most specific one that exists for it:

1. clauses listing its exact `{id, form}`;
2. otherwise clauses listing its `id` with no form;
3. otherwise clauses with no `pokemon` list — *everything else*.

Within the selected group the clauses are OR'd, and the conditions inside a
clause are AND'd (an OR of ANDs: the DNF). A less specific group never applies
to a pokemon that has a more specific one. Fort and tappable filters are
different: a plain OR over every clause.

Range conditions (`{"min": .., "max": ..}`, here and in the fort and tappable
filters) are inclusive. An omitted `min` is 0; an omitted or null `max` is
unbounded (32767), as over gRPC. So `{"min": 90}` means 90 and up, and `{}`
matches any value. A PVP range (`pvp_little`/`pvp_great`/`pvp_ultra`) never
matches a pokemon with no rank in that league, whatever its bounds. A pokemon
with no value for a field (no encounter stats yet, unknown size or gender) is
indexed with -1 for it, so any range from 0 up excludes it.

What follows from that:

- **"Everything else" means it.** With
  `[{"pokemon":[{"id":1}], "gender":[2], "iv":{"min":100,"max":100}}, {"iv":{"min":100,"max":100}}]`
  you get female perfect Bulbasaur and every perfect pokemon *except*
  Bulbasaur. A male perfect Bulbasaur is not returned: Bulbasaur has its own
  group, so the second clause never applies to it.
- **A shared clause that must also apply to a species with its own rule is
  listed under that species too.** The client owns that merge; Golbat never
  copies clauses between groups (done server-side it costs species × shared
  clauses per request, before any scanning — see #417). "Female Bulbasaur,
  plus every perfect pokemon including Bulbasaur" is
  `[{"pokemon":[{"id":1}], "gender":[2]}, {"pokemon":[{"id":1}], "iv":{"min":100,"max":100}}, {"iv":{"min":100,"max":100}}]`.
- **Hiding a species** is a clause for it that can never hold:
  `{"pokemon":[{"id":710}], "iv":{"min":1,"max":0}}` hides Pumpkaboo while
  `{"size":{"min":5,"max":5}}` still returns every other XXL pokemon.
- **Forms.** `{"id":1,"form":0}` and `{"id":1}` are different keys: for a
  form-0 Bulbasaur the exact key wins and the id-only clauses do not apply.
  Forms belong to a species, so `"id":0` with a form can never match.
- **An empty `filters` list matches nothing**; `[{}]` (one clause with no
  conditions) matches every pokemon.
- The `pokemon` array files one clause under several keys at once; it is
  shorthand for repeating the clause.

Why it works this way: a real filter is hundreds of per-species rules plus a
few shared ones. Grouping lets Golbat pick the applicable clauses with a couple
of map probes per candidate instead of evaluating every clause, and it is what
makes "everything else" expressible at all — without negation, the alternative
is enumerating the complement. The behaviour was introduced with the DNF
filters in #134 and is discussed in #417; `decoder/api_pokemon_semantics_test.go`
pins it.

(The examples use the v3 `gender` list; in v2 `gender` is a range.)

---

### POST /api/pokemon/v3/scan

Query pokemon with DNF filters; returns scan counters with the results.

**Authentication:** Required

**Request Body:** Same as v2, with two differences: `gender` is a list of
allowed values (`"gender": [1, 2]`; empty or omitted = no constraint) instead
of a range, and an optional `updated_after` (unix seconds) returns only pokemon
with `updated > updated_after` — see [`updated_after`](#updated_after). Filter
semantics as for v2 (see above).

**Response:**
```json
{
  "pokemon": [],
  "examined": 1000,
  "skipped": 50,
  "total": 250000,
  "limit_reached": false
}
```

- `examined`: points visited in the pokemon index within the box, matched or not.
- `skipped`: visited points with no lookup entry (mid-eviction), not filtered.
- `total`: points in the whole pokemon index, not just the box.
- `limit_reached`: the matched list reached the effective limit. Expired
  pokemon and the `updated_after` gate are applied afterwards, so a page can
  come back short with `limit_reached` true.

**Status Codes:**
- 202: OK
- 422: Request body fails validation

---

### POST /api/pokemon/search

Find the pokemon of the given species nearest a point.

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

- The search walks the pokemon index outward from `center`, nearest first, and
  returns pokemon whose id is in `searchIds`, in that order.
- `min`/`max` do not clip the results to a box: the box's size sets how far
  from `center` the walk goes. Omitting both uses a fixed default radius.
  They must be no more than `tuning.max_pokemon_distance` km apart.
- `limit`: 0 = server default; clamped by `tuning.max_pokemon_results`.

**Response:** Array of [ApiPokemonResult](#apipokemonresult)

**Status Codes:**
- 202: Success
- 400: `searchIds` missing, or `min`/`max` too far apart

---

## Fort Scans

`POST /api/gym/scan`, `/api/pokestop/scan`, `/api/station/scan` and
`/api/fort/scan` search the in-memory fort index. They need
`fort_in_memory = true` and return `503` (`fort_in_memory not enabled`)
otherwise. Results are built from the fort records (read through to the
database on a cache miss).

### POST /api/gym/scan
### POST /api/pokestop/scan
### POST /api/station/scan

One fort type within a bounding box.

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"lat": 40.7, "lon": -74.0},
  "max": {"lat": 40.8, "lon": -73.9},
  "limit": 0,
  "updated_after": 0,
  "with_incidents": false,
  "filters": [
    {"raid_level": [5, 6], "raid_pokemon_id": [{"pokemon_id": 150}]},
    {"team_id": [1], "available_slots": {"min": 1}}
  ]
}
```

- `limit`: 0 = server default; clamped by `tuning.max_fort_results`.
- `updated_after`: see [`updated_after`](#updated_after).
- `with_incidents` (pokestop scan only): attach each pokestop's active
  incidents as `invasions`.
- `filters`: a plain OR over the clauses — a fort matches when any one clause
  holds — and every condition within a clause must hold. Omitted, null or an
  empty `filters` matches every fort of the type. Within a clause an omitted or
  null field is no constraint; an explicitly empty list matches nothing.
  Fields for another fort type are ignored, so a clause made only of them
  matches every fort of this type.

**Filter fields** (ranges are `{"min": .., "max": ..}`, inclusive, as for
pokemon; pokemon/form pairs are `{"pokemon_id": .., "form": ..}`, where an
omitted or null `form` matches any form):

| Field | Type | Applies to | Matches |
|-------|------|-----------|---------|
| `is_ar_scan_eligible` | bool | all | AR scan eligible forts. Any non-null value, `false` included, restricts to eligible forts. |
| `available_slots` | range | gym | open defender slots |
| `team_id` | list | gym | controlling team |
| `raid_level` | list | gym | active raid level |
| `raid_pokemon_id` | pokemon/form list | gym | active raid boss |
| `raid_temp_evolution_id` | list | gym | raid boss temp evolution; 0 = base form or unhatched egg |
| `lure_id` | list | pokestop | unexpired lure module |
| `quest_reward_type` | list | pokestop | reward type of the AR or the non-AR quest |
| `quest_reward_amount` | range | pokestop | reward amount of the AR or the non-AR quest |
| `quest_reward_item_id` | list | pokestop | reward item of the AR or the non-AR quest |
| `quest_reward_pokemon` | pokemon/form list | pokestop | reward pokemon of the AR or the non-AR quest |
| `incident_display_type` | list | pokestop | an unexpired incident's display type |
| `incident_character` | list | pokestop | an unexpired incident's character (both incident fields must hold on the same incident) |
| `contest_pokemon` | pokemon/form list | pokestop | unexpired showcase focus pokemon |
| `contest_pokemon_type` | list | pokestop | unexpired showcase focus type |
| `contest_focus` | list of `{"type": "buddy", "min_level": n}` | pokestop | unexpired Buddy showcase with exactly that minimum level; other focus types match nothing |
| `contest_ranking_standard` | list | pokestop | unexpired showcase ranking standard; 0 = unknown |
| `battle_level` | list | station | an unexpired battle's level |
| `battle_pokemon` | pokemon/form list | station | an unexpired battle's pokemon (with `battle_level`, both must hold on the same battle) |
| `stationed_gmax` | bool | station | `true`: at least one Gigantamax pokemon stationed; `false`: none |
| `station_active` | bool | station | `true`: not inactive and `start_time < now < end_time`; `false`: the opposite. Expired stations stay in the index, so map clients usually send `true`. |
| `battle_available` | bool | station | the station's `is_battle_available` flag as last decoded. It does not mean a battle is running; combine with `station_active` and `battle_level`/`battle_pokemon`. |

The raid fields match only gyms whose raid battle or raid end is still in the
future.

**Response:**
```json
{
  "gyms": [ApiGymResult],
  "examined": 120,
  "skipped": 0,
  "total": 85000,
  "limit_reached": false
}
```

The result array is `gyms` ([ApiGymResult](#apigymresult)), `pokestops`
([ApiPokestopResult](#apipokestopresult)) or `stations`
([ApiStationResult](#apistationresult)) by endpoint. `examined` counts index
points of every fort type within the box; `skipped` those with no lookup entry;
`total` is every point in the fort index; `limit_reached` says the matched list
reached the limit, measured before `updated_after` is applied.

**Status Codes:**
- 200: OK
- 422: Request body fails validation
- 503: `fort_in_memory` not enabled

---

### POST /api/fort/scan

All three fort types in one walk of the index, each with its own clauses and
limit.

**Authentication:** Required

**Request Body:**
```json
{
  "min": {"lat": 40.7, "lon": -74.0},
  "max": {"lat": 40.8, "lon": -73.9},
  "limit": 0,
  "updated_after": 0,
  "with_incidents": false,
  "gyms": {"filters": [{"raid_level": [5]}], "limit": 500},
  "pokestops": {"filters": [{"lure_id": [501]}], "limit": 0},
  "stations": {"filters": [], "limit": 0}
}
```

- Each present group (`gyms`, `pokestops`, `stations`) opts its type in. Its
  `filters` are a plain OR, as on the single-type scans, evaluated only against
  forts of that type; omitted, null or empty matches every fort of the type.
- An omitted or null group excludes its type. Omitting all three returns every
  type.
- Each group's `limit` (0 = server default) is clamped by
  `tuning.max_fort_results`. A type stops accepting matches at its limit while
  the walk keeps filling the others. The top-level `limit` (0 = server default,
  same clamp) caps the total across types. The walk ends when every requested
  type is full or the total cap is reached.
- `updated_after` applies to every type; `with_incidents` to pokestops.

**Response:**
```json
{
  "gyms": [ApiGymResult],
  "pokestops": [ApiPokestopResult],
  "stations": [ApiStationResult],
  "examined": 300,
  "skipped": 0,
  "total": 85000,
  "limit_reached": false,
  "gyms_stats": {"examined": 40, "limit_reached": false},
  "pokestops_stats": {"examined": 250, "limit_reached": false},
  "stations_stats": {"examined": 10, "limit_reached": false}
}
```

- `examined`/`skipped`/`total`: as on the single-type scans, across all types.
- `*_stats.examined`: forts of that type examined (lookup loaded), matched or
  not. Zero for an excluded type.
- `*_stats.limit_reached`: that type reached its own limit. Always false for an
  excluded type.
- Top-level `limit_reached`: any type reached its limit, or the top-level
  limit ended the walk.

**Status Codes:**
- 200: OK
- 422: Request body fails validation
- 503: `fort_in_memory` not enabled

---

### updated_after

`updated_after` (unix seconds) on the v3 pokemon scan, every fort scan and the
tappable scan returns only entities with `updated > updated_after`; 0 or
omitted returns everything. It is applied when the response is built, after the
spatial scan, filter matching and the result limit, so `examined`, `skipped`,
`total` and `limit_reached` describe the scan, and a page may come back short or
empty with `limit_reached` true. Entities that expire, are deleted, or stop
matching the filters simply disappear from later responses; poll with a broad
filter and reconcile locally, and refresh fully now and then. `updated` has
one-second resolution, so pass `max(updated) - 1` from the previous response
and expect the boundary second to be re-delivered. Advertised as
`filters.updated_after` on `/api/status`.

---

## Fort Availability

`GET /api/pokestop/available`, `/api/gym/available`, `/api/station/available`
and `/api/fort/available` list the distinct filter options active on any
resident fort, for building filter menus. They are whole-instance (no bounding
box), need `fort_in_memory = true` (503 otherwise), and scan neither the forts
nor the database:

- Lures, showcases, invasions, raids and battles are read from maintained
  max-expiry indexes, updated whenever a fort is saved or preloaded. An option
  is listed until the latest expiry seen for it passes, so an option that was
  replaced (an egg that hatched, a grunt lineup that was confirmed) can still
  be listed until its own expiry.
- Quests come from a maintained count aggregate that also follows quests being
  cleared, and are the only entries with a `count`.

### GET /api/pokestop/available

**Authentication:** Required

**Response:**
```json
{
  "showcase_focus_filter": true,
  "quests": [
    {
      "with_ar": true,
      "reward_type": 2,
      "item_id": 701,
      "amount": 3,
      "pokemon_id": 0,
      "form_id": 0,
      "temp_evolution_id": 0,
      "title": "quest_catch_pokemon_plural",
      "target": 5,
      "count": 12
    }
  ],
  "invasions": [
    {
      "character": 4,
      "display_type": 1,
      "confirmed": true,
      "slot1_pokemon_id": 1, "slot1_form": 0,
      "slot2_pokemon_id": 2, "slot2_form": 0,
      "slot3_pokemon_id": 3, "slot3_form": 0
    }
  ],
  "lures": [{"lure_id": 501}],
  "showcases": [
    {
      "pokemon_id": null,
      "form": null,
      "type_id": null,
      "ranking_standard": 1,
      "showcase_focus": {"type": "buddy", "min_level": 3}
    }
  ]
}
```

- `quests`: one entry per distinct reward + temp evolution + title + target
  per quest slot (`with_ar` true for the AR quest, false for the non-AR one).
  `reward_type` is the `QuestRewardProto.Type` number; `item_id`, `amount`,
  `pokemon_id`, `form_id` and `temp_evolution_id` describe the first reward
  and are 0 where they do not apply. `temp_evolution_id` is set for temp evo
  branch mega energy (`reward_type` 20). `count` is the number of resident
  pokestops offering exactly that option in that slot.
- `invasions`: distinct (character, display type, confirmed, confirmed lineup)
  signatures; slot pokemon and forms are null when unconfirmed.
- `showcases`: distinct showcases. `pokemon_id`/`form`/`type_id` mirror a
  pokemon- or type-focused showcase and are null otherwise; `showcase_focus` is
  the structured focus object (null when the stored focus is absent or
  unparseable).
- `showcase_focus_filter`: always true. Deprecated in favour of
  `filters.showcase_focus` on `/api/status`; kept because ReactMap requires it
  on this response.

**Status Codes:**
- 200: OK
- 503: `fort_in_memory` not enabled

### GET /api/gym/available

**Authentication:** Required

**Response:**
```json
{
  "raids": [
    {"raid_level": 5, "pokemon_id": 150, "form": 135, "temp_evolution_id": 0},
    {"raid_level": 1, "pokemon_id": null, "form": null, "temp_evolution_id": 0}
  ]
}
```

A null `pokemon_id`/`form` is an unhatched egg. Team and slot options are not
listed; consumers generate those statically.

**Status Codes:**
- 200: OK
- 503: `fort_in_memory` not enabled

### GET /api/station/available

**Authentication:** Required

**Response:**
```json
{
  "battles": [
    {"battle_level": 3, "pokemon_id": 1, "form": 0},
    {"battle_level": 1, "pokemon_id": null, "form": null}
  ]
}
```

A null `pokemon_id`/`form` is a battle whose pokemon is not known yet.

**Status Codes:**
- 200: OK
- 503: `fort_in_memory` not enabled

### GET /api/fort/available

The three responses above in one: `{"pokestops": {...}, "gyms": {...},
"stations": {...}}`, each with the same shape and source as its per-type
endpoint. Use it instead of three calls when refreshing everything.

**Authentication:** Required

**Status Codes:**
- 200: OK
- 503: `fort_in_memory` not enabled

---

## Pokestop Endpoints

### GET /api/pokestop/id/:fort_id

Retrieve a single pokestop by fort ID, from the in-memory cache only (no
database fallback).

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| fort_id | string | path | Pokestop fort ID |

**Response:** [ApiPokestopResult](#apipokestopresult). With `fort_in_memory`
enabled, active incidents are attached as `invasions`.

**Status Codes:**
- 202: Pokestop found
- 404: Pokestop not cached, or malformed ID

---

### POST /api/pokestop-positions

Get the id and position of every enabled pokestop within a geofence, from the
database.

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

**Status Codes:**
- 202: Success
- 400: Body is not an area geofence
- 500: Database error

Pokestop scan and availability: see [Fort Scans](#fort-scans) and
[Fort Availability](#fort-availability).

---

## Gym Endpoints

### GET /api/gym/id/:gym_id

Retrieve a single gym by gym ID. Reads through to the database on a cache
miss; does not need `fort_in_memory`.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| gym_id | string | path | Gym ID |

**Response:** [ApiGymResult](#apigymresult)

**Status Codes:**
- 202: Gym found
- 404: Gym not found, or malformed ID
- 500: Database error

---

### POST /api/gym/query

Get multiple gyms by IDs. Reads through to the database on a cache miss; does
not need `fort_in_memory`.

**Authentication:** Required

**Request Body:**
```json
{
  "ids": ["gym_id1", "gym_id2"]
}
```

**Response:** Array of [ApiGymResult](#apigymresult). Unknown IDs are omitted;
a missing or null `ids` returns `[]`.

**Limits:**
- Maximum 500 IDs per request, counted before deduplication
- Duplicate and malformed IDs are dropped

**Status Codes:**
- 200: Success
- 413: Request Entity Too Large (exceeds 500 IDs)
- 500: Database error or the 5 s budget ran out

---

### POST /api/gym/search

Search the database for gyms by name, description or location.

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

- Every condition of every filter is AND'd: a second filter narrows the result,
  it does not widen it. `name` and `description` are substring matches (SQL
  `LIKE`); blank strings are ignored. `distance` is in meters.
- Only enabled gyms are returned, ordered by ID.

**Response:** Array of [ApiGymResult](#apigymresult)

**Limits:**
- Default limit: 500 (a missing, zero or negative limit)
- Max limit: 10,000
- Max distance: 500,000 meters (larger values are clamped)

**Status Codes:**
- 200: Success
- 400: Bad Request (no filters, `distance` <= 0, coordinates out of range, or `min` > `max` in `bbox`)
- 500: Database error
- 504: Gateway Timeout (the 5 s budget ran out)

Gym scan and availability: see [Fort Scans](#fort-scans) and
[Fort Availability](#fort-availability).

---

## Station Endpoints

### GET /api/station/id/:station_id

Retrieve a single station by ID. Reads through to the database on a cache
miss; does not need `fort_in_memory`.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| station_id | string | path | Station ID |

**Response:** [ApiStationResult](#apistationresult)

**Status Codes:**
- 202: Station found
- 404: Station not found, or malformed ID
- 500: Database error

---

### POST /api/station/query

Get multiple stations by IDs. Same request, limits and status codes as
[`/api/gym/query`](#post-apigymquery).

**Authentication:** Required

**Request Body:**
```json
{
  "ids": ["station_id1", "station_id2"]
}
```

**Response:** Array of [ApiStationResult](#apistationresult)

Station scan and availability: see [Fort Scans](#fort-scans) and
[Fort Availability](#fort-availability).

---

## Quest Endpoints

### POST /api/quest-status

Count the quests held by pokestops within a geofence, from the database.

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

`total` counts the enabled, non-deleted pokestops in the fence; `ar_quests` and
`no_ar_quests` how many of them hold an AR and a non-AR quest. A database error
returns all zeros.

**Status Codes:**
- 200: OK
- 400: Body is not an area geofence

---

### POST /api/clear-quests

Clear the AR and non-AR quests of every pokestop within a geofence.

**Authentication:** Required

**Request Body:** GeoJSON Feature or Geometry (Polygon, MultiPolygon, or a GeometryCollection of polygons), or Golbat Geofence format

**Response:**
```json
{
  "status": "ok"
}
```

**Status Codes:**
- 202: Quests cleared
- 400: Body is not an area geofence
- 500: Clearing failed or exceeded its 10 s budget

---

## Tappable Endpoints

### GET /api/tappable/id/:tappable_id

Retrieve a tappable by encounter ID.

Cache-only: a tappable is cached until 60 s past its `expire_timestamp` (60 min without one), then returns 404.

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| tappable_id | uint64 | path | Tappable ID |

**Response:** [ApiTappableResult](#apitappableresult)

**Status Codes:**
- 202: Tappable found
- 404: Tappable not found
- 422: ID is not an unsigned integer

### GET /api/tappable/available

Distinct live reward options and encounter species, from a maintained max-expiry index (no tappable or DB scan). Each entry can be sent back as a `/api/tappable/scan` filter clause. Not gated on `fort_in_memory`.

**Authentication:** Required

**Response:**
```json
{
  "rewards": [
    {"reward_type": "item", "item_id": 1301, "reward_pokemon_id": null},
    {"reward_type": "pokemon_candy", "item_id": null, "reward_pokemon_id": 25},
    {"reward_type": "stardust", "item_id": null, "reward_pokemon_id": null}
  ],
  "encounters": [{"pokemon_id": 25}]
}
```

- Rewards describe the first loot item. An option is reported until the latest expiry seen for it passes (tappables without an expiry: 60 min), so a replaced option can over-report briefly.
- `features.tappable_scan` in `/api/status` is true on builds that have this and the scan.

**Status Codes:**
- 200: OK

---

### POST /api/tappable/scan

Unexpired tappables within a bounding box, filtered by DNF clauses. Not gated on `fort_in_memory`.

**Authentication:** Required

**Request:**
```json
{
  "min": {"lat": 47.0, "lon": 8.0},
  "max": {"lat": 47.5, "lon": 8.6},
  "limit": 0,
  "updated_after": 0,
  "filters": [
    {"reward_type": ["item"], "item_id": [1301]},
    {"reward_type": ["pokemon_candy"], "reward_pokemon_id": [25]},
    {"pokemon_id": [25]},
    {"reward_type": ["stardust"], "count": {"min": 500}}
  ]
}
```

- `limit`: 0 = server default; clamped by `tuning.max_tappable_results`.
- `updated_after`: see [`updated_after`](#updated_after); applied after the scan and limit.
- `filters`: clauses are OR'd, fields within a clause AND'd. Omitted/null list = no constraint; empty list = matches nothing. Omitted/empty `filters` matches every tappable.
- `reward_type` values: `item`, `stardust`, `pokecoin`, `pokemon_candy`, `experience`, `pokemon_egg`, `avatar_template_id`, `sticker_id`, `mega_energy_pokemon_id`, `xl_candy`, `follower_pokemon`, `neutral_avatar_template_id`, `neutral_avatar_item_template`, `neutral_avatar_item_display`, `temp_evo_pokemon_branch`. Case-sensitive.
- `pokemon_id` matches encounter tappables only; the reward fields match reward tappables only.
- `count`: inclusive reward amount range, as fort `quest_reward_amount`; an omitted `max` is unbounded. Encounters have no count and never match.
- Reward fields match the first loot item (`rewards[0]`) only.
- Negative `item_id` / `reward_pokemon_id` / `pokemon_id` values → 400.
- Served from memory. With `preload` off, tappables known before a restart appear once they are seen again.

**Response:**
```json
{"tappables": [ApiTappableResult], "examined": 0, "skipped": 0, "total": 0, "limit_reached": false}
```

**Status Codes:**
- 200: OK
- 400: Unknown `reward_type` or negative id
- 422: Request body fails validation

---

## Device Endpoints

### GET /api/devices/all

The last location reported by each device. A device is listed when a raw
submission carried its `uuid` and a non-zero `lat_target`/`lon_target`, and
drops out `cleanup.device_hours` hours after its last such submission.

**Authentication:** Required

**Response:**
```json
{
  "devices": {
    "device_uuid": {
      "latitude": 40.7128,
      "longitude": -74.0060,
      "last_update": 1234567890,
      "scan_context": "context_string"
    }
  }
}
```

**Status Codes:**
- 200: OK

---

## Fort Tracker Endpoints

The fort tracker records which forts each GMO map cell held, to detect removed
and converted forts. These endpoints expose its state.

### GET /api/fort-tracker/cell/:cell_id

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| cell_id | uint64 | path | S2 cell ID (decimal), as the GMO reports it |

**Response:**
```json
{
  "cell_id": "9926595610352287744",
  "last_seen": 1731580000000,
  "pokestops": ["fort_id"],
  "gyms": ["fort_id"]
}
```

`last_seen` is in unix milliseconds. `cell_id` is a decimal string.

**Status Codes:**
- 200: OK
- 404: Cell not tracked
- 422: Cell ID is not an unsigned integer

### GET /api/fort-tracker/forts/:fort_id

**Authentication:** Required

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| fort_id | string | path | Fort ID |

**Response:**
```json
{
  "fort_id": "fort_id",
  "cell_id": "9926595610352287744",
  "last_seen": 1731580000000,
  "is_gym": false
}
```

**Status Codes:**
- 200: OK
- 404: Fort not tracked, or malformed ID

---

## Admin Endpoints

### POST /api/reload-geojson
### GET /api/reload-geojson

Reload geofence boundaries (from Koji, or `geojson/geofence.json`) and clear
the area stats.

**Authentication:** Required

**Response:**
```json
{
  "status": "ok"
}
```

Always `ok`; a failed reload is logged and leaves the current geofences and
stats in place.

**Status Codes:**
- 202: Accepted

---

### POST /api/skip-preserve-pokemon
### GET /api/skip-preserve-pokemon

Skip writing the in-memory pokemon to the database on the next shutdown. Only
relevant with `pokemon_memory_only` and `preserve_pokemon` enabled.
The flag can only be cleared by restarting.

**Authentication:** Required

**Response:**
```json
{
  "status": "ok",
  "message": "Pokemon preservation will be skipped on shutdown"
}
```

**Status Codes:**
- 200: OK

---

## Debug Endpoints

These endpoints are only available if `tuning.profile_routes` is enabled in
configuration. They are the standard `net/http/pprof` handlers.

**Authentication:** Required

| Endpoint | Description |
|----------|-------------|
| GET /debug/pprof/ | Index of the available profiles |
| GET /debug/pprof/cmdline | Command line arguments |
| GET /debug/pprof/profile | CPU profile (`?seconds=N`) |
| GET, POST /debug/pprof/symbol | Symbol lookup |
| GET /debug/pprof/trace | Execution trace (`?seconds=N`) |
| GET /debug/pprof/allocs | Allocation profile |
| GET /debug/pprof/block | Block profile |
| GET /debug/pprof/goroutine | Goroutine stacks |
| GET /debug/pprof/heap | Heap memory profile |
| GET /debug/pprof/mutex | Mutex profile |
| GET /debug/pprof/threadcreate | Thread creation profile |

The block and mutex profiles are empty unless `tuning.profile_contention` is
also enabled, which turns on full block and mutex sampling (with a runtime
cost).

---

## gRPC API

Golbat serves a gRPC API on `grpc_port` (the same listener as raw ingest). The
schema is `grpc/api.proto`, package `golbat_api`, service `GolbatApi`. Server
reflection is enabled, so `grpcurl` and `ghz` work without the proto files.
Pokemon scan `filters` follow the same grouping rules as the HTTP v2/v3
endpoints (see "Filter semantics" above).

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
| `ScanTappables(TappableScanRequest) → TappableScanResponse` | `POST /api/tappable/scan` — not gated on `fort_in_memory`; empty `filters` = every tappable; unknown `reward_type` or negative id → `INVALID_ARGUMENT`; `rewards` is `rewards_json` |
| `GetTappable(GetTappableRequest) → GetTappableResponse` | `GET /api/tappable/id/{tappable_id}`, batched; misses are omitted; capped at `tuning.max_tappable_results` ids per call (`INVALID_ARGUMENT` above it) |

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
  bound), the same as an omitted or null `max` in JSON.
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
- **`updated_after`** on the pokemon scan, every fort scan and the tappable
  scan has the JSON semantics — see [`updated_after`](#updated_after).
- **Errors are gRPC status codes**: `UNAUTHENTICATED` (secret),
  `FAILED_PRECONDITION` (fort scans without `fort_in_memory`, HTTP 503),
  `INVALID_ARGUMENT` (`min` or `max` missing, more than
  `max_pokemon_results` ids in `GetPokemon` or `max_tappable_results` ids
  in `GetTappable`, or an unknown `reward_type` / negative id in a tappable
  scan).

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

Scan requests also accept `{"latitude": .., "longitude": ..}`.

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
  "display_pokemon_form": 0,
  "is_ditto": false,
  "seen_type": "encounter",
  "shiny": false,
  "username": "trainer_name",
  "capture_1": null,
  "capture_2": null,
  "capture_3": null,
  "pvp": {"great": [{"pokemon": 1, "form": 0, "cap": 50, "value": 1800000, "level": 20.5, "cp": 1490, "percentage": 0.98, "rank": 12}]},
  "is_event": 0
}
```

- Every key is always present; a nullable column is `null` when unset.
- `size`: 1 XXS, 2 XS, 3 M, 4 XL, 5 XXL.
- `seen_type`: `wild`, `encounter`, `nearby_stop`, `nearby_cell`, `lure_wild`,
  `lure_encounter`, `tappable_encounter` or `tappable_lure_encounter`. See
  [`seen_type` says whether the stats were observed](#seen_type-says-whether-the-stats-were-observed).
- `capture_1`/`capture_2`/`capture_3` are always `null` and `is_event` always
  `0`; they are kept for wire compatibility and never populated.
- `pvp` is computed per request: one list per league (`little`, `great`,
  `ultra`) that has a ranking, `{}` when PVP is disabled or there is none.

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
  "quest_type": 4,
  "quest_timestamp": 1234567800,
  "quest_target": 3,
  "quest_reward_type": 2,
  "quest_item_id": 701,
  "quest_reward_amount": 3,
  "quest_pokemon_id": null,
  "quest_pokemon_form_id": null,
  "quest_conditions": [{"type": 1, "info": {"pokemon_type_ids": [10]}}],
  "quest_rewards": [{"type": 2, "info": {"item_id": 701, "amount": 3}}],
  "quest_template": "template_string",
  "quest_title": "quest_title_string",
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
  "alternative_quest_reward_type": null,
  "alternative_quest_item_id": null,
  "alternative_quest_reward_amount": null,
  "alternative_quest_pokemon_id": null,
  "alternative_quest_pokemon_form_id": null,
  "alternative_quest_conditions": null,
  "alternative_quest_rewards": null,
  "alternative_quest_template": null,
  "alternative_quest_title": null,
  "alternative_quest_expiry": null,
  "description": "Pokestop description",
  "showcase_focus": {"type": "pokemon", "pokemon_id": 1, "pokemon_form": 0},
  "showcase_pokemon_id": 1,
  "showcase_pokemon_form_id": 0,
  "showcase_pokemon_type_id": null,
  "showcase_ranking_standard": 1,
  "showcase_expiry": 1234567890,
  "showcase_rankings": {"total_entries": 10, "last_update": 1234567800, "contest_entries": []},
  "invasions": [ApiPokestopIncident]
}
```

- `quest_*` is the AR quest, `alternative_quest_*` the non-AR quest. The
  `*_reward_type`, `*_item_id`, `*_reward_amount`, `*_pokemon_id` and
  `*_pokemon_form_id` columns describe the first reward.
- `quest_conditions`, `quest_rewards`, `showcase_focus` and
  `showcase_rankings` (and their `alternative_` counterparts) are the stored
  JSON emitted as native JSON, not strings; `null` when unset.
- `showcase_focus` and `showcase_rankings` keep the most recent showcase after
  it ends; check `showcase_expiry` for liveness.
- `invasions` ([ApiPokestopIncident](#apipokestopincident)) is omitted when
  there are none. It is attached on by-id reads and on scans with
  `with_incidents`, and only with `fort_in_memory` enabled.

### ApiPokestopIncident

```json
{
  "id": "incident_id",
  "pokestop_id": "fort_id",
  "display_type": 1,
  "style": 0,
  "character": 4,
  "start": 1234567000,
  "expiration": 1234568800,
  "confirmed": true,
  "updated": 1234567800,
  "slot_1_pokemon_id": 1,
  "slot_1_form": 0,
  "slot_2_pokemon_id": 2,
  "slot_2_form": 0,
  "slot_3_pokemon_id": 3,
  "slot_3_form": 0
}
```

Only unexpired incidents are returned. The slot fields are `null` until the
lineup is known.

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
  "guarding_pokemon_display": {"form": 0, "gender": 1},
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
  "defenders": [{"pokemon_id": 25, "gender": 1, "battles_won": 0, "battles_lost": 0, "times_fed": 0, "motivation_now": 1, "cp_now": 1200, "cp_when_deployed": 1200}],
  "rsvps": null
}
```

`guarding_pokemon_display`, `defenders` and `rsvps` are the stored JSON
emitted as native JSON, not strings; `null` when unset or not valid JSON. The
OpenAPI document describes the `guarding_pokemon_display` and `defenders`
shapes.

### ApiStationResult

```json
{
  "id": "station_id",
  "lat": 40.7128,
  "lon": -74.0060,
  "name": "Station Name",
  "cell_id": 123456789,
  "start_time": 1234560000,
  "end_time": 1235160000,
  "cooldown_complete": 0,
  "is_battle_available": true,
  "is_inactive": false,
  "updated": 1234567800,
  "battle_level": 3,
  "battle_start": 1234560000,
  "battle_end": 1234646400,
  "battle_pokemon_id": 1,
  "battle_pokemon_form": 0,
  "battle_pokemon_costume": 0,
  "battle_pokemon_gender": 1,
  "battle_pokemon_alignment": 0,
  "battle_pokemon_bread_mode": 1,
  "battle_pokemon_move_1": 214,
  "battle_pokemon_move_2": 90,
  "battle_pokemon_stamina": 15000,
  "battle_pokemon_cp_multiplier": 0.7,
  "total_stationed_pokemon": 4,
  "total_stationed_gmax": 1,
  "stationed_pokemon": [],
  "battles": [
    {
      "bread_battle_seed": 123,
      "battle_level": 3,
      "battle_start": 1234560000,
      "battle_end": 1234646400,
      "updated": 1234567800,
      "battle_pokemon_id": 1,
      "battle_pokemon_form": 0
    }
  ]
}
```

- The top-level `battle_*` fields describe the top battle of `battles`; they
  are `null` when no battle is known.
- `battles` lists the station's known battles (omitted when there are none);
  each entry also carries the `battle_pokemon_*` fields shown at top level.
- `is_battle_available` is the flag as last decoded; it does not by itself mean
  a battle is running.
- `stationed_pokemon` is the stored JSON emitted as native JSON; `null` when
  none.

### ApiTappableResult

```json
{
  "id": "12345678901234567890",
  "lat": 40.7128,
  "lon": -74.0060,
  "fort_id": null,
  "spawn_id": 987654321,
  "type": "TAPPABLE_TYPE_BREAKFAST",
  "pokemon_id": null,
  "reward_type": "pokemon_candy",
  "item_id": null,
  "reward_pokemon_id": 25,
  "count": 6,
  "rewards": [{"pokemon_candy": 25, "count": 6}],
  "expire_timestamp": 1234567890,
  "expire_timestamp_verified": true,
  "updated": 1234567800
}
```

- `id`: decimal string (encounter ids exceed 2^53).
- `pokemon_id`: encounter species (encounter tappables); `reward_*`/`item_id`/`count` describe the first loot item; `rewards` holds all loot items as protojson.

---

## Derived Stats and Despawn Times

Golbat can compute a Pokemon's IVs, level, size, moves, despawn time and
Ditto identity directly from data every sighting already carries — the
encounter id and the spawn id — instead of waiting for a real encounter or
a verified despawn second.

[`docs/pokemon-precedence.md`](docs/pokemon-precedence.md) lists, for IVs,
despawn time and Ditto identity, every source a value can come from, their
precedence, and the guard that enforces it.

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
| `api_docs` | Serve `/docs`, `/openapi.json` and `/schemas` without the secret (default true) |
| `fort_in_memory` | Keep forts in memory with a spatial index; required by the fort scan and availability endpoints |
| `tuning.extended_timeout` | Enable 30s timeout for raw processing |
| `tuning.profile_routes` | Enable pprof debug endpoints |
| `tuning.profile_contention` | Enable block and mutex profiling (has overhead) |
| `tuning.max_pokemon_results` | Max pokemon returned per scan or search (default 3000; `limits.max_pokemon_results` in `/api/status`) |
| `tuning.max_fort_results` | Max forts returned per fort scan, and per type group on `/api/fort/scan` (default 9000; `limits.max_fort_results` in `/api/status`) |
| `tuning.max_tappable_results` | Max tappables returned per `/api/tappable/scan` (default 9000; `limits.max_tappable_results` in `/api/status`) |
| `tuning.max_pokemon_distance` | Max distance in km between the min/max points of `/api/pokemon/search` (default 100) |
| `cleanup.device_hours` | How long a device stays in `/api/devices/all` after its last submission |
