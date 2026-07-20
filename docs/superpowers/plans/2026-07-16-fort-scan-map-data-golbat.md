# Golbat Fort-Scan Map-Data — Golbat PR Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Golbat-side gaps that let ReactMap serve fort map-data (pokestops, gyms, stations) from Golbat: whole-row invasions in the pokestop scan/by-id, a station by-id endpoint, station preload under bare `fort_in_memory`, and gym/station `available` list endpoints.

**Architecture:** `FortLookup` stays a DNF-only index; responses are whole records from the record caches (`pokestopCache`/`incidentCache`/`gymCache`/`stationCache`). Invasions attach via a plain **string** fetch handle on `FortLookupIncident` (the `int64` re-key is parked → UnownHash/Golbat#384). The two new `available` endpoints are single `fortLookupCache.Range` aggregates mirroring `GetAvailablePokestops`.

**Tech Stack:** Go 1.26 (module `golbat`), Huma v2 (`humatest` for route tests), `xsync/v4` (`fortLookupCache`), `ottercache` (`incidentCache`), `guregu/null/v6`, logrus. Build tag `go_json`.

## Global Constraints

- `FortLookup` carries only what `isFortDnfMatch` reads. The new `FortLookupIncident.Id string` is a **record locator**, not display data — no other display fields added to `FortLookup`.
- Whole records only: invasion payloads are the whole `IncidentData` row from `incidentCache` (read-through to DB on miss via `getIncidentRecordReadOnly`), never a `FortLookup` projection.
- Incident id stays a **string** everywhere (proto `IncidentId` is a string; the `int64` re-key is out of scope → UnownHash/Golbat#384). No DB schema change.
- Scan + `available` routes: `FortInMemory`-gated (`huma.Error503ServiceUnavailable("fort_in_memory not enabled")`), `Security: golbatSecret`, `draftBadge(&op)`, `Tags` per family. **By-id routes are NOT gated** — mirror the existing gym/pokestop by-id (`GetXRecordReadOnly` with DB fallback). (This corrects spec §7.4's "FortInMemory-gated" phrasing, which applies to scan/available, not by-id.)
- `with_incidents` is a body field on the shared `ApiFortScan` (`json:"with_incidents"`, `required:"false"`, default false); only the pokestop + combined scan handlers honor it.
- Lock ordering: the existing `saveIncidentRecord` locks incident → pokestop. Any code that fetches incidents for a pokestop result MUST release the pokestop lock **before** locking incidents, to avoid the reverse order.
- House test style: reset only the package-level cache vars a test touches (`fortLookupCache = xsync.NewMap[string, FortLookup]()`), struct-literal fixtures, plain `if … { t.Fatalf }`, no assertion lib. Decoder tests: `go test ./decoder/... -run TestX -v`. Route tests (package `main`, repo root): `go test . -run TestX -v`. Build: `go build -tags go_json golbat`.

---

### Task 1: String incident fetch handle on `FortLookupIncident`

**Files:**
- Modify: `decoder/station_battle.go:51-59` (`FortLookupIncident` struct)
- Modify: `decoder/fortRtree.go:267-306` (`updatePokestopIncidentLookup`)
- Test: `decoder/fort_incident_id_test.go` (create)

**Interfaces:**
- Produces: `FortLookupIncident.Id string` — the incident id, populated on every incident upsert; consumed by Task 2's `CollectPokestopIncidents`.

- [ ] **Step 1: Write the failing test** — `decoder/fort_incident_id_test.go`:

```go
package decoder

import (
	"testing"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
)

// updatePokestopIncidentLookup must carry the incident Id onto the FortLookup
// projection so the scan can fetch the whole incident row from incidentCache.
func TestUpdatePokestopIncidentLookupCarriesId(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	const id = "stop-1"
	fortLookupCache.Store(id, FortLookup{FortType: POKESTOP, Lat: 1, Lon: 2})

	inc := &Incident{IncidentData: IncidentData{
		Id:             "-1016089077232382347",
		DisplayType:    1,
		Character:      5,
		Confirmed:      true,
		Slot1PokemonId: null.IntFrom(41),
		ExpirationTime: 9_999_999_999,
	}}
	updatePokestopIncidentLookup(id, inc)

	fl, ok := fortLookupCache.Load(id)
	if !ok || len(fl.Incidents) != 1 {
		t.Fatalf("expected 1 incident, got %+v", fl.Incidents)
	}
	if fl.Incidents[0].Id != "-1016089077232382347" {
		t.Fatalf("incident Id not carried: %q", fl.Incidents[0].Id)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/... -run TestUpdatePokestopIncidentLookupCarriesId -v`
Expected: FAIL to compile — `fl.Incidents[0].Id undefined (type FortLookupIncident has no field or method Id)`.

- [ ] **Step 3: Add the field** — in `decoder/station_battle.go`, add `Id` as the first field of `FortLookupIncident`:

```go
type FortLookupIncident struct {
	Id              string // incident id — fetch handle into incidentCache (not DNF-used)
	DisplayType     int8
	Style           int8
	Character       int16
	Confirmed       bool
	Slot1PokemonId  int16
	Slot1Form       int16
	ExpireTimestamp int64 // used to skip expired incidents at filter time
}
```

- [ ] **Step 4: Populate it** — in `decoder/fortRtree.go`, in `updatePokestopIncidentLookup`, add `Id` to the `updated` literal:

```go
	updated := FortLookupIncident{
		Id:              incident.Id,
		DisplayType:     int8(incident.DisplayType),
		Style:           int8(incident.Style),
		Character:       incident.Character,
		Confirmed:       incident.Confirmed,
		Slot1PokemonId:  int16(incident.Slot1PokemonId.ValueOrZero()),
		Slot1Form:       int16(incident.Slot1Form.ValueOrZero()),
		ExpireTimestamp: incident.ExpirationTime,
	}
```

- [ ] **Step 5: Run tests to verify they pass** (incl. the existing concurrency + DNF tests, which construct `FortLookupIncident` without `Id` — a leading new field keeps positional literals valid only if they use field names; the existing tests at `fort_incident_test.go` and `api_pokestop_available_test.go` use **named** fields, so they still compile):

Run: `go test ./decoder/... -run 'TestUpdatePokestopIncidentLookupCarriesId|TestFortDnfMatch_IncidentSlice|TestFortLookupConcurrentPokestopAndIncidentWriters|TestGetAvailablePokestops' -v`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add decoder/station_battle.go decoder/fortRtree.go decoder/fort_incident_id_test.go
git commit -m "feat(fort): carry incident id on FortLookupIncident as a fetch handle"
```

---

### Task 2: Invasions in the pokestop scan + by-id (`with_incidents`)

**Files:**
- Modify: `decoder/api_pokestop.go` (add `ApiPokestopIncident`, `Invasions` field, `buildPokestopIncident`, `CollectPokestopIncidents`)
- Modify: `decoder/api_fort.go:14-19` (`ApiFortScan.WithIncidents`), `:344-367` (`PokestopScanEndpoint`), and the pokestop loop in `FortCombinedScanEndpoint`
- Modify: `routes_huma.go:509-531` (pokestop by-id handler attaches incidents)
- Test: `decoder/api_pokestop_incidents_test.go` (create)

**Interfaces:**
- Consumes: `FortLookupIncident.Id` (Task 1).
- Produces: `ApiPokestopResult.Invasions []ApiPokestopIncident`; `decoder.CollectPokestopIncidents(ctx context.Context, dbDetails db.DbDetails, fortId string, now int64) []ApiPokestopIncident`; `ApiFortScan.WithIncidents bool`.

- [ ] **Step 1: Write the failing test** — `decoder/api_pokestop_incidents_test.go`:

```go
package decoder

import (
	"context"
	"testing"

	db "golbat/db"

	"github.com/guregu/null/v6"
	"github.com/puzpuzpuz/xsync/v4"
)

// CollectPokestopIncidents returns the whole active-incident rows for a fort,
// looked up from incidentCache via the FortLookup handles, skipping expired.
func TestCollectPokestopIncidents(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	incidentCache = newTestIncidentCache()
	now := int64(1_000_000)

	active := &Incident{IncidentData: IncidentData{
		Id: "inc-active", PokestopId: "s1", DisplayType: 1, Character: 5,
		Confirmed: true, Slot1PokemonId: null.IntFrom(41), ExpirationTime: now + 100,
	}}
	expired := &Incident{IncidentData: IncidentData{
		Id: "inc-expired", PokestopId: "s1", DisplayType: 3, Character: 30, ExpirationTime: now - 1,
	}}
	incidentCache.Set("inc-active", active, 0)
	incidentCache.Set("inc-expired", expired, 0)

	fortLookupCache.Store("s1", FortLookup{FortType: POKESTOP, Incidents: []FortLookupIncident{
		{Id: "inc-active", DisplayType: 1, Character: 5, ExpireTimestamp: now + 100},
		{Id: "inc-expired", DisplayType: 3, Character: 30, ExpireTimestamp: now - 1},
	}})

	got := CollectPokestopIncidents(context.Background(), db.DbDetails{}, "s1", now)
	if len(got) != 1 {
		t.Fatalf("expected 1 active incident, got %d: %+v", len(got), got)
	}
	if got[0].Id != "inc-active" || got[0].Character != 5 || got[0].Slot1PokemonId == nil || *got[0].Slot1PokemonId != 41 {
		t.Fatalf("wrong incident payload: %+v", got[0])
	}
}
```

Note: this test needs a helper `newTestIncidentCache()` because `incidentCache` is created in `main.go`'s init path. Add it to the test file:

```go
func newTestIncidentCache() *ottercache.OtterCache[string, *Incident] {
	return ottercache.NewOtterCache(ottercache.OtterCacheConfig[string, *Incident]{
		Name: "incident-test", DefaultTTL: 60 * time.Minute,
	})
}
```

with imports `"time"` and `ottercache "golbat/ottercache"` (confirm the import path from `decoder/main.go:203`'s usage; it is imported there as `ottercache`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/... -run TestCollectPokestopIncidents -v`
Expected: FAIL to compile — `undefined: CollectPokestopIncidents`, `undefined: ApiPokestopIncident field`.

- [ ] **Step 3: Add the response type + builders** — append to `decoder/api_pokestop.go`:

```go
// ApiPokestopIncident is one active incident (whole row) on a pokestop, as
// returned in a scan/by-id response when with_incidents is set. Sourced from
// incidentCache via the FortLookup fetch handle; nullable slots are pointers.
type ApiPokestopIncident struct {
	Id             string `json:"id" doc:"Incident id"`
	DisplayType    int16  `json:"display_type" doc:"Incident display type (1-4 rocket, 7 goldstop, 8 kecleon, 9 showcase)"`
	Style          int16  `json:"style" doc:"Incident style"`
	Character      int16  `json:"character" doc:"Invasion character id (grunt/leader/giovanni); 0 for non-rocket"`
	StartTime      int64  `json:"start" doc:"Unix timestamp when the incident started"`
	ExpirationTime int64  `json:"expiration" doc:"Unix timestamp when the incident expires"`
	Confirmed      bool   `json:"confirmed" doc:"True when the lineup is confirmed (grunts only)"`
	Slot1PokemonId *int64 `json:"slot_1_pokemon_id" doc:"Confirmed lead pokemon id, else null"`
	Slot1Form      *int64 `json:"slot_1_form" doc:"Confirmed lead pokemon form, else null"`
	Slot2PokemonId *int64 `json:"slot_2_pokemon_id" doc:"Slot 2 pokemon id, else null"`
	Slot2Form      *int64 `json:"slot_2_form" doc:"Slot 2 form, else null"`
	Slot3PokemonId *int64 `json:"slot_3_pokemon_id" doc:"Slot 3 pokemon id, else null"`
	Slot3Form      *int64 `json:"slot_3_form" doc:"Slot 3 form, else null"`
}

func buildPokestopIncident(inc *Incident) ApiPokestopIncident {
	return ApiPokestopIncident{
		Id:             inc.Id,
		DisplayType:    inc.DisplayType,
		Style:          inc.Style,
		Character:      inc.Character,
		StartTime:      inc.StartTime,
		ExpirationTime: inc.ExpirationTime,
		Confirmed:      inc.Confirmed,
		Slot1PokemonId: inc.Slot1PokemonId.Ptr(),
		Slot1Form:      inc.Slot1Form.Ptr(),
		Slot2PokemonId: inc.Slot2PokemonId.Ptr(),
		Slot2Form:      inc.Slot2Form.Ptr(),
		Slot3PokemonId: inc.Slot3PokemonId.Ptr(),
		Slot3Form:      inc.Slot3Form.Ptr(),
	}
}

// CollectPokestopIncidents returns the whole-row active incidents for a fort,
// resolved from incidentCache via the string handles in the fort's FortLookup
// (read-through to DB on the rare cache miss). Callers MUST NOT hold the
// pokestop lock — this locks incidents, and saveIncidentRecord locks
// incident->pokestop, so holding pokestop here would invert the order.
func CollectPokestopIncidents(ctx context.Context, dbDetails db.DbDetails, fortId string, now int64) []ApiPokestopIncident {
	fl, ok := fortLookupCache.Load(fortId)
	if !ok || len(fl.Incidents) == 0 {
		return nil
	}
	out := make([]ApiPokestopIncident, 0, len(fl.Incidents))
	for _, li := range fl.Incidents {
		if li.ExpireTimestamp <= now || li.Id == "" {
			continue
		}
		inc, unlock, err := getIncidentRecordReadOnly(ctx, dbDetails, li.Id, "API.CollectPokestopIncidents")
		if err != nil || inc == nil {
			if unlock != nil {
				unlock()
			}
			continue
		}
		out = append(out, buildPokestopIncident(inc))
		unlock()
	}
	return out
}
```

Add the `Invasions` field to `ApiPokestopResult` (after `ShowcaseRankings`):

```go
	ShowcaseRankings           *string `json:"showcase_rankings" doc:"Serialized showcase contest rankings"`
	Invasions                  []ApiPokestopIncident `json:"invasions,omitempty" doc:"Active incidents; present only when with_incidents was requested"`
```

Ensure `decoder/api_pokestop.go` imports `"context"` and `db "golbat/db"` (match the alias used in `api_fort.go`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/... -run TestCollectPokestopIncidents -v`
Expected: PASS.

- [ ] **Step 5: Wire `with_incidents` into the request + endpoints.** Add the field to `ApiFortScan` (`decoder/api_fort.go:14-19`):

```go
type ApiFortScan struct {
	Min        ApiLatLon          `json:"min" doc:"SW (minimum lat/lon) corner of the bounding box."`
	Max        ApiLatLon          `json:"max" doc:"NE (maximum lat/lon) corner of the bounding box."`
	Limit      int                `json:"limit" required:"false" doc:"Max results to return; 0 uses the server default."`
	DnfFilters []ApiFortDnfFilter `json:"filters" required:"false" doc:"OR'd filter clauses; a fort matches if it satisfies any one clause. List conditions apply only when present: omit or send null for no constraint — an explicitly empty list matches nothing."`
	WithIncidents bool             `json:"with_incidents" required:"false" doc:"Pokestop only: when true, each pokestop result includes its active incidents (invasions). Ignored for gym/station."`
}
```

In `PokestopScanEndpoint` (`decoder/api_fort.go:344-367`), attach incidents **after releasing the pokestop lock**:

```go
func PokestopScanEndpoint(retrieveParameters ApiFortScan, dbDetails db.DbDetails) *ApiPokestopScanResult {
	returnKeys, examined, skipped, total := internalGetForts(POKESTOP, retrieveParameters)
	results := make([]*ApiPokestopResult, 0, len(returnKeys))
	start := time.Now()
	now := time.Now().Unix()

	for _, key := range returnKeys {
		pokestop, unlock, err := getPokestopRecordReadOnly(context.Background(), dbDetails, key, "API.GetScanpokemon")
		if err == nil && pokestop != nil {
			pokestopCopy := buildPokestopResult(pokestop)
			if unlock != nil {
				unlock() // release pokestop lock BEFORE locking incidents (lock-order)
				unlock = nil
			}
			if retrieveParameters.WithIncidents {
				pokestopCopy.Invasions = CollectPokestopIncidents(context.Background(), dbDetails, key, now)
			}
			results = append(results, &pokestopCopy)
		}
		if unlock != nil {
			unlock()
		}
	}
	log.Infof("PokestopScan - result buffer time %s, %d added", time.Since(start), len(results))

	return &ApiPokestopScanResult{
		Pokestops: results,
		Examined:  examined,
		Skipped:   skipped,
		Total:     total,
	}
}
```

Apply the **same** attach in `FortCombinedScanEndpoint`'s pokestop-building loop (same file): after `buildPokestopResult`, release the pokestop unlock, then `if retrieveParameters.WithIncidents { copy.Invasions = CollectPokestopIncidents(context.Background(), dbDetails, key, now) }`. (Read the current `FortCombinedScanEndpoint` body first; its pokestop loop mirrors `PokestopScanEndpoint`'s.)

- [ ] **Step 6: Attach incidents in the pokestop by-id handler** — `routes_huma.go:509-531`, restructure to release the pokestop lock before the incident lookup:

```go
	}, func(ctx context.Context, in *pokestopByIdInput) (*pokestopByIdOutput, error) {
		pokestop, unlock, err := decoder.PeekPokestopRecord(in.FortId, "API.GetPokestop")
		if err != nil {
			if unlock != nil {
				unlock()
			}
			return nil, huma.Error500InternalServerError("error retrieving pokestop")
		}
		if pokestop == nil {
			if unlock != nil {
				unlock()
			}
			return nil, huma.Error404NotFound("pokestop not found")
		}
		body := decoder.BuildPokestopResult(pokestop)
		if unlock != nil {
			unlock() // release before locking incidents
		}
		body.Invasions = decoder.CollectPokestopIncidents(ctx, dbDetails, in.FortId, time.Now().Unix())
		return &pokestopByIdOutput{Body: body}, nil
	})
```

- [ ] **Step 7: Build + run the full decoder + route suites**

Run: `go build -tags go_json golbat && go test ./decoder/... -run 'TestCollectPokestopIncidents|TestGetAvailablePokestops|TestFortDnfMatch_IncidentSlice' -v`
Expected: build OK; PASS.

- [ ] **Step 8: Commit**

```bash
git add decoder/api_pokestop.go decoder/api_fort.go routes_huma.go decoder/api_pokestop_incidents_test.go
git commit -m "feat(fort): attach whole-row invasions to pokestop scan/by-id via with_incidents"
```

---

### Task 3: Station by-id endpoint

**Files:**
- Modify: `routes_huma.go` (add `stationByIdInput`/`stationByIdOutput` near `:276`, register `GET /api/station/id/{station_id}` near the gym by-id at `:483`)
- Test: `huma_routes_test.go` (add a route test)

**Interfaces:**
- Consumes: `decoder.GetStationRecordReadOnly(ctx, dbDetails, id, caller)` and `decoder.BuildStationResult(station)` (both exist).

- [ ] **Step 1: Write the failing test** — add to `huma_routes_test.go`:

```go
// TestHumaStationByIdRoute verifies the new station by-id route is registered,
// requires the secret, and 404s for an unknown id (empty cache, no DB).
func TestHumaStationByIdRoute(t *testing.T) {
	prev := config.Config.ApiSecret
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prev }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	t.Run("no secret is 401", func(t *testing.T) {
		resp := api.Get("/api/station/id/does-not-exist")
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("got %d, want 401", resp.Code)
		}
	})
	t.Run("unknown id is 404", func(t *testing.T) {
		resp := api.Get("/api/station/id/does-not-exist", "X-Golbat-Secret: topsecret")
		if resp.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404; body=%s", resp.Code, resp.Body.String())
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestHumaStationByIdRoute -v`
Expected: FAIL — the unknown-id case returns 404 only if the route exists; before registration it's 404 from the router's default "no operation" or 401 mismatch. Confirm it fails (route missing).

- [ ] **Step 3: Add the input/output types** — near `routes_huma.go:276`:

```go
type stationByIdInput struct {
	StationId string `path:"station_id" doc:"ID of the station"`
}
type stationByIdOutput struct{ Body decoder.ApiStationResult }
```

- [ ] **Step 4: Register the route** — mirror `GET /api/gym/id/{gym_id}` (ungated), placed next to it (~`routes_huma.go:507`):

```go
	// GET /api/station/id/{station_id}
	huma.Register(api, huma.Operation{
		OperationID:   "get-station",
		Method:        http.MethodGet,
		Path:          "/api/station/id/{station_id}",
		Summary:       "Get a single station by id",
		Description:   "Returns the station with the given id, or 404 if not present.",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *stationByIdInput) (*stationByIdOutput, error) {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		station, unlock, err := decoder.GetStationRecordReadOnly(tctx, dbDetails, in.StationId, "API.GetStation")
		if unlock != nil {
			defer unlock()
		}
		cancel()
		if err != nil {
			return nil, huma.Error500InternalServerError("error retrieving station")
		}
		if station == nil {
			return nil, huma.Error404NotFound("station not found")
		}
		return &stationByIdOutput{Body: decoder.BuildStationResult(station)}, nil
	})
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go build -tags go_json golbat && go test . -run TestHumaStationByIdRoute -v`
Expected: build OK; PASS (401 then 404).

- [ ] **Step 6: Commit**

```bash
git add routes_huma.go huma_routes_test.go
git commit -m "feat(fort): add GET /api/station/id/{id} single-station endpoint"
```

---

### Task 4: Station preload under bare `fort_in_memory`

**Files:**
- Modify: `decoder/preload.go:60-84` (`PreloadForts`)

**Interfaces:**
- Consumes: `preloadStations(dbDetails, populateRtree)` and `preloadStationBattles(dbDetails, populateRtree)` (both exist).

Note: this loads from the DB, so it is verified by build + a documented integration smoke, not a unit test (the existing `preload*` functions have no unit tests — they require a live DB). The ordering constraint is real: `preloadStationBattles` needs `stationCache` populated first (`station_battle.go:686` checks `stationCache.Has`).

- [ ] **Step 1: Add station loading to `PreloadForts`**, preserving the two-phase order (stations before battles), mirroring `Preload`:

```go
func PreloadForts(dbDetails db.DbDetails, populateRtree bool) error {
	startTime := time.Now()

	var wg sync.WaitGroup
	var pokestopCount, gymCount, stationCount int32

	// Phase 1: forts (pokestops, gyms, stations) in parallel.
	wg.Add(3)
	go func() {
		defer wg.Done()
		pokestopCount = preloadPokestops(dbDetails, populateRtree)
	}()
	go func() {
		defer wg.Done()
		gymCount = preloadGyms(dbDetails, populateRtree)
	}()
	go func() {
		defer wg.Done()
		stationCount = preloadStations(dbDetails, populateRtree)
	}()
	wg.Wait()

	// Phase 2: station battles depend on stationCache being populated.
	stationBattleCount := preloadStationBattles(dbDetails, populateRtree)

	log.Infof("PreloadForts: loaded %d pokestops, %d gyms, %d stations, %d station battles in %v (rtree=%v)",
		pokestopCount, gymCount, stationCount, stationBattleCount, time.Since(startTime), populateRtree)

	return nil
}
```

- [ ] **Step 2: Build**

Run: `go build -tags go_json golbat`
Expected: OK.

- [ ] **Step 3: Documented integration smoke** (record in the PR, not a unit test): start Golbat with `fort_in_memory = true` and `preload = false`; confirm the log line `PreloadForts: loaded … stations, … station battles`; `curl -H "X-Golbat-Secret: <s>" -XPOST <host>/api/station/scan -d '{"min":…,"max":…}'` returns stations in a scanned area (empty before this change).

- [ ] **Step 4: Commit**

```bash
git add decoder/preload.go
git commit -m "feat(preload): load stations + battles under bare fort_in_memory"
```

---

### Task 5: Gym `available` endpoint

**Files:**
- Create: `decoder/api_gym_available.go`
- Modify: `routes_huma.go` (register `GET /api/gym/available` in `registerFortScanRoutes`, add `gymAvailableOutput`)
- Test: `decoder/api_gym_available_test.go` (create)

**Interfaces:**
- Produces: `decoder.GetAvailableGyms(now int64) *ApiAvailableGyms`.

- [ ] **Step 1: Write the failing test** — `decoder/api_gym_available_test.go`:

```go
package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

func TestGetAvailableGyms(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	now := int64(1_000_000)

	// gym with team + active raid boss
	fortLookupCache.Store("g1", FortLookup{
		FortType: GYM, TeamId: 1, AvailableSlots: 2,
		RaidLevel: 5, RaidPokemonId: 150, RaidPokemonForm: 0, RaidEndTimestamp: now + 100,
	})
	// gym with an active egg (no boss) and an EXPIRED raid on another
	fortLookupCache.Store("g2", FortLookup{
		FortType: GYM, TeamId: 2, AvailableSlots: 6,
		RaidLevel: 3, RaidPokemonId: 0, RaidEndTimestamp: now + 100,
	})
	fortLookupCache.Store("g3", FortLookup{
		FortType: GYM, TeamId: 1, AvailableSlots: 0,
		RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: now - 1, // expired -> excluded
	})
	// a pokestop must be ignored
	fortLookupCache.Store("s1", FortLookup{FortType: POKESTOP, LureId: 501})

	res := GetAvailableGyms(now)

	if len(res.Teams) != 3 { // (1,2),(2,6),(1,0)
		t.Fatalf("teams: %+v", res.Teams)
	}
	// raids: boss 150 lvl5, egg lvl3; expired 999 excluded
	var bosses, eggs int
	for _, r := range res.Raids {
		if r.PokemonId == 999 {
			t.Fatalf("expired raid leaked: %+v", r)
		}
		if r.PokemonId == 0 {
			eggs++
		} else {
			bosses++
		}
	}
	if bosses != 1 || eggs != 1 {
		t.Fatalf("raids: %+v", res.Raids)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./decoder/... -run TestGetAvailableGyms -v`
Expected: FAIL — `undefined: GetAvailableGyms`.

- [ ] **Step 3: Implement** — `decoder/api_gym_available.go`:

```go
package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiGymTeamAvailable is one distinct (team, available-slots) pair present on
// resident gyms, with how many gyms carry it. ReactMap derives its t/g keys.
type ApiGymTeamAvailable struct {
	TeamId         int8 `json:"team_id" doc:"Controlling team id (0 = uncontested)"`
	AvailableSlots int8 `json:"available_slots" doc:"Open defender slots"`
	Count          int  `json:"count" doc:"Number of resident gyms with this team/slots"`
}

// ApiGymRaidAvailable is one distinct active raid option on resident gyms.
// PokemonId 0 means an egg (no boss yet). ReactMap derives its e/r/boss keys.
type ApiGymRaidAvailable struct {
	RaidLevel int8  `json:"raid_level" doc:"Raid level/tier"`
	PokemonId int16 `json:"pokemon_id" doc:"Raid boss pokemon id; 0 = egg (unhatched)"`
	Form      int16 `json:"form" doc:"Raid boss form id, else 0"`
	Count     int   `json:"count" doc:"Number of resident gyms with this raid option"`
}

// ApiAvailableGyms is the whole-instance gym filter snapshot served by
// GET /api/gym/available.
type ApiAvailableGyms struct {
	Teams []ApiGymTeamAvailable `json:"teams" doc:"Distinct team + available-slot pairs on resident gyms"`
	Raids []ApiGymRaidAvailable `json:"raids" doc:"Distinct active raid levels/bosses/eggs on resident gyms"`
}

// GetAvailableGyms builds the gym filter snapshot from a single fortLookupCache
// range over resident gyms — no maintained map (FortLookup carries every gym
// filter field). Teams are all-resident (no time filter); raids require an
// unexpired raid with level > 0.
func GetAvailableGyms(now int64) *ApiAvailableGyms {
	start := time.Now()
	res := &ApiAvailableGyms{Teams: []ApiGymTeamAvailable{}, Raids: []ApiGymRaidAvailable{}}
	teams := map[ApiGymTeamAvailable]int{}
	raids := map[ApiGymRaidAvailable]int{}
	forts := 0

	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType != GYM {
			return true
		}
		forts++
		teams[ApiGymTeamAvailable{TeamId: fl.TeamId, AvailableSlots: fl.AvailableSlots}]++
		if fl.RaidLevel > 0 && fl.RaidEndTimestamp > now {
			raids[ApiGymRaidAvailable{RaidLevel: fl.RaidLevel, PokemonId: fl.RaidPokemonId, Form: fl.RaidPokemonForm}]++
		}
		return true
	})

	for k, n := range teams {
		k.Count = n
		res.Teams = append(res.Teams, k)
	}
	for k, n := range raids {
		k.Count = n
		res.Raids = append(res.Raids, k)
	}

	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-gyms", time.Since(start).Seconds())
	}
	log.Infof("available-gyms built in %s: scanned %d gyms -> %d team/slot, %d raid options",
		time.Since(start), forts, len(res.Teams), len(res.Raids))
	return res
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./decoder/... -run TestGetAvailableGyms -v`
Expected: PASS.

- [ ] **Step 5: Register the route** — in `routes_huma.go`, add near the pokestop-available registration (`:220-236`), inside `registerFortScanRoutes`:

```go
type gymAvailableOutput struct {
	Body *decoder.ApiAvailableGyms
}
```
(place with the other `*Output` type decls, ~`:140`), then the handler:
```go
	gymAvailableOp := huma.Operation{
		OperationID:   "available-gyms",
		Method:        http.MethodGet,
		Path:          "/api/gym/available",
		Summary:       "List currently available gym teams/slots and raid options",
		Description:   "Distinct (team, available-slots) pairs and active raid levels/bosses/eggs on resident gyms, from the in-memory fort cache (no DB scan). Whole-instance; requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&gymAvailableOp)
	huma.Register(api, gymAvailableOp, func(ctx context.Context, _ *struct{}) (*gymAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &gymAvailableOutput{Body: decoder.GetAvailableGyms(time.Now().Unix())}, nil
	})
```

- [ ] **Step 6: Add a route gating test** — append to `huma_routes_test.go`:

```go
func TestHumaGymAvailableRoute(t *testing.T) {
	prevSecret := config.Config.ApiSecret
	prevFim := config.Config.FortInMemory
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prevSecret; config.Config.FortInMemory = prevFim }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	config.Config.FortInMemory = false
	if resp := api.Get("/api/gym/available", "X-Golbat-Secret: topsecret"); resp.Code != http.StatusServiceUnavailable {
		t.Errorf("fim off: got %d, want 503", resp.Code)
	}
	config.Config.FortInMemory = true
	resp := api.Get("/api/gym/available", "X-Golbat-Secret: topsecret")
	if resp.Code != http.StatusOK {
		t.Fatalf("fim on: got %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	for _, key := range []string{"teams", "raids"} {
		if !strings.Contains(resp.Body.String(), `"`+key+`"`) {
			t.Errorf("body missing %q: %s", key, resp.Body.String())
		}
	}
}
```

- [ ] **Step 7: Build + test**

Run: `go build -tags go_json golbat && go test ./decoder/... -run TestGetAvailableGyms -v && go test . -run TestHumaGymAvailableRoute -v`
Expected: build OK; both PASS.

- [ ] **Step 8: Commit**

```bash
git add decoder/api_gym_available.go decoder/api_gym_available_test.go routes_huma.go huma_routes_test.go
git commit -m "feat(fort): add GET /api/gym/available (team/slots + raid aggregate)"
```

---

### Task 6: Station `available` endpoint

**Files:**
- Create: `decoder/api_station_available.go`
- Modify: `routes_huma.go` (register `GET /api/station/available`, add `stationAvailableOutput`)
- Test: `decoder/api_station_available_test.go` (create)

**Interfaces:**
- Produces: `decoder.GetAvailableStations(now int64) *ApiAvailableStations`.

The aggregate mirrors `isFortDnfMatch`'s station branch (`api_fort.go:215-240`): iterate `StationBattles` when non-empty, else fall back to the top-battle projection; skip expired (`BattleEndTimestamp <= now`) and level-0 battles (ReactMap excludes `!battle_level`).

- [ ] **Step 1: Write the failing test** — `decoder/api_station_available_test.go`:

```go
package decoder

import (
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

func TestGetAvailableStations(t *testing.T) {
	fortLookupCache = xsync.NewMap[string, FortLookup]()
	now := int64(1_000_000)

	// station with two active battles (multi-battle path) + one expired
	fortLookupCache.Store("st1", FortLookup{FortType: STATION, StationBattles: []FortLookupStationBattle{
		{BattleLevel: 3, BattlePokemonId: 150, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 5, BattlePokemonId: 384, BattlePokemonForm: 0, BattleEndTimestamp: now + 100},
		{BattleLevel: 1, BattlePokemonId: 1, BattleEndTimestamp: now - 1}, // expired -> excluded
	}})
	// station with only the top-battle projection (no StationBattles slice)
	fortLookupCache.Store("st2", FortLookup{FortType: STATION,
		BattleLevel: 6, BattlePokemonId: 999, BattlePokemonForm: 0, BattleEndTimestamp: now + 100,
	})
	// station with a level-0 battle -> excluded
	fortLookupCache.Store("st3", FortLookup{FortType: STATION, StationBattles: []FortLookupStationBattle{
		{BattleLevel: 0, BattlePokemonId: 5, BattleEndTimestamp: now + 100},
	}})
	fortLookupCache.Store("g1", FortLookup{FortType: GYM, TeamId: 1}) // ignored

	res := GetAvailableStations(now)
	// expect: (3,150),(5,384) from st1, (6,999) from st2 = 3 distinct; expired + level-0 excluded
	if len(res.Battles) != 3 {
		t.Fatalf("battles: %+v", res.Battles)
	}
	for _, b := range res.Battles {
		if b.BattleLevel == 0 || b.PokemonId == 1 {
			t.Fatalf("excluded battle leaked: %+v", b)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./decoder/... -run TestGetAvailableStations -v`
Expected: FAIL — `undefined: GetAvailableStations`.

- [ ] **Step 3: Implement** — `decoder/api_station_available.go`:

```go
package decoder

import (
	"time"

	log "github.com/sirupsen/logrus"
)

// ApiStationBattleAvailable is one distinct active (battle_level, pokemon, form)
// option on resident stations. ReactMap derives its <id>-<form> and j<level> keys.
type ApiStationBattleAvailable struct {
	BattleLevel int8  `json:"battle_level" doc:"Max battle level"`
	PokemonId   int16 `json:"pokemon_id" doc:"Battle pokemon id, else 0"`
	Form        int16 `json:"form" doc:"Battle pokemon form id, else 0"`
	Count       int   `json:"count" doc:"Number of resident stations with this active battle option"`
}

// ApiAvailableStations is the whole-instance station filter snapshot served by
// GET /api/station/available.
type ApiAvailableStations struct {
	Battles []ApiStationBattleAvailable `json:"battles" doc:"Distinct active battle level/pokemon options on resident stations"`
}

// GetAvailableStations builds the station filter snapshot from a single
// fortLookupCache range. Mirrors isFortDnfMatch's station branch: iterate the
// StationBattles slice when present, else fall back to the top-battle
// projection; skip expired and level-0 battles.
func GetAvailableStations(now int64) *ApiAvailableStations {
	start := time.Now()
	res := &ApiAvailableStations{Battles: []ApiStationBattleAvailable{}}
	battles := map[ApiStationBattleAvailable]int{}
	forts := 0

	add := func(level int8, pokemonId, form int16, end int64) {
		if level == 0 || end <= now {
			return
		}
		battles[ApiStationBattleAvailable{BattleLevel: level, PokemonId: pokemonId, Form: form}]++
	}

	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType != STATION {
			return true
		}
		forts++
		if len(fl.StationBattles) == 0 {
			add(fl.BattleLevel, fl.BattlePokemonId, fl.BattlePokemonForm, fl.BattleEndTimestamp)
			return true
		}
		for _, b := range fl.StationBattles {
			add(b.BattleLevel, b.BattlePokemonId, b.BattlePokemonForm, b.BattleEndTimestamp)
		}
		return true
	})

	for k, n := range battles {
		k.Count = n
		res.Battles = append(res.Battles, k)
	}

	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-stations", time.Since(start).Seconds())
	}
	log.Infof("available-stations built in %s: scanned %d stations -> %d battle options",
		time.Since(start), forts, len(res.Battles))
	return res
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./decoder/... -run TestGetAvailableStations -v`
Expected: PASS.

- [ ] **Step 5: Register the route** — in `routes_huma.go`, `registerFortScanRoutes`, mirroring Task 5:

```go
type stationAvailableOutput struct {
	Body *decoder.ApiAvailableStations
}
```
then:
```go
	stationAvailableOp := huma.Operation{
		OperationID:   "available-stations",
		Method:        http.MethodGet,
		Path:          "/api/station/available",
		Summary:       "List currently available station battle options",
		Description:   "Distinct active (battle level, pokemon) options on resident stations, from the in-memory fort cache (no DB scan). Whole-instance; requires fort_in_memory (503 otherwise).",
		Tags:          []string{"Fort"},
		Security:      []map[string][]string{{securitySchemeName: {}}},
		DefaultStatus: http.StatusOK,
	}
	draftBadge(&stationAvailableOp)
	huma.Register(api, stationAvailableOp, func(ctx context.Context, _ *struct{}) (*stationAvailableOutput, error) {
		if !config.Config.FortInMemory {
			return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
		}
		return &stationAvailableOutput{Body: decoder.GetAvailableStations(time.Now().Unix())}, nil
	})
```

- [ ] **Step 6: Add a route gating test** — append to `huma_routes_test.go` (mirror `TestHumaGymAvailableRoute`, asserting the `"battles"` key and 503-when-off):

```go
func TestHumaStationAvailableRoute(t *testing.T) {
	prevSecret := config.Config.ApiSecret
	prevFim := config.Config.FortInMemory
	config.Config.ApiSecret = "topsecret"
	defer func() { config.Config.ApiSecret = prevSecret; config.Config.FortInMemory = prevFim }()

	_, api := humatest.New(t, newHumaConfig("test"))
	api.UseMiddleware(golbatSecretMiddleware(api))
	registerHumaRoutes(api)

	config.Config.FortInMemory = false
	if resp := api.Get("/api/station/available", "X-Golbat-Secret: topsecret"); resp.Code != http.StatusServiceUnavailable {
		t.Errorf("fim off: got %d, want 503", resp.Code)
	}
	config.Config.FortInMemory = true
	resp := api.Get("/api/station/available", "X-Golbat-Secret: topsecret")
	if resp.Code != http.StatusOK {
		t.Fatalf("fim on: got %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"battles"`) {
		t.Errorf("body missing battles: %s", resp.Body.String())
	}
}
```

- [ ] **Step 7: Build + full suite**

Run: `go build -tags go_json golbat && go test ./decoder/... && go test . -run TestHuma -v`
Expected: build OK; all PASS.

- [ ] **Step 8: Commit**

```bash
git add decoder/api_station_available.go decoder/api_station_available_test.go routes_huma.go huma_routes_test.go
git commit -m "feat(fort): add GET /api/station/available (battle option aggregate)"
```

---

## Self-Review

**Spec coverage** (§7.1–7.6 of `2026-07-16-fort-scan-map-data-design.md`):
- §7.1 string incident handle → Task 1. §7.2 invasions in scan/by-id + `with_incidents` → Task 2. §7.3 (no FortLookup widening) → honored (only a string locator added). §7.4 station by-id → Task 3. §7.5 station preload → Task 4. §7.6 gym/station available → Tasks 5–6. Instrumentation (`ObserveApiScan`) → in Tasks 5–6; the scan endpoints were already only `log.Infof` (unchanged). No gap.

**Placeholder scan:** every code step has complete code; the two DB-dependent verifications (Task 4 preload, and the live golden checks) are explicitly documented as integration smokes, not faked unit tests.

**Type consistency:** `FortLookupIncident.Id` (Task 1) is consumed by `CollectPokestopIncidents` (Task 2); `ApiPokestopResult.Invasions []ApiPokestopIncident` matches the builder; `ApiFortScan.WithIncidents` is read in `PokestopScanEndpoint` + by-id; `GetAvailableGyms`/`GetAvailableStations` return `*ApiAvailableGyms`/`*ApiAvailableStations` matching the route `*Output` bodies. Route tests use the confirmed harness (`humatest.New` + `registerHumaRoutes` + `golbatSecretMiddleware`).

**Cross-task ordering:** Task 2 depends on Task 1 (`Id` field). Tasks 3–6 are independent of 1–2 and of each other. Suggested order 1→2→3→4→5→6 groups the incident work first, then the additive routes.

**Open verification for the implementer:** in Task 2 Step 5, read the current `FortCombinedScanEndpoint` body before editing (its pokestop loop was not quoted here); apply the identical lock-release-then-`CollectPokestopIncidents` change. In Task 2 Step 3, confirm `decoder/api_pokestop.go`'s import block gains `context` + the `db` alias used elsewhere in the package.
