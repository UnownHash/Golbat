# Migrate Remaining APIs to Huma — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the remaining `/api` endpoints (fort scan, pokemon search/by-id, tier-3 reads, tier-4 operational) from gin handlers to documented Huma operations, with pointer-based response structs and a "Draft" badge on the fort scan endpoints.

**Architecture:** Reuse PR #368's Huma infrastructure (`setupHumaAPI`, `newHumaConfig`, `golbatSecret` scheme, goccy serializer, `ApiLatLon`, the body logger). Each endpoint becomes a `huma.Register` call in `routes_huma.go` wrapping the existing `decoder` logic; its gin route + handler are removed. Response structs using `guregu/null` are converted to pointers (`*T`) following `ApiPokemonResult`, updating their existing builders to `.Ptr()`. Done on branch `feat/huma-remaining-apis` (stacked on `feat/huma-pokemon-v2-v3-scan`).

**Tech Stack:** Go 1.26, gin, Huma v2 (+ humagin), goccy/go-json, guregu/null/v6, Stoplight Elements (docs UI).

**Reference spec:** `docs/superpowers/specs/2026-05-30-huma-remaining-apis-design.md`
**Reference pattern (read first):** `routes_huma.go`, `huma_api.go`, `decoder/api_latlon.go`, `decoder/api_pokemon_response.go` (the pokemon v2/v3 migration — every task follows these patterns).

**Conventions used throughout:**
- Operation security: `Security: []map[string][]string{{securitySchemeName: {}}}`.
- Preserve each endpoint's current HTTP status via `DefaultStatus`.
- Errors: return `huma.Error404NotFound(...)`, `huma.Error503ServiceUnavailable(...)`, `huma.Error400BadRequest(...)` as appropriate (Huma renders them as RFC7807 problem JSON).
- Null→pointer conversion rule: `null.Int→*int64`, `null.Float→*float64`, `null.String→*string`, `null.Bool→*bool`, **no `omitempty`** (preserve `null`), keep json tags + field order, add `doc:` tags. Update the builder to assign `field.Ptr()`.
- After each struct conversion, a golden-snapshot test pins the JSON (mirrors `decoder/api_pokemon_response_test.go:TestBuildApiPokemonResult_GoldenSnapshot`).

---

## PHASE 0 — Shared infrastructure

### Task 1: Unify the fort DNF range types

**Files:** Modify `decoder/api_fort.go` (types `ApiFortDnfMinMax8`/`ApiFortDnfMinMax16`, the `isFortDnfMatch` comparisons, and any `convertToFortMinMax8/16` helpers/grpc builders).

Mirrors the pokemon `ApiPokemonDnfMinMax` unification (commit `1a1845c`). The fort filter has `ApiFortDnfMinMax8` (int8) and `ApiFortDnfMinMax16` (int16) — collapse to one `ApiFortDnfMinMax` (int16) so the schema exposes one range type.

- [ ] **Step 1: Grep the usages**

Run: `grep -rn "ApiFortDnfMinMax8\|ApiFortDnfMinMax16\|ApiFortDnfMinMax\b" decoder/`
Record every field using the int8 variant (`PowerUpLevel`, `AvailableSlots`) and the int16 variant (`QuestRewardAmount`, `ContestTotalEntries`), plus the comparison sites in `isFortDnfMatch` (search `decoder/api_fort.go` for where these filter fields are compared against the `FortLookup` fields).

- [ ] **Step 2: Replace the two types with one**

In `decoder/api_fort.go`, delete `ApiFortDnfMinMax8` and `ApiFortDnfMinMax16`, add:
```go
// ApiFortDnfMinMax is an inclusive integer range used by the fort filter clauses
// (int16 internally — wide enough for all fort range fields).
type ApiFortDnfMinMax struct {
	Min int16 `json:"min" doc:"Minimum value (inclusive)."`
	Max int16 `json:"max" doc:"Maximum value (inclusive)."`
}
```
Change all `*ApiFortDnfMinMax8` and `*ApiFortDnfMinMax16` fields in `ApiFortDnfFilter` to `*ApiFortDnfMinMax`.

- [ ] **Step 3: Fix the comparison sites**

In `isFortDnfMatch`, wherever an int8 `FortLookup` field is compared to a now-int16 filter bound, cast the lookup field up: `int16(lookup.PowerUpLevel) < filter.PowerUpLevel.Min`. (The int16 fields need no cast.) Read the function and apply to each affected comparison. Collapse any `convertToFortMinMax8`/`convertToFortMinMax16` helpers to one `convertToFortMinMax` and update call sites.

- [ ] **Step 4: Build + vet**

Run: `go build -tags go_json ./... && go vet ./...`
Expected: clean. `grep -rn "ApiFortDnfMinMax8\|ApiFortDnfMinMax16" decoder/` → no matches.

- [ ] **Step 5: Commit**

```bash
git add decoder/api_fort.go
git commit -m "refactor: unify fort DNF range types (hide int8/int16 from schema)"
```

### Task 2: `ApiFortScan` bounding box → `ApiLatLon`

**Files:** Modify `decoder/api_fort.go` (`ApiFortScan` struct + the `internalGetForts`/`internalGetFortsCombined` reads of `.Min`/`.Max`).

`ApiFortScan.Min/Max` are `geo.Location` (capitalized JSON fields — same bug pokemon had). Switch to `ApiLatLon`.

- [ ] **Step 1: Change the struct + add accessors**

In `decoder/api_fort.go`:
```go
type ApiFortScan struct {
	Min        ApiLatLon          `json:"min" doc:"SW (minimum lat/lon) corner of the bounding box."`
	Max        ApiLatLon          `json:"max" doc:"NE (maximum lat/lon) corner of the bounding box."`
	Limit      int                `json:"limit" required:"false" doc:"Max results to return; 0 uses the server default."`
	DnfFilters []ApiFortDnfFilter `json:"filters" required:"false" doc:"OR'd filter clauses; a fort matches if it satisfies any one clause."`
}
```

- [ ] **Step 2: Fix the `.Min`/`.Max` reads**

`internalGetForts` (and `internalGetFortsCombined`) read `retrieveParameters.Min` / `.Max` as `geo.Location` (around api_fort.go:220-221, 240, 427). Replace those reads with `retrieveParameters.Min.Location()` / `.Max.Location()` so they still get a `geo.Location`.

- [ ] **Step 3: Build + vet**

Run: `go build -tags go_json ./... && go vet ./...` → clean.

- [ ] **Step 4: Commit**

```bash
git add decoder/api_fort.go
git commit -m "fix: fort scan bounding box takes ApiLatLon (lat/lon), not geo.Location"
```

### Task 3: Draft-badge helper + geofence bytes parser

**Files:** Create `routes_huma_draft.go` (package `main`); modify `geo/` (add a bytes-based fence parser); Test: `routes_huma_draft_test.go`.

- [ ] **Step 1: Write the draft-badge test**

`routes_huma_draft_test.go`:
```go
package main

import (
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestDraftBadge(t *testing.T) {
	op := &huma.Operation{Description: "Does a thing."}
	draftBadge(op)
	if op.Extensions["x-badges"] == nil {
		t.Errorf("expected x-badges to be set")
	}
	if !strings.HasPrefix(op.Description, "**Draft") {
		t.Errorf("expected description to start with the Draft note, got %q", op.Description)
	}
}
```

- [ ] **Step 2: Run it (fails to compile)**

Run: `go test . -run TestDraftBadge` → FAIL `undefined: draftBadge`.

- [ ] **Step 3: Implement the helper**

`routes_huma_draft.go`:
```go
package main

import "github.com/danielgtaylor/huma/v2"

// draftBadge marks an operation as a draft API: a "Draft" badge in the Stoplight
// docs (via the x-badges extension) and a note prepended to the description. Used
// for endpoints with no stable public consumers yet.
func draftBadge(op *huma.Operation) {
	op.Description = "**Draft — subject to change.**\n\n" + op.Description
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions["x-badges"] = []map[string]any{{"name": "Draft", "color": "orange"}}
}
```

- [ ] **Step 4: Add a bytes-based fence parser**

Read `geo/` for `NormaliseFenceRequest(c *gin.Context)`. It reads the request body and parses a GeoJSON feature. Refactor so the body-parsing logic is available without gin:
- Add `func NormaliseFenceFromBytes(body []byte) (*geojson.Feature, error)` containing the parse logic.
- Rewrite `NormaliseFenceRequest` to read `c`'s body bytes and delegate to `NormaliseFenceFromBytes` (so existing gin callers are unchanged).
Show the exact refactor by reading the current `NormaliseFenceRequest` body first; keep its existing behavior identical.

- [ ] **Step 5: Run tests + build**

Run: `go test . -run TestDraftBadge -v && go build -tags go_json ./...` → PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add routes_huma_draft.go routes_huma_draft_test.go geo/
git commit -m "feat: add draft-badge helper and bytes-based geofence parser"
```

---

## PHASE 1 — Fort scan endpoints

### Task 4: Convert `ApiGymResult` to pointers

**Files:** Modify `decoder/api_gym.go` (struct `ApiGymResult` + `buildGymResult`); Test: `decoder/api_gym_test.go`.

- [ ] **Step 1: Convert the struct**

Apply the null→pointer rule to every `null.X` field of `ApiGymResult` (api_gym.go:15-57). `null.String→*string`, `null.Int→*int64`. Keep `Id string`, `Lat/Lon float64`, `Updated int64`, `Deleted bool`, `FirstSeenTimestamp int64` as-is. Add a `doc:` tag to every field. Example (first fields; apply the same to all):
```go
type ApiGymResult struct {
	Id                    string   `json:"id" doc:"Gym fort ID"`
	Lat                   float64  `json:"lat" doc:"Latitude"`
	Lon                   float64  `json:"lon" doc:"Longitude"`
	Name                  *string  `json:"name" doc:"Gym name"`
	Url                   *string  `json:"url" doc:"Image URL"`
	LastModifiedTimestamp *int64   `json:"last_modified_timestamp" doc:"Last modified unix timestamp"`
	// ... convert every remaining null.String -> *string, null.Int -> *int64, add doc tags ...
}
```

- [ ] **Step 2: Update the builder**

In `buildGymResult`, change each converted field assignment from `gym.Field` to `gym.Field.Ptr()`. Plain fields (`Id`, `Lat`, `Lon`, `Updated`, `Deleted`, `FirstSeenTimestamp`) stay as direct assignments. (`BuildGymResult` just calls `buildGymResult`, no change.)

- [ ] **Step 3: Write the golden snapshot test**

`decoder/api_gym_test.go` — construct a `*Gym` with a representative mix of set/unset fields, marshal `buildGymResult(g)`, assert the exact JSON string. Generate the expected string by running the test once with a placeholder and copying the actual output (like `TestBuildApiPokemonResult_GoldenSnapshot`). Assert unset null fields serialize as `null`.

- [ ] **Step 4: Run + build**

Run: `go test ./decoder/ -run TestBuildGymResult -v && go build -tags go_json ./...` → PASS + clean.

- [ ] **Step 5: Commit**

```bash
git add decoder/api_gym.go decoder/api_gym_test.go
git commit -m "refactor: ApiGymResult pointer-based + doc tags (Huma-documentable)"
```

### Task 5: Convert `ApiPokestopResult` to pointers

**Files:** Modify `decoder/api_pokestop.go` (`ApiPokestopResult` + `buildPokestopResult`); Test: `decoder/api_pokestop_test.go`.

Identical pattern to Task 4. Convert every `null.String→*string`, `null.Int→*int64`, `null.Bool→*bool` field in `ApiPokestopResult` (api_pokestop.go:5-49); keep `Id`, `Lat`, `Lon`, `Updated`, `Deleted`, `LureId int16`, `FirstSeenTimestamp int16`. Add `doc:` tags. Update `buildPokestopResult` to `.Ptr()` the converted fields. Add a golden-snapshot test `TestBuildPokestopResult`. Run `go test ./decoder/ -run TestBuildPokestopResult -v && go build -tags go_json ./...` → PASS. Commit:
```bash
git commit -m "refactor: ApiPokestopResult pointer-based + doc tags"
```

### Task 6: Convert `ApiStationResult` to pointers

**Files:** Modify `decoder/api_station.go` (`ApiStationResult` + `BuildStationResult`); Test: `decoder/api_station_test.go`.

Same pattern. Convert every `null.Int→*int64`, `null.String→*string` field in `ApiStationResult` (api_station.go:5-28); keep `Id`, `Lat`, `Lon`, `Name string`, `StartTime`, `EndTime`, `IsBattleAvailable`, `Updated`. Add `doc:` tags. Update `BuildStationResult` to `.Ptr()`. Add golden-snapshot `TestBuildStationResult`. Run + commit:
```bash
git commit -m "refactor: ApiStationResult pointer-based + doc tags"
```

### Task 7: Document the fort filter + envelopes

**Files:** Modify `decoder/api_fort.go` (`ApiFortDnfFilter`, `ApiDnfId`, the scan-result envelopes).

- [ ] **Step 1: Add doc tags + required/optional**

Add a `doc:` tag to every field of `ApiFortDnfFilter` (explain raid/quest/incident/contest/battle filters), `ApiDnfId` (`pokemon_id`, `form`), and the envelope structs `ApiGymScanResult`/`ApiPokestopScanResult`/`ApiStationScanResult`/`ApiFortCombinedScanResult`. Mark all `ApiFortDnfFilter` fields `required:"false"` (every filter attribute is optional). For `ApiDnfId`, mark `form` `required:"false"` and leave `pokemon_id` required (mirrors pokemon `ApiPokemonDnfId`).

- [ ] **Step 2: Build + vet**

Run: `go build -tags go_json ./... && go vet ./...` → clean.

- [ ] **Step 3: Commit**

```bash
git add decoder/api_fort.go
git commit -m "docs: doc tags + required/optional on fort scan request/result types"
```

### Task 8: Register the 4 fort scan Huma operations

**Files:** Modify `routes_huma.go` (add input/output types + registrations), `main.go` (remove the 4 gin routes), `routes.go` (remove `GymScan`/`PokestopScan`/`StationScan`/`FortScan` handlers); Test: `huma_routes_test.go` (e2e + draft-badge assertions).

- [ ] **Step 1: Add the operations**

In `routes_huma.go`, for each of gym/pokestop/station/fort scan, add input/output types and a `huma.Register` (the `dbDetails` package global is available in `main`). Pattern (gym shown; repeat for the other three with their types/paths/endpoint funcs):
```go
type gymScanInput struct{ Body decoder.ApiFortScan }
type gymScanOutput struct{ Body decoder.ApiGymScanResult }

func registerFortScanRoutes(api huma.API) {
	gymOp := huma.Operation{
		OperationID:   "scan-gyms", Method: http.MethodPost, Path: "/api/gym/scan",
		Summary:       "Search gyms in a bounding box (DNF filters)",
		Description:   "Returns gyms within [min,max] matching any DNF filter clause.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&gymOp)
	huma.Register(api, gymOp, func(ctx context.Context, in *gymScanInput) (*gymScanOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &gymScanOutput{Body: *decoder.GymScanEndpoint(in.Body, dbDetails)}, nil
	})
	// ... repeat: scan-pokestops (/api/pokestop/scan, PokestopScanEndpoint, ApiPokestopScanResult),
	//             scan-stations (/api/station/scan, StationScanEndpoint, ApiStationScanResult),
	//             scan-forts    (/api/fort/scan, FortCombinedScanEndpoint, ApiFortCombinedScanResult)
	//     each with draftBadge(&op) and the FortInMemory 503 guard.
}
```
Call `registerFortScanRoutes(humaAPI)` from where `registerHumaRoutes` is invoked (in `main.go`, alongside the existing `registerHumaRoutes(humaAPI)` — or call it inside `registerHumaRoutes`).

- [ ] **Step 2: Remove the gin routes + handlers**

In `main.go` delete the four `apiGroup.POST(".../scan", ...)` fort lines. In `routes.go` delete the `GymScan`, `PokestopScan`, `StationScan`, `FortScan` functions. Fix any now-unused imports.

- [ ] **Step 3: Write e2e + draft-badge tests**

In `huma_routes_test.go` add: (a) an e2e test posting an empty-filter `ApiFortScan` (with `config.Config.FortInMemory=true`, `ApiSecret=""`) to `/api/gym/scan` asserting 200 and a `{gyms,examined,skipped,total}` envelope; a 401 without the secret when `ApiSecret` set; and a 503 when `FortInMemory=false`. (b) An OpenAPI assertion that the four fort ops carry the `x-badges` Draft badge (marshal `api.OpenAPI()`, find the operations, assert the extension) and that `scan-pokemon-v2` does NOT.

- [ ] **Step 4: Run + build + vet**

Run: `go test ./... && go build -tags go_json ./... && go vet ./...` → all green.

- [ ] **Step 5: Commit**

```bash
git add routes_huma.go main.go routes.go huma_routes_test.go
git commit -m "feat: serve fort scan via Huma (draft), retire gin handlers"
```

---

## PHASE 2 — Pokemon search/by-id + Tier 3 reads

### Task 9: Pokemon search + by-id

**Files:** Modify `decoder/api_pokemon.go` (`ApiPokemonSearch` Min/Max/Center → `ApiLatLon` + doc tags), `routes_huma.go` (register), `main.go`/`routes.go` (remove gin route + `PokemonSearch`/`PokemonOne`).

- [ ] **Step 1: Convert ApiPokemonSearch coordinates**

In `decoder/api_pokemon.go`, change `ApiPokemonSearch.Min/Max/Center` from `geo.Location` to `ApiLatLon`, add doc tags + `required:"false"` on `center`/`limit`/`searchIds` (min/max required). Update `SearchPokemon` internals that read `.Min`/`.Max`/`.Center` to use `.Location()`.

- [ ] **Step 2: Register the operations**

In `routes_huma.go`:
```go
type pokemonSearchInput struct{ Body decoder.ApiPokemonSearch }
type pokemonSearchOutput struct{ Body []decoder.ApiPokemonResult }
// POST /api/pokemon/search, OperationID "search-pokemon", Tags ["Pokemon"],
// DefaultStatus 202, security golbatSecret.
// handler: res, err := decoder.SearchPokemon(in.Body); on err return huma.Error400BadRequest(...);
//          build []ApiPokemonResult (deref the []*ApiPokemonResult into values) and return.

type pokemonByIdInput struct{ PokemonId uint64 `path:"pokemon_id" doc:"Encounter ID"` }
type pokemonByIdOutput struct{ Body decoder.ApiPokemonResult }
// GET /api/pokemon/id/{pokemon_id}, OperationID "get-pokemon", DefaultStatus 202.
// handler: res := decoder.GetOnePokemon(in.PokemonId); if res == nil return nil, huma.Error404NotFound("not found");
//          return &pokemonByIdOutput{Body: *res}, nil
```
Note: Huma path params use `{name}` not `:name`. `SearchPokemon` returns `[]*ApiPokemonResult`; deref into `[]ApiPokemonResult` for the body (or change the output to `[]*decoder.ApiPokemonResult` — pick one, keep consistent).

- [ ] **Step 3: Remove gin routes/handlers**

Delete `apiGroup.POST("/pokemon/search", ...)` and `apiGroup.GET("/pokemon/id/:pokemon_id", ...)` from `main.go`; delete `PokemonSearch` and `PokemonOne` from `routes.go`.

- [ ] **Step 4: Test + build + commit**

e2e: search with empty searchIds (expect the current 400 behavior preserved) and a by-id 404. Run `go test ./... && go build -tags go_json ./...`. Commit:
```bash
git commit -m "feat: serve pokemon search + by-id via Huma"
```

### Task 10: Convert `ApiTappableResult` to pointers

**Files:** Modify `decoder/api_tappable.go` (`ApiTappableResult` + `BuildTappableResult`); Test: `decoder/api_tappable_test.go`.

Same null→pointer pattern as Task 4 (fields: `FortId→*string`; `SpawnId`/`Encounter`/`ItemId`/`Count`/`ExpireTimestamp→*int64`; keep `Id uint64`, `Lat`, `Lon`, `Type string`, `ExpireTimestampVerified bool`, `Updated int64`). Add doc tags, update `BuildTappableResult` to `.Ptr()`, add golden-snapshot `TestBuildTappableResult`. Commit:
```bash
git commit -m "refactor: ApiTappableResult pointer-based + doc tags"
```

### Task 11: Register Tier-3 read endpoints

**Files:** Modify `routes_huma.go`, `decoder/api_gym.go` (doc tags on `ApiGymSearch`/`ApiGymSearchFilter`), `main.go`/`routes.go` (remove gin routes/handlers `GetGyms`, `GetStations`, `SearchGyms`, `GetGym`, `GetPokestop`, `GetTappable`, `GetPokestopPositions`).

Register each (reuse the existing decoder functions; the converted result structs document automatically):

- [ ] **`gym/query`** (`GetGyms`): input `struct{ Body struct{ IDs []string `json:"ids" doc:"Fort IDs to fetch (max 500)"` } }` — **standardize on `{"ids":[...]}` only** (drop the bare-array form). Output `struct{ Body []decoder.ApiGymResult }`, status 200. Handler replicates the existing dedup + 500-cap + 5s-timeout loop calling `decoder.GetGymRecordReadOnly` + `decoder.BuildGymResult`; over-500 → `huma.Error400BadRequest`.
- [ ] **`station/query`** (`GetStations`): same shape, `[]decoder.ApiStationResult`, `GetStationRecordReadOnly` + `BuildStationResult`.
- [ ] **`gym/search`** (`SearchGyms`): input `struct{ Body decoder.ApiGymSearch }` (add doc tags to `ApiGymSearch`/`ApiGymSearchFilter`/`LocationDistance`), output `[]decoder.ApiGymResult`, status 200. Handler replicates the existing filter validation + `decoder.SearchGymsAPI` + result fetch; timeout → `huma.Error504GatewayTimeout`, bad filters → `huma.Error400BadRequest`.
- [ ] **`gym/id/{gym_id}`** (`GetGym`): input `struct{ GymId string `path:"gym_id"` }`, output `decoder.ApiGymResult`, status 202. Handler: `GetGymRecordReadOnly`; nil → 404.
- [ ] **`pokestop/id/{fort_id}`** (`GetPokestop`): input `struct{ FortId string `path:"fort_id"` }`, output `decoder.ApiPokestopResult`, status 202. Handler: `PeekPokestopRecord`; nil → 404.
- [ ] **`tappable/id/{tappable_id}`** (`GetTappable`): input `struct{ TappableId uint64 `path:"tappable_id"` }`, output `decoder.ApiTappableResult`, status 202. Handler: `PeekTappableRecord`; nil → 404.
- [ ] **`pokestop-positions`** (`GetPokestopPositions`): geofence endpoint — input `struct{ RawBody []byte }`, output `struct{ Body []db.QuestLocation }`, status 202. Handler: `fence, err := geo.NormaliseFenceFromBytes(in.RawBody)`; on err `huma.Error400BadRequest`; `decoder.GetPokestopPositions(dbDetails, fence)`.

Remove each corresponding gin route from `main.go` and handler from `routes.go`. Add e2e tests for a representative few (a by-id 404; a `gym/query` with `{"ids":[]}` → empty array 200). Run `go test ./... && go build -tags go_json ./...`. Commit:
```bash
git commit -m "feat: serve tier-3 read endpoints via Huma"
```

---

## PHASE 3 — Tier 4 operational

### Task 12: Geofence write/status endpoints

**Files:** Modify `routes_huma.go`, `main.go`/`routes.go` (remove `GetQuestStatus`, `ClearQuests`).

- [ ] **`quest-status`** (`GetQuestStatus`): input `struct{ RawBody []byte }`, output `struct{ Body db.QuestStatus }`, status 200. Handler: `fence, err := geo.NormaliseFenceFromBytes(in.RawBody)`; err → 400; `decoder.GetQuestStatusWithGeofence(dbDetails, fence)`.
- [ ] **`clear-quests`** (`ClearQuests`): input `struct{ RawBody []byte }`, output `struct{ Body StatusResponse }` (move/duplicate the `StatusResponse{Status string}` type into a shared spot accessible to `routes_huma.go`), status 202. Handler: parse fence (err → 400), 10s context, `decoder.ClearQuestsWithinGeofence(ctx, dbDetails, fence)`, return `{Status:"ok"}`. (Performs DB deletes — keep behavior identical.)

Remove the two gin routes/handlers. Test + build. Commit:
```bash
git commit -m "feat: serve quest-status + clear-quests via Huma (geofence body)"
```

### Task 13: Remaining operational endpoints

**Files:** Modify `routes_huma.go`, `main.go`/`routes.go` (remove `GetDevices`, `GetFortTrackerCell`, `GetFortTrackerFort`, `ReloadGeojson`, `SkipPreservePokemon`).

- [ ] **`devices/all`** (`GetDevices`): GET, output `struct{ Body struct{ Devices map[string]ApiDeviceLocation `json:"devices"` } }`, status 200. Handler returns `GetAllDevices()` wrapped.
- [ ] **`fort-tracker/cell/{cell_id}`** (`GetFortTrackerCell`): input `struct{ CellId uint64 `path:"cell_id"` }`, output `decoder.CellFortInfo`, status 200. Handler: tracker nil → 503; `GetCellInfo`; nil → 404.
- [ ] **`fort-tracker/forts/{fort_id}`** (`GetFortTrackerFort`): input `struct{ FortId string `path:"fort_id"` }`, output `decoder.FortTrackerInfo`, status 200. Handler: tracker nil → 503; `GetFortInfo`; nil → 404.
- [ ] **`reload-geojson`** (`ReloadGeojson`): register BOTH GET and POST `/api/reload-geojson` (two `huma.Register` calls, distinct OperationIDs `reload-geojson-get`/`-post`), no input, output `struct{ Body StatusResponse }`, status 202. Handler calls `decoder.ReloadGeofenceAndClearStats()`.
- [ ] **`skip-preserve-pokemon`** (`SkipPreservePokemon`): register GET + POST, no input, output `struct{ Body struct{ Status string `json:"status"`; Message string `json:"message"` } }`, status 200. Handler calls `decoder.SetSkipPreservePokemon(true)`.

Remove the gin routes/handlers. Test + build. Commit:
```bash
git commit -m "feat: serve tier-4 operational endpoints via Huma"
```

---

## Final task: cleanup + verification

- [ ] **Step 1: Confirm only intended routes remain on gin**

Run: `grep -nE "apiGroup\.(GET|POST)" main.go`
Expected remaining: `/api/health`, `/api/pokemon/scan` (v1), `/api/pokemon/available`, `/api/reload-geojson` only if not migrated. Everything else should be gone. Confirm `r.POST("/raw")`, `/health`, `/version` remain on root.

- [ ] **Step 2: Full green + manual docs check**

Run: `go build -tags go_json ./... && go vet ./... && go test ./...` → all green. Boot against the live DB, open `/docs`, confirm the new operations appear grouped by tag (Pokemon/Fort/etc.), the fort scan ops show the orange **Draft** badge, and the converted result schemas show typed nullable fields.

- [ ] **Step 3: Record results in the spec**

Append a short "Results" section to `docs/superpowers/specs/2026-05-30-huma-remaining-apis-design.md` (endpoints migrated, anything left on gin, draft badge confirmed). Commit.

---

## Self-review notes

- **Spec coverage:** Task 1–3 = shared infra (fort range unification, ApiLatLon, draft+geofence helpers); Tasks 4–8 = fort scan (draft); Tasks 9–11 = pokemon search/id + tier 3; Tasks 12–13 = tier 4. All spec endpoints covered; v1 pokemon scan + `/raw` + `/health` + `/version` + `/pokemon/available` intentionally left on gin.
- **Wire-compat traps:** null→pointer (no omitempty → still `null`); golden snapshots per converted struct; status codes preserved per endpoint (fort scans 200, by-id 202, etc.); `gym/query`/`station/query` standardized to `{"ids":[...]}` (documented behavior change).
- **Decisions baked in:** geofence endpoints use `RawBody` + `geo.NormaliseFenceFromBytes`; fort range types unified to `ApiFortDnfMinMax` (int16, cast lookups); draft badge on fort scan only.
- **Reuse:** every registration follows the pokemon `routes_huma.go` pattern; `ApiLatLon`, `securitySchemeName`, goccy config, the body logger all reused from PR #368.
