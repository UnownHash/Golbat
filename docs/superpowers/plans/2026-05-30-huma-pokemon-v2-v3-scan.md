# Huma Pokemon v2 + v3 Scan Migration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate `POST /api/pokemon/v2/scan` and `POST /api/pokemon/v3/scan` from hand-rolled gin handlers to documented Huma operations, with clean pointer-based response types and a fully documented PVP structure, producing a browsable OpenAPI 3.1 spec.

**Architecture:** Mount a Huma API on the root gin engine (public `/docs` + `/openapi.json`), with goccy/go-json as the serializer for parity with the current gin setup and an apiKey security scheme mirroring `AuthRequired()`. New shared response types (`PokemonResult`, `PvpEntry`, `PvpRankings`) replace `guregu/null` with pointers (kept as `null` on the wire). The existing rtree/DNF search (`internalGetPokemonInArea2/3`) is reused verbatim; only the bind/build/return boundary changes. v2 keeps its bare-array envelope, v3 its `{pokemon,examined,skipped,total}` wrapper.

**Tech Stack:** Go 1.26, gin, Huma v2 (+ humagin adapter), goccy/go-json, guregu/null/v6, UnownHash/gohbem.

**Reference spec:** `docs/superpowers/specs/2026-05-30-huma-pokemon-v2-v3-scan-design.md`

---

## Task 1: Add Huma dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the Huma module**

Run:
```bash
go get github.com/danielgtaylor/huma/v2@v2.38.0
```
Expected: `go.mod` gains `github.com/danielgtaylor/huma/v2 v2.38.0`. The humagin adapter lives in the same module (no separate `go get` needed).

- [ ] **Step 2: Tidy and verify it builds**

Run:
```bash
go mod tidy && go build -tags go_json ./...
```
Expected: builds with no errors; `go.mod` shows the huma dependency promoted to the require block.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add huma v2 dependency for OpenAPI endpoints"
```

---

## Task 2: Shared response types + builder (`PokemonResult`, `PvpEntry`, `PvpRankings`)

**Files:**
- Create: `decoder/api_pokemon_response.go`
- Test: `decoder/api_pokemon_response_test.go`

**Context:** `buildPokemonResult` must be wire-identical to the existing `buildApiPokemonResult` (`decoder/api_pokemon_common.go:72`). That builder sets fields up to `Username` and `Pvp`, and **deliberately leaves `Capture1/Capture2/Capture3` and `IsEvent` unset** (so today's JSON has `capture_1/2/3: null` and `is_event: 0`). Replicate that exactly. Field declaration order matches `ApiPokemonResult` for diff-friendly output. `ohbem` is a package global (`decoder/main.go:75`), nil when PVP is disabled.

- [ ] **Step 1: Write the failing test**

Create `decoder/api_pokemon_response_test.go`:

```go
package decoder

import (
	"encoding/json"
	"testing"

	"github.com/guregu/null/v6"
)

func TestBuildPokemonResult_NullablesAndDefaults(t *testing.T) {
	p := &Pokemon{
		PokemonData: PokemonData{
			Id:                 12345,
			Lat:                51.5,
			Lon:                -0.1,
			PokemonId:          25,
			Cp:                 null.IntFrom(500),
			AtkIv:              null.IntFrom(15),
			FirstSeenTimestamp: 1000,
			Changed:            2000,
			// Level intentionally left unset -> should be a nil pointer (null)
		},
	}

	got := buildPokemonResult(p) // ohbem is nil in tests -> empty PVP

	if got.Id != "12345" {
		t.Errorf("Id = %q, want \"12345\"", got.Id)
	}
	if got.Cp == nil || *got.Cp != 500 {
		t.Errorf("Cp = %v, want pointer to 500", got.Cp)
	}
	if got.Level != nil {
		t.Errorf("Level = %v, want nil (null)", got.Level)
	}
	if got.PokemonId != 25 {
		t.Errorf("PokemonId = %d, want 25", got.PokemonId)
	}
	// Capture fields and IsEvent are intentionally never populated (parity).
	if got.Capture1 != nil || got.IsEvent != 0 {
		t.Errorf("Capture1/IsEvent should be unset for parity, got %v / %d", got.Capture1, got.IsEvent)
	}
	// PVP with nil ohbem: leagues are nil slices.
	if got.Pvp.Little != nil || got.Pvp.Great != nil || got.Pvp.Ultra != nil {
		t.Errorf("PVP leagues should be nil when ohbem is nil, got %+v", got.Pvp)
	}

	// Wire-compat: unset nullable still serializes as null, not omitted.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(m["level"]) != "null" {
		t.Errorf("level should serialize as null, got %s", m["level"])
	}
	if _, ok := m["pvp"]; !ok {
		t.Errorf("pvp key missing from output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestBuildPokemonResult_NullablesAndDefaults -v`
Expected: FAIL — `undefined: buildPokemonResult` (compile error).

- [ ] **Step 3: Create the types and builder**

Create `decoder/api_pokemon_response.go`:

```go
package decoder

import "github.com/UnownHash/gohbem"

// PvpEntry mirrors gohbem.PokemonEntry (gohbem v0.12.0) with documentation.
// Keep in sync with that struct if the gohbem dependency is upgraded.
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

// PvpRankings holds per-league PVP rankings. The league set is fixed (see
// decoder/main.go ohbem init).
type PvpRankings struct {
	Little []PvpEntry `json:"little" doc:"Little League rankings under a 500 CP cap"`
	Great  []PvpEntry `json:"great" doc:"Great League rankings under a 1500 CP cap"`
	Ultra  []PvpEntry `json:"ultra" doc:"Ultra League rankings under a 2500 CP cap"`
}

// PokemonResult is the documented, pointer-based equivalent of ApiPokemonResult.
// Nullable DB columns are pointers (no omitempty) so they still serialize as
// `null`, matching the legacy guregu/null output exactly.
type PokemonResult struct {
	Id                      string      `json:"id" doc:"Encounter id"`
	PokestopId              *string     `json:"pokestop_id" doc:"Lured pokestop id, if spawned from a lure"`
	SpawnId                 *int64      `json:"spawn_id" doc:"Spawnpoint id"`
	Lat                     float64     `json:"lat" doc:"Latitude"`
	Lon                     float64     `json:"lon" doc:"Longitude"`
	Weight                  *float64    `json:"weight" doc:"Weight in kg"`
	Size                    *int64      `json:"size" doc:"Size class (1=XXS .. 5=XXL)"`
	Height                  *float64    `json:"height" doc:"Height in m"`
	ExpireTimestamp         *int64      `json:"expire_timestamp" doc:"Despawn unix timestamp"`
	Updated                 *int64      `json:"updated" doc:"Last update unix timestamp"`
	PokemonId               int16       `json:"pokemon_id" doc:"Pokedex number"`
	Move1                   *int64      `json:"move_1" doc:"Fast move id"`
	Move2                   *int64      `json:"move_2" doc:"Charge move id"`
	Gender                  *int64      `json:"gender" doc:"Gender (1=male, 2=female, 3=genderless)"`
	Cp                      *int64      `json:"cp" doc:"Combat power"`
	AtkIv                   *int64      `json:"atk_iv" doc:"Attack IV (0-15)"`
	DefIv                   *int64      `json:"def_iv" doc:"Defense IV (0-15)"`
	StaIv                   *int64      `json:"sta_iv" doc:"Stamina IV (0-15)"`
	Iv                      *float64    `json:"iv" doc:"IV percentage (0-100)"`
	Form                    *int64      `json:"form" doc:"Form id"`
	Level                   *int64      `json:"level" doc:"Pokemon level"`
	Weather                 *int64      `json:"weather" doc:"Weather boost id"`
	Costume                 *int64      `json:"costume" doc:"Costume id"`
	FirstSeenTimestamp      int64       `json:"first_seen_timestamp" doc:"First seen unix timestamp"`
	Changed                 int64       `json:"changed" doc:"Last changed unix timestamp"`
	CellId                  *int64      `json:"cell_id" doc:"S2 cell id"`
	ExpireTimestampVerified bool        `json:"expire_timestamp_verified" doc:"True if despawn time is exact"`
	DisplayPokemonId        *int64      `json:"display_pokemon_id" doc:"Displayed pokemon id (Ditto disguise)"`
	DisplayPokemonForm      *int64      `json:"display_pokemon_form" doc:"Displayed pokemon form (Ditto disguise)"`
	IsDitto                 bool        `json:"is_ditto" doc:"True if this is a disguised Ditto"`
	SeenType                *string     `json:"seen_type" doc:"How the pokemon was seen (wild, encounter, nearby_stop, ...)"`
	Shiny                   *bool       `json:"shiny" doc:"True if shiny"`
	Username                *string     `json:"username" doc:"Scanner account username"`
	Capture1                *float64    `json:"capture_1" doc:"Capture probability with a Pokeball"`
	Capture2                *float64    `json:"capture_2" doc:"Capture probability with a Great Ball"`
	Capture3                *float64    `json:"capture_3" doc:"Capture probability with an Ultra Ball"`
	Pvp                     PvpRankings `json:"pvp" doc:"PVP rankings per league"`
	IsEvent                 int8        `json:"is_event" doc:"Event flag"`
}

// buildPokemonResult mirrors buildApiPokemonResult (api_pokemon_common.go) but
// emits the documented pointer-based PokemonResult. It intentionally leaves
// Capture1/2/3 and IsEvent unset to stay wire-identical with the legacy builder.
func buildPokemonResult(pokemon *Pokemon) PokemonResult {
	return PokemonResult{
		Id:                      pokemon.Id.String(),
		PokestopId:              pokemon.PokestopId.Ptr(),
		SpawnId:                 pokemon.SpawnId.Ptr(),
		Lat:                     pokemon.Lat,
		Lon:                     pokemon.Lon,
		Weight:                  pokemon.Weight.Ptr(),
		Size:                    pokemon.Size.Ptr(),
		Height:                  pokemon.Height.Ptr(),
		ExpireTimestamp:         pokemon.ExpireTimestamp.Ptr(),
		Updated:                 pokemon.Updated.Ptr(),
		PokemonId:               pokemon.PokemonId,
		Move1:                   pokemon.Move1.Ptr(),
		Move2:                   pokemon.Move2.Ptr(),
		Gender:                  pokemon.Gender.Ptr(),
		Cp:                      pokemon.Cp.Ptr(),
		AtkIv:                   pokemon.AtkIv.Ptr(),
		DefIv:                   pokemon.DefIv.Ptr(),
		StaIv:                   pokemon.StaIv.Ptr(),
		Iv:                      pokemon.Iv.Ptr(),
		Form:                    pokemon.Form.Ptr(),
		Level:                   pokemon.Level.Ptr(),
		Weather:                 pokemon.Weather.Ptr(),
		Costume:                 pokemon.Costume.Ptr(),
		FirstSeenTimestamp:      pokemon.FirstSeenTimestamp,
		Changed:                 pokemon.Changed,
		CellId:                  pokemon.CellId.Ptr(),
		ExpireTimestampVerified: pokemon.ExpireTimestampVerified,
		DisplayPokemonId:        pokemon.DisplayPokemonId.Ptr(),
		DisplayPokemonForm:      pokemon.DisplayPokemonForm.Ptr(),
		IsDitto:                 pokemon.IsDitto,
		SeenType:                pokemon.SeenType.Ptr(),
		Shiny:                   pokemon.Shiny.Ptr(),
		Username:                pokemon.Username.Ptr(),
		Pvp:                     buildPvpRankings(pokemon),
	}
}

// buildPvpRankings runs ohbem.QueryPvPRank (same call as the legacy builder) and
// splits the league map into the fixed little/great/ultra fields.
func buildPvpRankings(pokemon *Pokemon) PvpRankings {
	var out PvpRankings
	if ohbem == nil {
		return out
	}
	pvp, err := ohbem.QueryPvPRank(
		int(pokemon.PokemonId),
		int(pokemon.Form.ValueOrZero()),
		int(pokemon.Costume.ValueOrZero()),
		int(pokemon.Gender.ValueOrZero()),
		int(pokemon.AtkIv.ValueOrZero()),
		int(pokemon.DefIv.ValueOrZero()),
		int(pokemon.StaIv.ValueOrZero()),
		float64(pokemon.Level.ValueOrZero()),
	)
	if err != nil {
		return out
	}
	out.Little = convertPvpEntries(pvp["little"])
	out.Great = convertPvpEntries(pvp["great"])
	out.Ultra = convertPvpEntries(pvp["ultra"])
	return out
}

func convertPvpEntries(entries []gohbem.PokemonEntry) []PvpEntry {
	if entries == nil {
		return nil
	}
	out := make([]PvpEntry, len(entries))
	for i, e := range entries {
		out[i] = PvpEntry{
			Pokemon:    e.Pokemon,
			Form:       e.Form,
			Cap:        e.Cap,
			Value:      e.Value,
			Level:      e.Level,
			Cp:         e.Cp,
			Percentage: e.Percentage,
			Rank:       e.Rank,
			Capped:     e.Capped,
			Evolution:  e.Evolution,
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/ -run TestBuildPokemonResult_NullablesAndDefaults -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add decoder/api_pokemon_response.go decoder/api_pokemon_response_test.go
git commit -m "feat: add documented pointer-based PokemonResult + PVP types"
```

---

## Task 3: Entry functions + v3 wrapper type + wire-compat tests

**Files:**
- Modify: `decoder/api_pokemon_response.go`
- Test: `decoder/api_pokemon_response_test.go`

**Context:** Reuse the existing search verbatim. `internalGetPokemonInArea2(ApiPokemonScan2)` and `internalGetPokemonInArea3(ApiPokemonScan3)` each return `(keys []uint64, examined, skipped, total int)`. The expiry check and `peekPokemonRecordReadOnly` usage mirror the legacy `GetPokemonInArea2`/`GetPokemonInArea3`.

- [ ] **Step 1: Write the failing test**

Append to `decoder/api_pokemon_response_test.go`:

```go
func TestPokemonScanResultV3_WireShape(t *testing.T) {
	res := PokemonScanResultV3{
		Pokemon:  []PokemonResult{{Id: "1", PokemonId: 25}},
		Examined: 5,
		Skipped:  1,
		Total:    6,
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"pokemon", "examined", "skipped", "total"} {
		if _, ok := m[k]; !ok {
			t.Errorf("v3 wrapper missing key %q", k)
		}
	}
}

func TestPokemonV2_BareArrayShape(t *testing.T) {
	// v2 must serialize as a bare JSON array, not an object.
	res := []PokemonResult{{Id: "1", PokemonId: 25}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 || b[0] != '[' {
		t.Errorf("v2 response must be a bare array, got: %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run 'TestPokemonScanResultV3_WireShape|TestPokemonV2_BareArrayShape' -v`
Expected: FAIL — `undefined: PokemonScanResultV3`.

- [ ] **Step 3: Add the wrapper type and entry functions**

Append to `decoder/api_pokemon_response.go`:

```go
import "time" // add to the existing import block

// PokemonScanResultV3 is the v3-only response envelope.
type PokemonScanResultV3 struct {
	Pokemon  []PokemonResult `json:"pokemon" doc:"Matched pokemon"`
	Examined int             `json:"examined" doc:"Candidates examined from the spatial index"`
	Skipped  int             `json:"skipped" doc:"Candidates skipped (expired or filtered)"`
	Total    int             `json:"total" doc:"Total candidates in the bounding box"`
}

// GetPokemonInArea2Clean is the documented v2 entry point. It returns a bare
// slice (no envelope) to match the legacy v2 wire format.
func GetPokemonInArea2Clean(req ApiPokemonScan2) []PokemonResult {
	keys, _, _, _ := internalGetPokemonInArea2(req)
	return collectPokemonResults(keys, "API.ScanPokemon.v2.clean")
}

// GetPokemonInArea3Clean is the documented v3 entry point with counts.
func GetPokemonInArea3Clean(req ApiPokemonScan3) *PokemonScanResultV3 {
	keys, examined, skipped, total := internalGetPokemonInArea3(req)
	return &PokemonScanResultV3{
		Pokemon:  collectPokemonResults(keys, "API.ScanPokemon.v3.clean"),
		Examined: examined,
		Skipped:  skipped,
		Total:    total,
	}
}

// collectPokemonResults locks each matched key, applies the same expiry filter as
// the legacy builders, and builds the documented result.
func collectPokemonResults(keys []uint64, caller string) []PokemonResult {
	results := make([]PokemonResult, 0, len(keys))
	nowUnix := time.Now().Unix()
	for _, key := range keys {
		pokemon, unlock, _ := peekPokemonRecordReadOnly(key, caller)
		if pokemon != nil {
			if pokemon.ExpireTimestamp.ValueOrZero() > nowUnix {
				results = append(results, buildPokemonResult(pokemon))
			}
			unlock()
		}
	}
	return results
}
```

Note: `time` may already be imported in this file after Task 2 (it is not). Add it to the import block; gofmt/goimports will group it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./decoder/ -run 'TestPokemonScanResultV3_WireShape|TestPokemonV2_BareArrayShape|TestBuildPokemonResult' -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add decoder/api_pokemon_response.go decoder/api_pokemon_response_test.go
git commit -m "feat: add v2/v3 clean entry functions and v3 wrapper type"
```

---

## Task 4: Huma infrastructure (`setupHumaAPI` with goccy + auth scheme)

**Files:**
- Create: `huma_api.go` (package `main`)
- Test: `huma_api_test.go` (package `main`)

**Context:** `version.go` exposes `gitRevision` (package `main`). `config.Config.ApiSecret` is the shared secret; empty means auth is disabled (mirror `AuthRequired()` in `routes.go:321`). Huma middleware signature is `func(ctx huma.Context, next func(huma.Context))`; read headers with `ctx.Header(name)`, short-circuit with `huma.WriteErr(api, ctx, status, msg)`.

- [ ] **Step 1: Write the failing test**

Create `huma_api_test.go`:

```go
package main

import (
	"bytes"
	"testing"

	gojson "github.com/goccy/go-json"
)

func TestHumaConfigUsesGoccy(t *testing.T) {
	cfg := newHumaConfig("test")
	f, ok := cfg.Formats["application/json"]
	if !ok || f.Marshal == nil {
		t.Fatal("application/json format not configured")
	}
	// Round-trip through the configured marshaler and confirm it matches goccy.
	var buf bytes.Buffer
	if err := f.Marshal(&buf, map[string]int{"a": 1}); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want, _ := gojson.Marshal(map[string]int{"a": 1})
	if got := bytes.TrimSpace(buf.Bytes()); !bytes.Equal(got, want) {
		t.Errorf("configured marshaler output = %s, want %s", got, want)
	}
}

func TestHumaConfigDeclaresSecurityScheme(t *testing.T) {
	cfg := newHumaConfig("test")
	if cfg.Components == nil || cfg.Components.SecuritySchemes == nil {
		t.Fatal("no security schemes configured")
	}
	scheme, ok := cfg.Components.SecuritySchemes["golbatSecret"]
	if !ok {
		t.Fatal("golbatSecret scheme missing")
	}
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != "X-Golbat-Secret" {
		t.Errorf("unexpected scheme: %+v", scheme)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestHumaConfig -v`
Expected: FAIL — `undefined: newHumaConfig`.

- [ ] **Step 3: Implement the Huma setup**

Create `huma_api.go`:

```go
package main

import (
	"io"
	"net/http"

	"golbat/config"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	gojson "github.com/goccy/go-json"
)

// securitySchemeName is referenced by operations that require the API secret.
const securitySchemeName = "golbatSecret"

// newHumaConfig builds the Huma config: goccy JSON serializer (parity with the
// gin go_json build tag, which Huma does not honor) plus an apiKey security
// scheme that documents the X-Golbat-Secret requirement.
func newHumaConfig(version string) huma.Config {
	cfg := huma.DefaultConfig("Golbat API", version)

	goccyFmt := huma.Format{
		Marshal:   func(w io.Writer, v any) error { return gojson.NewEncoder(w).Encode(v) },
		Unmarshal: gojson.Unmarshal,
	}
	cfg.Formats = map[string]huma.Format{
		"application/json": goccyFmt,
		"json":             goccyFmt,
	}

	if cfg.Components == nil {
		cfg.Components = &huma.Components{}
	}
	if cfg.Components.SecuritySchemes == nil {
		cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	cfg.Components.SecuritySchemes[securitySchemeName] = &huma.SecurityScheme{
		Type: "apiKey",
		In:   "header",
		Name: "X-Golbat-Secret",
	}
	return cfg
}

// setupHumaAPI mounts a Huma API on the root gin engine so /docs and
// /openapi.json are publicly reachable, and enforces the API secret for any
// operation declaring the golbatSecret scheme.
func setupHumaAPI(r *gin.Engine) huma.API {
	version := gitRevision
	if version == "" {
		version = "dev"
	}
	api := humagin.New(r, newHumaConfig(version))

	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		secret := config.Config.ApiSecret
		if secret == "" {
			next(ctx)
			return
		}
		requiresAuth := false
		for _, req := range ctx.Operation().Security {
			if _, ok := req[securitySchemeName]; ok {
				requiresAuth = true
				break
			}
		}
		if requiresAuth && ctx.Header("X-Golbat-Secret") != secret {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid or missing X-Golbat-Secret")
			return
		}
		next(ctx)
	})

	return api
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run TestHumaConfig -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add huma_api.go huma_api_test.go
git commit -m "feat: add Huma API setup with goccy serializer and secret auth"
```

---

## Task 5: Register v2 + v3 operations, wire into main, retire old routes

**Files:**
- Create: `routes_huma.go` (package `main`)
- Modify: `main.go` (route registration block near line 354), `routes.go` (remove `PokemonScan2`, `PokemonScan3`), `decoder/api_pokemon_scan_v2.go` (remove `GetPokemonInArea2`), `decoder/api_pokemon_scan_v3.go` (remove `GetPokemonInArea3`, `PokemonScan3Result`)

**Context:** `main.go` currently has `apiGroup.POST("/pokemon/v2/scan", PokemonScan2)` and `apiGroup.POST("/pokemon/v3/scan", PokemonScan3)`. We register these paths on the Huma API instead. The Huma API is mounted on root `r`, so call `setupHumaAPI(r)` and register before `r.Run`.

- [ ] **Step 1: Verify the legacy functions have no other callers**

Run:
```bash
grep -rn "GetPokemonInArea2\b\|GetPokemonInArea3\b\|PokemonScan3Result\b\|PokemonScan2\b\|PokemonScan3\b" --include='*.go' .
```
Expected: the only matches are the definitions and the `main.go` route registrations we are about to remove. If any OTHER caller exists, do NOT delete that symbol — leave it and note it. (`GrpcGetPokemonInArea2/3`, `internalGetPokemonInArea2/3`, `buildApiPokemonResult`, `ApiPokemonResult` must remain — they are used elsewhere.)

- [ ] **Step 2: Create the Huma operation registrations**

Create `routes_huma.go`:

```go
package main

import (
	"context"
	"net/http"

	"golbat/decoder"

	"github.com/danielgtaylor/huma/v2"
)

type pokemonV2ScanInput struct {
	Body decoder.ApiPokemonScan2
}

type pokemonV2ScanOutput struct {
	Body []decoder.PokemonResult
}

type pokemonV3ScanInput struct {
	Body decoder.ApiPokemonScan3
}

type pokemonV3ScanOutput struct {
	Body decoder.PokemonScanResultV3
}

// registerHumaRoutes registers all Huma-backed operations on the given API.
func registerHumaRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v2",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v2/scan",
		Summary:       "Search pokemon in a bounding box (v2, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns a bare array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV2ScanInput) (*pokemonV2ScanOutput, error) {
		return &pokemonV2ScanOutput{Body: decoder.GetPokemonInArea2Clean(in.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "scan-pokemon-v3",
		Method:        http.MethodPost,
		Path:          "/api/pokemon/v3/scan",
		Summary:       "Search pokemon in a bounding box (v3, DNF filters)",
		Description:   "Returns pokemon within [min,max] matching any DNF filter clause. Clauses are OR'd; conditions within a clause are AND'd. Returns counts plus the matched array.",
		Tags:          []string{"Pokemon"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *pokemonV3ScanInput) (*pokemonV3ScanOutput, error) {
		return &pokemonV3ScanOutput{Body: *decoder.GetPokemonInArea3Clean(in.Body)}, nil
	})
}
```

- [ ] **Step 3: Wire into main.go and remove the two old gin routes**

In `main.go`, delete these two lines from the route block (around line 354):
```go
	apiGroup.POST("/pokemon/v2/scan", PokemonScan2)
	apiGroup.POST("/pokemon/v3/scan", PokemonScan3)
```

Then, after the full route block is registered and before the server starts (locate the existing `r.Run(...)` / server start; register Huma just before it), add:
```go
	humaAPI := setupHumaAPI(r)
	registerHumaRoutes(humaAPI)
```

- [ ] **Step 4: Remove the now-dead legacy handlers and functions**

In `routes.go`, delete the entire `PokemonScan2` function (around line 379-394) and the entire `PokemonScan3` function (around line 396-411).

In `decoder/api_pokemon_scan_v2.go`, delete the `GetPokemonInArea2` function (around line 98-120). Keep `internalGetPokemonInArea2` and `GrpcGetPokemonInArea2`.

In `decoder/api_pokemon_scan_v3.go`, delete the `GetPokemonInArea3` function (around line 105-131) and the `PokemonScan3Result` type (around line 46-51). Keep `internalGetPokemonInArea3` and `GrpcGetPokemonInArea3`.

(Only delete a symbol if Step 1 confirmed it has no other callers.)

- [ ] **Step 5: Build and run the full test suite**

Run:
```bash
go build -tags go_json ./... && go test ./...
```
Expected: builds clean (no unused-import or undefined-symbol errors), all tests pass. If the build complains about an unused import in `routes.go` (e.g. a now-unused `net/http`), remove only the genuinely-unused import.

- [ ] **Step 6: Commit**

```bash
git add main.go routes.go routes_huma.go decoder/api_pokemon_scan_v2.go decoder/api_pokemon_scan_v3.go
git commit -m "feat: serve pokemon v2/v3 scan via Huma, retire gin handlers"
```

---

## Task 6: OpenAPI spec assertion test

**Files:**
- Test: `huma_api_test.go` (append)

**Context:** A built Huma API exposes the spec via `api.OpenAPI()`, which can be serialized with `.YAML()`. Asserting on the serialized spec proves the operations, schemas, nullable fields, and security scheme are all discoverable.

- [ ] **Step 1: Write the failing test**

Append to `huma_api_test.go`:

```go
import "github.com/gin-gonic/gin" // add to existing imports

func TestOpenAPISpecIsDiscoverable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := humagin.New(r, newHumaConfig("test")) // local API; no auth middleware needed for spec
	registerHumaRoutes(api)

	spec, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	s := string(spec)

	for _, want := range []string{
		"scan-pokemon-v2",
		"scan-pokemon-v3",
		"PvpRankings",
		"PvpEntry",
		"PokemonResult",
		"golbatSecret",
		"X-Golbat-Secret",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("OpenAPI spec missing %q", want)
		}
	}
}
```

Add the required imports to the test file's import block: `"strings"` and `"github.com/danielgtaylor/huma/v2/adapters/humagin"`.

- [ ] **Step 2: Run test to verify it fails (or passes immediately)**

Run: `go test . -run TestOpenAPISpecIsDiscoverable -v`
Expected: PASS if all symbols are present. If it FAILs on a missing string, that's a real discoverability gap — inspect the generated spec with the manual step below and fix the offending `doc:`/type before continuing.

- [ ] **Step 3: Commit**

```bash
git add huma_api_test.go
git commit -m "test: assert pokemon scan operations and schemas appear in OpenAPI"
```

---

## Task 7: Manual verification (the evaluation deliverable)

**Files:** none (manual)

- [ ] **Step 1: Run the server**

Run (with a local config that sets `api.secret` and pvp level caps as usual):
```bash
go run -tags go_json .
```
Expected: server starts; logs show no Huma registration panics.

- [ ] **Step 2: Open the docs UI**

Open `http://localhost:9001/docs` (adjust to your configured port). Expected: the Pokemon tag lists `scan-pokemon-v2` and `scan-pokemon-v3`; expanding a response schema shows `pvp` with `little`/`great`/`ultra`, each an array of `PvpEntry` with documented fields; nullable fields are marked nullable; the operations show the `X-Golbat-Secret` security requirement.

- [ ] **Step 3: Compare against a real v2 request (wire-compat check)**

Capture a representative request body from the real v2 consumer. POST it to the running server's `/api/pokemon/v2/scan` with the `X-Golbat-Secret` header, e.g.:
```bash
curl -s -X POST http://localhost:9001/api/pokemon/v2/scan \
  -H "X-Golbat-Secret: <secret>" -H "Content-Type: application/json" \
  --data @v2-sample.json | jq 'type, .[0] | keys'
```
Expected: top-level `type` is `"array"`; element keys match the legacy field set (`id`, `cp`, `pvp`, `capture_1` present and `null`, `is_event` present as `0`, etc.). Note any differences; a mismatch is a wire-compat bug to fix in `buildPokemonResult`.

- [ ] **Step 4: Record findings**

Append a short "POC results" section to the design spec (`docs/superpowers/specs/2026-05-30-huma-pokemon-v2-v3-scan-design.md`) describing how the docs look and whether the real v2 consumer payload round-tripped unchanged. Commit:
```bash
git add docs/superpowers/specs/2026-05-30-huma-pokemon-v2-v3-scan-design.md
git commit -m "docs: record Huma migration POC results"
```

---

## Self-Review notes (for the implementer)

- **Spec coverage:** infra (Task 4) ↔ spec §1; shared types/builder (Task 2) ↔ §2/§3; entry funcs + v2 bare array / v3 wrapper (Task 3) ↔ §2/§3; operations + retire old (Task 5) ↔ §4/§5; OpenAPI assertion (Task 6) ↔ §testing.3; serializer test (Task 4) ↔ §testing.4; manual (Task 7) ↔ §testing.5.
- **Wire-compat traps baked into the plan:** pointers without `omitempty` (still `null`); `Capture1/2/3` + `IsEvent` deliberately left unset for parity; field order mirrors `ApiPokemonResult`; v2 stays a bare array; status preserved at 202 via `DefaultStatus`.
- **Do-not-touch:** `buildApiPokemonResult`, `ApiPokemonResult`, `internalGetPokemonInArea2/3`, `GrpcGetPokemonInArea2/3` (used by v1/search/gRPC).
- **Huma version pinned:** v2.38.0 — `huma.Context` middleware signature, `SecurityScheme{Type,In,Name}`, and `huma.WriteErr` are correct for that version.
