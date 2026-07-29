# Maintained Fort Availability Index Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-request full-fort `fortLookupCache.Range` in the availability endpoints with five maintained max-expiry maps, updated incrementally on fort save.

**Architecture:** Each dynamic aggregate (pokestop lure/showcase/invasion, gym raid, station battle) gets an `xsync.Map[optionKey, maxExpiry]`. The fort update functions `observe` each active option (atomic keep-larger); the availability builders `Range` the small map, emit `exp > now`, and prune-on-read the rest. Quests keep their existing `reconcile` aggregate untouched. No full-fort scan remains anywhere in the availability path.

**Tech Stack:** Go, `github.com/puzpuzpuz/xsync/v4` v4.5.0 (`Map.Compute` / strong `Map.Range`), huma. Tests are Go `_test.go` run with `go test ./decoder/`.

## Global Constraints

- **Golbat only.** ReactMap is unaffected (response shapes are identical except the dropped `count`, which no consumer reads). Do not touch any ReactMap file.
- **Quests are out of scope and unchanged.** `questConditionCount`, `reconcileFortQuestConditions`, `GetAvailableQuestConditions`, `questFortKeys` stay exactly as they are. Quest availability continues to come from `GetAvailableQuestConditions()`.
- **Prune-on-read is conditional, never blind.** Deleting an expired key must go through `Compute` with a delete-if-still-`<= now` predicate (`pruneExpired`), so it cannot race an `observe` that just refreshed the key. A blind `m.Delete(k)` is a defect.
- **Use strong `Map.Range`, not `RangeRelaxed`.** `RangeRelaxed` may visit a key twice → a duplicate availability entry.
- **`observe` ignores already-expired observations** (`expiry <= now` → no-op) so dead keys never enter a map.
- **Single writer-domain per map.** `lureExpiry`/`showcaseExpiry` are written only from the pokestop lock domain (`updatePokestopLookup`), `invasionExpiry` only from the incident domain (`updatePokestopIncidentLookup`), `raidExpiry` only from the gym domain (`updateGymLookup`), `battleExpiry` only from the station domain (`updateStationLookupWithBattles`). Do not observe an aggregate from a foreign domain.
- **Build + test gate each task:** `go build -tags go_json ./...` and `go test ./decoder/ -count=1` both green before commit.
- **Empty aggregates marshal as `[]`, not `null`** — initialize result slices to `[]ApiXAvailable{}`.
- Commit subjects: Conventional Commits, imperative, lowercase, ≤100 chars; end the body with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

## File Structure

- **Create `decoder/fort_availability.go`** — the maintained index: the five key types, the five maps, `initFortAvailability`, the generic `observeExpiry` + `pruneExpired`, and per-aggregate `observe*` / `read*` functions. One responsibility: the max-expiry index.
- **Create `decoder/fort_availability_test.go`** — unit tests for the primitive, observe, and read functions.
- **Modify `decoder/fortRtree.go`** — call `initFortAvailability()` from `initFortRtree`; add `observe*` calls to `updatePokestopLookup`, `updatePokestopIncidentLookup`, `updateGymLookup`, `updateStationLookupWithBattles`.
- **Modify `decoder/api_gym_available.go`** — `GetAvailableGyms` reads `readRaids`; delete `gymAvailAcc`; drop `ApiGymRaidAvailable.Count`.
- **Modify `decoder/api_station_available.go`** — `GetAvailableStations` reads `readBattles`; delete `stationAvailAcc`; drop `ApiStationBattleAvailable.Count`.
- **Modify `decoder/api_pokestop_available.go`** — `GetAvailablePokestops` reads `readLures`/`readShowcases`/`readInvasions` + `GetAvailableQuestConditions`; delete `pokestopAvailAcc`, `verifyQuestAggregate`, `questRewardKey`; drop `Count` on lure/showcase/invasion structs.
- **Modify `decoder/api_fort_available.go`** — `GetAvailableForts` assembles from the read functions; no `fortLookupCache.Range`.
- **Modify the availability `_test.go` files** — seed via `observe*` (or the update hooks) instead of `fortLookupCache`.

---

## Task 1: Maintained-index primitive + gym raids (proves the pattern)

**Files:**
- Create: `decoder/fort_availability.go`
- Create: `decoder/fort_availability_test.go`
- Modify: `decoder/fortRtree.go` (`initFortRtree`, `updateGymLookup`)
- Modify: `decoder/api_gym_available.go`
- Modify: `decoder/api_gym_available_test.go`

**Interfaces:**
- Produces (used by Tasks 2-4):
  - `func observeExpiry[K comparable](m *xsync.Map[K, int64], key K, expiry, now int64)`
  - `func pruneExpired[K comparable](m *xsync.Map[K, int64], key K, now int64)`
  - `func initFortAvailability()`
  - `raidExpiry *xsync.Map[raidKey, int64]`, `type raidKey struct { RaidLevel int8; PokemonId, Form int16 }`
  - `func observeRaid(fl *FortLookup, now int64)`, `func readRaids(now int64) []ApiGymRaidAvailable`

- [ ] **Step 1: Write the failing test** — `decoder/fort_availability_test.go`

```go
package decoder

import "testing"

func TestObserveExpiryAndReadRaids(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// active raid boss + active egg
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidPokemonForm: 0, RaidEndTimestamp: 2000}, now)
	observeRaid(&FortLookup{RaidLevel: 3, RaidPokemonId: 0, RaidPokemonForm: 0, RaidEndTimestamp: 2000}, now)
	// no raid (level 0) -> ignored
	observeRaid(&FortLookup{RaidLevel: 0, RaidEndTimestamp: 2000}, now)
	// already-expired -> ignored
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: 500}, now)

	got := readRaids(now)
	if len(got) != 2 {
		t.Fatalf("want 2 raid options, got %d: %+v", len(got), got)
	}
	for _, r := range got {
		if r.PokemonId == 999 {
			t.Fatal("expired raid must not appear")
		}
	}

	// keep-larger: re-observe boss 150 with a LATER expiry, then read after the
	// first expiry has passed — it must still be present.
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: 3000}, now)
	if len(readRaids(2500)) == 0 {
		t.Fatal("refreshed raid should survive past its first expiry")
	}

	// prune-on-read: once fully expired, it drops out.
	if len(readRaids(4000)) != 0 {
		t.Fatal("all raids expired -> empty")
	}
	// and empty read returns [] not nil
	if readRaids(4000) == nil {
		t.Fatal("read must return non-nil empty slice")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestObserveExpiryAndReadRaids -count=1`
Expected: FAIL — `undefined: initFortAvailability` / `observeRaid` / `readRaids`.

- [ ] **Step 3: Create `decoder/fort_availability.go`**

```go
package decoder

import "github.com/puzpuzpuz/xsync/v4"

// Maintained max-expiry availability index. Each map holds, per distinct filter
// option, the latest expiry timestamp seen on any resident fort. Availability
// reads the maps instead of scanning fortLookupCache; see
// docs/superpowers/specs/2026-07-17-maintained-fort-availability-design.md.
//
// Quests are NOT here — they can be retracted mid-life (geofence clear, event
// swap) which a monotonic max-expiry cannot express, so they keep the reconcile
// aggregate (questConditionCount).

type showcaseKey struct {
	PokemonId int16
	Form      int16
	TypeId    int8
}

type invasionKey struct {
	Character      int16
	DisplayType    int16
	Confirmed      bool
	Slot1PokemonId int16
	Slot1Form      int16
}

type raidKey struct {
	RaidLevel int8
	PokemonId int16
	Form      int16
}

type battleKey struct {
	BattleLevel int8
	PokemonId   int16
	Form        int16
}

var (
	lureExpiry     *xsync.Map[int16, int64]
	showcaseExpiry *xsync.Map[showcaseKey, int64]
	invasionExpiry *xsync.Map[invasionKey, int64]
	raidExpiry     *xsync.Map[raidKey, int64]
	battleExpiry   *xsync.Map[battleKey, int64]
)

func initFortAvailability() {
	lureExpiry = xsync.NewMap[int16, int64]()
	showcaseExpiry = xsync.NewMap[showcaseKey, int64]()
	invasionExpiry = xsync.NewMap[invasionKey, int64]()
	raidExpiry = xsync.NewMap[raidKey, int64]()
	battleExpiry = xsync.NewMap[battleKey, int64]()
}

// observeExpiry records key as available until at least expiry, keeping the
// larger of any prior expiry (a still-active fort refreshes the lifetime).
// Already-expired observations are ignored so dead keys never enter the map.
func observeExpiry[K comparable](m *xsync.Map[K, int64], key K, expiry, now int64) {
	if expiry <= now {
		return
	}
	m.Compute(key, func(old int64, _ bool) (int64, xsync.ComputeOp) {
		if old >= expiry {
			return old, xsync.CancelOp
		}
		return expiry, xsync.UpdateOp
	})
}

// pruneExpired deletes key iff it is STILL expired. It must never be a blind
// Delete: that could race an observe that just refreshed the key and wrongly
// drop a live option. Compute re-checks under the key's lock.
func pruneExpired[K comparable](m *xsync.Map[K, int64], key K, now int64) {
	m.Compute(key, func(cur int64, loaded bool) (int64, xsync.ComputeOp) {
		if loaded && cur <= now {
			return 0, xsync.DeleteOp
		}
		return cur, xsync.CancelOp
	})
}

func observeRaid(fl *FortLookup, now int64) {
	if fl.RaidLevel > 0 {
		observeExpiry(raidExpiry, raidKey{fl.RaidLevel, fl.RaidPokemonId, fl.RaidPokemonForm}, fl.RaidEndTimestamp, now)
	}
}

// readRaids emits the distinct active raid options, pruning expired keys.
// Strong Range (not RangeRelaxed): each key visited at most once.
func readRaids(now int64) []ApiGymRaidAvailable {
	out := []ApiGymRaidAvailable{}
	raidExpiry.Range(func(k raidKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiGymRaidAvailable{RaidLevel: k.RaidLevel, PokemonId: k.PokemonId, Form: k.Form})
		} else {
			pruneExpired(raidExpiry, k, now)
		}
		return true
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/ -run TestObserveExpiryAndReadRaids -count=1`
Expected: PASS.

- [ ] **Step 5: Wire init + the gym hook** — `decoder/fortRtree.go`

In `initFortRtree`, add `initFortAvailability()` next to the existing `initQuestConditions()` call:

```go
	initQuestConditions()
	initFortAvailability()
```

Rewrite `updateGymLookup` to build the lookup as a value, store it, then observe (mirrors how updateStationLookupWithBattles builds a `lookup` var):

```go
func updateGymLookup(gym *Gym) {
	now := time.Now().Unix()
	fl := FortLookup{
		FortType:            GYM,
		Lat:                 gym.Lat,
		Lon:                 gym.Lon,
		IsArScanEligible:    gym.ArScanEligible.ValueOrZero() == 1,
		AvailableSlots:      int8(gym.AvailableSlots.ValueOrZero()),
		TeamId:              int8(gym.TeamId.ValueOrZero()),
		RaidEndTimestamp:    gym.RaidEndTimestamp.ValueOrZero(),
		RaidBattleTimestamp: gym.RaidBattleTimestamp.ValueOrZero(),
		RaidLevel:           int8(gym.RaidLevel.ValueOrZero()),
		RaidPokemonId:       int16(gym.RaidPokemonId.ValueOrZero()),
		RaidPokemonForm:     int16(gym.RaidPokemonForm.ValueOrZero()),
	}
	fortLookupCache.Store(gym.Id, fl)
	observeRaid(&fl, now)
}
```

Confirm `time` is already imported in `fortRtree.go` (it is — `updateStationLookup` uses `time.Now()`).

- [ ] **Step 6: Point `GetAvailableGyms` at the map + drop the accumulator and Count** — `decoder/api_gym_available.go`

Replace everything from the `ApiGymRaidAvailable` struct through `GetAvailableGyms` with:

```go
// ApiGymRaidAvailable is one distinct active raid option on resident gyms.
// PokemonId 0 means an egg (no boss yet). ReactMap derives its e/r/boss keys.
type ApiGymRaidAvailable struct {
	RaidLevel int8  `json:"raid_level" doc:"Raid level/tier"`
	PokemonId int16 `json:"pokemon_id" doc:"Raid boss pokemon id; 0 = egg (unhatched)"`
	Form      int16 `json:"form" doc:"Raid boss form id, else 0"`
}

// ApiAvailableGyms is the whole-instance gym filter snapshot. Only raids are
// dynamic — team/slot filter keys are generated statically by the consumer, so
// they are not aggregated here.
type ApiAvailableGyms struct {
	Raids []ApiGymRaidAvailable `json:"raids" doc:"Distinct active raid levels/bosses/eggs on resident gyms"`
}

// GetAvailableGyms reads the maintained raid index (no fort scan).
func GetAvailableGyms(now int64) *ApiAvailableGyms {
	res := &ApiAvailableGyms{Raids: readRaids(now)}
	log.Infof("available-gyms: %d raid options (maintained)", len(res.Raids))
	return res
}
```

Delete the now-unused `gymAvailAcc`, `newGymAvailAcc`, `ingest`, `result`. Remove the `"time"` import from `api_gym_available.go` if it is now unused (it is — no more `time.Now`/`time.Since`); keep `log`.

- [ ] **Step 7: Rewrite the gym availability test** — `decoder/api_gym_available_test.go`

Replace `TestGetAvailableGyms` (which seeded `fortLookupCache`) with one that seeds via the hook:

```go
func TestGetAvailableGyms(t *testing.T) {
	initFortAvailability()
	now := int64(1_000_000)

	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100}, now) // boss
	observeRaid(&FortLookup{RaidLevel: 3, RaidPokemonId: 0, RaidEndTimestamp: now + 100}, now)   // egg
	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 999, RaidEndTimestamp: now - 1}, now)   // expired -> ignored

	res := GetAvailableGyms(now)

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

- [ ] **Step 8: Build + full decoder test**

Run: `go build -tags go_json ./... && go test ./decoder/ -count=1`
Expected: build OK; all tests pass. (The combined test `TestGetAvailableForts` still passes here because `GetAvailableForts` is untouched until Task 4 — but note it now depends on `GetAvailableGyms` reading the map; if it seeds `fortLookupCache` it will show 0 raids. If `TestGetAvailableForts` fails on the gyms section, that is expected and is fixed in Task 4 — leave it; do NOT weaken it here. If it fails, note it in the commit and proceed.)

Actually, to avoid a red suite between tasks, update the gyms assertion in `TestGetAvailableForts` now only if it references `combined.Gyms.Raids` counts that the map won't have. Prefer: in Task 4 the combined test is rewritten to seed via observe. If Step 8's suite is red solely due to `TestGetAvailableForts`, temporarily skip that one test with `t.Skip("rebuilt in Task 4: combined reads maintained maps")` and remove the skip in Task 4.

- [ ] **Step 9: Commit**

```bash
git add decoder/fort_availability.go decoder/fort_availability_test.go decoder/fortRtree.go decoder/api_gym_available.go decoder/api_gym_available_test.go
git commit
```
Message: `feat(availability): maintained raid index + observe/prune primitive`

---

## Task 2: Station battles

**Files:**
- Modify: `decoder/fort_availability.go` (add `battleExpiry` observe/read)
- Modify: `decoder/fort_availability_test.go` (add battle test)
- Modify: `decoder/fortRtree.go` (`updateStationLookupWithBattles`)
- Modify: `decoder/api_station_available.go`
- Modify: `decoder/api_station_available_test.go`

**Interfaces:**
- Consumes (Task 1): `observeExpiry`, `pruneExpired`, `battleExpiry`, `type battleKey`, `initFortAvailability`.
- Produces: `func observeStationBattles(fl *FortLookup, now int64)`, `func readBattles(now int64) []ApiStationBattleAvailable`.

- [ ] **Step 1: Write the failing test** — append to `decoder/fort_availability_test.go`

```go
func TestObserveStationBattlesAndRead(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// station with two active battles (slice) — both distinct options
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 5, BattlePokemonId: 150, BattlePokemonForm: 0, BattleEndTimestamp: 2000},
		{BattleLevel: 3, BattlePokemonId: 0, BattlePokemonForm: 0, BattleEndTimestamp: 2000},
	}}, now)
	// level 0 -> ignored; expired -> ignored
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 0, BattleEndTimestamp: 2000},
		{BattleLevel: 5, BattlePokemonId: 999, BattleEndTimestamp: 500},
	}}, now)
	// no slice: fall back to the top-battle scalar projection
	observeStationBattles(&FortLookup{BattleLevel: 4, BattlePokemonId: 200, BattleEndTimestamp: 2000}, now)

	got := readBattles(now)
	if len(got) != 3 {
		t.Fatalf("want 3 battle options, got %d: %+v", len(got), got)
	}
	for _, b := range got {
		if b.PokemonId == 999 {
			t.Fatal("expired battle leaked")
		}
	}
	if len(readBattles(3000)) != 0 {
		t.Fatal("all battles expired -> empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestObserveStationBattlesAndRead -count=1`
Expected: FAIL — `undefined: observeStationBattles` / `readBattles`.

- [ ] **Step 3: Add battle observe/read** — `decoder/fort_availability.go`

```go
func observeStationBattles(fl *FortLookup, now int64) {
	obs := func(level int8, id, form int16, end int64) {
		if level == 0 {
			return
		}
		observeExpiry(battleExpiry, battleKey{level, id, form}, end, now)
	}
	if len(fl.StationBattles) == 0 {
		obs(fl.BattleLevel, fl.BattlePokemonId, fl.BattlePokemonForm, fl.BattleEndTimestamp)
		return
	}
	for _, b := range fl.StationBattles {
		obs(b.BattleLevel, b.BattlePokemonId, b.BattlePokemonForm, b.BattleEndTimestamp)
	}
}

func readBattles(now int64) []ApiStationBattleAvailable {
	out := []ApiStationBattleAvailable{}
	battleExpiry.Range(func(k battleKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiStationBattleAvailable{BattleLevel: k.BattleLevel, PokemonId: k.PokemonId, Form: k.Form})
		} else {
			pruneExpired(battleExpiry, k, now)
		}
		return true
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/ -run TestObserveStationBattlesAndRead -count=1`
Expected: PASS.

- [ ] **Step 5: Add the station hook** — `decoder/fortRtree.go`

In `updateStationLookupWithBattles`, after the `fortLookupCache.Store(station.Id, lookup)` line, add:

```go
	fortLookupCache.Store(station.Id, lookup)
	observeStationBattles(&lookup, time.Now().Unix())
```

(`applyTopStationBattleToFortLookup` has already populated `lookup`'s scalar top-battle fields, so the no-slice fallback in `observeStationBattles` sees them.)

- [ ] **Step 6: Point `GetAvailableStations` at the map + drop the accumulator and Count** — `decoder/api_station_available.go`

Replace from the `ApiStationBattleAvailable` struct through `GetAvailableStations` with:

```go
// ApiStationBattleAvailable is one distinct active (battle_level, pokemon, form)
// option on resident stations. ReactMap derives its <id>-<form> and j<level> keys.
type ApiStationBattleAvailable struct {
	BattleLevel int8  `json:"battle_level" doc:"Max battle level"`
	PokemonId   int16 `json:"pokemon_id" doc:"Battle pokemon id, else 0"`
	Form        int16 `json:"form" doc:"Battle pokemon form id, else 0"`
}

// ApiAvailableStations is the whole-instance station filter snapshot served by
// GET /api/station/available.
type ApiAvailableStations struct {
	Battles []ApiStationBattleAvailable `json:"battles" doc:"Distinct active battle level/pokemon options on resident stations"`
}

// GetAvailableStations reads the maintained battle index (no fort scan).
func GetAvailableStations(now int64) *ApiAvailableStations {
	res := &ApiAvailableStations{Battles: readBattles(now)}
	log.Infof("available-stations: %d battle options (maintained)", len(res.Battles))
	return res
}
```

Delete `stationAvailAcc`, `newStationAvailAcc`, `add`, `ingest`, `result`. Remove the now-unused `"time"` import; keep `log`.

- [ ] **Step 7: Rewrite the station availability test** — `decoder/api_station_available_test.go`

Replace the body that seeded `fortLookupCache` with observe-based seeding (mirror Task 1 Step 7): `initFortAvailability()`, `observeStationBattles(&FortLookup{...}, now)` for a live battle and an expired one, then assert `GetAvailableStations(now).Battles` contains only the live option. Keep the same level/pokemon values the old test used.

- [ ] **Step 8: Build + full decoder test**

Run: `go build -tags go_json ./... && go test ./decoder/ -count=1`
Expected: build OK; tests pass (the `TestGetAvailableForts` skip from Task 1 still in place).

- [ ] **Step 9: Commit**

```bash
git add decoder/fort_availability.go decoder/fort_availability_test.go decoder/fortRtree.go decoder/api_station_available.go decoder/api_station_available_test.go
git commit
```
Message: `feat(availability): maintained station battle index`

---

## Task 3: Pokestop lures, showcases, invasions

**Files:**
- Modify: `decoder/fort_availability.go` (add lure/showcase/invasion observe/read)
- Modify: `decoder/fort_availability_test.go`
- Modify: `decoder/fortRtree.go` (`updatePokestopLookup`, `updatePokestopIncidentLookup`)
- Modify: `decoder/api_pokestop_available.go`
- Modify/Create: `decoder/api_pokestop_available_test.go`

**Interfaces:**
- Consumes (Task 1): `observeExpiry`, `pruneExpired`, `lureExpiry`, `showcaseExpiry`, `invasionExpiry`, `showcaseKey`, `invasionKey`.
- Produces: `func observePokestop(fl *FortLookup, now int64)` (lure + showcase), `func observeInvasion(inc *FortLookupIncident, now int64)`, `func readLures/readShowcases/readInvasions(now int64) []Api...`.

- [ ] **Step 1: Write the failing test** — append to `decoder/fort_availability_test.go`

```go
func TestObservePokestopAggregatesAndRead(t *testing.T) {
	initFortAvailability()
	now := int64(1000)

	// lure + showcase on one stop
	observePokestop(&FortLookup{
		LureId: 501, LureExpireTimestamp: 2000,
		ContestPokemonId: 25, ContestPokemonForm: 0, ContestPokemonType: 0, ShowcaseExpiry: 2000,
	}, now)
	// expired lure + no showcase -> both ignored
	observePokestop(&FortLookup{LureId: 502, LureExpireTimestamp: 500}, now)

	// invasions (per incident)
	observeInvasion(&FortLookupIncident{Character: 5, DisplayType: 1, Confirmed: true, Slot1PokemonId: 41, ExpireTimestamp: 2000}, now)
	observeInvasion(&FortLookupIncident{DisplayType: 9, ExpireTimestamp: 2000}, now) // showcase incident, character 0
	observeInvasion(&FortLookupIncident{Character: 30, DisplayType: 3, ExpireTimestamp: 500}, now) // expired

	if l := readLures(now); len(l) != 1 || l[0].LureId != 501 {
		t.Fatalf("lures: %+v", l)
	}
	if s := readShowcases(now); len(s) != 1 || s[0].PokemonId != 25 {
		t.Fatalf("showcases: %+v", s)
	}
	inv := readInvasions(now)
	if len(inv) != 2 {
		t.Fatalf("want 2 invasions, got %d: %+v", len(inv), inv)
	}
	for _, in := range inv {
		if in.Character == 30 {
			t.Fatal("expired invasion leaked")
		}
	}
	// everything expires
	if len(readLures(3000)) != 0 || len(readShowcases(3000)) != 0 || len(readInvasions(3000)) != 0 {
		t.Fatal("all pokestop aggregates should expire to empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestObservePokestopAggregatesAndRead -count=1`
Expected: FAIL — undefined `observePokestop` / `observeInvasion` / `readLures` / `readShowcases` / `readInvasions`.

- [ ] **Step 3: Add pokestop observe/read** — `decoder/fort_availability.go`

```go
func observePokestop(fl *FortLookup, now int64) {
	if fl.LureId != 0 {
		observeExpiry(lureExpiry, fl.LureId, fl.LureExpireTimestamp, now)
	}
	if fl.ContestPokemonId != 0 {
		observeExpiry(showcaseExpiry, showcaseKey{fl.ContestPokemonId, fl.ContestPokemonForm, fl.ContestPokemonType}, fl.ShowcaseExpiry, now)
	}
}

func observeInvasion(inc *FortLookupIncident, now int64) {
	observeExpiry(invasionExpiry, invasionKey{
		Character: inc.Character, DisplayType: int16(inc.DisplayType), Confirmed: inc.Confirmed,
		Slot1PokemonId: inc.Slot1PokemonId, Slot1Form: inc.Slot1Form,
	}, inc.ExpireTimestamp, now)
}

func readLures(now int64) []ApiPokestopLureAvailable {
	out := []ApiPokestopLureAvailable{}
	lureExpiry.Range(func(k int16, exp int64) bool {
		if exp > now {
			out = append(out, ApiPokestopLureAvailable{LureId: k})
		} else {
			pruneExpired(lureExpiry, k, now)
		}
		return true
	})
	return out
}

func readShowcases(now int64) []ApiPokestopShowcaseAvailable {
	out := []ApiPokestopShowcaseAvailable{}
	showcaseExpiry.Range(func(k showcaseKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiPokestopShowcaseAvailable{PokemonId: k.PokemonId, Form: k.Form, TypeId: k.TypeId})
		} else {
			pruneExpired(showcaseExpiry, k, now)
		}
		return true
	})
	return out
}

func readInvasions(now int64) []ApiPokestopInvasionAvailable {
	out := []ApiPokestopInvasionAvailable{}
	invasionExpiry.Range(func(k invasionKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiPokestopInvasionAvailable{
				Character: k.Character, DisplayType: k.DisplayType, Confirmed: k.Confirmed,
				Slot1PokemonId: k.Slot1PokemonId, Slot1Form: k.Slot1Form,
			})
		} else {
			pruneExpired(invasionExpiry, k, now)
		}
		return true
	})
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/ -run TestObservePokestopAggregatesAndRead -count=1`
Expected: PASS.

- [ ] **Step 5: Add the pokestop hooks** — `decoder/fortRtree.go`

In `updatePokestopLookup`, after the `Compute` block that stores the FortLookup and before `reconcileFortQuestConditions(...)`, observe lure + showcase from the freshly built values. The function already has `pokestop`; add a `now` and observe:

```go
	})

	observePokestop(&FortLookup{
		LureId:             pokestop.LureId,
		LureExpireTimestamp: pokestop.LureExpireTimestamp.ValueOrZero(),
		ContestPokemonId:   int16(pokestop.ShowcasePokemon.ValueOrZero()),
		ContestPokemonForm: int16(pokestop.ShowcasePokemonForm.ValueOrZero()),
		ContestPokemonType: int8(pokestop.ShowcasePokemonType.ValueOrZero()),
		ShowcaseExpiry:     pokestop.ShowcaseExpiry.ValueOrZero(),
	}, time.Now().Unix())

	// This is the sole writer of a pokestop's FortLookup entry ...
	reconcileFortQuestConditions(pokestop.Id, questConditionKeysFromPokestop(pokestop))
```

In `updatePokestopIncidentLookup`, after the `updated := FortLookupIncident{...}` is built (before or after the Compute), observe it — it already has `now`:

```go
	observeExpiry(invasionExpiry, invasionKey{
		Character: updated.Character, DisplayType: int16(updated.DisplayType), Confirmed: updated.Confirmed,
		Slot1PokemonId: updated.Slot1PokemonId, Slot1Form: updated.Slot1Form,
	}, updated.ExpireTimestamp, now)
```

Prefer calling the helper: `observeInvasion(&updated, now)`.

- [ ] **Step 6: Point `GetAvailablePokestops` at the maps + delete the scan machinery** — `decoder/api_pokestop_available.go`

Drop `Count` from `ApiPokestopInvasionAvailable`, `ApiPokestopLureAvailable`, `ApiPokestopShowcaseAvailable` (delete each `Count int ...` field line). Leave `ApiPokestopQuestAvailable` and `ApiQuestConditionResult` untouched (quests keep counts).

Replace `pokestopAvailAcc`, `newPokestopAvailAcc`, its `ingest`, its `result`, `GetAvailablePokestops`, `questRewardKey`, `verifyQuestAggregate`, and `logAvailablePokestops` with:

```go
// GetAvailablePokestops reads the maintained lure/showcase/invasion indexes and
// the maintained quest-conditions aggregate (quests unchanged) — no fort scan.
func GetAvailablePokestops(now int64) *ApiAvailablePokestops {
	res := &ApiAvailablePokestops{
		Quests:    []ApiPokestopQuestAvailable{},
		Invasions: readInvasions(now),
		Lures:     readLures(now),
		Showcases: readShowcases(now),
	}
	for _, c := range GetAvailableQuestConditions() {
		res.Quests = append(res.Quests, ApiPokestopQuestAvailable(c))
	}
	log.Infof("available-pokestops: %d quests, %d invasions, %d lures, %d showcases (maintained)",
		len(res.Quests), len(res.Invasions), len(res.Lures), len(res.Showcases))
	return res
}
```

Keep the `ApiAvailablePokestops` struct and the four `Api*Available` types (minus Count). Remove the now-unused `"time"` import if unused; keep `log`. If `questRewardKey` is referenced nowhere else (confirm with `grep -rn questRewardKey decoder/`), delete it.

- [ ] **Step 7: Pokestop availability test** — `decoder/api_pokestop_available_test.go`

If a pokestop-available test exists, rewrite it to seed via `observePokestop` / `observeInvasion` + a seeded quest condition, then assert `GetAvailablePokestops(now)` sections. If none exists, the coverage in `fort_availability_test.go` Step 1 plus the endpoint smoke in Task 4 suffice — do not invent a redundant one. Verify no test still references the deleted `verifyQuestAggregate` / `pokestopAvailAcc` (grep; fix any).

- [ ] **Step 8: Build + full decoder test**

Run: `go build -tags go_json ./... && go test ./decoder/ -count=1`
Expected: build OK; tests pass.

- [ ] **Step 9: Commit**

```bash
git add decoder/fort_availability.go decoder/fort_availability_test.go decoder/fortRtree.go decoder/api_pokestop_available.go decoder/api_pokestop_available_test.go
git commit
```
Message: `feat(availability): maintained pokestop lure/showcase/invasion indexes`

---

## Task 4: Combined endpoint reads maps; final scan removal

**Files:**
- Modify: `decoder/api_fort_available.go`
- Modify: `decoder/api_fort_available_test.go`

**Interfaces:**
- Consumes: `GetAvailablePokestops`, `GetAvailableGyms`, `GetAvailableStations` (all now map-backed).

- [ ] **Step 1: Rewrite `TestGetAvailableForts`** — `decoder/api_fort_available_test.go`

Remove any `t.Skip` added in Task 1. Seed the maintained maps via observe, then assert the combined result assembles all three sections:

```go
func TestGetAvailableForts(t *testing.T) {
	initFortAvailability()
	initQuestConditions()
	now := int64(1_000_000)

	observeRaid(&FortLookup{RaidLevel: 5, RaidPokemonId: 150, RaidEndTimestamp: now + 100}, now)
	observePokestop(&FortLookup{LureId: 501, LureExpireTimestamp: now + 100}, now)
	observeInvasion(&FortLookupIncident{Character: 5, DisplayType: 1, ExpireTimestamp: now + 100}, now)
	observeStationBattles(&FortLookup{StationBattles: []FortLookupStationBattle{
		{BattleLevel: 5, BattlePokemonId: 150, BattleEndTimestamp: now + 100},
	}}, now)

	combined := GetAvailableForts(now)
	if len(combined.Gyms.Raids) != 1 {
		t.Fatalf("gyms: %+v", combined.Gyms)
	}
	if len(combined.Pokestops.Lures) != 1 || len(combined.Pokestops.Invasions) != 1 {
		t.Fatalf("pokestops: %+v", combined.Pokestops)
	}
	if len(combined.Stations.Battles) != 1 {
		t.Fatalf("stations: %+v", combined.Stations)
	}

	// parity: combined sections equal the per-type reads over the same maps
	if len(GetAvailableGyms(now).Raids) != len(combined.Gyms.Raids) ||
		len(GetAvailableStations(now).Battles) != len(combined.Stations.Battles) {
		t.Fatal("combined diverges from per-type builders")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestGetAvailableForts -count=1`
Expected: FAIL — `GetAvailableForts` still does `fortLookupCache.Range` and returns empty sections (maps are seeded, cache is not).

- [ ] **Step 3: Rewrite `GetAvailableForts`** — `decoder/api_fort_available.go`

```go
// GetAvailableForts assembles all three availability sections from the
// maintained indexes — no fortLookupCache scan.
func GetAvailableForts(now int64) *ApiAvailableForts {
	res := &ApiAvailableForts{
		Pokestops: GetAvailablePokestops(now),
		Gyms:      GetAvailableGyms(now),
		Stations:  GetAvailableStations(now),
	}
	log.Infof("available-forts: %d raid, %d lure, %d invasion, %d showcase, %d battle options (maintained)",
		len(res.Gyms.Raids), len(res.Pokestops.Lures), len(res.Pokestops.Invasions),
		len(res.Pokestops.Showcases), len(res.Stations.Battles))
	return res
}
```

Update the `ApiAvailableForts` doc comment to drop "one fortLookupCache range". Remove the now-unused `"time"` import; keep `log`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./decoder/ -run TestGetAvailableForts -count=1`
Expected: PASS.

- [ ] **Step 5: Assert the scan is gone**

Run: `grep -rn "fortLookupCache.Range" decoder/api_*_available.go decoder/api_fort_available.go`
Expected: no matches (the availability path no longer scans). If any remain, they are a leftover to remove.

- [ ] **Step 6: Build + full decoder + route tests**

Run: `go build -tags go_json ./... && go test ./decoder/ -count=1 && go test . -count=1`
Expected: all green (route/huma tests for the availability endpoints still register and 503-gate correctly; the response shapes changed only by dropping `count`).

- [ ] **Step 7: Commit**

```bash
git add decoder/api_fort_available.go decoder/api_fort_available_test.go
git commit
```
Message: `feat(availability): combined endpoint reads maintained indexes; drop fort scan`

---

## Self-Review

**Spec coverage:**
- §3 five maps + observe primitive → Task 1 (primitive + raid), Tasks 2-3 (battle, lure/showcase/invasion). ✅
- §3.1 `observeExpiry` (ignore expired, keep-larger) → Task 1 Step 3, tested Step 1. ✅
- §3.2 hooks in the four update fns → Task 1 Step 5 (gym), Task 2 Step 5 (station), Task 3 Step 5 (pokestop + incident). ✅
- §3.3 prune-on-read conditional delete, strong Range → `pruneExpired` (Task 1 Step 3), used by every `read*`. ✅
- §4 removals (scan, accumulators, verifyQuestAggregate, Count) → Tasks 1/2/3 drop per-type accs + Count; Task 3 drops verifyQuestAggregate; Task 4 drops the combined scan and asserts none remain (Step 5). ✅
- §3 init warms at startup → `initFortAvailability` in `initFortRtree`; hooks fire during preload because preload calls the same update fns (unchanged behavior). ✅
- §8 drop Count, quests keep counts → Tasks 1/2/3 drop Count on the five dynamic structs; `ApiPokestopQuestAvailable`/`ApiQuestConditionResult` untouched. ✅
- §9 tests → each task TDDs its aggregate; Task 4 covers the combined + a no-scan grep assertion. ✅
- §10 non-goals (quests, ReactMap, pokemon, no sweep) → honored; no ReactMap or pokemon file touched, no sweep added. ✅

**Placeholder scan:** No TBD/TODO; every code step shows complete code. The only conditional instruction (Task 3 Step 7 "if a pokestop-available test exists") resolves to a concrete grep-and-decide, not a placeholder.

**Type consistency:** `raidKey`/`battleKey`/`showcaseKey`/`invasionKey` field names are consistent between the key structs (Task 1 Step 3), the observe functions, and the read functions. `observeExpiry`/`pruneExpired` signatures match all call sites. `ApiGymRaidAvailable`/`ApiStationBattleAvailable`/`ApiPokestop*Available` field names match their (Count-stripped) struct definitions.
