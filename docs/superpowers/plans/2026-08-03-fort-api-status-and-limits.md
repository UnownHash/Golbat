# Fort API Status + Limits + Availability Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One PR delivering the Golbat-side prerequisites for Diadem's fort API migration (ccev/diadem#174): a status/feature-flags endpoint, `limit_reached` on the fort scan responses (mirroring #392, which is cherry-picked onto this branch with ccev's attribution), `temp_evolution_id`/`ranking_standard` availability enrichment, and the `first_seen_timestamp` int16 truncation fix.

**Architecture:** Each piece follows an existing in-repo pattern: `limit_reached` copies #392's helper-pair shape onto `internalGetForts`/`internalGetFortsCombined`; the status endpoint is a plain huma GET (secret-gated, NOT `fort_in_memory`-gated); enrichment threads two new fields through `FortLookup` → availability keys → readers → API structs; the int16 fix widens the model field and lets the compiler surface every cast site.

**Tech Stack:** Go, huma v2, xsync, testify-free stdlib tests, golden snapshots in existing test files.

**Branch state at start:** `feat/fort-api-status-and-limits` off `origin/main` (`d5e33dd`), with `53e3592` = cherry-pick of ccev's #392 (`71da944`, authorship preserved) already applied. Do not rewrite that commit.

## Global Constraints

- Build/test commands (all must pass before every commit): `go build -tags go_json ./...`, `go test -tags go_json ./decoder/ .`, and `golangci-lint run` before the final push.
- Follow #392's conventions exactly for limit work: helper named like `fortScanLimit`, `limit_reached` JSON field, `doc:` tag wording consistent with the pokemon one ("Whether the pre-filtered result list reached the effective result limit").
- The status endpoint contract is consumed by Diadem (`docs/superpowers/plans/2026-08-03-golbat-fort-api.md` Task 10 in the diadem repo): response must be `{"features":{"fort_in_memory":bool},"limits":{"max_pokemon_results":int,"max_fort_results":int}}`. Do not rename these JSON keys without updating the diadem plan.
- `TestApiResultsExposeEveryDbColumn` (decoder/api_completeness_test.go) must stay green — never weaken it; extend `allow` sets only with a justifying comment.
- Commit messages: conventional (`feat:`/`fix:`), no scope parens, matching `git log` style.

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `decoder/api_fort.go` | modify | `fortScanLimit*` helpers, `LimitReached` on 4 result structs + 4 endpoints |
| `decoder/api_fort_limit_test.go` | create | unit tests for the fort limit helpers |
| `decoder/api_status.go` | create | `ApiStatusResult` + `GetApiStatus()` |
| `routes_huma.go` | modify | register `GET /api/status`; wire fort scan `limit_reached` docs |
| `huma_routes_test.go` | modify | route-registration/auth tests for status; fort schema goldens |
| `decoder/fortRtree.go` | modify | `FortLookup` + `RaidPokemonEvolution`, `ShowcaseRankingStandard`; populate at gym/pokestop sites |
| `decoder/fort_availability.go` | modify | extend `raidKey`/`showcaseKey`, observers, readers |
| `decoder/api_gym_available.go` | modify | `TempEvolutionId` on `ApiGymRaidAvailable` |
| `decoder/api_pokestop_available.go` | modify | `RankingStandard` on `ApiPokestopShowcaseAvailable` |
| `decoder/fort_availability_test.go` + `*_available_test.go` | modify | cover new fields, update goldens |
| `decoder/pokestop.go` | modify | `FirstSeenTimestamp int16` → `int64` (+ setter + cast sites) |
| `decoder/api_pokestop.go` | modify | `ApiPokestopResult.FirstSeenTimestamp` → `int64` |

---

### Task 1: `limit_reached` on fort scan responses (mirror of #392)

**Files:**
- Modify: `decoder/api_fort.go`
- Create: `decoder/api_fort_limit_test.go`
- Modify: `huma_routes_test.go` (only if its fort schema goldens fail)

**Interfaces:**
- Consumes: `internalGetForts` (`api_fort.go:292`, returns `(keys []string, examined, skipped, total int)`), `internalGetFortsCombined` (`api_fort.go:488`), `config.Config.Tuning.MaxFortResults`.
- Produces: `fortScanLimit(limit int) int`, `fortScanLimitReached(limit, resultCount int) bool`; `LimitReached bool` on `ApiGymScanResult`, `ApiPokestopScanResult`, `ApiStationScanResult`, `ApiFortCombinedScanResult`.

- [ ] **Step 1: Write failing tests** in `decoder/api_fort_limit_test.go`, mirroring `api_pokemon_common_test.go` from the cherry-picked commit (read it first for the exact style — it covers requested, default, and server-capped limits):

```go
package decoder

import (
	"testing"

	"golbat/config"
)

func TestFortScanLimit(t *testing.T) {
	origMax := config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxFortResults = 100
	defer func() { config.Config.Tuning.MaxFortResults = origMax }()

	if got := fortScanLimit(0); got != 100 {
		t.Errorf("default limit: got %d, want 100", got)
	}
	if got := fortScanLimit(40); got != 40 {
		t.Errorf("requested below cap: got %d, want 40", got)
	}
	if got := fortScanLimit(500); got != 100 {
		t.Errorf("requested above cap: got %d, want 100", got)
	}
}

func TestFortScanLimitReached(t *testing.T) {
	origMax := config.Config.Tuning.MaxFortResults
	config.Config.Tuning.MaxFortResults = 100
	defer func() { config.Config.Tuning.MaxFortResults = origMax }()

	if fortScanLimitReached(40, 39) {
		t.Error("39 of 40 should not be limit_reached")
	}
	if !fortScanLimitReached(40, 40) {
		t.Error("40 of 40 should be limit_reached")
	}
	if !fortScanLimitReached(0, 100) {
		t.Error("100 of default 100 should be limit_reached")
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test -tags go_json -run TestFortScanLimit ./decoder/` — expected: FAIL (undefined: fortScanLimit).

- [ ] **Step 3: Implement.** In `api_fort.go`, extract the existing clamp (used at lines 299 and 495) into the helpers and use them at both sites:

```go
// fortScanLimit resolves the effective result limit for a fort scan: the
// requested limit clamped by the server's max_fort_results tuning cap
// (0 = server default). Mirrors pokemonScanLimit.
func fortScanLimit(limit int) int {
	maxForts := config.Config.Tuning.MaxFortResults
	if limit > 0 && limit < maxForts {
		maxForts = limit
	}
	return maxForts
}

func fortScanLimitReached(limit, resultCount int) bool {
	return resultCount >= fortScanLimit(limit)
}
```

Replace the inline clamps in `internalGetForts` and `internalGetFortsCombined` with `maxForts := fortScanLimit(retrieveParameters.Limit)`.

Add to each of the four result structs (`api_fort.go:115-143`), matching #392's doc wording:

```go
	LimitReached bool `json:"limit_reached" doc:"Whether the pre-filtered result list reached the effective result limit"`
```

Set it in the four endpoint functions (`GymScanEndpoint`, `PokestopScanEndpoint`, `StationScanEndpoint` at `api_fort.go:422-465`, `FortCombinedScanEndpoint` at `:467-486`) from the pre-collection key counts:

```go
	LimitReached: fortScanLimitReached(retrieveParameters.Limit, len(keys)),
```

For the combined endpoint the count is `len(gymKeys)+len(pokestopKeys)+len(stationKeys)`.

- [ ] **Step 4: Run tests** — `go test -tags go_json ./decoder/ .` — expected: PASS, except possibly golden-schema assertions in `huma_routes_test.go`; if those fail, update the fort response goldens exactly the way the cherry-picked commit updated the pokemon one (look at `git show 53e3592 -- huma_routes_test.go`).

- [ ] **Step 5: Commit**

```bash
git add decoder/api_fort.go decoder/api_fort_limit_test.go huma_routes_test.go
git commit -m "feat: expose fort scan limit status"
```

---

### Task 2: `GET /api/status` — feature flags + server limits

**Files:**
- Create: `decoder/api_status.go`
- Modify: `routes_huma.go` (inside `registerHumaRoutes`, `routes_huma.go:36`, before the tiered groups)
- Modify: `huma_routes_test.go`

**Interfaces:**
- Consumes: `config.Config.FortInMemory`, `config.Config.Tuning.MaxPokemonResults`, `config.Config.Tuning.MaxFortResults`.
- Produces: `ApiStatusResult` + `GetApiStatus() *ApiStatusResult` in package `decoder`; route `GET /api/status`, secret-gated via the existing `securitySchemeName` security entry, **NOT** gated on `fort_in_memory` (it exists to report that flag).

- [ ] **Step 1: Write the failing route test.** Find the existing route-registration and auth test patterns in `huma_routes_test.go` (there are registration, 503-gating and auth cases for the fort endpoints — copy the closest GET-route case, e.g. the `/api/gym/available` one). Add cases asserting:
  1. `GET /api/status` is registered and returns 200 with `fort_in_memory` reflecting `config.Config.FortInMemory` (test both true and false — 200 either way, no 503).
  2. The response body contains `limits.max_pokemon_results` and `limits.max_fort_results` matching the tuning config set in the test.
  3. Auth: request without `X-Golbat-Secret` is rejected when a secret is configured (same assertion style as the existing fort-route auth cases).

- [ ] **Step 2: Run to verify failure** — `go test -tags go_json -run <YourTestName> .` — expected: FAIL (404 route not registered).

- [ ] **Step 3: Implement `decoder/api_status.go`:**

```go
package decoder

import (
	"golbat/config"
)

// ApiStatusResult reports which optional Golbat features are enabled and the
// server's effective scan limits, so map consumers (e.g. Diadem) can detect
// capabilities and clamp their own request limits instead of probing gated
// endpoints. Contract consumers: ccev/diadem#174.
type ApiStatusResult struct {
	Features struct {
		FortInMemory bool `json:"fort_in_memory" doc:"Whether the in-memory fort index (and with it the /api/{gym,pokestop,station,fort}/scan, /available and station by-id endpoints) is enabled"`
	} `json:"features" doc:"Enabled optional features"`
	Limits struct {
		MaxPokemonResults int `json:"max_pokemon_results" doc:"Server cap on pokemon scan results (tuning.max_pokemon_results)"`
		MaxFortResults    int `json:"max_fort_results" doc:"Server cap on fort scan results per request (tuning.max_fort_results)"`
	} `json:"limits" doc:"Effective server-side result caps"`
}

func GetApiStatus() *ApiStatusResult {
	status := &ApiStatusResult{}
	status.Features.FortInMemory = config.Config.FortInMemory
	status.Limits.MaxPokemonResults = config.Config.Tuning.MaxPokemonResults
	status.Limits.MaxFortResults = config.Config.Tuning.MaxFortResults
	return status
}
```

Register in `routes_huma.go` inside `registerHumaRoutes` following the exact shape of the neighbouring GET registrations (operation id `get-api-status`, summary "Server status", `Security: []map[string][]string{{securitySchemeName: {}}}`, `DefaultStatus: http.StatusOK`), with a handler that wraps `decoder.GetApiStatus()` in the same input/output envelope style the other GET routes use. **No `fort_in_memory` 503 gate.**

- [ ] **Step 4: Run tests** — `go test -tags go_json ./decoder/ .` — expected: PASS (update the route-inventory golden if `huma_routes_test.go` asserts the full route list).

- [ ] **Step 5: Commit**

```bash
git add decoder/api_status.go routes_huma.go huma_routes_test.go
git commit -m "feat: add /api/status reporting feature flags and scan limits"
```

---

### Task 3: Availability enrichment — `temp_evolution_id` on raids, `ranking_standard` on showcases

**Files:**
- Modify: `decoder/fortRtree.go` (FortLookup struct `:17-62`, gym population site `:251` area, pokestop population sites `:193-225` — populate in **every** spot that sets `RaidPokemonId:` / `ContestPokemonId:`, including partial-update paths)
- Modify: `decoder/fort_availability.go` (`raidKey :32`, `showcaseKey :14`, `observeRaid :119`, `observePokestop :162`, `readRaids :125`, `readShowcases`)
- Modify: `decoder/api_gym_available.go`, `decoder/api_pokestop_available.go`
- Modify: `decoder/fort_availability_test.go`, `decoder/api_gym_available_test.go`, `decoder/api_pokestop_available_test.go` (whichever assert these shapes/goldens)

**Interfaces:**
- Produces: `FortLookup.RaidPokemonEvolution int16`, `FortLookup.ShowcaseRankingStandard int16`; `ApiGymRaidAvailable.TempEvolutionId int16` (`json:"temp_evolution_id"`, 0 = none/egg); `ApiPokestopShowcaseAvailable.RankingStandard int16` (`json:"ranking_standard"`, 0 = unknown). Diadem's Task 8 maps these once released — keep the JSON names exactly.

- [ ] **Step 1: Write failing availability tests.** In `decoder/fort_availability_test.go`, find the existing observe/read test for raids (it builds a `FortLookup`, calls `observeRaid`, asserts `readRaids` output). Add/extend cases:

```go
// raid with a mega evolution surfaces temp_evolution_id
fl := &FortLookup{FortType: GYM, RaidLevel: 5, RaidPokemonId: 150, RaidPokemonForm: 0, RaidPokemonEvolution: 2, RaidEndTimestamp: now + 600}
observeRaid(fl, now)
// assert readRaids(now) contains {RaidLevel: 5, PokemonId: &150, Form: &0, TempEvolutionId: 2}
```

and the showcase equivalent (`ShowcaseRankingStandard: 3` observed → `readShowcases` entry has `RankingStandard: 3`). Two raids differing only in evolution (mega vs base) must yield **two** distinct entries — that's the point of keying on it. Match the file's existing assertion style; reset the availability maps between cases the same way neighbouring tests do.

- [ ] **Step 2: Run to verify failure** — `go test -tags go_json -run TestFortAvailability ./decoder/` (adjust `-run` to the actual test names) — expected: FAIL (unknown field `RaidPokemonEvolution`).

- [ ] **Step 3: Implement**, in dependency order:

1. `fortRtree.go` FortLookup: add `RaidPokemonEvolution int16` under the Gym block, `ShowcaseRankingStandard int16` under the Pokestop-contest block.
2. Populate: at the gym site (`:251` area) add `RaidPokemonEvolution: int16(gym.RaidPokemonEvolution.ValueOrZero()),`; at **both** pokestop sites (`:193-225`) add `ShowcaseRankingStandard: int16(pokestop.ShowcaseRankingStandard.ValueOrZero()),`. Grep `RaidPokemonId:` and `ContestPokemonId:` in `fortRtree.go` afterwards to confirm no population path was missed (partial updates included — the pokestop Compute at `:298` too if it touches contest fields).
3. `fort_availability.go`: `raidKey` gains `TempEvolution int16`; `showcaseKey` gains `RankingStandard int16`. `observeRaid` passes `fl.RaidPokemonEvolution`; `observePokestop` passes `fl.ShowcaseRankingStandard`. **Use named struct fields in the observe calls** (the current `raidKey{fl.RaidLevel, fl.RaidPokemonId, fl.RaidPokemonForm}` positional form breaks silently on reorder — switch it to named while touching it).
4. Readers: `readRaids` sets `TempEvolutionId: k.TempEvolution`; `readShowcases` sets `RankingStandard: k.RankingStandard`.
5. API structs: on `ApiGymRaidAvailable` add `TempEvolutionId int16 \`json:"temp_evolution_id" doc:"Temp evolution (mega) id of the raid boss; 0 for none or an unhatched egg"\``; on `ApiPokestopShowcaseAvailable` add `RankingStandard int16 \`json:"ranking_standard" doc:"Ranking standard of the showcase contest; 0 when unknown"\``.

- [ ] **Step 4: Run the full decoder suite** — `go test -tags go_json ./decoder/` — expected: the new tests pass; any golden-snapshot failures in the `*_available_test.go` files show the new fields — update those goldens to include them (verify the diff is ONLY the new fields).

- [ ] **Step 5: Commit**

```bash
git add decoder/fortRtree.go decoder/fort_availability.go decoder/api_gym_available.go decoder/api_pokestop_available.go decoder/*_test.go
git commit -m "feat: expose temp_evolution_id and ranking_standard in fort availability"
```

---

### Task 4: Fix `first_seen_timestamp` int16 truncation on pokestops

**Files:**
- Modify: `decoder/pokestop.go` (`:39` model field, `:466` setter)
- Modify: `decoder/api_pokestop.go` (`:39` result field)
- Modify: any call sites the compiler reveals

The `Pokestop` model declares `FirstSeenTimestamp int16` (`pokestop.go:39`) — a unix timestamp wraps int16 every ~18 hours, so the persisted value and the API field are both garbage. `Gym.FirstSeenTimestamp` is already `int64` (`gym.go:42`); this is a typo, not a design.

- [ ] **Step 1: Widen the types.** Change `decoder/pokestop.go:39` to `FirstSeenTimestamp int64 \`db:"first_seen_timestamp"\``, the setter at `:466` to `func (p *Pokestop) SetFirstSeenTimestamp(v int64)`, and `decoder/api_pokestop.go:39` to `FirstSeenTimestamp int64`. Build: `go build -tags go_json ./...` — fix every compile error it surfaces by removing the now-wrong `int16(...)` casts (do NOT re-add casts; the sources are int64 timestamps).

- [ ] **Step 2: Check the write path.** Grep `first_seen_timestamp` across `decoder/pokestop.go` — if the INSERT/UPDATE named-parameter list binds the model field, nothing else changes; note in the commit message that previously-persisted values are already truncated in affected DBs (the column is INT in MySQL; correct values return as new fort saves occur).

- [ ] **Step 3: Test** — `go test -tags go_json ./decoder/ .` — expected: PASS (update any goldens asserting the old truncated value/type).

- [ ] **Step 4: Commit**

```bash
git add decoder/pokestop.go decoder/api_pokestop.go
git commit -m "fix: pokestop first_seen_timestamp truncated to int16"
```

---

### Task 5: Full verification + PR

- [ ] **Step 1:** `go build -tags go_json ./...` && `go test -tags go_json ./decoder/ .` && `golangci-lint run` — all clean. Also run the race-sensitive availability tests the way #385 did: `go test -tags go_json -race -run 'Availability' ./decoder/`.

- [ ] **Step 2:** Push the branch to origin (UnownHash — committer flow, same as fix/lure-request-first):

```bash
git push -u origin feat/fort-api-status-and-limits
```

- [ ] **Step 3:** Open the PR (draft) against UnownHash/Golbat main, titled `feat(api): status endpoint, fort scan limit status, availability enrichment`. Body must: link ccev/diadem#174 as the consumer context; state that it **includes #392 as a cherry-pick with attribution and closes #392**; list the four deliverables; document the `/api/status` contract JSON; call out the `first_seen_timestamp` data note from Task 4 Step 2. End with the Claude Code attribution line.

- [ ] **Step 4:** Comment on ccev/diadem#174: Phase 0 PR is up, link it, note the status-endpoint contract is now concrete (so diadem Task 10 unblocks when it merges).
