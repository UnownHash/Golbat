# Golbat `GET /api/pokestop/available` — Design Spec

- **Date:** 2026-07-14
- **Status:** Approved design → planning
- **Golbat branch:** `feat/pokestop-available-api` (worktree `~/GolandProjects/Golbat-wt/pokestop-available-api`), off `perf/eviction-lock-contention`
- **ReactMap branch:** to be cut off `develop`
- **Author:** James Berry (with Claude)

## 1. Problem

ReactMap builds its pokestop filter options (quest rewards + conditions, invasions, lures,
showcases) via `Pokestop.getAvailable()` (`server/src/models/Pokestop.js:1253`) — **~30 concurrent
`DISTINCT`/`GROUP BY` scans** over the whole `pokestop`/`incident` tables, **no geo filter, always
raw SQL**, re-run every **15 min** (`EventManager.js:227`, `api.queryUpdateHours.quests`=0.25). It's
the most expensive periodic query ReactMap issues. Golbat already solves the Pokémon analog
(`GET /api/pokemon/available`, in-memory, no DB scan). This adds the pokestop equivalent.

## 2. Goals / Non-Goals

**Goals**
- `GET /api/pokestop/available` returning quest rewards + conditions, invasions, lures, showcases —
  computed **in-memory, no DB scan**, whole-instance, `FortInMemory`-gated.
- Serve from the **lightweight `fortLookupCache`** (value-stored `FortLookup`) + tiny `incidentCache`
  + a maintained quest-conditions map — not the heavy `pokestopCache` full records.
- Fix `FortLookup`'s incident representation (currently a single flat, last-write-wins slot) so the
  **scan filter `isFortDnfMatch` is correct** for multi-incident stops — folded in now.
- Structured tuples; ReactMap builds its own filter keys. Instrumented build time.
- ReactMap consumes it via the existing `mem`/`evalQuery` path (SQL fallback). **Hybrid**: only the
  available-list moves to the API in Phase 1; map-data stays on DB.

**Non-Goals**
- Not the pokestop map-data migration (Phase 2 — needs incidents in the scan response + per-fort
  quest title/target for `adv` filtering).
- No per-area/geofenced variant.
- Not a full maintained aggregate for rewards/lures/showcases (the "fully aggregated total" — a
  later efficiency project; only quest *conditions* get a maintained map now).

## 3. Decisions (locked)

| # | Decision |
|---|---|
| D1 | Scope: full replacement of the available-list query (quests+conditions, invasions, lures, showcases) |
| D2 | Serve from `fortLookupCache` (lightweight, value-stored) + `incidentCache` + maintained conditions map. No DB scan. `FortInMemory`-gated (503 otherwise) |
| D3 | Wire format: structured tuples; ReactMap builds keys and maps display_type labels (e.g. 7→goldstop) |
| D4 | Geo scope: whole-instance |
| D5 | Instrument build/response time |
| D6 | ReactMap: hybrid (available via API, map-data via DB/SQL fallback) |
| D7 | **Incident slice + `isFortDnfMatch` fix folded into Phase 1** (not deferred) |
| D8 | **Conditions via a maintained titles map** (part of the aggregate totals) — the available API returns per-reward title/target *options*, reconciled from the `Pokestop` record at quest-save/eviction (no strings in `FortLookup`). The actual title/target *filtering* is applied **locally in ReactMap**, so the scan filter never needs per-fort title/target |
| D9 | Incident model: **slot1 only** (slots 2/3 validated dead); per-incident `confirmed`+slot1 only on grunts (dt1) |

## 4. Validation data (production snapshot, `incident` table, drove D7–D9)

- **Slots 2/3 never populated:** of 1838 active incidents, slot1=110, slot2=0, slot3=0 → carry slot1 only.
- **Field richness by display_type:** dt1 grunt (149; 110 confirmed+slot1), dt2 leader (426; char only), dt3 giovanni (388; char only), dt8 kecleon (14; display+expiry), dt9 showcase (861; display+expiry). Non-rocket = display+expiry only. **dt7 = goldstop** (`INVASION_GENERIC`; none active in snapshot; behaves like 8/9).
- **Multi-incident stops ~11%:** 1464 stops ×1 incident, 175 ×2, 8 ×3.
- **Coexistence incl. two rockets:** giovanni+showcase 85, leader+showcase 71, **leader+giovanni 40** (two rocket incidents on one stop), plus rarer. → a flat/two-channel `FortLookup` incident is provably wrong; the **slice** is required for correct per-stop filtering.

Display-type taxonomy the design relies on:

| dt | label | aggregate keys | per-incident fields |
|----|-------|----------------|---------------------|
| 1 | grunt | `i` + `a` | character, confirmed, slot1 |
| 2 / 3 | leader / giovanni | `i` | character |
| 7 | goldstop | `b7` | display+expiry |
| 8 | kecleon | `b8` | display+expiry |
| 9 | showcase | `b9` (+`f`/`h` from pokestop fields) | display+expiry |

## 5. ReactMap output contract to reproduce

`Pokestop.getAvailable()` → `{ available: string[], conditions }` consumed by
`server/src/filters/builder/pokestop.js`:

| Category | Key | Source |
|---|---|---|
| Items(2)/Stardust(3)/XP(1)/Mega(12)/Candy(4)/XL(9)/Pokémon(7)/other | `q`/`d`/`p`/`m<id>-<amt>`/`c`/`x`/`<id>`(`-<form>`)/`u<type>` | quest reward fields |
| Invasion grunt | `i<character>` | incident character |
| Confirmed rocket mon | `a<pokemonId>-<form>` | incident slot1 (grunts, confirmed) |
| Event/display | `b<displayType>` | incident display_type (goldstop 7, kecleon 8, showcase 9) |
| Lures | `l<lureId>` | active lure |
| Showcases | `f<pokemonId>-<form>`, `h<typeId>` | showcase pokemon/type (active) |

`conditions` = `{ [rewardKey]: { "<title>-<target>": {title, target} } }` — the per-reward
title/target *options*, supplied by Golbat's maintained conditions map (§6.2) as part of the
aggregate response. The actual title/target *filtering* (matching a stop to a selected condition) is
applied **locally in ReactMap** (`QuestConditions.jsx` / `Pokestop.js:1110`), so Golbat's scan filter
never needs per-fort title/target. ReactMap keeps owning special cases (GO-Fest Mewtwo fallback,
temp-evo type 20, Ditto normalization).

## 6. Golbat design

### 6.1 `FortLookup` changes (`decoder/fortRtree.go`)
`FortLookup` is `xsync.Map[string, FortLookup]` **stored by value** (compact, cache-friendly) — the
purpose-built query index the scan filter already uses. Changes:

1. **`LureExpireTimestamp int64`** + **`ShowcaseExpiry int64`** — populated in `updatePokestopLookup`
   from the `Pokestop` record; used to filter *active* lures/showcases in both the aggregate and the
   scan (follows the existing `RaidEndTimestamp`/`BattleEndTimestamp` precedent; also fixes the scan
   returning expired lures/showcases today, `api_fort.go:140,172`).
2. **Incidents as a slice** — replace the flat incident fields with
   `Incidents []FortLookupIncident{ DisplayType int8; Style int8; Character int16; Confirmed bool;
   Slot1PokemonId int16; Slot1Form int16; ExpireTimestamp int64 }`, mirroring `StationBattles`
   (`fortRtree.go:66`, `api_fort.go:210-227`). Slot1 only (D9).
   - `updatePokestopIncidentLookup` (`fortRtree.go:259`): upsert the incident into the slice by
     incident id, drop expired entries. Full `*Incident` is already in hand at the call site
     (`incident_state.go:157`), so `Confirmed`/`ExpireTimestamp`/slot1 are available.
   - `updatePokestopLookup` (`fortRtree.go:182-218`): preserve the slice across pokestop re-saves
     (as it preserves flat fields today).
   - `isFortDnfMatch` (`api_fort.go:183-195`): iterate the slice, skip expired, match-any — exactly
     the `StationBattles` pattern. Fixes clobbering (~11% of stops), the missing incident-expiry
     check, and slot-1-only matching.

### 6.2 Maintained quest-conditions map (new)
Provides the per-reward `conditions` *options* without scanning or storing strings in `FortLookup`:
- Key: `{ reward signature (with_ar, reward_type, item_id, amount, pokemon_id, form_id), title, target }` → count.
- Reconcile at the quest save path (snapshot the pre-apply quest title/target, then increment the
  new key / decrement the old) and decrement at fort eviction (the evicted `*Pokestop` carries the
  quest title/target) — the `pokemonFormCount` reconcile+eviction pattern, hooked in the quest path
  because `FortLookup` deliberately holds no title/target.
- The endpoint ranges it to emit distinct `(reward, title, target)` per reward key.
- **Only supplies options.** Filtering by a selected title/target is applied locally in ReactMap, so
  the scan filter (`isFortDnfMatch`) never gains title/target.

### 6.3 Endpoint + aggregate (`decoder/api_pokestop.go` new, `routes_huma.go`)
```
GET /api/pokestop/available   — X-Golbat-Secret; 503 when !FortInMemory; whole-instance; no params
```
`GetAvailablePokestops()` builds, all in-memory:
- **quests** — reward keys from the maintained conditions map (or `fortLookupCache` reward fields);
  each tuple carries `title`/`target` for `conditions`.
- **lures** — `fortLookupCache.Range`, `LureExpireTimestamp > now`.
- **showcases** — `fortLookupCache.Range` (`ContestPokemonId/Form/Type`), `ShowcaseExpiry > now`.
- **invasions** — from the *same* `fortLookupCache.Range`, iterating each fort's `Incidents` slice
  (Task 2), `ExpireTimestamp > now`: character→`i`, display_type→`b`, confirmed+slot1→`a`. One range
  covers lures+showcases+invasions; `incidentCache` is **not** scanned.
- **quest cross-check** — the same range also tallies FortLookup reward fields and compares them to
  the maintained conditions map (`verifyQuestAggregate`); any divergence logs/metrics a desync, so
  the map's reconciliation (§6.2) is continuously verified against a bulletproof direct read.

Response (structured tuples; distinct rows + `count`):
```jsonc
{
  "quests":    [{ "with_ar": false, "reward_type": 2, "item_id": 1, "amount": 3,
                  "pokemon_id": 0, "form_id": 0, "title": "challenge_x", "target": 3, "count": 42 }],
  "invasions": [{ "character": 1, "display_type": 1, "confirmed": true,
                  "slot1_pokemon_id": 41, "slot1_form": 0, "count": 5 }],
  "lures":     [{ "lure_id": 501, "count": 12 }],
  "showcases": [{ "pokemon_id": 1, "form": 0, "type_id": 0, "count": 3 }]
}
```
Register one `huma.Register` (new `registerPokestopReadRoutes` or in `registerFortScanRoutes`),
`FortInMemory`-gated, `Security: golbatSecret`.

### 6.4 Instrumentation (D5)
`time.Now()`/`time.Since()` + `log.Infof` per build (like `routes_huma.go:613`): duration + scanned
forts/incidents + per-category counts. Prometheus histogram via `StatsCollector`
(`ObserveApiScan("available-pokestops", seconds)`, mirroring `ObserveDbQuery`).

## 7. ReactMap design (branch off `develop`)

Mirror `Pokemon.getAvailable` (`models/Pokemon.js:874`): `Pokestop.getAvailable({ mem, secret,
httpAuth, ... })` — when `mem` set, `GET {mem}/api/pokestop/available` via `evalQuery`, map tuples
→ existing `{ available, conditions }` (§5). SQL path unchanged when `mem` falsy **or** endpoint 503.
`DbManager.getDbContext` already provides `mem/secret/httpAuth`. No change to
`filters/builder/pokestop.js`, resolvers, `getPokestops` (map-data stays SQL), or the interval.

## 8. Coverage caveats

- **Resident set vs whole DB:** aggregate reflects the ~25h-resident fort set + active incidents —
  fresher, but undercounts reward types only on forts this instance never scans (shared-DB). Keep
  SQL for such connections.
- **`FortInMemory` required** (whole-cache `Range` has no per-key DB fallback) → 503, ReactMap
  falls back to SQL.
- **Base-branch coordination:** design touches `fortRtree.go`/`api_fort.go` (hot incident path) on a
  branch actively reworking eviction; uses only `Get`/`Range` + additive struct fields; rebase and
  re-verify accessor names before merge.

## 9. Phasing

- **Phase 1 (this spec):** endpoint + `FortLookup` `+2 ints` + incident slice + `isFortDnfMatch` fix
  + maintained conditions map + instrumentation; ReactMap hybrid consumer.
- **Phase 2 (separate spec):** add `incidents[]` to the pokestop scan response + per-fort quest
  title/target (for `adv` filtering) → move ReactMap `getPokestops` to Golbat → full API retrieval;
  optionally the "fully aggregated total" for rewards/lures/showcases; retire `getFilterContext()`.

## 10. Testing

**Golbat** — unit: seed `fortLookupCache`/`incidentCache`/conditions-map fixtures (active+expired
lure/showcase/incident; grunt-confirmed-slot1; leader+giovanni and rocket+showcase coexisting stops;
AR+no-AR quests w/ distinct titles) → assert aggregate tuples, expiry exclusion, counts, and
`isFortDnfMatch` per-incident matching. Gating: `!FortInMemory`→503; bad secret→401/403.
Instrumentation emitted.

**ReactMap** — unit: mock response → `getAvailable` yields the same `{available, conditions}` keys as
the SQL path (golden). Fallback: `mem` unset / 503 → SQL. Integration: dev ReactMap → Golbat
(`FortInMemory` on), diff filter options vs SQL baseline via `GET /api/v1/available/quests?current=`.

## 11. Open questions

1. Status code (`200` vs `202` to match `/api/pokemon/available`).
2. Log level (`Info` vs `Debug`) at ~15-min cadence.
3. Whether the maintained conditions map also supplants the `fortLookupCache` reward scan in Phase 1
   or that's left to the Phase-2 "fully aggregated total".
