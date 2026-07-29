# Golbat Fort DNF Scan → ReactMap Map-Data — Design Spec

- **Date:** 2026-07-16
- **Status:** Approved design → planning
- **Golbat branch:** off `feat/pokestop-available-api` (worktree `~/GolandProjects/Golbat-wt/pokestop-available-api`) once that lands; scan infra is pre-existing WIP on that lineage
- **ReactMap branch:** to be cut off `develop`
- **Author:** James Berry (with Claude)
- **Follows:** `2026-07-14-pokestop-available-api-design.md` (this is its "Phase 2")

## 1. Problem

ReactMap renders pokestop/gym/raid **map markers, popups, and search** from direct SQL against the
scanner DB (`Pokestop.getAll` `models/Pokestop.js:188`, `Gym.getAll` `models/Gym.js:114`). `getAll`
runs on **every map pan/zoom** — the highest-frequency fort query ReactMap issues. Golbat already
holds every fort in memory (`pokestopCache`/`gymCache`, whole record) and already exposes a
**DNF fort-scan** over a lightweight spatial index. Routing `getAll` through that scan offloads the
per-pan SQL and, with the DNF filter, lets Golbat do the marker narrowing instead of ReactMap.

The available-list (Phase 1, shipped) already moved to `GET /api/pokestop/available`; this moves the
map-data itself.

## 2. What already exists (Golbat, pre-existing WIP on this lineage)

Four **draft, `FortInMemory`-gated** scan endpoints, built on the pokemon-scan template
(`/api/pokemon/v3/scan`):

| Endpoint | Handler | Response builder | Completeness |
|---|---|---|---|
| `POST /api/gym/scan` | `GymScanEndpoint` | `buildGymResult` → `ApiGymResult` | **complete** (raid + gym detail) |
| `POST /api/pokestop/scan` | `PokestopScanEndpoint` | `buildPokestopResult` → `ApiPokestopResult` | complete **except invasions** |
| `POST /api/station/scan` | `StationScanEndpoint` | `BuildStationResult` → `ApiStationResult` | **complete** (name/battle/stationed/`Battles[]`) |
| `POST /api/fort/scan` | `FortCombinedScanEndpoint` | — | all three in one rtree pass |

Plus single-record fetch: `GET /api/pokestop/id/{fort_id}` (`routes_huma.go:513`) and
`GET /api/gym/id/{gym_id}` (`routes_huma.go:487`), returning the full `BuildPokestopResult`/
`BuildGymResult`. **No station by-id endpoint exists** — added by this work (§7.4).

**Two-phase scan** (`api_fort.go:319-392`): (1) DNF-filter candidate fort ids against the lightweight
value-stored `FortLookup` rtree snapshot via `isFortDnfMatch`; (2) load each matched full record from
the in-memory record cache (`pokestopCache`/`gymCache`, resident when `FortInMemory` is on) and run
the `build*Result` builder. **`FortLookup` is only the filter index; it is never the response
payload.** Request shape mirrors pokemon: `{min, max, limit, dnfFilters[]}`, OR across clauses / AND
within a clause.

`ApiFortDnfFilter` already carries: shared `PowerUpLevel`/`IsArScanEligible`; gym `AvailableSlots`,
`TeamId[]`, `RaidLevel[]`, `RaidPokemonId[]`; pokestop `LureId[]`, `QuestRewardType[]`,
`QuestRewardAmount`, `QuestRewardItemId[]`, `QuestRewardPokemon[]`, `IncidentDisplayType[]`,
`IncidentStyle[]`, `IncidentCharacter[]`, `IncidentPokemon[]`, `ContestPokemon[]`,
`ContestPokemonType[]`, `ContestTotalEntries`.

## 3. Goals / Non-Goals

**Goals**
- Route **`getAll` (markers) and `getOne` (single record)** for **pokestops, gyms/raids, and
  stations** through the fort scan / by-id endpoints when a source has a Golbat `endpoint`, mirroring
  `Pokemon.getAll`'s `mem` branch. DB fallback (dual source) for un-migrated queries and for
  `FortInMemory` off / 503.
- **One per-record mapper per fort type, shared by `getAll` and `getOne`.** The by-id endpoints
  return the same struct as one scan element (`ApiPokestopResult`/`ApiGymResult`/`ApiStationResult`),
  so `getAll` maps the array and `getOne` maps the single with the same code.
- **Value phase is DNF**: Golbat narrows to matching forts so ReactMap ships a fraction of the forts
  and stops running `getAll`'s SQL filter logic. Match-all is only a correctness stepping stone.
- Fill the Golbat gaps: **invasions in the pokestop scan response** (whole `IncidentData` rows fetched
  from `incidentCache` via a **string** fetch handle on `FortLookupIncident`; the `int64` re-key is
  parked → [UnownHash/Golbat#384]), a **station by-id endpoint** (`GET /api/station/id/{id}`) for
  station `getOne`, and **station preload under bare `fort_in_memory`** so the station index is
  complete without the full `Preload` config.
- **Gym + station `getAvailable`** (filter lists) — new `GET /api/gym/available` +
  `GET /api/station/available`, each a single `fortLookupCache.Range` aggregate mirroring
  `/api/pokestop/available`. Unlike pokestops, **no maintained map** is needed: `FortLookup` omits no
  gym/station filter field (the pokestop map existed only for quest title/target).
- Keep `FortLookup` DNF-only; consumers receive **whole records from the record caches**, as if
  loaded from the DB, so new columns appear automatically.

**Non-Goals**
- `search`/`getSubmissions`/`getBadges` migration — left on the DB fallback (low frequency; text
  search / PoI cells / ReactMap-local badge table).
- Fort-id key-representation change (§10, deferred).
- Any DB **schema** change (incident id stays `varchar` on disk).

## 4. Decisions (locked)

| # | Decision |
|---|---|
| D1 | Migrate `getAll` (markers) **and `getOne`** for **pokestops, gyms/raids, and stations** to the fort scan / by-id endpoints — `getOne` reusing each type's per-record `getAll` mapper. `search`/`submissions`/`badges` stay on the bound DB (dual source). |
| D2 | `FortLookup` stays **DNF-only** (filter fields the scan reads). Response = **whole records** from `pokestopCache`/`gymCache`/`incidentCache`, like a DB load — future-proof to new columns. |
| D3 | Incidents in the pokestop scan via **Option A**: an `Id` fetch handle on `FortLookupIncident`; whole `IncidentData` row loaded from `incidentCache`. Gated by a **`with_incidents`** request flag (default off; ReactMap sets it when the invasion layer is active). Read-through to DB on the rare cache miss. |
| D4 | Incident fetch handle (D3) is a plain **`string`**: `incidentCache` stays string-keyed; `FortLookupIncident.Id string` copied from `incident.Id`; `incidentCache.Get(id)` directly — no new type, no parse. The `int64` re-key (native-int key via `Int64Str`) is **parked** → [UnownHash/Golbat#384]. Rationale: the incident id is a proto *string* (`PokestopIncidentDisplayProto.IncidentId`, set with no parse), so a re-key needs a fallible parse-on-ingest with a silent-drop mode; the feature doesn't need it. Ids are numeric on the validating instance (1091/1091), but the string handle carries zero portability risk. |
| D5 | Filter: **match-all MVP** (Golbat returns all forts in bbox; ReactMap's existing `secondaryFilter` unchanged) → **DNF immediately after** (translate ReactMap's filter to `ApiFortDnfFilter`). Back-to-back, DNF is the deliverable. |
| D6 | **Gyms first** — zero Golbat change (response complete, DNF fields exist), so both match-all and DNF land end-to-end with no Golbat work. Pokestops second (need D3). |
| D7 | Area restriction moves from SQL `ST_CONTAINS` to app-side `filterRTree` (already wired for `Pokemon`, add for forts). |
| D8 | Dual-source `getAll`: `mem` branch → scan endpoint, else/​on-503 → bound DB. `deDupeResults` keeps the larger `updated`, so the endpoint must return `updated` in **unix seconds** matching SQL. |
| D9 | Fort-id key representation stays **string** (128-bit hex + `.NN` suffix; can't collapse to `uint64`). Deferred, profile-first (§10). |
| D10 | 25h resident-cache eviction gap **accepted** (raise the TTL if it ever bites); the bound DB source is also a completeness floor. |
| D11 | Ship as **one Golbat PR** (G1+G2) + **one ReactMap PR** (gyms+pokestops, match-all+DNF), each built whole but with **review/test checkpoints** at intermediate states. `with_incidents` = body field on `ApiFortScan`; DNF via a fort filter `Backend` mirroring `PkmnBackend`. |

## 5. Id analysis (drove D4, D9)

- **Fort ids (pokestop/gym):** e.g. `85e40e3a838b41a08589eb19fc35611b.16` — 32 hex chars (**128-bit**)
  + a `.NN` byte-ish suffix = 35 chars, exactly `varchar(35)`. Wider than `uint64`; a fixed
  `[16]byte`+tag key is the only non-lossy option and only sheds string overhead on a 128-bit hash —
  marginal. **Stays string** (D9).
- **Incident ids:** e.g. `-1016089077232382347` — numeric (signed int64) as decimal-string values in
  `varchar(35)` on the validating instance (1091/1091). But the proto field
  (`PokestopIncidentDisplayProto.IncidentId`) is a **`string`** set with no parse — unlike pokemon's
  `EncounterId uint64` — so an `int64` re-key needs a fallible parse-on-ingest (silent-drop mode for a
  non-numeric id). The feature doesn't need it, so this PR uses a **string** fetch handle (D4) and the
  int64 re-key is parked → [UnownHash/Golbat#384].

## 6. Incident handle: string (int64 re-key parked → #384)

This PR adds the fetch handle as a plain `string` (`FortLookupIncident.Id string`, copied from
`incident.Id`; `incidentCache.Get(id)` directly) — no new type, no re-key, no parse. The `int64`
optimization (a signed `Int64Str` mirroring `decoder/uint64str.go` + an `int64`-keyed `incidentCache`,
the way `pokemonCache` keys pokemon by `uint64`) is recorded in **[UnownHash/Golbat#384]** with its
prerequisites (universal-numeric confirmation + a profile). Not in scope here.

## 7. Golbat design

### 7.1 Incident fetch handle — string (D4)
- `FortLookupIncident` gains `Id string` (`station_battle.go:51`), copied in
  `updatePokestopIncidentLookup` (`fortRtree.go:270`) from `incident.Id` (the full `*Incident` is
  already in hand) — rides the existing atomic `Compute`, no new structure/lock ordering, no re-key.
- No `Int64Str`, no `incidentCache` re-key, no parse. (The `int64` optimization is parked → #384.)

### 7.2 Invasions in the pokestop scan (D3)
- `ApiPokestopResult` gains `Invasions []ApiPokestopIncident` (whole-row shape: character/display_type/
  style/confirmed/slots/expiration/etc.). Populated **only when `with_incidents`** is set: for each
  matched fort, `fortLookupCache.Load(fortId)` (the scan's phase-2 loop has only the fort-id string,
  not the `FortLookup` — `api_fort.go:349`), iterate its `Incidents`, `incidentCache.Get(inc.Id)` →
  whole `IncidentData` → map. `getOne` by-id does the same `fortLookupCache.Load(fortId)`.
- **Miss path:** incident lifetime (~30–60 min) ≪ cache TTL (~25h), so an active incident is
  essentially always resident; the `Get` miss is a rare race → read through `getIncidentRecordReadOnly`
  (existing DB-load) to preserve the whole-row contract. Expired entries skipped by `ExpireTimestamp`.
- `WithIncidents bool` added to the shared **`ApiFortScan` request body** (`json:"with_incidents"`,
  `required:"false"`, default false) — matches the scan endpoints' all-fields-in-POST-body pattern.
  Honored by the pokestop and combined `/fort/scan` handlers; gym/station ignore it. (By-id `getOne`
  attaches incidents unconditionally, or gains its own query flag if payload matters.)

### 7.3 No `FortLookup` display-widening
`FortLookup` keeps only what `isFortDnfMatch` reads (incl. the slot1 incident projection it filters
on). The id in 7.2 is a **record locator**, not display data. Whole invasion rows come from
`incidentCache`, whole pokestop rows from `pokestopCache`, whole gym rows from `gymCache`.

### 7.4 Station by-id endpoint (new) — for station `getOne`
`GET /api/station/id/{station_id}` → `BuildStationResult`, mirroring the existing gym/pokestop by-id
routes (`routes_huma.go:487,513`): `FortInMemory`-gated, `Security: golbatSecret`, read-through to DB
on cache miss. Trivial; the only Golbat addition stations need (their scan response is already
complete). Pokestop/gym by-id already exist; the pokestop by-id should attach incidents (via
`fortLookupCache.Load(fortId)`) so `getOne` popups carry invasions — for a single record, attach them
unconditionally rather than behind `with_incidents`.

### 7.5 Station preload under bare `fort_in_memory` (trivial)
`PreloadForts` (`preload.go:63`, the bare-`fort_in_memory` path) loads pokestops (`:106`) and gyms
(`:171`) but **not** stations; only the full `Preload` (`:16`) calls `preloadStations` (`:211`) +
`preloadStationBattles` (`:48`). Add those two existing calls to `PreloadForts` so a
`fort_in_memory`-only instance indexes stations (and their battles) completely — otherwise station
scans undercount until each station is next touched. ~2 lines; both functions already exist.

### 7.6 Gym + station `getAvailable` (new endpoints — pure scans)
`GET /api/gym/available` and `GET /api/station/available`, each a single `fortLookupCache.Range` over
`FortType == GYM`/`STATION`, structured like `GetAvailablePokestops` (`api_pokestop_available.go`) but
**without** the maintained-map machinery:
- **Gym** — per resident gym emit team/slots (`TeamId`,`AvailableSlots`); and if `RaidEndTimestamp >
  now && RaidLevel > 0`, the raid level, plus a boss `(RaidPokemonId,RaidPokemonForm)` when
  `RaidPokemonId != 0` else an egg at `RaidLevel`. Structured tuples + counts; ReactMap builds
  `t`/`g`/`r`/`e`/boss keys.
- **Station** — per resident station iterate `StationBattles` (or the top battle), `BattleEndTimestamp
  > now`, emit `(BattlePokemonId,BattlePokemonForm,BattleLevel)`. ReactMap builds `<id>-<form>`/`j`
  keys.
- No `FortLookup` change, no reconcile, no cross-check — every field is already present and DNF-used.
  `FortInMemory`-gated, `Security: golbatSecret`, instrumented like §6.

## 8. ReactMap output contract to reproduce

`Pokestop.getAll`/`Gym.getAll` return marker objects consumed by the GraphQL resolvers and the map.
The mapper must reproduce the SQL path's fields (perms-gated in `secondaryFilter`, which stays as-is):

**Pokestop** — core `id,lat,lon,enabled,url,name,last_modified_timestamp,updated`; `ar_scan_eligible,
power_up_points,power_up_level,power_up_end_timestamp`; `lure_id,lure_expire_timestamp`; `quests[]`
(both AR/no-AR layers, reward-type-specific fields + `title`); `invasions[]` (`grunt_type,display_type,
confirmed,incident_expire_timestamp,slot_1_*`, slots 2/3 null); `events[]` (showcase fields).
Filter-key vocabulary (for DNF): `l`, `q/d/u/p/c/x/m`(+pokémon), `i/a/b`, `f/h`.

**Gym** — core `id,name,url,lat,lon,updated,last_modified_timestamp`; gym `team_id,available_slots,
ex_raid_eligible,ar_scan_eligible,in_battle,guarding_pokemon_id,guarding_pokemon_display,defenders,
total_cp,power_up_*`; raid `raid_level,raid_battle_timestamp,raid_end_timestamp,raid_pokemon_id/form/
gender/costume/evolution/move_1/move_2/alignment`; computed `hasRaid`/`hasGym`. Filter vocabulary:
`e`(egg tier), `t`(team), `g`(team-slots), raid-boss `<id>-<form>`(+gender). `getBadges` stays on the
ReactMap-local DB (out of scope).

**Station** (`Station.getAll` `models/Station.js:588`) — core `id,name,lat,lon,updated`; battle
`start_time,end_time,is_battle_available,battle_level,battle_pokemon_*`(id/form/costume/gender/
alignment/bread_mode/move_1/move_2), `total_stationed_pokemon,total_stationed_gmax,stationed_pokemon`,
`battles[]`. Filter vocabulary: `onlyMaxBattles`, `onlyBattleTier`, `onlyGmaxStationed`,
`onlyIncludeUpcoming` → DNF `BattleLevel[]`/`BattlePokemon[]`. `ApiStationResult` already carries all
of this.

**`getOne`** (all three types): the by-id endpoint returns one `Api*Result`; run it through the same
per-record mapper `getAll` uses, then return it. ReactMap's `getOne` today yields only `lat,lon` for
recenter — the endpoint superset is harmless.

Invasion mapping from the whole incident row: `Character→grunt_type`, `DisplayType→display_type`,
`Confirmed→confirmed`, `ExpirationTime→incident_expire_timestamp`, `Slot1PokemonId→slot_1_pokemon_id`,
`Slot1Form→slot_1_form` (covers kecleon 8 / goldstop 7 blocker display types too).

## 9. ReactMap design

**Endpoint response shapes — read these, don't assume a bare array.** The fort *scan* endpoints
return an **envelope**, NOT a bare array like `/api/pokemon/v2/scan` (which the `Pokemon.getAll`
template mirrors). A `getAll` `mem` branch must read the typed array off the envelope, or
`Array.isArray(res)` is always false and it silently falls back to SQL on a healthy 200 (the bug that
bit the gyms slice — fixed by reading `res.gyms`):

| Endpoint | Response | `getAll`/consumer reads |
|---|---|---|
| `POST /api/gym/scan` | `{ gyms:[], examined, skipped, total }` | `res.gyms` |
| `POST /api/pokestop/scan` | `{ pokestops:[], examined, skipped, total }` | `res.pokestops` |
| `POST /api/station/scan` | `{ stations:[], examined, skipped, total }` | `res.stations` |
| `POST /api/fort/scan` (combined) | `{ gyms:[], pokestops:[], stations:[], examined, skipped, total }` | per type |
| `GET /api/{gym\|pokestop\|station}/id/{id}` | bare `Api*Result` object | check `res.lat`/`res.lon` |
| `GET /api/gym/available` | `{ teams:[], raids:[] }` | `res.teams`/`res.raids` |
| `GET /api/pokestop/available` | `{ quests:[], invasions:[], lures:[], showcases:[] }` | those arrays |
| `GET /api/station/available` | `{ battles:[] }` | `res.battles` |

Diagnose fallbacks with a shared `describeScannerResponse(res)` (HTTP status / body shape / network),
not a hard-coded "fort_in_memory off" guess.

Mirror `Pokemon.getAll` (`models/Pokemon.js:131`, `mem` branch), for `Pokestop`/`Gym`/`Station`:

- **`getAll` `mem` branch**: `POST {mem}/api/{gym|pokestop|station}/scan` with
  `{min,max,limit,filters,with_incidents?}` via `evalQuery` (sets `X-Golbat-Secret`/httpAuth); read the
  matched array off the **envelope** (`res.{gyms|pokestops|stations}`, per the table above). On 503
  / network error / any non-envelope response → fall through to the SQL block (dual source: bound DB
  runs it; pure-endpoint source drops out of `Promise.allSettled`). Plumbing from the Phase-1 available
  PR is reused.
- **`getOne` `mem` branch**: `GET {mem}/api/{gym|pokestop|station}/id/{id}` → the **same per-record
  mapper**; SQL fallback otherwise.
- **Pure per-record mappers** `mapGymResult` / `mapPokestopResult` / `mapStationResult` (like
  `pokestopAvailableMapper.js`), each projecting one whole-record `Api*Result` into the marker shape
  `secondaryFilter` expects — shared by that type's `getAll` (map the array) and `getOne` (map the
  single). `secondaryFilter` (perms) is source-agnostic and stays unchanged.
- **Area restriction (D7):** post-filter results through `filterRTree` (Golbat can't know ReactMap's
  area polygons), as `Pokemon.getAll`/`.search` already do. New wiring for forts.
- **`updated` (D8):** ensure the mapper carries `updated` as unix seconds so `deDupeResults` behaves in
  dual DB+endpoint mode.
- **Filter (D5):**
  - *Phase 1 — match-all:* send an empty/permissive filter, let `secondaryFilter` do all matching in
    JS. Correct by construction; unchanged filter logic. Caveat: returns all bbox forts (dense-city
    volume).
  - *Phase 2 — DNF:* a **fort filter `Backend`** with `buildApiFilter()`, mirroring
    `server/src/filters/pokemon/Backend.js` (`PkmnBackend`), translates ReactMap's flat category
    filter to `ApiFortDnfFilter[]` (one disjunct per active category). Sub-filters `FortLookup` can't
    express (quest **title/target**) stay JS post-filters via `secondaryFilter`, as in Phase 1's
    available work. This is the payoff phase.

## 10. Deferred / follow-ups

- **Fort-id `[16]byte` key (D9):** profile fort-id hashing first (likely dominated by the rtree walk +
  DNF compares); only worth it if measured, and it touches every fort cache + rtree + both boundaries
  with an outlier-validation burden. What the `.NN` suffix means (S2 level? source tag? — determines
  whether it's in the key) is an open input.
- **Incident `int64` re-key → [UnownHash/Golbat#384]** — parked. `Int64Str` + `int64`-keyed
  `incidentCache` + `int64` handle. Prereqs: confirm incident ids are numeric across all deployments
  (the proto field is a string), and profile the win. This PR uses a string handle instead.
- `search`/`getSubmissions`/`getBadges` stay on the DB (§3 non-goals) — migrate later only if the
  per-record SQL they issue proves worth it.
- **Combined `/api/fort/scan` via a request-scoped batch (optimization).** ReactMap currently hits
  `/api/{gym,pokestop,station}/scan` once **per enabled fort layer** (each an independent GraphQL
  resolver → `SubModel.getAll`), so a 3-layer view makes 3 separate rtree passes + round-trips. The
  combined `POST /api/fort/scan` returns `{gyms,pokestops,stations,examined,skipped,total}` in one
  rtree pass. To adopt it: add a **request-scoped memoized fetch** keyed by `(bbox, source)` — the
  first model's `getAll` triggers `/api/fort/scan`, the others read the cached envelope and take their
  slice. To avoid over-fetching layers that are off, add an **overall scope field to `ApiFortScan`**
  (e.g. `fort_types: ["gym","station"]`, honored by `FortCombinedScanEndpoint`) so the combined scan
  only processes/returns the enabled types — cleaner than trying to suppress a whole type via DNF
  clauses. Best sequenced **after DNF** (so the per-type `ApiFortDnfFilter[]` union is settled). Golbat
  side = the `fort_types` scope field; ReactMap side = the batch/dedupe layer.
- **`onlyManualId` out-of-viewport (cross-model).** The `getAll` `mem` branches (gym, station, and
  future pokestop) send only the bbox, so a manually-targeted deep-link station/gym *outside* the
  viewport isn't returned (the SQL path pulls it via `... OR id = manualId`). Mirror `Pokemon.getAll`'s
  manual-id fallback (a `GET /api/{type}/id/{id}` when the bbox scan didn't include the pinned id).
  Shared across all fort `mem` models — do once.

## 11. Packaging & build order (D11)

**Two PRs**, each built whole but with **pause-for-review/test checkpoints** at intermediate states.

**Golbat PR** = §7.1–§7.6: incident **string** fetch handle on `FortLookupIncident`; `with_incidents`
+ `Invasions[]` on the pokestop scan (+ pokestop by-id incidents); `GET /api/station/id/{id}`; station
preload under `fort_in_memory`; `GET /api/gym/available` + `GET /api/station/available` (pure scans).
Standalone-testable via `curl` on the scan / by-id / available routes. Ships and deploys **first** —
it's the dependency for the pokestop `getAll` half of the ReactMap PR (gyms/stations `getAll`, all
`getOne`-by-id, and gym/station `getAvailable` don't need it, but a single deploy is simplest).

**ReactMap PR** = the full consumer (pokestops + gyms + stations; `getAll` match-all → DNF; `getOne`;
gym/station `getAvailable`), built match-all-first so a cross-fort-type state is testable early
(matches the "move quickly past match-all" intent):

| Step | Content | Dep | Checkpoint |
|---|---|---|---|
| 1 | **Gyms** `getAll` mem branch + per-record mapper + `filterRTree` (match-all) + `getOne` + `getAvailable` | — | test gyms |
| 2 | **Stations** `getAll` + mapper + `getOne` + `getAvailable` (match-all) | — | |
| 3 | **Pokestops** `getAll` (+`with_incidents`) + mapper + `getOne` (match-all) | Golbat PR | **test all three, match-all** |
| 4 | **Gyms / Stations / Pokestops** DNF (fort `Backend`) | 1–3 | **test full DNF** |

(Gym/station `getAvailable` mirror the shipped `Pokestop.getAvailable` `mem` branch — small, and they
depend only on the two new available endpoints, not on the scan work.)

Gyms + stations (steps 1–2) carry **no Golbat dependency**, so they prove the whole consumer pattern
(per-record mapper shared by `getAll`/`getOne`, dual source, `filterRTree`, match-all→DNF) even before
the Golbat PR is built. `getOne` rides each type's mapper, so it's folded into that type's step, not a
separate phase. Exact checkpoint grouping is a build-time call; the natural pauses are after all-three
match-all and after full DNF.

## 12. Coverage caveats

- **Resident set vs whole DB (D10):** the scan reflects the ~25h-resident fort set; long-idle forts in
  unscanned regions can drop until re-touched. Accepted (raise TTL if needed); the bound DB source
  remains a floor.
- **`FortInMemory` required** → 503 → SQL fallback (dual source).
- **Stations:** the bare-`fort_in_memory` preload gap is *fixed here* (§7.5), so stations are indexed
  from startup like pokestops/gyms — no longer a standing caveat.
- **Gym/station `getAvailable` semantics (§7.6):** the SQL station-available adds `is_inactive=false`
  + `updated > activeCutoff` hygiene filters; the scan approximates "active" via `BattleEndTimestamp >
  now` + residency (a station with no live battle emits no keys anyway). Gym team/slots have no time
  filter (all resident gyms). Both are the same resident-set approximation as D10 — accepted.
- **Base-branch coordination:** §7 touches `fortRtree.go`/`incident_state.go`/`api_fort.go`/`preload.go`
  on a branch reworking eviction/locking; keep changes additive; rebase and re-verify accessor names.

## 13. Testing

**Golbat** — `FortLookupIncident.Id` carried through `updatePokestopIncidentLookup` (concurrency test
still green). `with_incidents` on/off, multi-incident stop (~11%), whole-row fields incl. slot1,
cache-miss read-through, expired skipped, `getOne` by-id incident attach. Gym/station `available`
aggregates (team/slots/raid/egg; battle level/pokemon incl. multi-battle) with expiry exclusion.
Station by-id returns the record; station preload under `fort_in_memory` indexes stations. Gating:
`!FortInMemory`→503 on the gated routes.

**ReactMap** — mapper golden vs SQL `getAll` on live data (gyms first, then pokestops incl. invasions),
per perms gate; dual-source fallback (503 → SQL); `filterRTree` area exclusion; DNF phase: assert the
translated filter returns the same marker set as match-all + JS filtering on the same bbox.

## 14. Resolved (were open)

1. `with_incidents` — **body field** on `ApiFortScan` (`json:"with_incidents"`, default false),
   matching the scan endpoints' all-fields-in-POST-body pattern; honored by pokestop + combined scans.
2. DNF translation — a **fort filter `Backend`** with `buildApiFilter()`, mirroring
   `server/src/filters/pokemon/Backend.js` (`PkmnBackend`).
3. Packaging — **one Golbat PR** (§7.1–§7.6) + **one ReactMap PR** (gyms+pokestops+stations,
   match-all+DNF), with review/test checkpoints (§11); DNF is built in the same PR, not a follow-up.
4. Incident id typing — **string handle** this PR; `int64` re-key parked → [UnownHash/Golbat#384].
