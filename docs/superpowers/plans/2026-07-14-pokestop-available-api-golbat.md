# Golbat `GET /api/pokestop/available` — Implementation Plan (Golbat side)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an in-memory `GET /api/pokestop/available` endpoint returning the distinct quest rewards (+conditions), lures, showcases and invasions currently available, so ReactMap can drop its ~30-query 15-minute SQL block.

**Architecture:** Serve from the lightweight, value-stored `fortLookupCache` (quests/lures/showcases) + the tiny `incidentCache` (invasions) + a new maintained conditions map (quest title/target options). Fold in the `FortLookup` fixes the endpoint needs — two expiry fields and an incidents slice — which also correct existing `isFortDnfMatch` bugs. No DB scan; `FortInMemory`-gated.

**Tech Stack:** Go; `puzpuzpuz/xsync/v4` (`xsync.Map`, `Compute`); `maypok86/otter` caches (`Range`, `Get`, `OnEviction`); Huma v2 routes (`huma.Register`); `prometheus/client_golang`; `logrus`.

## Global Constraints

- Branch `feat/pokestop-available-api` off `perf/eviction-lock-contention`; rebase before merge and re-verify cache accessor names (that branch actively reworks eviction).
- Touch caches only via `Get`/`Range`/`OnEviction` — never internal eviction machinery.
- Endpoint auth: security scheme `securitySchemeName` (`golbatSecret`, header `X-Golbat-Secret`); gate on `config.Config.FortInMemory` → `huma.Error503ServiceUnavailable("fort_in_memory not enabled")`.
- Incident model: **slot1 only** (slots 2/3 are dead per production data); `confirmed`+slot1 only occur on grunts (display_type 1).
- Conditions map supplies *options only*; title/target *filtering* is applied in ReactMap — never add title/target to `FortLookup` or `isFortDnfMatch`.
- Structured tuples on the wire; ReactMap builds filter keys and maps display_type labels (7→goldstop, 8→kecleon, 9→showcase).
- Format with `gofmt`; keep functions in the file that owns the responsibility (see File Structure).
- Design reference: `docs/superpowers/specs/2026-07-14-pokestop-available-api-design.md`.

## File Structure

- `decoder/fortRtree.go` (modify) — `FortLookup` struct (+2 expiry fields, +`Incidents` slice, −flat incident fields); `updatePokestopLookup`, `updatePokestopIncidentLookup`; conditions eviction hook in `initFortRtree`.
- `decoder/api_fort.go` (modify) — `isFortDnfMatch` POKESTOP branch: lure/showcase expiry checks + incident-slice iteration.
- `decoder/quest_conditions.go` (create) — the maintained conditions map: key type, `xsync.Map`, `adjustQuestConditionCount`, `questConditionKeysFromPokestop`, `reconcileQuestConditions`, init.
- `decoder/pokestop_process.go` (modify) — snapshot old / reconcile new around quest apply.
- `decoder/pokestop_state.go` / `decoder/fortRtree.go` (modify) — increment conditions on fort load/preload, decrement on eviction.
- `decoder/api_pokestop_available.go` (create) — response structs + `GetAvailablePokestops()` (ranges the three sources) + timing/log.
- `routes_huma.go` (modify) — register `GET /api/pokestop/available` (`FortInMemory`-gated).
- `stats_collector/stats_collector.go`, `stats_collector/prometheus.go`, and the no-op collector (modify) — `ObserveApiScan`.
- `decoder/*_test.go` (create/modify) — one test file per task.

---

### Task 1: `FortLookup` lure + showcase expiry (aggregate needs active-only; also fixes the scan)

**Files:**
- Modify: `decoder/fortRtree.go` — `FortLookup` struct; `updatePokestopLookup` (`:197-223`)
- Modify: `decoder/api_fort.go` — `isFortDnfMatch` POKESTOP branch (`:139-181`)
- Test: `decoder/fort_expiry_test.go` (create)

**Interfaces:**
- Produces: `FortLookup.LureExpireTimestamp int64`, `FortLookup.ShowcaseExpiry int64` (used by Task 4 and the scan).

- [ ] **Step 1: Write the failing test** — an active lure matches, an expired lure does not; likewise showcase.

```go
package decoder

import "testing"

func TestFortDnfMatch_LureExpiry(t *testing.T) {
	now := int64(1_000_000)
	active := &FortLookup{FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now + 100}
	expired := &FortLookup{FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now - 100}
	f := &ApiFortDnfFilter{LureId: []int16{501}}
	if !isFortDnfMatch(POKESTOP, active, f, now) {
		t.Fatal("active lure should match")
	}
	if isFortDnfMatch(POKESTOP, expired, f, now) {
		t.Fatal("expired lure should NOT match")
	}
}

func TestFortDnfMatch_ShowcaseExpiry(t *testing.T) {
	now := int64(1_000_000)
	active := &FortLookup{FortType: POKESTOP, ContestPokemonId: 1, ShowcaseExpiry: now + 100}
	expired := &FortLookup{FortType: POKESTOP, ContestPokemonId: 1, ShowcaseExpiry: now - 100}
	f := &ApiFortDnfFilter{ContestPokemon: []ApiDnfId{{Id: 1, Form: 0}}}
	if !isFortDnfMatch(POKESTOP, active, f, now) {
		t.Fatal("active showcase should match")
	}
	if isFortDnfMatch(POKESTOP, expired, f, now) {
		t.Fatal("expired showcase should NOT match")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run 'TestFortDnfMatch_(Lure|Showcase)Expiry' -v`
Expected: FAIL — `LureExpireTimestamp`/`ShowcaseExpiry` undefined.

- [ ] **Step 3: Add the two fields to `FortLookup`** (in the `// Pokestop` region of the struct, `fortRtree.go`):

```go
	LureId              int16
	LureExpireTimestamp int64 // used to check expiry at filter time
	// ... existing quest reward fields ...
	ContestPokemonId    int16
	ContestPokemonForm  int16
	ContestPokemonType  int8
	ContestTotalEntries int16
	ShowcaseExpiry      int64 // used to check expiry at filter time
```

- [ ] **Step 4: Populate them in `updatePokestopLookup`** — add to the `FortLookup{...}` literal (`fortRtree.go:197`):

```go
		LureId:              pokestop.LureId,
		LureExpireTimestamp: pokestop.LureExpireTimestamp.ValueOrZero(),
		// ...
		ShowcaseExpiry:      pokestop.ShowcaseExpiry.ValueOrZero(),
```

- [ ] **Step 5: Add expiry gates in `isFortDnfMatch`** — the lure check (`api_fort.go:140`) becomes:

```go
		if filter.LureId != nil &&
			(fortLookup.LureExpireTimestamp <= now || !slices.Contains(filter.LureId, fortLookup.LureId)) {
			return false
		}
```

and gate the three Contest checks (`api_fort.go:172-181`) on `fortLookup.ShowcaseExpiry > now`, e.g.:

```go
		if filter.ContestPokemon != nil &&
			(fortLookup.ShowcaseExpiry <= now ||
				!matchDnfIdPair(filter.ContestPokemon, fortLookup.ContestPokemonId, fortLookup.ContestPokemonForm)) {
			return false
		}
```

(apply the same `ShowcaseExpiry <= now ||` guard to the `ContestPokemonType` and `ContestTotalEntries` clauses).

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./decoder/ -run 'TestFortDnfMatch_(Lure|Showcase)Expiry' -v` → PASS
Run: `go build ./...` → no errors.

- [ ] **Step 7: Commit**

```bash
git add decoder/fortRtree.go decoder/api_fort.go decoder/fort_expiry_test.go
git commit -m "feat(fort): add lure/showcase expiry to FortLookup and enforce at scan filter"
```

---

### Task 2: `FortLookup` incidents slice (fix multi-incident clobbering, expiry, slot1 matching)

**Files:**
- Modify: `decoder/fortRtree.go` — `FortLookup` (−5 flat incident fields, +`Incidents` slice); `updatePokestopLookup` (preserve slice); `updatePokestopIncidentLookup` (`:259-273`, upsert+prune)
- Modify: `decoder/api_fort.go` — `isFortDnfMatch` incident block (`:183-195`)
- Test: `decoder/fort_incident_test.go` (create)

**Interfaces:**
- Produces: `type FortLookupIncident struct { DisplayType int8; Style int8; Character int16; Confirmed bool; Slot1PokemonId int16; Slot1Form int16; ExpireTimestamp int64 }`; `FortLookup.Incidents []FortLookupIncident`. Consumed by **both** Task 4's aggregate (invasions, in the shared range) and `isFortDnfMatch` (scan). Load-bearing for Phase 1 — ensure preload populates slices (forts before incidents).

- [ ] **Step 1: Write the failing test** — two incidents on one stop (leader + showcase) are both filterable; an expired incident is skipped; slot1 pokemon matches.

```go
package decoder

import "testing"

func TestFortDnfMatch_IncidentSlice(t *testing.T) {
	now := int64(1_000_000)
	fl := &FortLookup{FortType: POKESTOP, Incidents: []FortLookupIncident{
		{DisplayType: 2, Character: 20, ExpireTimestamp: now + 100},               // leader
		{DisplayType: 9, ExpireTimestamp: now + 100},                              // showcase
		{DisplayType: 1, Character: 5, Confirmed: true, Slot1PokemonId: 41, Slot1Form: 0, ExpireTimestamp: now + 100}, // grunt
		{DisplayType: 3, Character: 30, ExpireTimestamp: now - 100},               // EXPIRED giovanni
	}}
	// both coexisting characters match (no clobber)
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentCharacter: []int16{20}}, now) {
		t.Fatal("leader (20) should match")
	}
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentDisplayType: []int8{9}}, now) {
		t.Fatal("showcase (dt9) should match")
	}
	// slot1 pokemon matches
	if !isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentPokemon: []ApiDnfId{{Id: 41, Form: 0}}}, now) {
		t.Fatal("slot1 pokemon 41 should match")
	}
	// expired incident does not match
	if isFortDnfMatch(POKESTOP, fl, &ApiFortDnfFilter{IncidentCharacter: []int16{30}}, now) {
		t.Fatal("expired giovanni (30) should NOT match")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestFortDnfMatch_IncidentSlice -v`
Expected: FAIL — `FortLookupIncident` / `Incidents` undefined.

- [ ] **Step 3: Define the slice type and field; remove the flat incident fields** — in `fortRtree.go`, replace the `// Pokestop - incident (first active incident, flat fields)` block with:

```go
	// Pokestop - incidents (all active incidents; slot1 only — slots 2/3 are unused).
	// Mirrors the StationBattles slice pattern.
	Incidents []FortLookupIncident
```

and add, near `FortLookupStationBattle` (`decoder/station_battle.go`):

```go
type FortLookupIncident struct {
	DisplayType     int8
	Style           int8
	Character       int16
	Confirmed       bool
	Slot1PokemonId  int16
	Slot1Form       int16
	ExpireTimestamp int64 // used to skip expired incidents at filter time
}
```

- [ ] **Step 4: Preserve the slice in `updatePokestopLookup`** — replace the flat-field snapshot/restore (`fortRtree.go:183-195, 214-218`) with slice preservation:

```go
	var incidents []FortLookupIncident
	if existing, ok := fortLookupCache.Load(pokestop.Id); ok {
		incidents = existing.Incidents
	}
	// ... in the FortLookup{...} literal, replace the five Incident* fields with:
		Incidents: incidents,
```

- [ ] **Step 5: Upsert + prune in `updatePokestopIncidentLookup`** — replace the body (`fortRtree.go:260-272`) with a merge keyed by display slot semantics (one entry per incident id is not available here, so key by `DisplayType`+`Character`, which is unique per active incident on a stop):

```go
func updatePokestopIncidentLookup(pokestopId string, incident *Incident) {
	existing, ok := fortLookupCache.Load(pokestopId)
	if !ok {
		return
	}
	now := time.Now().Unix()
	updated := FortLookupIncident{
		DisplayType:     int8(incident.DisplayType),
		Style:           int8(incident.Style),
		Character:       incident.Character,
		Confirmed:       incident.Confirmed,
		Slot1PokemonId:  int16(incident.Slot1PokemonId.ValueOrZero()),
		Slot1Form:       int16(incident.Slot1Form.ValueOrZero()),
		ExpireTimestamp: incident.ExpirationTime,
	}
	out := existing.Incidents[:0:0] // fresh backing array; never mutate a shared slice in place
	replaced := false
	for _, inc := range existing.Incidents {
		if inc.ExpireTimestamp <= now {
			continue // prune expired
		}
		if inc.DisplayType == updated.DisplayType && inc.Character == updated.Character {
			out = append(out, updated) // replace the same incident
			replaced = true
		} else {
			out = append(out, inc)
		}
	}
	if !replaced && updated.ExpireTimestamp > now {
		out = append(out, updated)
	}
	existing.Incidents = out
	fortLookupCache.Store(pokestopId, existing)
}
```

- [ ] **Step 6: Iterate the slice in `isFortDnfMatch`** — replace the flat incident block (`api_fort.go:183-195`) with match-any-over-active, mirroring the `StationBattles` loop (`api_fort.go:210-227`):

```go
		if filter.IncidentDisplayType != nil || filter.IncidentStyle != nil ||
			filter.IncidentCharacter != nil || filter.IncidentPokemon != nil {
			matched := false
			for _, inc := range fortLookup.Incidents {
				if inc.ExpireTimestamp <= now {
					continue
				}
				if filter.IncidentDisplayType != nil && !slices.Contains(filter.IncidentDisplayType, inc.DisplayType) {
					continue
				}
				if filter.IncidentStyle != nil && !slices.Contains(filter.IncidentStyle, inc.Style) {
					continue
				}
				if filter.IncidentCharacter != nil && !slices.Contains(filter.IncidentCharacter, inc.Character) {
					continue
				}
				if filter.IncidentPokemon != nil && !matchDnfIdPair(filter.IncidentPokemon, inc.Slot1PokemonId, inc.Slot1Form) {
					continue
				}
				matched = true
				break
			}
			if !matched {
				return false
			}
		}
```

- [ ] **Step 7: Run tests + build**

Run: `go test ./decoder/ -run TestFortDnfMatch_IncidentSlice -v` → PASS
Run: `go test ./decoder/ -run TestFortDnfMatch -v` (Task 1 still green) → PASS
Run: `go build ./...` → confirm no remaining references to the removed `Incident*` flat fields (fix any).

- [ ] **Step 8: Commit**

```bash
git add decoder/fortRtree.go decoder/station_battle.go decoder/api_fort.go decoder/fort_incident_test.go
git commit -m "feat(fort): store pokestop incidents as a slice; fix multi-incident scan filtering"
```

---

### Task 3: Maintained quest-conditions map (per-reward title/target options)

**Files:**
- Create: `decoder/quest_conditions.go`
- Modify: `decoder/pokestop_process.go` — `UpdatePokestopWithQuest` (`:31`): snapshot old, reconcile new
- Modify: `decoder/fortRtree.go` — `initFortRtree` (`:92`): decrement on pokestop eviction; increment on `fortRtreeUpdatePokestopOnGet`
- Test: `decoder/quest_conditions_test.go` (create)

**Interfaces:**
- Produces: `type questConditionKey struct { WithAr bool; RewardType, ItemId, Amount, PokemonId, FormId int16; Title string; Target int32 }`; `func questConditionKeysFromPokestop(p *Pokestop) []questConditionKey`; `func adjustQuestConditions(keys []questConditionKey, delta int64)`; `func GetAvailableQuestConditions() []ApiQuestConditionResult` (consumed by Task 4).

- [ ] **Step 1: Write the failing test** — counting: two forts with the same (reward,title,target) → one entry, count 2; decrement to zero removes it.

```go
package decoder

import "testing"

func TestQuestConditions_Aggregate(t *testing.T) {
	initQuestConditions()
	k := []questConditionKey{{RewardType: 2, ItemId: 1, Title: "catch_x", Target: 3}}
	adjustQuestConditions(k, +1)
	adjustQuestConditions(k, +1)
	got := GetAvailableQuestConditions()
	if len(got) != 1 || got[0].Count != 2 || got[0].Title != "catch_x" {
		t.Fatalf("want 1 entry count=2, got %+v", got)
	}
	adjustQuestConditions(k, -2)
	if len(GetAvailableQuestConditions()) != 0 {
		t.Fatal("entry should be removed at count 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestQuestConditions_Aggregate -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement `decoder/quest_conditions.go`** — mirror `pokemonFormCount`/`adjustPokemonFormCount`:

```go
package decoder

import "github.com/puzpuzpuz/xsync/v4"

type questConditionKey struct {
	WithAr     bool
	RewardType int16
	ItemId     int16
	Amount     int16
	PokemonId  int16
	FormId     int16
	Title      string
	Target     int32
}

type ApiQuestConditionResult struct {
	WithAr     bool   `json:"with_ar"`
	RewardType int16  `json:"reward_type"`
	ItemId     int16  `json:"item_id"`
	Amount     int16  `json:"amount"`
	PokemonId  int16  `json:"pokemon_id"`
	FormId     int16  `json:"form_id"`
	Title      string `json:"title"`
	Target     int32  `json:"target"`
	Count      int    `json:"count"`
}

var questConditionCount *xsync.Map[questConditionKey, int64]

func initQuestConditions() { questConditionCount = xsync.NewMap[questConditionKey, int64]() }

func adjustQuestConditions(keys []questConditionKey, delta int64) {
	for _, k := range keys {
		questConditionCount.Compute(k, func(old int64, _ bool) (int64, xsync.ComputeOp) {
			if old+delta <= 0 {
				return 0, xsync.DeleteOp
			}
			return old + delta, xsync.UpdateOp
		})
	}
}

// questConditionKeysFromPokestop returns one key per present quest slot (AR + no-AR).
func questConditionKeysFromPokestop(p *Pokestop) []questConditionKey {
	var keys []questConditionKey
	if p.QuestRewardType.Valid {
		keys = append(keys, questConditionKey{
			WithAr: false, RewardType: int16(p.QuestRewardType.ValueOrZero()),
			ItemId: int16(p.QuestItemId.ValueOrZero()), Amount: int16(p.QuestRewardAmount.ValueOrZero()),
			PokemonId: int16(p.QuestPokemonId.ValueOrZero()), FormId: int16(p.QuestPokemonFormId.ValueOrZero()),
			Title: p.QuestTitle.ValueOrZero(), Target: int32(p.QuestTarget.ValueOrZero()),
		})
	}
	if p.AlternativeQuestRewardType.Valid {
		keys = append(keys, questConditionKey{
			WithAr: true, RewardType: int16(p.AlternativeQuestRewardType.ValueOrZero()),
			ItemId: int16(p.AlternativeQuestItemId.ValueOrZero()), Amount: int16(p.AlternativeQuestRewardAmount.ValueOrZero()),
			PokemonId: int16(p.AlternativeQuestPokemonId.ValueOrZero()), FormId: int16(p.AlternativeQuestPokemonFormId.ValueOrZero()),
			Title: p.AlternativeQuestTitle.ValueOrZero(), Target: int32(p.AlternativeQuestTarget.ValueOrZero()),
		})
	}
	return keys
}

func GetAvailableQuestConditions() []ApiQuestConditionResult {
	var out []ApiQuestConditionResult
	questConditionCount.Range(func(k questConditionKey, count int64) bool {
		if count > 0 {
			out = append(out, ApiQuestConditionResult{
				WithAr: k.WithAr, RewardType: k.RewardType, ItemId: k.ItemId, Amount: k.Amount,
				PokemonId: k.PokemonId, FormId: k.FormId, Title: k.Title, Target: k.Target, Count: int(count),
			})
		}
		return true
	})
	return out
}
```
> Verify exact `Pokestop` field/getter names against `decoder/pokestop.go` (`QuestTitle`, `QuestTarget`, `AlternativeQuest*`) — adjust `.ValueOrZero()` vs `.String`/`.Int64` per their `null.*` types.

- [ ] **Step 4: Run the aggregate test** → PASS. `go test ./decoder/ -run TestQuestConditions_Aggregate -v`

- [ ] **Step 5: Wire init** — call `initQuestConditions()` from `initFortRtree()` (`fortRtree.go:80`, alongside the lookup-cache init).

- [ ] **Step 6: Reconcile on quest change** — in `UpdatePokestopWithQuest` (`pokestop_process.go:31`), snapshot before the quest proto is applied and reconcile after save:

```go
	old := questConditionKeysFromPokestop(pokestop) // BEFORE updatePokestopFromQuestProto(...)
	// ... existing decode + savePokestopRecord(...) ...
	if config.Config.FortInMemory {
		adjustQuestConditions(old, -1)
		adjustQuestConditions(questConditionKeysFromPokestop(pokestop), +1)
	}
```
> If the fort was not previously resident (cache-miss load path also increments in Step 7), the `old` snapshot reads empty/zero and nets correctly.

- [ ] **Step 7: Increment on load, decrement on eviction** — in `fortRtreeUpdatePokestopOnGet` (`fortRtree.go:156`), after `updatePokestopLookup`, add `adjustQuestConditions(questConditionKeysFromPokestop(pokestop), +1)`; and in `initFortRtree` (`fortRtree.go:93`) extend the pokestop `OnEviction` callback:

```go
		pokestopCache.OnEviction(func(_ string, p *Pokestop, _ ottercache.EvictionReason) {
			adjustQuestConditions(questConditionKeysFromPokestop(p), -1)
			deferFortEviction(POKESTOP, p.Id, p.Lat, p.Lon)
		})
```
> Preload: add the same `+1` where preload calls `updatePokestopLookup` (`decoder/preload.go`), so startup population is counted once.

- [ ] **Step 8: Write + run a lifecycle test** — load(+1) → quest change(reconcile) → evict(−1) nets to empty. (Seed via the same helpers; assert `GetAvailableQuestConditions()` length transitions.) Run `go test ./decoder/ -run TestQuestConditions -v` → PASS; `go build ./...`.

- [ ] **Step 9: Commit**

```bash
git add decoder/quest_conditions.go decoder/pokestop_process.go decoder/fortRtree.go decoder/preload.go decoder/quest_conditions_test.go
git commit -m "feat(quest): maintain in-memory quest-condition (title/target) aggregate"
```

**RISK NOTE for the implementer:** the increment/decrement must net to exactly one per resident fort-with-quest. The eviction callback races re-caches (see `handlePokemonEviction`'s `Has`+lock guard, `pokemonRtree.go:168`). Verify with a concurrency test (load+evict+re-save interleavings) and confirm counts never go negative / never leak. **Drift is caught continuously by `verifyQuestAggregate` (Task 4)** — a direct FortLookup reward tally cross-checked against this map on every endpoint call — so a reconciliation bug surfaces as a logged/metriced desync, not silent wrong data. If that warning fires persistently in practice, switch to the more robust per-fort current-key tracker (`map[fortId][]questConditionKey`, updated in one place) — accept its memory rather than risk drift.

---

### Task 4: `GetAvailablePokestops()` aggregate + endpoint

**Files:**
- Create: `decoder/api_pokestop_available.go`
- Modify: `routes_huma.go` — new `huma.Register` (in `registerFortScanRoutes` or a new `registerPokestopReadRoutes`)
- Test: `decoder/api_pokestop_available_test.go` (create)

**Interfaces:**
- Consumes: `FortLookup` incl. the `Incidents` slice (Task 1/2), `GetAvailableQuestConditions()` (Task 3). **Not** `incidentCache` — invasions come from the fort lookup slice in the same range.
- Produces: `func GetAvailablePokestops(now int64) *ApiAvailablePokestops` and the wire structs below.

- [ ] **Step 1: Write the failing test** — seed `fortLookupCache` + `incidentCache`, assert distinct tuples, expiry exclusion, counts.

```go
package decoder

import "testing"

func TestGetAvailablePokestops(t *testing.T) {
	initFortRtree()
	initQuestConditions()
	now := int64(1_000_000)
	// quest reward + condition via the maintained map (the sole quest source)
	adjustQuestConditions([]questConditionKey{{RewardType: 2, ItemId: 1, Title: "catch_x", Target: 3}}, +1)
	// one fort: active lure, EXPIRED showcase (excluded), active grunt incident — all read in one range
	fortLookupCache.Store("s1", FortLookup{
		FortType: POKESTOP, LureId: 501, LureExpireTimestamp: now + 100,
		ContestPokemonId: 1, ShowcaseExpiry: now - 1, // expired -> excluded
		Incidents: []FortLookupIncident{
			{DisplayType: 1, Character: 5, Confirmed: true, Slot1PokemonId: 41, ExpireTimestamp: now + 100},
		},
	})
	res := GetAvailablePokestops(now)
	if len(res.Lures) != 1 || res.Lures[0].LureId != 501 { t.Fatalf("lure: %+v", res.Lures) }
	if len(res.Showcases) != 0 { t.Fatalf("expired showcase should be excluded: %+v", res.Showcases) }
	if len(res.Quests) != 1 || res.Quests[0].RewardType != 2 { t.Fatalf("quest: %+v", res.Quests) }
	if len(res.Invasions) != 1 || res.Invasions[0].Character != 5 { t.Fatalf("invasion: %+v", res.Invasions) }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./decoder/ -run TestGetAvailablePokestops -v` — FAIL (undefined).

- [ ] **Step 3: Implement response structs + aggregate** in `decoder/api_pokestop_available.go`:

```go
package decoder

import "time"

type ApiPokestopQuestAvailable struct {
	WithAr     bool   `json:"with_ar"`
	RewardType int16  `json:"reward_type"`
	ItemId     int16  `json:"item_id"`
	Amount     int16  `json:"amount"`
	PokemonId  int16  `json:"pokemon_id"`
	FormId     int16  `json:"form_id"`
	Title      string `json:"title"`
	Target     int32  `json:"target"`
	Count      int    `json:"count"`
}
type ApiPokestopInvasionAvailable struct {
	Character      int16 `json:"character"`
	DisplayType    int16 `json:"display_type"`
	Confirmed      bool  `json:"confirmed"`
	Slot1PokemonId int16 `json:"slot1_pokemon_id"`
	Slot1Form      int16 `json:"slot1_form"`
	Count          int   `json:"count"`
}
type ApiPokestopLureAvailable struct {
	LureId int16 `json:"lure_id"`
	Count  int   `json:"count"`
}
type ApiPokestopShowcaseAvailable struct {
	PokemonId int16 `json:"pokemon_id"`
	Form      int16 `json:"form"`
	TypeId    int8  `json:"type_id"`
	Count     int   `json:"count"`
}
type ApiAvailablePokestops struct {
	Quests    []ApiPokestopQuestAvailable    `json:"quests"`
	Invasions []ApiPokestopInvasionAvailable `json:"invasions"`
	Lures     []ApiPokestopLureAvailable     `json:"lures"`
	Showcases []ApiPokestopShowcaseAvailable `json:"showcases"`
}

func GetAvailablePokestops(now int64) *ApiAvailablePokestops {
	start := time.Now()
	res := &ApiAvailablePokestops{}
	forts, incidents := 0, 0
	lures := map[int16]int{}
	shows := map[ApiPokestopShowcaseAvailable]int{} // key without Count
	inv := map[ApiPokestopInvasionAvailable]int{}   // key without Count

	// Quests (rewards + title/target) come solely from the maintained conditions map — distinct+counted.
	for _, c := range GetAvailableQuestConditions() {
		res.Quests = append(res.Quests, ApiPokestopQuestAvailable{
			WithAr: c.WithAr, RewardType: c.RewardType, ItemId: c.ItemId, Amount: c.Amount,
			PokemonId: c.PokemonId, FormId: c.FormId, Title: c.Title, Target: c.Target, Count: c.Count,
		})
	}

	// ONE range: lures + showcases + invasions (response) + a quest-reward tally (verification only).
	rewards := map[questRewardKey]int{} // direct FortLookup reward count — cross-checks the maintained map
	fortLookupCache.Range(func(_ string, fl FortLookup) bool {
		if fl.FortType != POKESTOP {
			return true
		}
		forts++
		if fl.LureId != 0 && fl.LureExpireTimestamp > now {
			lures[fl.LureId]++
		}
		if fl.ContestPokemonId != 0 && fl.ShowcaseExpiry > now {
			shows[ApiPokestopShowcaseAvailable{PokemonId: fl.ContestPokemonId, Form: fl.ContestPokemonForm, TypeId: fl.ContestPokemonType}]++
		}
		for _, in := range fl.Incidents {
			if in.ExpireTimestamp <= now {
				continue
			}
			incidents++
			inv[ApiPokestopInvasionAvailable{
				Character: in.Character, DisplayType: int16(in.DisplayType), Confirmed: in.Confirmed,
				Slot1PokemonId: in.Slot1PokemonId, Slot1Form: in.Slot1Form,
			}]++
		}
		if fl.QuestNoArRewardType != 0 {
			rewards[questRewardKey{false, fl.QuestNoArRewardType, fl.QuestNoArRewardItemId, fl.QuestNoArRewardAmount, fl.QuestNoArRewardPokemonId, fl.QuestNoArRewardPokemonForm}]++
		}
		if fl.QuestArRewardType != 0 {
			rewards[questRewardKey{true, fl.QuestArRewardType, fl.QuestArRewardItemId, fl.QuestArRewardAmount, fl.QuestArRewardPokemonId, fl.QuestArRewardPokemonForm}]++
		}
		return true
	})

	for id, n := range lures {
		res.Lures = append(res.Lures, ApiPokestopLureAvailable{LureId: id, Count: n})
	}
	for k, n := range shows {
		k.Count = n
		res.Showcases = append(res.Showcases, k)
	}
	for k, n := range inv {
		k.Count = n
		res.Invasions = append(res.Invasions, k)
	}

	verifyQuestAggregate(rewards) // alert if the maintained map drifted from the direct FortLookup tally
	logAvailablePokestops(time.Since(start), forts, incidents, res)
	return res
}

// questRewardKey is the reward signature shared by the maintained conditions map (minus title/target)
// and the FortLookup reward tally used to detect reconciliation drift.
type questRewardKey struct {
	WithAr                                              bool
	RewardType, ItemId, Amount, PokemonId, FormId int16
}

// verifyQuestAggregate cross-checks the maintained conditions map against a direct FortLookup tally.
// Invariant: for each reward signature, sum(map counts over title/target) == resident forts carrying
// it. A persistent mismatch means the Task-3 reconciliation drifted. (A single-cycle mismatch under
// concurrent updates is possible since Range is a weakly-consistent snapshot — alert on persistence,
// not one occurrence.)
func verifyQuestAggregate(fortRewards map[questRewardKey]int) {
	mapRewards := map[questRewardKey]int{}
	for _, c := range GetAvailableQuestConditions() {
		mapRewards[questRewardKey{c.WithAr, c.RewardType, c.ItemId, c.Amount, c.PokemonId, c.FormId}] += c.Count
	}
	desync := 0
	for k, fortN := range fortRewards {
		if mapRewards[k] != fortN {
			desync++
			log.Debugf("quest aggregate desync %+v: fortLookup=%d map=%d", k, fortN, mapRewards[k])
		}
	}
	for k := range mapRewards {
		if _, ok := fortRewards[k]; !ok {
			desync++
			log.Debugf("quest aggregate desync %+v: fortLookup=0 map=%d", k, mapRewards[k])
		}
	}
	if desync > 0 {
		log.Warnf("quest aggregate desync: %d reward signatures differ (FortLookup tally vs maintained map)", desync)
		if statsCollector != nil {
			statsCollector.ObserveApiScan("quest-aggregate-desync-count", float64(desync)) // or a dedicated counter (Task 5)
		}
	}
}
```
> Confirm `fortLookupCache.Range` and `incidentCache.Range` callback signatures against `ottercache` / `xsync` on this branch; `fortLookupCache` value is `FortLookup` (value), `incidentCache` value is `*Incident`.

- [ ] **Step 4: Run aggregate test** → PASS (`logAvailablePokestops` may be a temporary no-op until Task 5).

- [ ] **Step 5: Register the route** in `routes_huma.go` — mirror `available-pokemon` (`:108`) with the `FortInMemory` gate from `registerFortScanRoutes` (`:172`):

```go
type pokestopAvailableOutput struct{ Body *decoder.ApiAvailablePokestops }

// inside registerFortScanRoutes (or a new registerPokestopReadRoutes wired in main.go):
op := huma.Operation{
	OperationID:   "available-pokestops",
	Method:        http.MethodGet,
	Path:          "/api/pokestop/available",
	Summary:       "List currently available pokestop rewards/invasions/lures/showcases",
	Tags:          []string{"Pokestop"},
	Security:      []map[string][]string{{securitySchemeName: {}}},
	DefaultStatus: http.StatusOK,
}
huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*pokestopAvailableOutput, error) {
	if !config.Config.FortInMemory {
		return nil, huma.Error503ServiceUnavailable("fort_in_memory not enabled")
	}
	return &pokestopAvailableOutput{Body: decoder.GetAvailablePokestops(time.Now().Unix())}, nil
})
```

- [ ] **Step 6: Build + manual smoke** — `go build ./...`; run Golbat with `FortInMemory` on, then `curl -sH "X-Golbat-Secret: $SECRET" localhost:<port>/api/pokestop/available | jq '.quests|length, .invasions|length'`. Confirm `503` when `FortInMemory` off.

- [ ] **Step 7: Commit**

```bash
git add decoder/api_pokestop_available.go routes_huma.go decoder/api_pokestop_available_test.go
git commit -m "feat(api): add GET /api/pokestop/available (in-memory aggregate)"
```

---

### Task 5: Instrumentation (build-time histogram + log)

**Files:**
- Modify: `stats_collector/stats_collector.go` (interface), `stats_collector/prometheus.go` (impl + histogram), and the no-op collector impl (search `func .*ObserveDbQuery` to find it)
- Modify: `decoder/api_pokestop_available.go` — `logAvailablePokestops`
- Test: manual/observability

**Interfaces:**
- Produces: `StatsCollector.ObserveApiScan(operation string, seconds float64)`.

- [ ] **Step 1: Add to the interface** (`stats_collector/stats_collector.go`, next to `ObserveDbQuery` `:61`):

```go
	ObserveApiScan(operation string, seconds float64)
```

- [ ] **Step 2: Add the histogram + method** in `stats_collector/prometheus.go` — mirror `dbQueryDuration` (`:362`) and its `Observe` (`:765`) and `MustRegister` (`:810`):

```go
	apiScanDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Namespace: namespace, Name: "api_scan_duration", Help: "In-memory API scan build time by operation"},
		[]string{"operation"},
	)
// ...
func (col *promCollector) ObserveApiScan(operation string, seconds float64) {
	apiScanDuration.WithLabelValues(operation).Observe(seconds)
}
// ... add apiScanDuration to the prometheus.MustRegister(...) list at :810
```
Add a no-op `ObserveApiScan` to the other `StatsCollector` implementation(s) so the build stays green.

- [ ] **Step 3: Implement `logAvailablePokestops`** in `decoder/api_pokestop_available.go`:

```go
func logAvailablePokestops(dur time.Duration, forts, incidents int, res *ApiAvailablePokestops) {
	if statsCollector != nil {
		statsCollector.ObserveApiScan("available-pokestops", dur.Seconds())
	}
	log.Infof("available-pokestops built in %s: scanned %d forts / %d incidents -> %d quests, %d invasions, %d lures, %d showcases",
		dur, forts, incidents, len(res.Quests), len(res.Invasions), len(res.Lures), len(res.Showcases))
}
```
> Use the same `statsCollector` package-level the decoder already uses (see `db/timing.go` / `decoder` usages of `statsCollector`); add the `log "github.com/sirupsen/logrus"` import.

- [ ] **Step 4: Build + verify** — `go build ./...`; hit the endpoint; confirm the log line and that `api_scan_duration` appears on `/metrics`.

- [ ] **Step 5: Commit**

```bash
git add stats_collector/ decoder/api_pokestop_available.go
git commit -m "feat(metrics): instrument available-pokestops build time"
```

---

## Self-Review

- **Spec coverage:** endpoint (Task 4) · in-memory sources fortLookup/incidentCache (Task 4) · lure/showcase expiry (Task 1) · incident slice + scan fix D7 (Task 2) · conditions map D8 (Task 3) · structured tuples D3 (Task 4) · FortInMemory gate D2 (Task 4) · instrumentation D5 (Task 5) · slot1-only D9 (Task 2). ReactMap consumer D6 → separate plan.
- **Types:** `FortLookupIncident`/`Incidents` (Task 2) consumed only by the scan; the aggregate reads `incidentCache` (Task 4) — consistent. `questConditionKey`/`GetAvailableQuestConditions` (Task 3) consumed by Task 4. `ObserveApiScan` (Task 5) consumed by Task 4's `logAvailablePokestops`.
- **Known verification points (flagged inline):** exact `null.*` getter names on `Pokestop`/`Incident`; `ottercache.Range`/`Set` signatures; the no-op `StatsCollector` impl location; Task 3's count-lifecycle race (concurrency test required).

## Follow-up

ReactMap consumer is a **separate plan** (`docs/superpowers/plans/…-pokestop-available-reactmap.md`): add `mem`/HTTP path to `Pokestop.getAvailable` mirroring `Pokemon.getAvailable`, map tuples → `{available, conditions}`, SQL fallback on no-`mem`/503, apply title/target sub-filter locally. Phase 2 (map-data via Golbat) is out of scope here.
