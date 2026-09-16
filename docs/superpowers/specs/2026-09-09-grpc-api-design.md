# gRPC API for pokemon and fort scans — design

**Date:** 2026-09-09
**Status:** Approved in discussion; spec pending review
**Base:** PR 399 (`perf/fortid-value-type`), which already carries main as of 2026-09-01
**Replaces:** the stub `Pokemon` gRPC service (`grpc/pokemon_api.proto`, `grpc_server_pokemon.go`)
**Borrows from:** PR 401's `GetPokemon` RPC and `PokemonResult` message (not merged; the
peer-lookup parameters are dropped)

## 1. Context and goals

The HTTP API is sometimes slower than querying the database directly. The suspected cost is
the JSON encode/decode cycle on large scan responses. To measure that, the same scans need to be
reachable over gRPC with protobuf serialisation and otherwise identical server work: same
spatial index, same DNF matching, same per-record lock and result build.

Golbat already has a `Pokemon` gRPC service with `Search` and `SearchV3`, but each result carries
only five fields (the commit that added it says "incomplete search results"). PR 401 added a
`GetPokemon` RPC to it whose request items carry `pokemon_id`, `form`, `weather` and `spawn_id`
for cross-instance verification. Those parameters make no sense for an API caller.

### Goals

1. Every pokemon scan (v3 semantics) and every fort scan (gym, pokestop, station, combined)
   available over gRPC with complete results, plus pokemon by encounter id.
2. The gRPC path does the same server-side work as the HTTP path apart from serialisation, so a
   timing comparison isolates the encoding cost.
3. gRPC API calls authenticate with the existing `api_secret` config value, the same one the
   HTTP `X-Golbat-Secret` header carries, not `raw_bearer`. No new secret or config key. Auth
   failures are gRPC status errors, not empty responses.
4. The HTTP result structs and the proto messages cannot drift silently: a field added to one
   without the other fails a test.

### Non-goals

- Fort by-id, pokemon search, `available`, `status`, fort-tracker, device and admin endpoints.
- v1/v2 pokemon scan shapes. v3 is a superset in response and its gender list can express the
  v2 gender range.
- Structured proto messages for the JSON blobs stored on forts (defenders, quest rewards, ...).
  They pass through as JSON text, exactly as the HTTP API does. Structured fields can be added
  later as new field numbers.
- Streaming responses, a client library, or a benchmark tool. `grpcurl` and `ghz` work against
  the reflection service this change registers.
- Any change to raw-ingest authentication.

## 2. Service surface

One new file, `grpc/api.proto`, package `golbat_api`, generated into the existing `golbat/grpc`
Go package alongside `raw_receiver` and `pokemon_internal`.

```protobuf
service GolbatApi {
  rpc ScanPokemon   (PokemonScanRequest)      returns (PokemonScanResponse);
  rpc GetPokemon    (GetPokemonRequest)       returns (GetPokemonResponse);
  rpc ScanGyms      (FortScanRequest)         returns (GymScanResponse);
  rpc ScanPokestops (FortScanRequest)         returns (PokestopScanResponse);
  rpc ScanStations  (FortScanRequest)         returns (StationScanResponse);
  rpc ScanForts     (FortCombinedScanRequest) returns (FortScanResponse);
}
```

| RPC | HTTP counterpart | decoder entry point |
|-----|------------------|---------------------|
| `ScanPokemon` | `POST /api/pokemon/v3/scan` | `internalGetPokemonInArea3` + per-key result build |
| `GetPokemon` | `GET /api/pokemon/id/{id}` (batched) | `GetOnePokemon` per id |
| `ScanGyms` | `POST /api/gym/scan` | `GymScanEndpoint` |
| `ScanPokestops` | `POST /api/pokestop/scan` | `PokestopScanEndpoint` |
| `ScanStations` | `POST /api/station/scan` | `StationScanEndpoint` |
| `ScanForts` | `POST /api/fort/scan` | `FortCombinedScanEndpoint` |

### 2.1 Shared request messages

```protobuf
message LatLon   { double lat = 1; double lon = 2; }
message IntRange { optional int32 min = 1; optional int32 max = 2; }
message DnfId    { int32 pokemon_id = 1; optional int32 form = 2; }
```

`IntRange` keeps the semantics of the stub service's `RangeMinMax`: an unset `min` is 0 and an
unset `max` is 32767 (no upper bound). This deliberately differs from the JSON API, where an
omitted `max` is 0 and a range with only `min` never matches. The JSON behaviour is documented as
a wart; the proto does not reproduce it.

`DnfId.pokemon_id` of 0 in a pokemon filter matches any pokemon (as in JSON). `form` unset
matches any form. A pokemon scan with an empty `filters` list matches nothing, exactly as the
JSON v3 scan does (the DNF lookup map has no catch-all entry); one clause with no conditions is
the catch-all. Fort scans are the other way round, also as in JSON: no clauses means every fort
of the requested type.

### 2.2 Pokemon

```protobuf
message PokemonDnfFilter {
  repeated DnfId pokemon  = 1;
  IntRange iv             = 2;
  IntRange atk_iv         = 3;
  IntRange def_iv         = 4;
  IntRange sta_iv         = 5;
  IntRange level          = 6;
  IntRange cp             = 7;
  repeated int32 gender   = 8;
  IntRange size           = 9;
  IntRange pvp_little     = 10;
  IntRange pvp_great      = 11;
  IntRange pvp_ultra      = 12;
}

message PokemonScanRequest {
  LatLon min = 1;
  LatLon max = 2;
  int32 limit = 3;                          // 0 = server default (tuning.max_pokemon_results)
  repeated PokemonDnfFilter filters = 4;    // OR'd clauses; empty matches NOTHING (JSON parity), {} matches all
}

message PokemonScanResponse {
  repeated Pokemon pokemon = 1;
  int32 examined = 2;
  int32 skipped = 3;
  int32 total = 4;
  bool limit_reached = 5;
}

message GetPokemonRequest  { repeated uint64 encounter_ids = 1; }
message GetPokemonResponse { repeated Pokemon pokemon = 1; }   // misses omitted; match by id
```

`Pokemon` mirrors `decoder.ApiPokemonResult` field for field, in struct order, with proto field
names equal to the JSON tags. Type mapping follows PR 399's storage widths:

| Go type on `ApiPokemonResult` | proto type |
|---|---|
| `string` Id (decimal encounter id) | `uint64 id` (numeric; the caller already holds a uint64) |
| `*uint8`, `*uint16` | `optional uint32` |
| `int16` PokemonId, `int8` IsEvent | `int32` |
| `*float32` (weight, height, iv) | `optional float` |
| `*float64` (capture_1..3) | `optional double` |
| `int64`, `*int64` | `int64`, `optional int64` |
| `*string`, `*bool` | `optional string`, `optional bool` |
| `ApiPvpRankings` | `PvpRankings pvp` |

```protobuf
message PvpEntry {
  int32 pokemon = 1; int32 form = 2; double cap = 3; double value = 4; double level = 5;
  int32 cp = 6; double percentage = 7; int32 rank = 8; bool capped = 9; int32 evolution = 10;
}
message PvpRankings { repeated PvpEntry little = 1; repeated PvpEntry great = 2; repeated PvpEntry ultra = 3; }
```

PVP is structured, not the JSON string PR 401 used. The HTTP API computes it live from ohbem per
result; that work is shared, and the gRPC path then avoids any JSON encoding of it.

`capture_1..3` and `is_event` are carried for parity with the HTTP struct and are always unset,
exactly as `buildApiPokemonResult` leaves them.

**64-bit ids carry `[jstype = JS_STRING]`** so JavaScript/TypeScript generators emit strings
instead of lossy numbers: `Pokemon.id`, `Pokemon.spawn_id`, `Pokemon.cell_id`,
`GetPokemonRequest.encounter_ids`, `Gym.cell_id`, `Pokestop.cell_id`, `Station.cell_id`,
`StationBattle.bread_battle_seed`. Timestamps and the small `int64` columns stay numeric; the
option changes nothing on the wire or in the Go code.

### 2.3 Forts

```protobuf
message ContestFocus { string type = 1; optional int32 min_level = 2; }

message FortDnfFilter {
  optional bool is_ar_scan_eligible     = 1;
  // Gym
  IntRange available_slots              = 2;
  repeated int32 team_id                = 3;
  repeated int32 raid_level             = 4;
  repeated DnfId raid_pokemon_id        = 5;
  repeated int32 raid_temp_evolution_id = 6;
  // Pokestop: quest (AR or no-AR)
  repeated int32 lure_id                = 7;
  repeated int32 quest_reward_type      = 8;
  IntRange quest_reward_amount          = 9;
  repeated int32 quest_reward_item_id   = 10;
  repeated DnfId quest_reward_pokemon   = 11;
  // Pokestop: incident
  repeated int32 incident_display_type  = 12;
  repeated int32 incident_character     = 13;
  // Pokestop: contest
  repeated DnfId contest_pokemon        = 14;
  repeated int32 contest_pokemon_type   = 15;
  repeated ContestFocus contest_focus   = 16;
  repeated int32 contest_ranking_standard = 17;
  // Station
  repeated int32 battle_level           = 18;
  repeated DnfId battle_pokemon         = 19;
  optional bool stationed_gmax          = 20;
  optional bool station_active          = 21;
}

message FortScanRequest {
  LatLon min = 1; LatLon max = 2;
  int32 limit = 3;                        // 0 = server default (tuning.max_fort_results)
  repeated FortDnfFilter filters = 4;     // empty = every fort of the requested type
  bool with_incidents = 5;                // pokestop scans only; ignored otherwise
}

message FortTypeScanGroup { repeated FortDnfFilter filters = 1; }

message FortCombinedScanRequest {
  LatLon min = 1; LatLon max = 2;
  int32 limit = 3;
  bool with_incidents = 4;
  FortTypeScanGroup gyms = 5;             // unset = exclude gyms (unless all three unset)
  FortTypeScanGroup pokestops = 6;
  FortTypeScanGroup stations = 7;
}

message GymScanResponse      { repeated Gym gyms = 1;           int32 examined = 2; int32 skipped = 3; int32 total = 4; bool limit_reached = 5; }
message PokestopScanResponse { repeated Pokestop pokestops = 1; int32 examined = 2; int32 skipped = 3; int32 total = 4; bool limit_reached = 5; }
message StationScanResponse  { repeated Station stations = 1;   int32 examined = 2; int32 skipped = 3; int32 total = 4; bool limit_reached = 5; }
message FortScanResponse {
  repeated Gym gyms = 1; repeated Pokestop pokestops = 2; repeated Station stations = 3;
  int32 examined = 4; int32 skipped = 5; int32 total = 6; bool limit_reached = 7;
}
```

**Repeated list semantics.** The JSON filter distinguishes an omitted or null list (no
constraint) from an explicitly empty one (matches nothing). proto3 cannot represent that
distinction, so an empty repeated field means no constraint. Every list converts to a nil slice
when empty, which is exactly the JSON "omitted" path in `isFortDnfMatch`. The "matches nothing"
case has no use and is not reproduced.

`Gym`, `Pokestop`, `Station`, `Incident` (from `ApiPokestopIncident`) and `StationBattle` (from
`ApiStationBattleResult`) mirror their Api structs field for field, in struct order, proto field
names equal to JSON tags, with these type rules:

| Go type | proto type |
|---|---|
| `string` | `string` |
| `*string`, `*bool` | `optional string`, `optional bool` |
| `int64` / `*int64` | `int64` / `optional int64` |
| `int16` (`lure_id`, `battle_level`, incident `display_type`/`style`/`character`) | `int32` |
| `bool` | `bool` |
| `*float64` | `optional double` |
| `*json.RawMessage`, `ApiGymGuardingPokemonRaw`, `ApiGymDefendersRaw` | `optional string` named `<json tag>_json` |
| `[]ApiPokestopIncident` invasions | `repeated Incident invasions` |
| `[]ApiStationBattleResult` battles | `repeated StationBattle battles` |

JSON passthrough fields, listed so the naming rule is concrete:

- Gym: `guarding_pokemon_display_json`, `defenders_json`, `rsvps_json`
- Pokestop: `quest_conditions_json`, `quest_rewards_json`, `alternative_quest_conditions_json`,
  `alternative_quest_rewards_json`, `showcase_focus_json`, `showcase_rankings_json`
- Station: `stationed_pokemon_json`

An unset `_json` field corresponds to JSON `null` (column unset, empty, or not valid JSON). The
text is copied from the stored column exactly as the HTTP API emits it.

### 2.4 Errors

Errors are gRPC status codes. There is no `Status` enum in any response (the stub service had
one; it duplicated the transport's own status).

| Condition | Code |
|---|---|
| `api_secret` configured and the secret metadata missing or wrong | `Unauthenticated` |
| Fort RPC called with `fort_in_memory` disabled (HTTP returns 503) | `FailedPrecondition` |
| `min` or `max` unset on a scan request | `InvalidArgument` |
| `GetPokemon` called with more `encounter_ids` than `tuning.max_pokemon_results` (cap > 0) | `InvalidArgument` |

`FailedPrecondition` is chosen over `Unavailable` because the condition is a configuration
state, not a transient fault; `Unavailable` tells generated clients to retry.

## 3. Authentication

The gRPC API uses the existing API secret: the `api_secret` value in the config file, which is
the same value the HTTP API checks in the `X-Golbat-Secret` header (`golbatSecretMiddleware`).
There is no new secret, no new config key, and `raw_bearer` plays no part.

A unary server interceptor in a new `grpc_auth.go` (package main):

- Applies only to methods whose full name starts with `/golbat_api.GolbatApi/`. Raw ingest
  (`/raw_receiver.RawProto/`) keeps its existing in-handler `raw_bearer` check unchanged, and the
  reflection service is unauthenticated (it exposes only the schema).
- Reads the secret from incoming metadata. The canonical key is `x-golbat-secret`, the same name
  as the HTTP header, carrying the bare secret. The `authorization` key is also accepted, as a
  bare secret or `Bearer <secret>`, because the stub service and its docs used it. Any one match
  passes. Compares with `crypto/subtle.ConstantTimeCompare`.
- An empty `config.Config.ApiSecret` disables the check, mirroring `golbatSecretMiddleware`.
- Failure returns `status.Error(codes.Unauthenticated, "invalid or missing api secret")`.

The same rule is also applied as a stream interceptor (`apiAuthStreamInterceptor`), so a future
streaming RPC on `GolbatApi` cannot be unauthenticated by omission; both interceptors share the
check via `apiSecretAllows(ctx, fullMethod)`.

`main.go` builds the server with `grpc.ChainUnaryInterceptor(<prometheus>, apiAuthUnaryInterceptor)`
when Prometheus is enabled and `grpc.ChainUnaryInterceptor(apiAuthUnaryInterceptor)` otherwise,
registers `GolbatApi` next to `RawProto`, and registers `reflection.Register(s)`
(`google.golang.org/grpc/reflection`, already in the grpc module). The stream chain
(`grpc.ChainStreamInterceptor`) is built the same way, in the same order.

## 4. Code layout

| File | Role |
|---|---|
| `grpc/api.proto`, `grpc/api.pb.go`, `grpc/api_grpc.pb.go` | new schema and generated code |
| `decoder/api_grpc_pokemon.go` | `GrpcScanPokemon`, `GrpcGetPokemon`; request → `ApiPokemonScan3`; `ApiPokemonResult` → `pb.Pokemon` |
| `decoder/api_grpc_fort.go` | `GrpcScanGyms/Pokestops/Stations/Forts`; request → `ApiFortScan` / `ApiFortCombinedScan`; `ApiGymResult` etc. → proto |
| `grpc_server_api.go` (main) | `grpcApiServer` implementing `pb.GolbatApiServer`: request validation, fort gate, delegate |
| `grpc_auth.go` (main) | the interceptor |
| `main.go` | interceptor chain, registration, reflection |
| `update_grpc.sh` | regenerates `api.proto` instead of `pokemon_api.proto` |

Deleted: `grpc/pokemon_api.proto` and its two generated files, `grpc_server_pokemon.go`,
`GrpcGetPokemonInArea2` (`api_pokemon_scan_v2.go`), `GrpcGetPokemonInArea3`
(`api_pokemon_scan_v3.go`), `convertToMinMax` (`api_pokemon_common.go`). `decoder` keeps its
`golbat/grpc` import for `pokemon_internal`.

Converters live in `decoder` because that is where every other API adapter lives (`api_*.go`),
and because the pokemon path needs the encounter id alongside each result: `ApiPokemonResult.Id`
is a decimal string, and parsing it back would be silly. `collectApiPokemonResults` is refactored
into a visitor, `forEachLivePokemonResult(keys, caller, func(id uint64, r *ApiPokemonResult))`,
used by both the JSON collector and the gRPC one, so the peek/expiry/build loop exists once.

Fort scans call the exported `*ScanEndpoint` functions and convert the returned Api structs.
Each conversion is a shallow field copy; JSON blob fields are one `string([]byte)` copy each,
matching the copy the HTTP path already makes.

Request conversion helpers: `intRangeToMinMax(*pb.IntRange) *ApiPokemonDnfMinMax` and a fort
twin returning `*ApiFortDnfMinMax`; `dnfIdsToApi([]*pb.DnfId)` for both `ApiPokemonDnfId` and
`ApiDnfId`; `int32sTo[T int8|int16]([]int32) []T` returning nil for empty input (the JSON
"omitted" representation).

## 5. Testing

**decoder (unit):**

- Golden conversion tests: build a fully populated `ApiPokemonResult` (every pointer set), convert,
  assert every proto field; repeat with every pointer nil and assert every optional is unset. Same
  for `ApiGymResult`, `ApiPokestopResult` (with invasions), `ApiStationResult` (with battles).
- Request conversion tests: `IntRange` defaults (unset max → 32767, unset min → 0), `DnfId`
  with and without form, gender list, empty repeated → nil slice, combined request group presence.
- Parity test (`api_grpc_parity_test.go`): for each pair (Api struct, proto message) walk the
  struct's exported fields, derive the expected proto field name from the JSON tag (plus `_json`
  for raw-JSON typed fields), and assert `msg.ProtoReflect().Descriptor().Fields().ByName(name)`
  exists; and the reverse, every proto field maps back to a struct field. Covers the four result
  messages, `Incident`, `StationBattle`, `PvpEntry`, `PvpRankings`, `PokemonDnfFilter`,
  `FortDnfFilter`, `FortScanRequest`, `FortCombinedScanRequest`, `PokemonScanRequest`. The Id
  field (`string` ↔ `uint64`) and the pokemon filter's `pokemon` list element (`ApiPokemonDnfId.id`
  ↔ `DnfId.pokemon_id`) are the two named exceptions.

**main (end to end over bufconn):**

- Auth: no secret configured → `ScanPokemon` succeeds without metadata; secret configured →
  missing → `Unauthenticated`, wrong value under either key → `Unauthenticated`,
  `x-golbat-secret` → OK, `authorization` bare → OK, `authorization: Bearer <secret>` → OK.
- Raw service: with `api_secret` set and no metadata, `SubmitRawProto` is not rejected by the
  interceptor (its own check governs).
- Fort gate: `fort_in_memory` off → `FailedPrecondition`; on → empty scan returns zero results
  with `total` equal to the (empty) tree size.
- Validation: missing `min` → `InvalidArgument`.
- Empty pokemon scan returns zero results and counts.
- `GetPokemon` cap: `encounter_ids` one over `tuning.max_pokemon_results` → `InvalidArgument`;
  exactly at the cap → OK.
- Stream interceptor (`apiAuthStreamInterceptor`, via a stub `grpc.ServerStream`): secret
  configured, `GolbatApi` method, no metadata → `Unauthenticated` and the handler does not run;
  `x-golbat-secret` right → handler runs; a raw (non-`GolbatApi`) method with no metadata →
  handler runs.
- Prometheus chain: a server built with a real `*grpcprom.ServerMetrics` still rejects an
  unauthenticated `ScanPokemon` call, proving interceptor order survives the metrics wrapper.

**Regeneration:** `update_grpc.sh` output must match the committed files (checked by running it
before the final commit; the tree already mixes `protoc` version comments, so only the plugin
versions v1.36.11 / v1.6.1 are pinned).

## 6. Documentation

- `api.md`: replace the gRPC section: port, that the secret is the same `api_secret` as HTTP,
  the `x-golbat-secret` metadata key (and the accepted `authorization` forms), the service listing, the semantic differences from JSON (§2.1 ranges, §2.3 lists, numeric encounter ids,
  `_json` fields, status codes), and a `grpcurl` example using reflection.
- `CLAUDE.md`: layout entries for the new files and a short gRPC API paragraph after the ingest
  section.
- `config.toml.example`: the `grpc_port` comment notes it serves both raw ingest and the API.

## 7. Compatibility

Removing the `Pokemon` service breaks any client of `Pokemon.Search` / `SearchV3`. Those returned
five fields per pokemon, so no real client can depend on them; the PR description states the
break explicitly.

The PR targets `perf/fortid-value-type` (PR 399) so it can be reviewed independently, and
retargets main once 399 merges.
