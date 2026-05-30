# Design: Migrate `POST /api/pokemon/v3/scan` to Huma

**Date:** 2026-05-30
**Status:** Approved (design), pending implementation plan
**Author:** James Berry (with Claude)

## Goal

Migrate the Pokemon v3 scan HTTP endpoint from a hand-rolled gin handler to a
[Huma](https://huma.rocks) operation, producing a fully self-documenting OpenAPI
3.1 spec. This is a **proof-of-concept** to evaluate how much discoverability we
gain from Huma before committing to migrating the rest of the API.

Success = a browsable `/docs` page where a consumer can understand the v3 scan
request and response — including the previously-opaque PVP structure — without
reading Go source.

### Non-goals

- Migrating any other endpoint (v1, v2, search, forts, etc.). Those stay on gin.
- Changing the rtree / DNF filtering logic. `internalGetPokemonInArea3` is reused
  verbatim.
- Changing the gRPC v3 path (`GrpcGetPokemonInArea3`).

## Decisions (resolved during brainstorming)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Endpoint strategy | **Replace v3 in place** at `/api/pokemon/v3/scan` | One canonical, documented endpoint; response stays wire-compatible (see nullables). |
| PVP representation | **Fixed league struct** `{little, great, ultra}` | Leagues are hardcoded in `decoder/main.go:209`, `decoder/pokemonRtree.go:177`, and the DNF filter inputs. A fixed struct documents each league explicitly instead of an opaque `additionalProperties` map. The "new leagues might appear" hypothesis never materialized and would require code changes everywhere anyway. |
| Nullable fields | **Pointers without `omitempty`** (`*int64` etc.) | Missing values still serialize as `null`, identical JSON to today's `guregu/null`. Non-breaking on the wire, clean Go types, documented as nullable. |
| JSON serializer | **Override Huma's format with `goccy/go-json`** | Huma uses its own serializer (stdlib `encoding/json`), NOT gin's. Our `-tags go_json` build tag only affects gin's `c.JSON()`. Without an override, the migrated endpoint would regress to stdlib JSON. |

## Background: how the current v3 path works

- `routes.go:396` — `PokemonScan3(c *gin.Context)`: `c.BindJSON` into
  `decoder.ApiPokemonScan3`, calls `decoder.GetPokemonInArea3`, returns
  `c.JSON(202, res)`.
- `decoder/api_pokemon_scan_v3.go:105` — `GetPokemonInArea3` → calls
  `internalGetPokemonInArea3` (rtree bbox search + DNF matching, returns matched
  keys + examined/skipped/total counts), then for each key
  `peekPokemonRecordReadOnly` + `buildApiPokemonResult`, returns
  `*PokemonScan3Result{ Pokemon []*ApiPokemonResult, Examined, Skipped, Total }`.
- `decoder/api_pokemon_common.go:72` — `buildApiPokemonResult` builds
  `ApiPokemonResult`, whose `Pvp interface{}` field (line 68) is actually a
  `map[string][]gohbem.PokemonEntry` produced by `ohbem.QueryPvPRank(...)`.

`buildApiPokemonResult` / `ApiPokemonResult` are **shared with v1, v2, and
search** — they must NOT be removed or altered.

## Design

### 1. Huma infrastructure (one-time, reusable by future migrations)

New file `huma_api.go` (package `main`) providing a `setupHumaAPI(r *gin.Engine) huma.API`:

- Add dependencies: `github.com/danielgtaylor/huma/v2` and
  `github.com/danielgtaylor/huma/v2/adapters/humagin`.
- Mount Huma on the **root** gin engine `r` (NOT the authed `/api` group), so the
  docs UI (`/docs`) and spec (`/openapi.json`) are publicly reachable for
  browsing. (This is the deliverable of the POC.)
- **Serializer parity:** override the JSON format with goccy so the migrated hot
  endpoint matches today's performance:

  ```go
  import gojson "github.com/goccy/go-json"

  cfg := huma.DefaultConfig("Golbat API", version) // version from the app's build info
  goccyFmt := huma.Format{
      Marshal:   func(w io.Writer, v any) error { return gojson.NewEncoder(w).Encode(v) },
      Unmarshal: gojson.Unmarshal,
  }
  cfg.Formats = map[string]huma.Format{"application/json": goccyFmt, "json": goccyFmt}
  ```

  Note: this is a runtime override that applies unconditionally; the `go_json`
  build tag remains only for the still-gin-served routes.

- **Auth as a documented security scheme.** Because the endpoint is mounted on
  root, it does not inherit the `/api` group's `AuthRequired()` middleware.
  Instead:
  - Register an `apiKey` security scheme in `cfg.Components.SecuritySchemes`
    named e.g. `golbatSecret` (type `apiKey`, `in: header`, name `X-Golbat-Secret`).
  - Add a Huma middleware (`huma.API`-level) that, for any operation declaring
    that security requirement, validates `X-Golbat-Secret` against
    `config.Config.ApiSecret` (bypassing when `ApiSecret` is empty, mirroring
    `AuthRequired()`), returning `huma.Error401Unauthorized` on mismatch.
  - This makes the auth requirement visible in the OpenAPI spec — a
    discoverability improvement over the invisible gin middleware.

### 2. New response types (new file `decoder/api_pokemon_v3_response.go`)

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

`PokemonResultV3` mirrors `ApiPokemonResult` field-for-field with the **same json
tag names**, except:
- Nullable columns become pointers without `omitempty`: `null.Int`→`*int64`,
  `null.Float`→`*float64`, `null.String`→`*string`, `null.Bool`→`*bool`.
- Always-present fields keep their plain types (`Id string`, `Lat/Lon float64`,
  `PokemonId int16`, `FirstSeenTimestamp int64`, `Changed int64`,
  `ExpireTimestampVerified bool`, `IsDitto bool`, `IsEvent int8`).
- `Pvp interface{}` → `Pvp PvpRankings`.
- Every field gets a `doc:` tag.

`PokemonScanResultV3` wraps the response:

```go
type PokemonScanResultV3 struct {
    Pokemon  []PokemonResultV3 `json:"pokemon" doc:"Matched pokemon"`
    Examined int               `json:"examined" doc:"Candidates examined from the spatial index"`
    Skipped  int               `json:"skipped" doc:"Candidates skipped (expired or filtered)"`
    Total    int               `json:"total" doc:"Total candidates in the bounding box"`
}
```

Request structs (`ApiPokemonScan3` + nested `ApiPokemonDnfFilter3`,
`ApiPokemonDnfId`, `ApiPokemonDnfMinMax`, `ApiPokemonDnfMinMax8`) get additive
`doc:` tags. These are shared with the gRPC path; adding doc tags is safe.

### 3. Builder (`decoder/api_pokemon_v3_response.go`)

`buildPokemonResultV3(p *Pokemon) PokemonResultV3`:
- Maps each `null.X` field via its `.Ptr()` helper (`null.Int.Ptr() *int64`, etc.).
- Builds `Pvp` by calling `ohbem.QueryPvPRank(...)` (same call as today) and
  splitting the returned `map[string][]gohbem.PokemonEntry` into the three named
  league slices, converting each `gohbem.PokemonEntry` → `PvpEntry`. Missing
  leagues → empty (nil) slices. When `ohbem == nil`, all three are nil.

`GetPokemonInArea3V2(req ApiPokemonScan3) *PokemonScanResultV3` (new):
- Reuses `internalGetPokemonInArea3` for keys + counts (unchanged).
- Per key: `peekPokemonRecordReadOnly`, expiry check (`ExpireTimestamp > now`,
  same as today), `buildPokemonResultV3`.

### 4. Huma operation (in `huma_api.go` or a new `routes_huma.go`)

```go
type pokemonV3ScanInput struct {
    Body decoder.ApiPokemonScan3
}
type pokemonV3ScanOutput struct {
    Body decoder.PokemonScanResultV3
}

huma.Register(humaAPI, huma.Operation{
    OperationID: "scan-pokemon-v3",
    Method:      http.MethodPost,
    Path:        "/api/pokemon/v3/scan",
    Summary:     "Search pokemon in a bounding box (v3, DNF filters)",
    Description: "Returns pokemon within [min,max] matching any DNF filter clause. " +
                 "Clauses are OR'd; conditions within a clause are AND'd.",
    Tags:        []string{"Pokemon"},
    Security:    []map[string][]string{{"golbatSecret": {}}},
}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
    res := decoder.GetPokemonInArea3V2(in.Body)
    return &pokemonV3ScanOutput{Body: *res}, nil
})
```

Note: Huma's default success status for a body-returning handler is 200, vs
today's 202. We will set the operation's `DefaultStatus` to 202 to preserve the
existing status code, OR accept 200 — **to confirm during planning** (leaning to
preserve 202 for strict wire-compat).

### 5. Retire the old path

- Remove `apiGroup.POST("/pokemon/v3/scan", PokemonScan3)` from `main.go`.
- Remove the `PokemonScan3` handler from `routes.go`.
- Remove `GetPokemonInArea3` and `PokemonScan3Result` from
  `decoder/api_pokemon_scan_v3.go` **after** verifying (grep) no remaining
  callers. If anything else references them, leave them.
- **Keep** `buildApiPokemonResult`, `ApiPokemonResult`, `internalGetPokemonInArea3`,
  and `GrpcGetPokemonInArea3` (used by v1/v2/search/gRPC).

## Data flow

```
gin engine (root)
  └─ Huma API (goccy JSON format)
       └─ security middleware (X-Golbat-Secret)
            └─ operation handler
                 └─ decoder.GetPokemonInArea3V2
                      ├─ internalGetPokemonInArea3   (rtree bbox + DNF — unchanged)
                      └─ per key: peekPokemonRecordReadOnly → buildPokemonResultV3
                                                              └─ ohbem.QueryPvPRank → PvpRankings
            ← PokemonScanResultV3 → goccy marshal → JSON
```

## Testing

1. **Builder unit test** (`decoder/api_pokemon_v3_response_test.go`): a `Pokemon`
   with a mix of set/unset null fields → assert pointer fields are nil when unset
   and correct when set; assert PVP map splits into the right league slices;
   assert `ohbem == nil` yields nil league slices.
2. **Wire-compat test**: marshal a `PokemonResultV3` with unset nullables → assert
   they serialize as `null` (not omitted) and that `pvp` is an object with
   `little`/`great`/`ultra` keys.
3. **Spec assertion test**: build the Huma API, fetch the generated OpenAPI
   document, assert the `scan-pokemon-v3` operation exists, the `PvpEntry` /
   `PvpRankings` / `PokemonResultV3` schemas are present, and nullable fields are
   typed as nullable.
4. **Serializer test**: assert `cfg.Formats["application/json"].Marshal` is the
   goccy-backed function (or a round-trip test through the configured format).
5. **Manual**: run the server, open `/docs`, POST a sample scan from the "Try it"
   UI, eyeball discoverability. This is the actual evaluation deliverable.

## Risks / notes

- **Stricter validation:** Huma validates request bodies (structured 422 on type
  mismatch) where gin's `BindJSON` was lenient. Generally an improvement; flagging
  as a behavior change for v3 clients sending malformed bodies.
- **Status code:** 200 vs current 202 — preserve via `DefaultStatus` (confirm in plan).
- **`gohbem.PokemonEntry` → `PvpEntry` drift:** the two structs must stay in sync;
  the conversion is a small explicit mapping. A compile-time field-count guard or
  a comment cross-referencing the gohbem version mitigates this.
- **Docs auth:** docs/spec are intentionally public; the operation itself is
  secured and the spec advertises the `X-Golbat-Secret` requirement.

## Future work (out of scope)

If the POC is judged successful, the `setupHumaAPI` infrastructure (adapter,
goccy format, security scheme) is reusable to migrate v2, search, and the fort
scan endpoints incrementally, one operation at a time, while un-migrated routes
continue on gin.
