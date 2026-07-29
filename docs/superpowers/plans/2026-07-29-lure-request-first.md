# Request-First Lure Pokemon Processing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix #390 (lure pokemon permanently poisoned by a failed pokestop lookup) and implement #283 (process the DiskEncounter *request* proto so lure pokemon no longer depend on GMO-first ordering), deleting `diskEncounterCache`.

**Architecture:** Each proto contributes what it alone knows, in any arrival order, to the record keyed by encounter ID. The GMO's fort entry (`fort.ActivePokemon` rides inside it) supplies placement + verified despawn for `lure_wild`; the `DiskEncounterProto` request supplies placement for `lure_encounter`; the `DiskEncounterOutProto` response supplies IVs. Placement happens exactly once, at record creation, by whichever proto arrives first — an unplaced record is never saved.

**Tech Stack:** Go, `guregu/null/v6`, package-level tests in `decoder` (memory-only mode, no DB), golangci-lint v2.

**Spec:** `docs/superpowers/specs/2026-07-29-lure-request-first-design.md` — read it before starting.

## Global Constraints

- Branch `fix/lure-request-first` off updated local `main` (currently `200c6d9`). Do NOT branch off `perf/eviction-lock-contention`.
- `decoder` tests share package-global caches; every test must use unique encounter/fort IDs (use the `9101xx` range as written in the tasks).
- `getPokemonRecordForUpdate` MUST NOT be deleted even when it loses its last caller — the peek/ReadOnly/ForUpdate/getOrCreate accessor set is intentionally symmetric across entities (`.golangci.yml` has an `unused` exclusion for exactly this; see CLAUDE.md "Record Access Patterns").
- The lure lifetime estimate is exactly 180 seconds (`lureSpawnLifetimeSeconds`) — a lure spits out a new pokemon every 3 minutes, each lasting 3 minutes.
- Never hold two entity locks simultaneously (CLAUDE.md locking model). No new pokestop lookups are introduced anywhere in this plan.
- Commit messages end with: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Verification commands: `go build ./...`, `go vet ./...`, `go test ./decoder/ -run '<pattern>' -v`, full `go test ./...`, `golangci-lint run`.

---

### Task 1: Branch setup and spec commit

**Files:**
- Commit: `docs/superpowers/specs/2026-07-29-lure-request-first-design.md` (exists untracked in the primary checkout)

**Interfaces:**
- Produces: branch `fix/lure-request-first` at `main` (200c6d9) with the spec as its first commit. All later tasks build on this branch.

- [ ] **Step 1: Create the branch (worktree optional per superpowers:using-git-worktrees)**

```bash
git -C /Users/james/GolandProjects/Golbat branch fix/lure-request-first main
```

If using a worktree: `git worktree add <worktree-path> fix/lure-request-first` and copy the untracked spec file from the primary checkout into the worktree at the same relative path. If working inline in the primary checkout, note the checkout currently has `perf/eviction-lock-contention` checked out with untracked files (`.claude/`, `contributor-analysis.md`, `mcp-server/`) — leave those untouched; `git switch fix/lure-request-first` is safe because the spec/plan files are untracked.

- [ ] **Step 2: Commit the spec and plan**

```bash
git add docs/superpowers/specs/2026-07-29-lure-request-first-design.md docs/superpowers/plans/2026-07-29-lure-request-first.md
git commit -m "docs: spec and plan for request-first lure processing (#390, #283)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

- [ ] **Step 3: Verify clean build baseline**

Run: `go build ./... && go test ./decoder/ -count=1 > /dev/null && echo BASELINE-OK`
Expected: `BASELINE-OK`

---

### Task 2: Capture fort identity at GMO extraction; rewrite `updateFromMap` as a merge

**Files:**
- Modify: `decoder/main.go:48-52` (`RawMapPokemonData` struct)
- Modify: `decode.go:518-520` (extraction populates fort fields)
- Modify: `decoder/pokemon_decode.go:180-226` (`updateFromMap`)
- Modify: `decoder/gmo_decode.go:179-197` (map-pokemon loop)
- Create: `decoder/pokemon_lure_test.go`

**Interfaces:**
- Consumes: `getOrCreatePokemonRecord(ctx, db, encounterId uint64, caller string) (*Pokemon, func(), error)`; `savePokemonRecordAsAtTime(ctx, db, pokemon, isEncounter, writeDB, webhook bool, now int64)`; `SetWebhooksSender(whSender webhooksSenderInterface)`; `SetStatsCollector(collector stats_collector.StatsCollector)`; seen-type constants `SeenType_LureWild`, `SeenType_LureEncounter` (`decoder/pokemon_decode.go:296-302`).
- Produces:
  - `RawMapPokemonData{Cell uint64, Data *pogo.MapPokemonProto, Timestamp int64, FortId string, Lat float64, Lon float64}`
  - `func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon RawMapPokemonData, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, username string) bool` — returns true when the record changed and must be saved.
  - Test helpers `lureTestSetup(t *testing.T) *testWebhookSink` and `testWebhookSink.drain() []recordedWebhook` used by Task 3's tests.

- [ ] **Step 1: Write the failing tests**

Create `decoder/pokemon_lure_test.go`:

```go
package decoder

import (
	"context"
	"sync"
	"testing"
	"time"

	"golbat/config"
	"golbat/db"
	"golbat/geo"
	"golbat/pogo"
	"golbat/stats_collector"
	"golbat/webhooks"

	"github.com/guregu/null/v6"
)

type recordedWebhook struct {
	whType  webhooks.WebhookType
	message any
}

type testWebhookSink struct {
	mu   sync.Mutex
	msgs []recordedWebhook
}

func (s *testWebhookSink) AddMessage(whType webhooks.WebhookType, message any, areas []geo.AreaName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, recordedWebhook{whType, message})
}

func (s *testWebhookSink) drain() []recordedWebhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.msgs
	s.msgs = nil
	return out
}

// lureTestSetup puts the decoder package into memory-only mode with a
// recording webhook sink. The package caches are shared across the test
// binary, so every test must use unique encounter/fort IDs.
func lureTestSetup(t *testing.T) *testWebhookSink {
	t.Helper()
	prevMemOnly := config.Config.PokemonMemoryOnly
	config.Config.PokemonMemoryOnly = true
	sink := &testWebhookSink{}
	SetWebhooksSender(sink)
	SetStatsCollector(stats_collector.NewNoopStatsCollector())
	t.Cleanup(func() {
		config.Config.PokemonMemoryOnly = prevMemOnly
		SetWebhooksSender(nil)
	})
	return sink
}

func testRawMapPokemon(encId uint64, fortId string, lat, lon float64, expireMs int64) RawMapPokemonData {
	return RawMapPokemonData{
		Cell:      5000000000000000000,
		Timestamp: time.Now().UnixMilli(),
		FortId:    fortId,
		Lat:       lat,
		Lon:       lon,
		Data: &pogo.MapPokemonProto{
			SpawnpointId:     fortId,
			EncounterId:      encId,
			ExpirationTimeMs: expireMs,
			PokedexTypeId:    25,
			PokemonDisplay:   &pogo.PokemonDisplayProto{},
		},
	}
}

// #390 regression: placement must come from the captured fort fields, with
// no pokestop record anywhere.
func TestUpdateFromMapPlacesNewRecordFromCapturedFort(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910101)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, "lure-fort-910101", 51.5007, -0.1246, expireMs)

	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	saveNeeded := pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester")

	if !saveNeeded {
		t.Errorf("updateFromMap on new record = false, want true")
	}
	if got := pokemon.PokestopId.ValueOrZero(); got != "lure-fort-910101" {
		t.Errorf("PokestopId = %q, want lure-fort-910101", got)
	}
	if pokemon.Lat != 51.5007 || pokemon.Lon != -0.1246 {
		t.Errorf("Lat/Lon = %v/%v, want 51.5007/-0.1246", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureWild {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureWild)
	}
	if !pokemon.ExpireTimestampVerified {
		t.Errorf("ExpireTimestampVerified = false, want true (GMO supplied ExpirationTimeMs)")
	}
	if got := pokemon.ExpireTimestamp.ValueOrZero(); got != expireMs/1000 {
		t.Errorf("ExpireTimestamp = %d, want %d", got, expireMs/1000)
	}
	unlock()
}

// The merge path: an existing lure record without verified expiry gains it
// from a later GMO; a repeat identical GMO changes nothing.
func TestUpdateFromMapMergeAddsVerifiedExpiryOnce(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910102)

	// First sighting carries no expiry -> unverified record.
	noExpiry := testRawMapPokemon(encId, "lure-fort-910102", 51.5, -0.12, 0)
	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	if !pokemon.updateFromMap(context.Background(), db.DbDetails{}, noExpiry, nil, "tester") {
		t.Fatalf("first updateFromMap = false, want true")
	}
	if pokemon.ExpireTimestampVerified {
		t.Fatalf("ExpireTimestampVerified = true after no-expiry GMO, want false")
	}

	// Second sighting supplies the despawn time.
	expireMs := time.Now().UnixMilli() + 90_000
	withExpiry := testRawMapPokemon(encId, "lure-fort-910102", 51.5, -0.12, expireMs)
	if !pokemon.updateFromMap(context.Background(), db.DbDetails{}, withExpiry, nil, "tester") {
		t.Errorf("merge updateFromMap = false, want true (expiry contributed)")
	}
	if !pokemon.ExpireTimestampVerified || pokemon.ExpireTimestamp.ValueOrZero() != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true",
			pokemon.ExpireTimestamp.ValueOrZero(), pokemon.ExpireTimestampVerified, expireMs/1000)
	}

	// Identical replay: nothing left to contribute.
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, withExpiry, nil, "tester") {
		t.Errorf("no-change replay updateFromMap = true, want false")
	}
	unlock()
}

// Non-lure records must be left untouched (old code was a no-op for every
// non-new record; the merge must not widen that).
func TestUpdateFromMapLeavesNonLureRecordsAlone(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910103)
	pokemon, unlock, err := getOrCreatePokemonRecord(context.Background(), db.DbDetails{}, encId, "test")
	if err != nil {
		t.Fatalf("getOrCreatePokemonRecord: %v", err)
	}
	pokemon.SetSeenType(null.StringFrom(SeenType_Wild))
	pokemon.newRecord = false

	raw := testRawMapPokemon(encId, "lure-fort-910103", 51.5, -0.12, time.Now().UnixMilli()+90_000)
	if pokemon.updateFromMap(context.Background(), db.DbDetails{}, raw, nil, "tester") {
		t.Errorf("updateFromMap on wild record = true, want false")
	}
	if pokemon.PokestopId.Valid {
		t.Errorf("PokestopId set on wild record, want untouched")
	}
	unlock()
}

// Batch-level #390 regression: full pipeline with an unknown fort — the
// record is placed and a webhook fires with real coordinates.
func TestUpdatePokemonBatchPlacesLureWithUnknownFort(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910104)
	expireMs := time.Now().UnixMilli() + 120_000
	raw := testRawMapPokemon(encId, "lure-fort-910104", 48.8584, 2.2945, expireMs)

	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d not in cache after batch", encId)
	}
	if pokemon.Lat != 48.8584 || pokemon.Lon != 2.2945 {
		t.Errorf("Lat/Lon = %v/%v, want 48.8584/2.2945", pokemon.Lat, pokemon.Lon)
	}
	if pokemon.isNewRecord() {
		t.Errorf("record still new after batch save, want committed")
	}
	unlock()

	hooks := sink.drain()
	if len(hooks) == 0 {
		t.Fatalf("no webhook emitted for placed lure pokemon")
	}
	hook, ok := hooks[0].message.(PokemonWebhook)
	if !ok {
		t.Fatalf("webhook message type %T, want PokemonWebhook", hooks[0].message)
	}
	if hook.Latitude != 48.8584 || hook.Longitude != 2.2945 {
		t.Errorf("webhook coords = %v/%v, want 48.8584/2.2945", hook.Latitude, hook.Longitude)
	}
	if hook.DisappearTime != expireMs/1000 {
		t.Errorf("webhook DisappearTime = %d, want %d", hook.DisappearTime, expireMs/1000)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./decoder/ -run 'TestUpdateFromMap|TestUpdatePokemonBatchPlacesLure' -v`
Expected: compile FAILURE — `unknown field FortId in struct literal` and `too many arguments` / type mismatch on `updateFromMap` (the new struct fields and signature don't exist yet). A compile failure of the new tests is this cycle's "red".

- [ ] **Step 3: Add the fort fields to `RawMapPokemonData`**

In `decoder/main.go`, replace lines 48-52:

```go
type RawMapPokemonData struct {
	Cell      uint64
	Data      *pogo.MapPokemonProto
	Timestamp int64
	FortId    string
	Lat       float64
	Lon       float64
}
```

- [ ] **Step 4: Populate them at GMO extraction**

In `decode.go`, replace the `ActivePokemon` block (currently lines 518-520):

```go
			if fort.ActivePokemon != nil {
				newMapPokemon = append(newMapPokemon, decoder.RawMapPokemonData{
					Cell:      mapCell.S2CellId,
					Data:      fort.ActivePokemon,
					Timestamp: mapCell.AsOfTimeMs,
					FortId:    fort.FortId,
					Lat:       fort.Latitude,
					Lon:       fort.Longitude,
				})
			}
```

(`MapPokemonProto` has its own `Latitude`/`Longitude` fields; do NOT use them — the original implementation bypassed them for a pokestop lookup, implying they are unpopulated in live lure traffic. The enclosing fort entry's coordinates are definitionally populated.)

- [ ] **Step 5: Rewrite `updateFromMap`**

In `decoder/pokemon_decode.go`, replace the whole function (lines 180-226):

```go
// updateFromMap applies a GMO lure sighting (fort.ActivePokemon) to this
// pokemon. The fort's identity and coordinates are captured at GMO
// extraction (RawMapPokemonData), so placement never depends on the
// pokestop cache. Returns true when the record changed and needs saving.
func (pokemon *Pokemon) updateFromMap(ctx context.Context, db db.DbDetails, mapPokemon RawMapPokemonData, weather map[int64]pogo.GameplayWeatherProto_WeatherCondition, username string) bool {
	if pokemon.isNewRecord() {
		pokemon.SetIsEvent(0)
		pokemon.SetPokestopId(null.StringFrom(mapPokemon.FortId))
		pokemon.SetLat(mapPokemon.Lat)
		pokemon.SetLon(mapPokemon.Lon)
		pokemon.SetSeenType(null.StringFrom(SeenType_LureWild))

		if mapPokemon.Data.PokemonDisplay != nil {
			pokemon.setPokemonDisplay(int16(mapPokemon.Data.PokedexTypeId), mapPokemon.Data.PokemonDisplay)
			pokemon.recomputeCpIfNeeded(ctx, db, weather)
			// The mapPokemon and nearbyPokemon GMOs don't contain actual shininess.
			// shiny = mapPokemon.pokemonDisplay.shiny
		} else {
			log.Warnf("[POKEMON] MapPokemonProto missing PokemonDisplay for %d", pokemon.Id)
		}
		pokemon.SetUsername(null.StringFrom(username))

		if mapPokemon.Data.ExpirationTimeMs > 0 {
			pokemon.SetExpireTimestamp(null.IntFrom(mapPokemon.Data.ExpirationTimeMs / 1000))
			pokemon.SetExpireTimestampVerified(true)
			// if we have cached an encounter for this pokemon, update the TTL.
			encounterCache.UpdateTTL(uint64(pokemon.Id), pokemon.encounterStatsDuration(mapPokemon.Timestamp/1000))
		} else {
			pokemon.SetExpireTimestampVerified(false)
		}
		pokemon.SetCellId(null.IntFrom(int64(mapPokemon.Cell)))
		return true
	}

	// Existing record: the GMO contributes only what it alone knows — the
	// verified despawn time. Never touch encounter data and never downgrade
	// lure_encounter to lure_wild.
	switch pokemon.SeenType.ValueOrZero() {
	case SeenType_LureWild, SeenType_LureEncounter:
	default:
		return false
	}

	changed := false
	if mapPokemon.Data.ExpirationTimeMs > 0 && !pokemon.ExpireTimestampVerified {
		pokemon.SetExpireTimestamp(null.IntFrom(mapPokemon.Data.ExpirationTimeMs / 1000))
		pokemon.SetExpireTimestampVerified(true)
		encounterCache.UpdateTTL(uint64(pokemon.Id), pokemon.encounterStatsDuration(mapPokemon.Timestamp/1000))
		changed = true
	}
	if !pokemon.CellId.Valid {
		pokemon.SetCellId(null.IntFrom(int64(mapPokemon.Cell)))
		changed = true
	}
	if !pokemon.Username.Valid {
		pokemon.SetUsername(null.StringFrom(username))
		changed = true
	}
	return changed
}
```

Notes: the old `pokemon.Id = Uint64Str(mapPokemon.EncounterId)` line is gone (`getOrCreatePokemonRecord` already sets `Id` at creation), and the old `getPokestopRecordReadOnly` call is gone entirely.

- [ ] **Step 6: Make the map loop save conditionally and drop the disk-cache apply**

In `decoder/gmo_decode.go`, replace the map-pokemon loop (lines 179-197):

```go
	for _, mapPokemon := range mapPokemonList {
		pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, mapPokemon.Data.EncounterId, "UpdatePokemonBatch.map")
		if err != nil {
			log.Printf("getOrCreatePokemonRecord: %s", err)
			continue
		}

		if pokemon.updateFromMap(ctx, db, mapPokemon, weatherLookup, username) {
			savePokemonRecordAsAtTime(ctx, db, pokemon, false, true, true, mapPokemon.Timestamp/1000)
		}
		unlock()
	}
```

(The `diskEncounterCache.Get`/`Delete`/apply block is deliberately dropped here — Task 3 replaces the stash mechanism with direct request processing, and Task 4 deletes the cache itself.)

- [ ] **Step 7: Run the new tests**

Run: `go test ./decoder/ -run 'TestUpdateFromMap|TestUpdatePokemonBatchPlacesLure' -v`
Expected: all 4 PASS.

- [ ] **Step 8: Run the full decoder suite and build**

Run: `go build ./... && go test ./decoder/ -count=1`
Expected: PASS (no other decoder test touches `updateFromMap`).

- [ ] **Step 9: Commit**

```bash
git add decoder/main.go decode.go decoder/pokemon_decode.go decoder/gmo_decode.go decoder/pokemon_lure_test.go
git commit -m "fix: place lure pokemon from same-GMO fort data (#390)

A MapPokemonProto only ever arrives as fort.ActivePokemon inside its
fort's own GMO entry, so capture the fort id and coordinates at
extraction instead of re-deriving them from a pokestop lookup that can
miss (and whose failure used to commit an unplaced record at 0,0 that
no later GMO could repair). updateFromMap becomes an order-tolerant
merge: new records are placed from the captured fields; existing lure
records only receive GMO-owned facts (verified despawn time).

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Process the DiskEncounter request proto

**Files:**
- Modify: `decoder/pokemon_decode.go:296-302` (add constant near the seen-type constants)
- Modify: `decoder/pokemon_process.go:39-69` (`UpdatePokemonRecordWithDiskEncounterProto`)
- Modify: `decode.go:61-63` (dispatch) and `decode.go:397-414` (`decodeDiskEncounter`)
- Create: `decode_disk_encounter_test.go` (package `main`)
- Modify: `decoder/pokemon_lure_test.go` (add tests)

**Interfaces:**
- Consumes: `lureTestSetup` / `testWebhookSink` / `testRawMapPokemon` from Task 2; `updatePokemonFromDiskEncounterProto` (existing, unchanged); `getOrCreatePokemonRecord`; `collectApiPokemonResults(keys []uint64, caller string) []ApiPokemonResult` (`decoder/api_pokemon_response.go:189`).
- Produces:
  - `const lureSpawnLifetimeSeconds = 180` (decoder package)
  - `func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, request *pogo.DiskEncounterProto, encounter *pogo.DiskEncounterOutProto, username string) string`
  - `func decodeDiskEncounter(ctx context.Context, request []byte, sDec []byte, username string) string`

- [ ] **Step 1: Write the failing decoder tests**

Append to `decoder/pokemon_lure_test.go`:

```go
func testDiskEncounterProtos(encId uint64, fortId string, lat, lon float64) (*pogo.DiskEncounterProto, *pogo.DiskEncounterOutProto) {
	request := &pogo.DiskEncounterProto{
		EncounterId:   int64(encId),
		FortId:        fortId,
		GymLatDegrees: lat,
		GymLngDegrees: lon,
	}
	response := &pogo.DiskEncounterOutProto{
		Result: pogo.DiskEncounterOutProto_SUCCESS,
		Pokemon: &pogo.PokemonProto{
			PokemonId:         pogo.HoloPokemonId(25),
			Cp:                500,
			CpMultiplier:      0.5,
			IndividualAttack:  15,
			IndividualDefense: 14,
			IndividualStamina: 13,
			PokemonDisplay:    &pogo.PokemonDisplayProto{DisplayId: int64(encId)},
		},
	}
	return request, response
}

// #283: an encounter with no prior GMO creates a fully placed record from
// the request proto — real coords, estimated expiry, IVs, webhook.
func TestDiskEncounterFirstCreatesPlacedRecord(t *testing.T) {
	sink := lureTestSetup(t)
	const encId = uint64(910105)
	request, response := testDiskEncounterProtos(encId, "lure-fort-910105", 40.7580, -73.9855)

	before := time.Now().Unix()
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")
	after := time.Now().Unix()

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d not in cache after disk encounter", encId)
	}
	if got := pokemon.PokestopId.ValueOrZero(); got != "lure-fort-910105" {
		t.Errorf("PokestopId = %q, want lure-fort-910105", got)
	}
	if pokemon.Lat != 40.7580 || pokemon.Lon != -73.9855 {
		t.Errorf("Lat/Lon = %v/%v, want request coords 40.7580/-73.9855", pokemon.Lat, pokemon.Lon)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureEncounter)
	}
	if pokemon.ExpireTimestampVerified {
		t.Errorf("ExpireTimestampVerified = true, want false (estimate)")
	}
	exp := pokemon.ExpireTimestamp.ValueOrZero()
	if exp < before+lureSpawnLifetimeSeconds || exp > after+lureSpawnLifetimeSeconds {
		t.Errorf("ExpireTimestamp = %d, want now+%ds (in [%d, %d])",
			exp, lureSpawnLifetimeSeconds, before+lureSpawnLifetimeSeconds, after+lureSpawnLifetimeSeconds)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15", got)
	}
	unlock()

	hooks := sink.drain()
	if len(hooks) == 0 {
		t.Fatalf("no webhook emitted for encounter-created lure pokemon")
	}
	hook, ok := hooks[0].message.(PokemonWebhook)
	if !ok {
		t.Fatalf("webhook message type %T, want PokemonWebhook", hooks[0].message)
	}
	if hook.Latitude != 40.7580 || hook.Longitude != -73.9855 {
		t.Errorf("webhook coords = %v/%v, want 40.7580/-73.9855 (the #390 symptom was 0/0)", hook.Latitude, hook.Longitude)
	}
	if hook.DisappearTime <= 0 {
		t.Errorf("webhook DisappearTime = %d, want > 0 (the #390 symptom was 0)", hook.DisappearTime)
	}
}

// A GMO arriving after the encounter tightens the estimate to a verified
// despawn without downgrading the seen type or touching IVs.
func TestGmoAfterDiskEncounterContributesVerifiedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910106)
	request, response := testDiskEncounterProtos(encId, "lure-fort-910106", 40.0, -73.0)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	expireMs := time.Now().UnixMilli() + 100_000
	raw := testRawMapPokemon(encId, "lure-fort-910106", 40.0, -73.0, expireMs)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d missing", encId)
	}
	if !pokemon.ExpireTimestampVerified || pokemon.ExpireTimestamp.ValueOrZero() != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true",
			pokemon.ExpireTimestamp.ValueOrZero(), pokemon.ExpireTimestampVerified, expireMs/1000)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q (must not downgrade)", got, SeenType_LureEncounter)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15 (encounter data must survive the GMO merge)", got)
	}
	unlock()
}

// The classic order still works with no cache hop: GMO creates lure_wild,
// the encounter upgrades it in place.
func TestDiskEncounterAfterGmoUpgradesRecord(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910107)
	expireMs := time.Now().UnixMilli() + 110_000
	raw := testRawMapPokemon(encId, "lure-fort-910107", 35.6595, 139.7005, expireMs)
	UpdatePokemonBatch(context.Background(), db.DbDetails{}, ScanParameters{}, nil, nil,
		[]RawMapPokemonData{raw}, nil, "tester")

	request, response := testDiskEncounterProtos(encId, "lure-fort-910107", 35.6595, 139.7005)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	pokemon, unlock, _ := peekPokemonRecordReadOnly(encId, "test")
	if pokemon == nil {
		t.Fatalf("pokemon %d missing", encId)
	}
	if got := pokemon.SeenType.ValueOrZero(); got != SeenType_LureEncounter {
		t.Errorf("SeenType = %q, want %q", got, SeenType_LureEncounter)
	}
	if got := pokemon.AtkIv.ValueOrZero(); got != 15 {
		t.Errorf("AtkIv = %d, want 15", got)
	}
	// GMO-verified expiry must survive: the estimate is only for new records.
	if !pokemon.ExpireTimestampVerified || pokemon.ExpireTimestamp.ValueOrZero() != expireMs/1000 {
		t.Errorf("expiry = %d verified=%v, want %d verified=true (estimate must not overwrite)",
			pokemon.ExpireTimestamp.ValueOrZero(), pokemon.ExpireTimestampVerified, expireMs/1000)
	}
	unlock()
}

// The issue's visibility symptom: the v2/v3 result collector must include a
// record whose only expiry is the unverified estimate.
func TestCollectApiPokemonResultsIncludesEstimatedExpiry(t *testing.T) {
	lureTestSetup(t)
	const encId = uint64(910108)
	request, response := testDiskEncounterProtos(encId, "lure-fort-910108", 40.1, -73.1)
	UpdatePokemonRecordWithDiskEncounterProto(context.Background(), db.DbDetails{}, request, response, "tester")

	results := collectApiPokemonResults([]uint64{encId}, "test")
	if len(results) != 1 {
		t.Fatalf("collectApiPokemonResults returned %d results, want 1", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./decoder/ -run 'TestDiskEncounter|TestGmoAfterDisk|TestCollectApiPokemonResultsIncludes' -v`
Expected: compile FAILURE — `too many arguments in call to UpdatePokemonRecordWithDiskEncounterProto` (the request parameter doesn't exist yet).

- [ ] **Step 3: Add the lifetime constant**

In `decoder/pokemon_decode.go`, directly after the seen-type constants block (after line 302, `SeenType_TappableLureEncounter`):

```go
// A lure spits out a new pokemon every 3 minutes, and each lasts 3 minutes.
// Worst-case remaining life when a lure pokemon is first seen via a disk
// encounter, before any GMO has supplied the real despawn time.
const lureSpawnLifetimeSeconds = 180
```

- [ ] **Step 4: Rewrite `UpdatePokemonRecordWithDiskEncounterProto`**

In `decoder/pokemon_process.go`, replace the function (lines 39-69):

```go
func UpdatePokemonRecordWithDiskEncounterProto(ctx context.Context, db db.DbDetails, request *pogo.DiskEncounterProto, encounter *pogo.DiskEncounterOutProto, username string) string {
	if encounter.Pokemon == nil {
		return "No encounter"
	}

	encounterId := uint64(request.EncounterId)
	if encounterId == 0 {
		return "Disk encounter request without encounter id"
	}
	if displayId := uint64(encounter.Pokemon.GetPokemonDisplay().GetDisplayId()); displayId != 0 && displayId != encounterId {
		log.Warnf("[POKEMON] Disk encounter id mismatch: request %d display %d", encounterId, displayId)
	}

	pokemon, unlock, err := getOrCreatePokemonRecord(ctx, db, encounterId, "UpdatePokemonFromDiskEncounter")
	if err != nil {
		log.Errorf("Error pokemon [%d]: %s", encounterId, err)
		return fmt.Sprintf("Error finding pokemon %s", err)
	}
	defer unlock()

	if pokemon.isNewRecord() {
		// Placement happens exactly once, at record creation, by whichever
		// proto arrives first — here from the disk encounter request. A
		// later GMO contributes the verified despawn time.
		pokemon.SetPokestopId(null.StringFrom(request.FortId))
		pokemon.SetLat(request.GymLatDegrees)
		pokemon.SetLon(request.GymLngDegrees)
		pokemon.SetExpireTimestamp(null.IntFrom(time.Now().Unix() + lureSpawnLifetimeSeconds))
		pokemon.SetExpireTimestampVerified(false)
	}

	pokemon.updatePokemonFromDiskEncounterProto(ctx, db, encounter, username)
	savePokemonRecordAsAtTime(ctx, db, pokemon, true, true, true, time.Now().Unix())
	// updateEncounterStats() should only be called for encounters, and called
	// even if we have the pokemon record already.
	enqueuePokemonStatsEvent(pokemonStatsEvent{snap: pokemon.statsSnapshot(), encounter: true})

	return fmt.Sprintf("%d Disk Pokemon %d CP%d", encounterId, pokemon.PokemonId, encounter.Pokemon.Cp)
}
```

Update imports in `decoder/pokemon_process.go`: remove `"golbat/ottercache"` (the deleted stash branch was its only use in this file), add `"github.com/guregu/null/v6"`.

- [ ] **Step 5: Rewrite `decodeDiskEncounter` and its dispatch**

In `decode.go`, change the dispatch case (lines 61-63):

```go
	case pogo.Method_METHOD_DISK_ENCOUNTER:
		result = decodeDiskEncounter(ctx, protoData.Request, protoData.Data, protoData.Account)
		processed = true
```

Replace `decodeDiskEncounter` (lines 397-414):

```go
func decodeDiskEncounter(ctx context.Context, request []byte, sDec []byte, username string) string {
	if len(request) == 0 {
		// The request carries the encounter id, fort id and fort location —
		// without it the encounter cannot be placed. len covers both nil
		// (gRPC, no payload) and empty (HTTP path base64-decodes "" to a
		// non-nil empty slice).
		statsCollector.IncDecodeDiskEncounter("error", "request_missing")
		return "DiskEncounter without request proto - ignored"
	}
	decodedRequest := &pogo.DiskEncounterProto{}
	if err := proto.Unmarshal(request, decodedRequest); err != nil {
		log.Errorf("Failed to parse DiskEncounterProto %s", err)
		statsCollector.IncDecodeDiskEncounter("error", "request_parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	decodedEncounterInfo := &pogo.DiskEncounterOutProto{}
	if err := proto.Unmarshal(sDec, decodedEncounterInfo); err != nil {
		log.Errorf("Failed to parse %s", err)
		statsCollector.IncDecodeDiskEncounter("error", "parse")
		return fmt.Sprintf("Failed to parse %s", err)
	}

	if decodedEncounterInfo.Result != pogo.DiskEncounterOutProto_SUCCESS {
		statsCollector.IncDecodeDiskEncounter("error", "non_success")
		res := fmt.Sprintf(`DiskEncounterOutProto: Ignored non-success value %d:%s`, decodedEncounterInfo.Result,
			pogo.DiskEncounterOutProto_Result_name[int32(decodedEncounterInfo.Result)])
		return res
	}

	statsCollector.IncDecodeDiskEncounter("ok", "")
	return decoder.UpdatePokemonRecordWithDiskEncounterProto(ctx, dbDetails, decodedRequest, decodedEncounterInfo, username)
}
```

- [ ] **Step 6: Write the main-package test for the request-missing path**

Create `decode_disk_encounter_test.go`:

```go
package main

import (
	"context"
	"strings"
	"testing"

	"golbat/stats_collector"
)

// Request payloads are required for disk encounters (they carry the
// encounter id, fort id and fort location); without one the proto is
// counted and skipped.
func TestDecodeDiskEncounterRequiresRequest(t *testing.T) {
	statsCollector = stats_collector.NewNoopStatsCollector()
	res := decodeDiskEncounter(context.Background(), nil, []byte{}, "tester")
	if !strings.Contains(res, "without request") {
		t.Errorf("decodeDiskEncounter without request = %q, want ignored-without-request message", res)
	}
}
```

- [ ] **Step 7: Run all new tests**

Run: `go test ./decoder/ -run 'TestDiskEncounter|TestGmoAfterDisk|TestCollectApiPokemonResultsIncludes' -v && go test . -run TestDecodeDiskEncounterRequiresRequest -v`
Expected: all PASS.

- [ ] **Step 8: Run the full suite**

Run: `go build ./... && go test ./decoder/ ./... -count=1 2>&1 | tail -20`
Expected: PASS everywhere.

- [ ] **Step 9: Commit**

```bash
git add decoder/pokemon_decode.go decoder/pokemon_process.go decode.go decode_disk_encounter_test.go decoder/pokemon_lure_test.go
git commit -m "feat: place lure pokemon from the DiskEncounter request proto (#283)

The request carries the encounter id, fort id and fort coordinates, so
a disk encounter no longer needs a prior GMO: it creates a fully placed
lure_encounter record with a 180s unverified expiry estimate (lure
spawns live 3 minutes), which a later GMO tightens to the verified
despawn. The request is now essential, matching GetStationDetails; the
DisplayId encounter-id stand-in is gone.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Delete `diskEncounterCache`

**Files:**
- Modify: `decoder/main.go:72` (declaration), `decoder/main.go:215-219` (construction in `InitDataCache`)

**Interfaces:**
- Consumes: nothing new. Tasks 2 and 3 already removed the only readers/writers (`gmo_decode.go` apply block, `pokemon_process.go` stash branch).
- Produces: a codebase with zero `diskEncounterCache` references.

- [ ] **Step 1: Delete the declaration**

In `decoder/main.go`, remove line 72:

```go
var diskEncounterCache *ottercache.OtterCache[uint64, *pogo.DiskEncounterOutProto]
```

- [ ] **Step 2: Delete the construction**

In `decoder/main.go`, remove the block (lines 215-219):

```go
	diskEncounterCache = ottercache.NewOtterCache(ottercache.OtterCacheConfig[uint64, *pogo.DiskEncounterOutProto]{
		Name:       "disk_encounter",
		DefaultTTL: 10 * time.Minute,
		TouchOnHit: false,
	})
```

- [ ] **Step 3: Verify no references remain**

Run: `grep -rn diskEncounterCache --include='*.go' . ; go build ./...`
Expected: grep finds nothing; build succeeds. (If the build reports `pogo` or `ottercache` as unused in any file, the earlier tasks missed an import cleanup — fix it; both packages have many other uses in `decoder/main.go`, so no import change is expected there.)

- [ ] **Step 4: Run the decoder suite**

Run: `go test ./decoder/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add decoder/main.go
git commit -m "refactor: delete diskEncounterCache

Disk encounter responses are processed directly against the record
created from their request proto; nothing waits for a GMO anymore.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Full verification

**Files:** none new — verification and any fallout fixes only.

**Interfaces:**
- Consumes: the complete branch.
- Produces: a branch ready for review/PR.

- [ ] **Step 1: Full build, vet, tests**

Run: `go build ./... && go vet ./... && go test ./... -count=1 2>&1 | tail -30`
Expected: all PASS. Fix any fallout in place (e.g. a test elsewhere constructing `RawMapPokemonData` without the new fields still compiles — fields are optional in struct literals — but check for semantic dependence).

- [ ] **Step 2: Lint**

Run: `golangci-lint run 2>&1 | head -30`
Expected: clean. Notably `getPokemonRecordForUpdate` must NOT be flagged: the `.golangci.yml` unused-exclusion for `(peek|get)*Record(ReadOnly|ForUpdate)` covers it. If any other new-code finding appears, fix it.

- [ ] **Step 3: Spec conformance sweep**

Re-read `docs/superpowers/specs/2026-07-29-lure-request-first-design.md` section by section and confirm each maps to landed code: model table (three contributors), no pokestop lookups in any lure path (`grep -n getPokestopRecordReadOnly decoder/pokemon_decode.go decoder/pokemon_process.go` — must show only `updateFromNearby`'s use in pokemon_decode.go and nothing in pokemon_process.go), placement-once rule, 180s constant, cache deletion, six test scenarios present.

- [ ] **Step 4: Final commit if fallout fixes were needed**

```bash
git status --short
# if dirty:
git add -A && git commit -m "test: verification fallout fixes for lure request-first branch

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```
