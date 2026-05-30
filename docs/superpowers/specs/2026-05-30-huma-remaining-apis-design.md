# Design: Migrate the remaining API endpoints to Huma

**Date:** 2026-05-30
**Status:** Approved (design), pending implementation plan
**Author:** James Berry (with Claude)
**Branch:** `feat/huma-remaining-apis` (stacked on `feat/huma-pokemon-v2-v3-scan` / PR #368)

## Goal

Migrate (almost) all remaining `/api` endpoints from the hand-rolled gin handlers
to Huma operations, so the whole API is documented in OpenAPI and browsable at
`/docs`. This builds directly on the pokemon v2/v3 migration (PR #368), reusing its
infrastructure (`setupHumaAPI`, `newHumaConfig`, the `golbatSecret` scheme, the
goccy serializer, `ApiLatLon`, the per-field required/optional pattern, the body
logger).

Because this depends on PR #368's infrastructure, this branch is **stacked** on
`feat/huma-pokemon-v2-v3-scan`. It should land after #368 merges (rebasing onto
`main`), or as a stacked PR targeting that branch.

## Decisions (resolved during brainstorming)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Representation | **Pointer-based response structs everywhere** (`*T`, no omitempty, `doc:` tags). No `RegisterTypeAlias`, no `null.X` in the API layer. | A pointer *is* the external contract (maps directly to "nullable"), decoupled from the internal `guregu`/DB type — the most honest external-schema representation. Pokemon already does this, so it is the template; uniform style across all endpoints. |
| Bounding box | **Reuse `ApiLatLon`** for every scan's min/max. | Fort scan embeds the same internal `geo.Location` (capitalized fields) bug pokemon had; `ApiLatLon` accepts lat/lon (+ latitude/longitude alias) and documents lat/lon. |
| Builders | **Reuse and modify the existing builders**; do not write new ones. | `buildGymResult`/`BuildGymResult`, `buildPokestopResult`/`BuildPokestopResult`, `BuildStationResult` already convert entities → `Api*Result`. The change is field types `null.X → *T` + `.Ptr()` in the builder. |
| Draft marking | **Fort scan endpoints only**, via Stoplight `x-badges` + a description note. | Fort scan has no real public consumers yet; the badge signals "subject to change" without the wrong semantics of OpenAPI `deprecated`. |
| Scope / PR | **One branch, all ~18 endpoints**, built as many small commits. | The work is mechanical and follows a proven template; keeping it one PR is fine given the existing builders. |
| v1 pokemon scan | **Leave on gin (deprecated).** | Its `[]int8`-as-min/max input documents terribly; migrating it is a deprecation decision, out of scope. |

## Architecture

### Per-endpoint pattern (uniform)

For each migrated endpoint:

1. Define Huma input/output wrapper structs in `routes_huma.go` (package `main`):
   `type xxxInput struct { Body decoder.RequestType }` (or path/query params via
   Huma field tags for GET-by-id endpoints), `type xxxOutput struct { Body ResponseType }`.
2. `huma.Register(api, huma.Operation{...}, handler)` where the handler is a thin
   wrapper calling the existing `decoder` logic function.
3. Operation metadata: `OperationID`, `Summary`, `Description`, `Tags`, `Security:
   {golbatSecret}`, and `DefaultStatus` matching the current gin status code.
4. Remove the corresponding `apiGroup` gin route and the now-dead gin handler in
   `routes.go` (keeping any shared logic the handler delegated to).

GET-by-id endpoints use Huma path params, e.g.
`type getGymInput struct { GymId string `path:"gym_id"` }`.

### Response struct conversion (the bulk of the work)

Every `Api*Result` response struct still using `guregu/null` is converted to
pointers, following `ApiPokemonResult`:
- `null.Int → *int64`, `null.Float → *float64`, `null.String → *string`,
  `null.Bool → *bool`. No `omitempty` (preserve `null` on the wire). Add `doc:`
  tags to every field. Keep field order and json tags identical (wire-compat).
- Update the struct's existing builder(s) to assign via `.Ptr()` instead of
  copying the `null.X` value. Plain (non-null) fields are unchanged.

Structs in scope (initial list — the plan will confirm the full set by grep):
`ApiGymResult`, `ApiPokestopResult`, `ApiStationResult`, the gym/station **query**
result structs, the tappable result struct, and any other `Api*Result` on `null.X`.
The scan **envelope** structs (`ApiGymScanResult` etc.) already hold `[]*Api*Result`
and need only `doc:` tags.

Wire compatibility holds because `null.X` and the corresponding pointer marshal
identically (value or `null`), and Huma does not validate responses.

### Draft marking helper

A small helper in `routes_huma.go`:

```go
func draftBadge(op *huma.Operation) {
    op.Description = "**Draft — subject to change.**\n\n" + op.Description
    op.Extensions = map[string]any{
        "x-badges": []map[string]any{{"name": "Draft", "color": "orange"}},
    }
}
```

Applied to the four fort scan operations only. (`x-badges` is rendered by Stoplight
Elements, which Huma serves at `/docs`.)

### Bounding box

`ApiFortScan.Min/Max` change from `geo.Location` to `ApiLatLon` (with the
`GetMin()/GetMax()` accessors returning `.Location()`), and the gRPC/other builders
that construct `ApiFortScan` switch to `ApiLatLon{Lat,Lon}` — mirroring the pokemon
change. The scan endpoints continue to 503 when `config.Config.FortInMemory` is off.

## Endpoint inventory

**Fort scan (draft):**
- `POST /api/gym/scan` → `GymScan`/`GymScanEndpoint`
- `POST /api/pokestop/scan` → `PokestopScan`/`PokestopScanEndpoint`
- `POST /api/station/scan` → `StationScan`/`StationScanEndpoint`
- `POST /api/fort/scan` → `FortScan`/`FortCombinedScanEndpoint`

**Pokemon (finish the family; v1 stays on gin):**
- `POST /api/pokemon/search` → `PokemonSearch`/`SearchPokemon`
- `GET /api/pokemon/id/:pokemon_id` → `PokemonOne`/`GetOnePokemon`

**Tier 3 (reads/queries):**
- `POST /api/gym/query` → `GetGyms`
- `POST /api/station/query` → `GetStations`
- `POST /api/gym/search` → `SearchGyms`
- `GET /api/gym/id/:gym_id` → `GetGym`
- `GET /api/pokestop/id/:fort_id` → `GetPokestop`
- `GET /api/tappable/id/:tappable_id` → `GetTappable`
- `POST /api/pokestop-positions` → `GetPokestopPositions`

**Tier 4 (operational/admin):**
- `GET /api/devices/all` → `GetDevices`
- `GET /api/fort-tracker/cell/:cell_id` → `GetFortTrackerCell`
- `GET /api/fort-tracker/forts/:fort_id` → `GetFortTrackerFort`
- `POST /api/quest-status` → `GetQuestStatus`
- `POST /api/clear-quests` → `ClearQuests`
- `GET|POST /api/reload-geojson` → `ReloadGeojson`
- `GET|POST /api/skip-preserve-pokemon` → `SkipPreservePokemon`

**Stay on gin (out of scope):** `POST /raw`, `GET /health`, `GET /version`,
`POST /api/pokemon/scan` (v1, deprecated), and `GET /api/pokemon/available` (not in
the requested scope — leave on gin for now).

## Error handling

- Request validation: Huma validates bodies against the schema and returns 422
  before the handler (same as pokemon). Per-field required/optional tags applied to
  request structs as appropriate (the plan will specify per endpoint; default to the
  pokemon convention — bounding box required, filters/limit optional).
- `fort_in_memory` gating: fort scan handlers return 503 via `huma.Error503...`
  when the feature is disabled, preserving current behavior.
- Reused `additionalProperties: false` strictness; coordinate objects stay lenient
  via `ApiLatLon` (only the coordinate accepts extra keys).
- Endpoints that currently return non-2xx (e.g. search with empty input) keep their
  status via mapped Huma errors.

## Testing

- **Golden snapshots** for each converted result struct (`ApiGymResult`,
  `ApiPokestopResult`, `ApiStationResult`, query results, tappable), asserting the
  exact JSON of the builder for a representative entity — guarding wire-compat after
  the `null.X → *T` flip (mirrors `TestBuildApiPokemonResult_GoldenSnapshot`).
- **OpenAPI discoverability** assertions: each new operation, its tags, and the key
  schemas appear in the generated spec.
- **Draft-badge assertion**: the four fort scan operations carry the `x-badges`
  Draft badge and the others do not.
- **e2e (humatest)** for a representative endpoint per group: auth (401/200),
  status, and response envelope shape (no DB needed; empty caches return empty).
- Full suite + `go build -tags go_json ./...` + `go vet` green at each step.

## Out of scope / future

- Migrating v1 pokemon scan (deprecated) and the ingest `/raw` path.
- Per-field semantic constraints on numeric ranges (e.g. IV 0–15) — a shared range
  type can't express per-field domains; separate future change.
- Splitting draft vs stable into separate PRs (we chose one PR).
