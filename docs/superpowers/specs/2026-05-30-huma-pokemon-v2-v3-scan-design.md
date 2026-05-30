# Design: Migrate `POST /api/pokemon/v2/scan` and `/v3/scan` to Huma

**Date:** 2026-05-30
**Status:** Approved (design), pending implementation plan
**Author:** James Berry (with Claude)

## Goal

Migrate the Pokemon **v2 and v3** scan HTTP endpoints from hand-rolled gin
handlers to [Huma](https://huma.rocks) operations, producing a fully
self-documenting OpenAPI 3.1 spec. This is a **proof-of-concept** to evaluate how
much discoverability we gain from Huma before committing to migrating the rest of
the API. v2 is included because we have an important production v2 consumer we can
test the migrated endpoint against.

Success = a browsable `/docs` page where a consumer can understand the v2/v3 scan
requests and responses — including the previously-opaque PVP structure — without
reading Go source, AND the real v2 consumer works unchanged against the migrated
endpoint.

### Non-goals

- Migrating v1, search, or fort endpoints. Those stay on gin.
- Changing the rtree / DNF filtering logic. `internalGetPokemonInArea2` and
  `internalGetPokemonInArea3` are reused verbatim.
- Changing the gRPC paths (`GrpcGetPokemonInArea2`, `GrpcGetPokemonInArea3`).

## Decisions (resolved during brainstorming)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Endpoint strategy | **Replace v2 and v3 in place** at their existing paths | One canonical, documented endpoint each; responses stay wire-compatible. |
| v2 response envelope | **Bare JSON array** `[]PokemonResult` (unchanged) | v2 returns a bare array today. The important v2 consumer depends on this shape; must not wrap it. v3 keeps its `{pokemon, examined, skipped, total}` wrapper. |
| PVP representation | **Fixed league struct** `{little, great, ultra}` | Leagues are hardcoded in `decoder/main.go:209`, `decoder/pokemonRtree.go:177`, and the DNF filter inputs. A fixed struct documents each league explicitly instead of an opaque `additionalProperties` map. The "new leagues might appear" hypothesis never materialized and would require code changes everywhere anyway. |
| Nullable fields | **Pointers without `omitempty`** (`*int64` etc.) | Missing values still serialize as `null`, identical JSON to today's `guregu/null`. Non-breaking on the wire, clean Go types, documented as nullable. |
| JSON serializer | **Override Huma's format with `goccy/go-json`** | Huma uses its own serializer (stdlib `encoding/json`), NOT gin's. Our `-tags go_json` build tag only affects gin's `c.JSON()`. Without an override, the migrated endpoints would regress to stdlib JSON. |

## Background: how the current v2/v3 paths work

**v2** (bare array response):
- `routes.go:379` — `PokemonScan2(c *gin.Context)`: `c.BindJSON` into
  `decoder.ApiPokemonScan2`, calls `decoder.GetPokemonInArea2`, returns
  `c.JSON(202, res)`.
- `decoder/api_pokemon_scan_v2.go:98` — `GetPokemonInArea2` calls
  `internalGetPokemonInArea2` (rtree bbox + DNF, returns matched keys + counts),
  discards the counts, and returns `[]*ApiPokemonResult` (bare array).
- Request `ApiPokemonScan2` uses `ApiPokemonDnfFilter` whose `Gender` is
  `*ApiPokemonDnfMinMax8` (min/max range).

**v3** (wrapper response):
- `routes.go:396` — `PokemonScan3(c *gin.Context)`: `c.BindJSON` into
  `decoder.ApiPokemonScan3`, calls `decoder.GetPokemonInArea3`, returns
  `c.JSON(202, res)`.
- `decoder/api_pokemon_scan_v3.go:105` — `GetPokemonInArea3` calls
  `internalGetPokemonInArea3`, then per key `peekPokemonRecordReadOnly` +
  `buildApiPokemonResult`, returns
  `*PokemonScan3Result{ Pokemon []*ApiPokemonResult, Examined, Skipped, Total }`.
- Request `ApiPokemonScan3` uses `ApiPokemonDnfFilter3` whose `Gender` is
  `[]int8` (array).

**Shared:** `decoder/api_pokemon_common.go:72` — `buildApiPokemonResult` builds
`ApiPokemonResult`, whose `Pvp interface{}` field (line 68) is actually a
`map[string][]gohbem.PokemonEntry` produced by `ohbem.QueryPvPRank(...)`.

`buildApiPokemonResult` / `ApiPokemonResult` are **shared with v1 and search** —
they must NOT be removed or altered.

## Design

### 1. Huma infrastructure (one-time, reusable by future migrations)

New file `huma_api.go` (package `main`) providing `setupHumaAPI(r *gin.Engine) huma.API`:

- Add dependencies: `github.com/danielgtaylor/huma/v2` and
  `github.com/danielgtaylor/huma/v2/adapters/humagin`.
- Mount Huma on the **root** gin engine `r` (NOT the authed `/api` group), so the
  docs UI (`/docs`) and spec (`/openapi.json`) are publicly reachable for browsing
  (the deliverable of the POC).
- **Serializer parity:** override the JSON format with goccy so the migrated hot
  endpoints match today's performance:

  ```go
  import gojson "github.com/goccy/go-json"

  cfg := huma.DefaultConfig("Golbat API", version) // version from the app's build info
  goccyFmt := huma.Format{
      Marshal:   func(w io.Writer, v any) error { return gojson.NewEncoder(w).Encode(v) },
      Unmarshal: gojson.Unmarshal,
  }
  cfg.Formats = map[string]huma.Format{"application/json": goccyFmt, "json": goccyFmt}
  ```

  This is a runtime override that applies unconditionally; the `go_json` build tag
  remains only for the still-gin-served routes.

- **Auth as a documented security scheme.** Mounted on root, the operations do not
  inherit the `/api` group's `AuthRequired()` middleware. Instead:
  - Register an `apiKey` security scheme in `cfg.Components.SecuritySchemes` named
    `golbatSecret` (type `apiKey`, `in: header`, name `X-Golbat-Secret`).
  - Add a Huma middleware that, for any operation declaring that security
    requirement, validates `X-Golbat-Secret` against `config.Config.ApiSecret`
    (bypassing when `ApiSecret` is empty, mirroring `AuthRequired()`), returning
    `huma.Error401Unauthorized` on mismatch.
  - This makes the auth requirement visible in the OpenAPI spec — a discoverability
    improvement over the invisible gin middleware.

### 2. New shared response types (new file `decoder/api_pokemon_response.go`)

```go
// PvpEntry mirrors gohbem.PokemonEntry with documentation.
type PvpEntry struct {
    Pokemon    int     `json:"pokemon" doc:"Pokedex number this ranking is for"`
    Form       int     `json:"form,omitempty" doc:"Form id (0 = base form)"`
    Cap        float64 `json:"cap,omitempty" doc:"Level cap this ranking was computed under"`
    Value      float64 `json:"value,omitempty" doc:"Stat product used for ranking"`
    Level      float64 `json:"level" doc:"Level at which this rank is achieved"`
    Cp         int     `json:"cp,omitempty" doc:"CP at the ranked level"`
    Percentage float64 `json:"percentage" doc:"Stat product relative to the league's #1 (1.0 = perfect)"`
    Rank       int16   `json:"rank" doc:"Rank within the league (1 = best)"`
    Capped     bool    `json:"capped,omitempty" doc:"True if the level was limited by the cap"`
    Evolution  int     `json:"evolution,omitempty" doc:"Evolution id if this ranking is for an evolved form"`
}

// PvpRankings holds the per-league PVP rankings. League set is fixed.
type PvpRankings struct {
    Little []PvpEntry `json:"little" doc:"Little League rankings under a 500 CP cap"`
    Great  []PvpEntry `json:"great" doc:"Great League rankings under a 1500 CP cap"`
    Ultra  []PvpEntry `json:"ultra" doc:"Ultra League rankings under a 2500 CP cap"`
}
```

`PokemonResult` mirrors `ApiPokemonResult` field-for-field with the **same json
tag names**, except:
- Nullable columns become pointers without `omitempty`: `null.Int`→`*int64`,
  `null.Float`→`*float64`, `null.String`→`*string`, `null.Bool`→`*bool`.
- Always-present fields keep their plain types (`Id string`, `Lat/Lon float64`,
  `PokemonId int16`, `FirstSeenTimestamp int64`, `Changed int64`,
  `ExpireTimestampVerified bool`, `IsDitto bool`, `IsEvent int8`).
- `Pvp interface{}` → `Pvp PvpRankings`.
- Every field gets a `doc:` tag.

`PokemonScanResultV3` is the v3-only envelope:

```go
type PokemonScanResultV3 struct {
    Pokemon  []PokemonResult `json:"pokemon" doc:"Matched pokemon"`
    Examined int             `json:"examined" doc:"Candidates examined from the spatial index"`
    Skipped  int             `json:"skipped" doc:"Candidates skipped (expired or filtered)"`
    Total    int             `json:"total" doc:"Total candidates in the bounding box"`
}
```

(v2 has no envelope — it returns `[]PokemonResult` directly.)

Request structs get additive `doc:` tags: `ApiPokemonScan2` + `ApiPokemonDnfFilter`
(v2), `ApiPokemonScan3` + `ApiPokemonDnfFilter3` (v3), and the shared
`ApiPokemonDnfId` / `ApiPokemonDnfMinMax` / `ApiPokemonDnfMinMax8`. These are shared
with the gRPC path; adding doc tags is safe.

### 3. Builder + entry functions (`decoder/api_pokemon_response.go`)

`buildPokemonResult(p *Pokemon) PokemonResult` (shared by v2 and v3):
- Maps each `null.X` field via its `.Ptr()` helper (`null.Int.Ptr() *int64`,
  `null.Float.Ptr() *float64`, `null.String.Ptr() *string`, `null.Bool.Ptr() *bool`).
- Builds `Pvp` by calling `ohbem.QueryPvPRank(...)` (same call as today) and
  splitting the returned `map[string][]gohbem.PokemonEntry` into the three named
  league slices, converting each `gohbem.PokemonEntry` → `PvpEntry`. Missing
  leagues → nil slices. When `ohbem == nil`, all three are nil.

Two entry functions reuse the existing internal search verbatim:

```go
// v2 — bare array, discards counts (matches today's GetPokemonInArea2 shape)
func GetPokemonInArea2Clean(req ApiPokemonScan2) []PokemonResult

// v3 — wrapper with counts
func GetPokemonInArea3Clean(req ApiPokemonScan3) *PokemonScanResultV3
```

Both: call `internalGetPokemonInArea2`/`internalGetPokemonInArea3` for keys +
counts (unchanged); per key `peekPokemonRecordReadOnly`, expiry check
(`ExpireTimestamp > now`, same as today), `buildPokemonResult`.

### 4. Huma operations (in `routes_huma.go`, package `main`)

```go
// v2
type pokemonV2ScanInput struct{ Body decoder.ApiPokemonScan2 }
type pokemonV2ScanOutput struct{ Body []decoder.PokemonResult }

huma.Register(humaAPI, huma.Operation{
    OperationID:   "scan-pokemon-v2",
    Method:        http.MethodPost,
    Path:          "/api/pokemon/v2/scan",
    Summary:       "Search pokemon in a bounding box (v2, DNF filters)",
    Description:   "Returns pokemon within [min,max] matching any DNF filter clause. " +
                   "Clauses are OR'd; conditions within a clause are AND'd. Returns a bare array.",
    Tags:          []string{"Pokemon"},
    Security:      []map[string][]string{{"golbatSecret": {}}},
    DefaultStatus: http.StatusAccepted, // preserve 202
}, func(ctx context.Context, in *pokemonV2ScanInput) (*pokemonV2ScanOutput, error) {
    return &pokemonV2ScanOutput{Body: decoder.GetPokemonInArea2Clean(in.Body)}, nil
})

// v3
type pokemonV3ScanInput struct{ Body decoder.ApiPokemonScan3 }
type pokemonV3ScanOutput struct{ Body decoder.PokemonScanResultV3 }

huma.Register(humaAPI, huma.Operation{
    OperationID:   "scan-pokemon-v3",
    Method:        http.MethodPost,
    Path:          "/api/pokemon/v3/scan",
    Summary:       "Search pokemon in a bounding box (v3, DNF filters)",
    Description:   "Returns pokemon within [min,max] matching any DNF filter clause. " +
                   "Clauses are OR'd; conditions within a clause are AND'd. Returns counts + array.",
    Tags:          []string{"Pokemon"},
    Security:      []map[string][]string{{"golbatSecret": {}}},
    DefaultStatus: http.StatusAccepted, // preserve 202
}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
    return &pokemonV3ScanOutput{Body: *decoder.GetPokemonInArea3Clean(in.Body)}, nil
})
```

### 5. Retire the old paths

- Remove `apiGroup.POST("/pokemon/v2/scan", PokemonScan2)` and
  `apiGroup.POST("/pokemon/v3/scan", PokemonScan3)` from `main.go`.
- Remove the `PokemonScan2` and `PokemonScan3` handlers from `routes.go`.
- Remove `GetPokemonInArea2`, `GetPokemonInArea3`, and `PokemonScan3Result`
  **after** grep-verifying no remaining callers. If anything else references them,
  leave them.
- **Keep** `buildApiPokemonResult`, `ApiPokemonResult`, `internalGetPokemonInArea2`,
  `internalGetPokemonInArea3`, `GrpcGetPokemonInArea2`, `GrpcGetPokemonInArea3`
  (used by v1/search/gRPC and the new entry functions).

## Data flow (v3 shown; v2 identical minus the envelope)

```
gin engine (root)
  └─ Huma API (goccy JSON format)
       └─ security middleware (X-Golbat-Secret)
            └─ operation handler
                 └─ decoder.GetPokemonInArea3Clean
                      ├─ internalGetPokemonInArea3   (rtree bbox + DNF — unchanged)
                      └─ per key: peekPokemonRecordReadOnly → buildPokemonResult
                                                              └─ ohbem.QueryPvPRank → PvpRankings
            ← PokemonScanResultV3 → goccy marshal → JSON
```

## Testing

1. **Builder unit test** (`decoder/api_pokemon_response_test.go`): a `Pokemon` with
   a mix of set/unset null fields → assert pointer fields are nil when unset and
   correct when set; assert PVP map splits into the right league slices; assert
   `ohbem == nil` yields nil league slices.
2. **Wire-compat tests**:
   - v3: marshal a `PokemonScanResultV3` with unset nullables → assert they
     serialize as `null` (not omitted), `pvp` is an object with
     `little`/`great`/`ultra`, and the top level has `pokemon`/`examined`/
     `skipped`/`total`.
   - v2: marshal `[]PokemonResult` → assert it serializes as a **bare JSON array**
     (top-level `[`), each element wire-identical to the v3 element.
3. **Spec assertion test**: build the Huma API, fetch the generated OpenAPI
   document, assert both `scan-pokemon-v2` and `scan-pokemon-v3` operations exist,
   the `PvpEntry` / `PvpRankings` / `PokemonResult` schemas are present, nullable
   fields are typed nullable, and the `golbatSecret` security scheme is declared.
4. **Serializer test**: assert `cfg.Formats["application/json"].Marshal` is the
   goccy-backed function (round-trip a value through the configured format).
5. **Manual**: run the server, open `/docs`, POST a sample scan from the "Try it"
   UI for both versions; replay a real v2 consumer request and diff the response
   against the pre-migration output. This is the actual evaluation deliverable.

## Risks / notes

- **Stricter validation:** Huma validates request bodies (structured 422 on type
  mismatch) where gin's `BindJSON` was lenient. Generally an improvement; flagging
  as a behavior change for clients sending malformed bodies. Verify the real v2
  consumer's payloads pass Huma validation (step 5).
- **Status code:** preserved at 202 via `DefaultStatus` on both operations.
- **`gohbem.PokemonEntry` → `PvpEntry` drift:** the two structs must stay in sync;
  the conversion is a small explicit mapping. A comment cross-referencing the
  gohbem version mitigates this.
- **Docs auth:** docs/spec are intentionally public; the operations themselves are
  secured and the spec advertises the `X-Golbat-Secret` requirement.

## POC Results (2026-05-30)

The migration was implemented on branch `feat/huma-pokemon-v2-v3-scan`. Both
`POST /api/pokemon/v2/scan` and `POST /api/pokemon/v3/scan` are now served by Huma,
with the gin handlers retired. Docs render at `/docs` and the spec at
`/openapi.json` (both public; operations secured via the `golbatSecret` scheme).

**Discoverability — achieved.** A committed test (`TestOpenAPISpecIsDiscoverable`)
asserts the generated OpenAPI contains both operations, the `PokemonResult`,
`PvpEntry`, and `PvpRankings` schemas, and the `X-Golbat-Secret` security scheme.
The previously-opaque `Pvp interface{}` is now a fully documented
`{little, great, ultra}` structure with per-field docs. This is the headline win:
the complex DNF request and the PVP response are now self-describing.

**Wire-compat — verified for the scalar surface.**
- A golden parity test marshals the legacy `buildApiPokemonResult` and the new
  `buildPokemonResult` for the same `*Pokemon` and asserts every shared non-`pvp`
  key is byte-identical. No unintended scalar differences.
- v2 preserved as a bare array (`[]`), v3 as the `{pokemon,examined,skipped,total}`
  wrapper, both at HTTP 202 (`DefaultStatus`).
- **Regression caught & fixed:** Huma's `DefaultConfig` injects a `$schema` field
  into object responses (and a `Link: rel="describedBy"` header) via a
  `SchemaLinkTransformer`. This would have added a `$schema` key to the v3 body.
  Disabled via `cfg.CreateHooks = nil` in `newHumaConfig`; an end-to-end test
  (`TestHumaScanEndpointsE2E`) now guards that v3 has no `$schema` and v2 is exactly
  `[]`. Found by the end-to-end HTTP exercise, not by unit tests.

**Intentional divergences (documented, by design):**
- `pvp` shape changed from the legacy dynamic `map[string][]gohbem.PokemonEntry`
  (empty leagues omitted; `null` when PVP disabled) to the fixed three-league struct
  (always emits `little`/`great`/`ultra`, `null` for empty/disabled). Documented on
  `PvpRankings`.
- Huma validates request bodies more strictly than gin's `BindJSON` (structured 422
  on type mismatch).

**Remaining manual step (requires the user's environment):** the full server boots
only against a live MariaDB (it `log.Fatal`s on DB ping before serving), so a real
end-to-end run and a replay of the **real v2 consumer's** request/response could not
be done in the implementation environment. All HTTP-contract behavior (auth 401/202,
v2/v3 envelopes, no `$schema`, goccy serialization) was verified via `humatest`
against empty cache state. Recommended before merge: boot against a populated DB,
open `/docs`, and diff a captured real v2 consumer response against the migrated
output.

## Future work (out of scope)

If the POC is judged successful, the `setupHumaAPI` infrastructure (adapter, goccy
format, security scheme) and the shared `PokemonResult`/`buildPokemonResult` are
reusable to migrate v1, search, and the fort scan endpoints incrementally, one
operation at a time, while un-migrated routes continue on gin.
