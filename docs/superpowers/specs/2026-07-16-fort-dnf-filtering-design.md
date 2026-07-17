# Fort DNF Filtering — Design Spec

- **Date:** 2026-07-16
- **Status:** Approved design → planning
- **Golbat branch:** `feat/fort-scan-map-data` (worktree `~/GolandProjects/Golbat-wt/pokestop-available-api`), PR #385 — still open
- **ReactMap branch:** `feat/fort-consumer`, PR #1228 — still open
- **Author:** James Berry (with Claude)
- **Extends:** `2026-07-16-fort-scan-map-data-design.md` — this is its "Phase 2 (DNF)", the payoff phase after match-all shipped.

## 1. Problem

`Pokestop/Gym/Station.getAll` now fetch map markers from Golbat's fort-scan endpoints, but send
`filters: []` (match-all): Golbat returns **every fort in the viewport** and ReactMap's `secondaryFilter`
does 100% of the narrowing in JS. In dense cities a specific-item filter (a rare quest reward, a raid
boss, a battle pokemon) still ships the whole viewport over the wire. DNF pushes that narrowing into
Golbat's rtree scan so it returns a fraction of the forts.

## 2. What already exists (Golbat, PR #385)

`ApiFortDnfFilter` + `isFortDnfMatch` + the `FortLookup` index are implemented and wired into all four
scan endpoints. Today they already DNF-match:

- **Pokestop:** `lure_id`, `quest_reward_type`, `quest_reward_item_id`, `quest_reward_pokemon` (id+form),
  `quest_reward_amount` (min/max) — matched against **either** the AR or non-AR quest; `incident_character`,
  `incident_display_type`, `incident_style`, `incident_pokemon` (slot-1 id+form); `contest_pokemon` (id+form),
  `contest_pokemon_type`, `contest_total_entries`.
- **Gym:** `team_id`, `available_slots` (min/max), `raid_level`, `raid_pokemon` (id+form) — raid fields only
  match gyms with an active raid.
- **Station:** `battle_level`, `battle_pokemon` (id+form) — matches the multi-battle list; only stations
  with an active battle.
- **Shared:** `power_up_level` (min/max), `is_ar_scan_eligible` (true only; `false` is a no-op — irrelevant,
  ReactMap only ever filters *for* AR-eligible).

**Request body** (unchanged — the mem branches already post this shape): `{ min, max, limit, with_incidents,
filters: [ApiFortDnfFilter] }`. `filters` is **OR across clauses, AND within a clause**; a null/omitted
list field inside a clause = no constraint; **an empty/omitted top-level `filters` array = match all forts
of that type** (`api_fort.go:288-290`). Response envelope carries `examined` (forts examined in the
viewport), `skipped` (cache misses), `total` (whole-index size), and the matched `<type>[]` array.

## 3. Architecture — DNF narrows, `secondaryFilter` finalizes

**The safety model:** `secondaryFilter` (and the station JS gate) **stays and runs after every fetch**.
DNF is therefore a best-effort **superset** narrow; `secondaryFilter` guarantees exactness. Two
consequences:

1. **Correctness is never at risk from an imperfect DNF translation.** Anything DNF over-returns,
   `secondaryFilter` drops. The single hard invariant: a DNF translation must **never be stricter** than
   the real filter (never under-return / drop a fort that should show).
2. **Anything DNF can't express stays in `secondaryFilter` as residual** — cleanly, no new code. That is
   where quest **title/target** (`adv` substring set-membership), raid/battle **gender**, gym
   **ex-eligible/in-battle**, invasion **confirmed**, and station **upcoming/time-window** gates live.

### 3.1 The poisoning rule (load-bearing)

ReactMap's fort filters combine with **OR** (a fort shows if it matches *any* active category). So a
backend may emit narrowing clauses **only if it can express every active category**. If any active
category is a match-all toggle (`onlyAllPokestops`, `onlyGyms`, `onlyAllStations`, …) or an
unexpressible gap (gym ex/in-battle, invasion-confirmed), the backend returns **`[]` (match-all)** for
the whole query — a correct superset; `secondaryFilter` narrows. Otherwise it emits **one clause per
active category** (OR-across). This keeps the superset invariant airtight.

## 4. ReactMap — three fort filter backends

New pure modules under `server/src/filters/fort/`: `pokestop.js`, `gym.js`, `station.js`, each exporting
`build<Type>DnfFilters(filters, ctx) → ApiFortDnfFilter[]`. Pure/dependency-light (mirrors `PkmnBackend`'s
`buildApiFilter` shape but without the PVP/IV class machinery), node-golden testable like the mappers.
Each `getAll` mem branch swaps `filters: []` for `build<Type>DnfFilters(args.filters, ctx)`.
`secondaryFilter` is **untouched**.

**Clause shape** (matches `ApiFortDnfFilter` json tags): a JS object per active category, with only the
constrained fields set (unset = unconstrained); id+form pairs as `{ pokemon_id, form }` (form omitted =
any form); ranges as `{ min, max }`.

### 4.1 Per-type translation (into existing Golbat fields)

**Pokestop** (`Pokestop.js` `secondaryFilter` key switch is the source of truth):
- `l<id>` → `lure_id: [id]`
- quest reward keys → one clause with `quest_reward_type`/`quest_reward_item_id`/`quest_reward_pokemon`:
  `q<item>`→type 2 + `quest_reward_item_id:[item]`; `d<amt>`→type 3; `p<amt>`→type 1; `c<pk>`→type 4 +
  `quest_reward_pokemon:[{pk}]`; `x<pk>`→type 9 + pokemon; `m<pk>-<amt>`→type 12 + pokemon; bare
  `<pk>[-<form>]`→type 7 + `quest_reward_pokemon:[{pk,form}]`; `u<type>`→`quest_reward_type:[type]`.
  **`adv` title/target is dropped from the clause** (residual). When `.all` is false and `adv` is present
  the clause is a strict superset; `secondaryFilter` applies the title/target substring check.
- `i<char>`→`incident_character:[char]`; `b<type>`→`incident_display_type:[type]`;
  `a<pk>-<form>`→`incident_pokemon:[{pk,form}]` (residual `confirmed` check stays JS).
- `f<pk>-<form>`→`contest_pokemon:[{pk,form}]`; `h<type>`→`contest_pokemon_type:[type]`.
- `onlyArEligible` → `is_ar_scan_eligible: true`; `onlyLevels` (power-up) → `power_up_level:{min,max}`.
- `onlyAllPokestops` → match-all (`[]`).

**Gym** (`Gym.js` `secondaryFilter` key switch):
- `t<team>-0` → `team_id:[team]`; `g<team>-<slot>` → `team_id:[team]` + `available_slots:{min,max}` from the
  slot index; `e<tier>` → `raid_level:[tiers…]`; bare `<id>-<form>` → `raid_pokemon:[{id,form}]` (gender →
  residual). Ignore the dead `r<tier>` keys (unused in `getAll`).
- `onlyArEligible`→`is_ar_scan_eligible:true`; `onlyLevels`→`power_up_level`.
- `onlyGyms`/`onlyAllGyms`, `onlyExEligible`, `onlyInBattle` → match-all (`[]`) — the last two are gaps
  (residual).

**Station** (`Station.js` `matchesStationBattleFilter`/key parsing):
- `onlyBattleTier`/`j<lvl>` → `battle_level:[lvls…]`; bare `<id>-<form>` → `battle_pokemon:[{id,form}]`
  (gender → residual).
- `onlyGmaxStationed` → **new** `stationed_gmax: true` (§5) — a direct `total_stationed_gmax > 0` column
  test, DNF-clean.
- `onlyInactiveStations` / the active-vs-inactive gate, `onlyIncludeUpcoming`, and the
  `battle_start<=ts`/`battle_end>ts` windows → **residual**. These are **now-relative time-window**
  predicates (`Station.js:745-954`: `end_time`/`updated` vs `activeCutoff`/`inactiveCutoff`), which DNF's
  static filter fields cannot express; `secondaryFilter`'s `passesTimeGate` keeps applying them. (The
  `is_inactive` *column* is not the active/inactive gate `getAll` uses, so it is deliberately **not** a
  DNF field.)
- `onlyMaxBattles` alone with no per-battle key → `[{station_active:true}]`; `onlyAllStations` →
  `[{station_active:true}]` (All-Stations mode only ever shows ACTIVE stations — no poison needed).
- **`station_active` (added post-live-testing):** stations are the one ephemeral fort type; expired
  stations accumulate in the index, and a match-all scan shipped 1330 stations of which 174 were live
  (−1156 residual). `station_active:true` (Golbat: `StationEndTimestamp > now`, mirroring the
  raid/lure/battle now-gating) is stamped into every station clause. The `updated > activeCutoff`
  config cutoff and the inactive mode's day-based cutoff stay residual; `onlyInactiveStations` still
  poisons (it needs expired stations OR filtered actives).

## 5. Golbat station gap-fill (folds into #385)

**One** new station DNF dimension, following the established pattern (`FortLookup` field + populator in
`updateStationLookup`/`updateStationLookupWithBattles` + `ApiFortDnfFilter` field + `isFortDnfMatch`
clause + golden snapshot):

- **`stationed_gmax *bool`** (`ApiFortDnfFilter`) → when `true`, matches stations with
  `FortLookup.TotalStationedGmax > 0` (new `int16` field, populated from `Station.TotalStationedGmax`).
  This is a direct, now-independent column test that matches `getAll`'s `onlyGmaxStationed` gate exactly.

`is_inactive` is **not** filled: `getAll`'s active/inactive gate is a now-relative time-window
computation (§4.1), not the `is_inactive` column, so a column filter would under-return. It stays
residual. Gym ex/in-battle, raid/battle gender, and invasion-confirmed also remain residual (§8).

**Live-testing postmortem — exact key semantics (the `-997` residual).** The observed 1030→10 quest
residual was NOT staleness (verified: zero stale/expired/NULL-expiry quests in the DB; the clearing
routines refresh DB, record cache and FortLookup together). A `quest_seen_after` freshness gate was
briefly added on that wrong theory and **reverted** (`503a4c5`). The true cause was in the ReactMap
translation: ReactMap quest keys are **exact** — a bare `<id>` key means "reward carries no form_id"
(`secondaryFilter` computes a bare key only when `quest_form_id` is null), and users accumulate
thousands of enabled keys from past rotations (client `deepMerge` never prunes). The backend
translated bare keys to `{pokemon_id}` — Golbat's **any-form wildcard** — so stale keys matched every
form of the species, including the current rotation's (`25-2825`, …): 997 over-returned stops that
exact-key matching then dropped. Fix (`50653c77`): **form-exact pairs everywhere** — bare/formless →
`form:0` (the pokemon-API pattern: proto `FORM_UNSET`=0, `FortLookup` NULL→0), explicit `<id>-<form>`
→ exact — and **one clause per reward type** (2/4/7/9/12 + type-only) so e.g. a candy pair can't
cross-match an encounter stop. No Golbat change needed; no
availability coupling (an enabled∩available intersection was considered and rejected — it would
inherit the availability refresh window as an under-return risk).

Two further exactness gaps surfaced and were closed in the same investigation:

- **Form-pinning on form-agnostic keys (latent under-return).** Candy/xl/mega keys (`c25`/`x64`/
  `m150-150`) carry **no form component**, so their match is form-agnostic by construction — the exact
  translation is the form **wildcard** (form omitted), not `form:0`. Pinning `form:0` would fail to
  match a formed reward if one ever appeared, while the key would match it. The fort matcher's
  convention was verified identical to the pokemon API's (`Form *int16`, null = any, set = exact; the
  `-1` in the pokemon v2/v3 scanners is an internal bucket sentinel, never a wire value).
- **Historic `<id>-0` encounter keys (the last −56).** Older ReactMap generated `id-0` quest keys;
  current code normalizes form-0 to a bare key. `id-0` only matches a stop whose reward carries an
  EXPLICIT `form_id: 0`, which reward JSON never contains (verified: 0 rows) — the keys are dead. But
  translated as `{id, form:0}` they collide with Golbat's NULL→0 form collapse and match every
  formless stop of the species. They are **dropped** from clauses (same accepted-divergence class as
  the availableMapper §form note). Amounts are also exact where the key carries one: mega keys group
  into per-amount clauses with `quest_reward_amount {amt,amt}`; `d<amt>`/`p<amt>` emit one
  amount-exact clause each; `u<type>` stays type-level by design.

**Verified live (2026-07-17):** `DNF(5): 8 matched → 8 after secondaryFilter (−0 residual)`, drop
reasons all zero — exact key parity end-to-end (previously 1030 matched → 10, −1020 residual). From
2373 forts scanned, Golbat ships only the rendered set.

## 6. Observability — the DNF-tuning log

Each fort mem branch, DNF path, after `secondaryFilter`, logs the two filter stages so the DNF gap is
visible per query:

```
[POKESTOP] DNF(<N> clauses): <examined> in viewport, −<examined-returned> by DNF → <returned>, −<returned-final> by secondaryFilter → <final> final
```

- `<N> clauses` (0 = match-all sent) from the backend output length.
- `examined` from the response envelope; `returned` = `res.<type>.length`; `final` = post-`secondaryFilter`.
- A large **`−… by secondaryFilter`** (residual drop) flags a filter combination where DNF is leaving
  narrowing on the table — the signal for whether to close a gap for that case. A match-all query
  (`0 clauses`) with a big residual drop is the clearest "should this become a DNF field?" candidate.

Emit at `log.info` on the DNF path (replacing/extending the existing per-type info line); keep the SQL
fallback warnings as they are.

## 7. Testing

- **Backend unit goldens** (pure, plain `node`): per type, assert (a) each filter-key family produces the
  expected clause; (b) the **poisoning rule** — a match-all toggle or a gap category present ⇒ `[]`; (c)
  the **superset invariant** on the tricky cases (quest `adv` present ⇒ clause omits title/target;
  gender present ⇒ clause omits gender). No test framework; throwaway goldens + eslint/prettier.
- **Golbat**: a small `isFortDnfMatch` unit test for the new `stationed_gmax` field (matches when
  `TotalStationedGmax > 0`, wildcard when null). No new `Api*Result` field, so the golden/completeness
  tests are unaffected.
- **Live parity gate (acceptance):** for a viewport + representative filters, the DNF result after
  `secondaryFilter` must **equal** the match-all result after `secondaryFilter` (same markers). Since
  `secondaryFilter` runs both ways, any divergence is a DNF **under-return** bug. Exercise: a rare quest
  reward, a raid boss, an invasion type, a battle pokemon, `onlyGmaxStationed`, `onlyInactiveStations`,
  and a mixed filter+match-all-toggle (poisoning) case.

## 8. Non-goals / deferred

- **Gym `ex_raid_eligible` / `in_battle` DNF** and **raid/battle/invasion gender & invasion-confirmed
  DNF** — stay `secondaryFilter` residual. Fill later only if the observability log shows they cost real
  over-fetch. (Gender & quest title/target are structurally inexpressible in DNF and stay residual
  permanently.)
- **Station `onlyInactiveStations` / active-vs-inactive / `onlyIncludeUpcoming`** — now-relative
  time-window predicates; stay `secondaryFilter` residual permanently (DNF has no `now` concept for
  these).
- **Gym badges (`onlyGymBadges`/`onlyBadge`)** — badge gyms surface via a ReactMap-local badge join
  (`secondaryFilter` OR); Golbat can't know badge IDs. The gym backend **poisons to `[]`** when either
  is active. Fill later only via a badge-id clause if worth it.
- **Pokestop rocket-reward `a<pokemon>` keys** — `invasionMatchesFilters` matches UNCONFIRMED invasions
  by the grunt type's *possible* encounters (`info.encounters`), not the confirmed slot, so Golbat's
  confirmed-slot `incident_pokemon` would under-return. The pure backend has no reward→grunt map, so it
  **poisons to `[]`** on any `a` key. Fill later by threading the event invasion config into the backend
  (then emit the matching `incident_character` set) if the log shows it costs real over-fetch.
- **Combined `/api/fort/scan` + `fort_types` scope** — separate optimization, already deferred in the
  fort-scan spec §10; sequence after DNF.
- **`is_ar_scan_eligible:false` no-op** in Golbat — not a blocker (ReactMap only sends `true`).

## 9. Sequencing & packaging

1. **Golbat station `stationed_gmax` field** (§5) → PR #385. Unblocks the station gmax narrow.
2. **ReactMap backends** (§4) → PR #1228, one per type with review/test checkpoints: **gym** (simplest —
   team/raid), then **pokestop** (richest — quest/invasion/showcase), then **station** (battle + the two
   new gap fields). The observability log (§6) lands with each.

Each backend swap is independently reviewable and correctness-safe (secondaryFilter unchanged), so the
slices can merge incrementally.

## 10. Decisions

| # | Decision |
|---|---|
| A | **Scope = MVP + station gmax.** Ship the three backends on Golbat's existing DNF fields; fill only `stationed_gmax` in Golbat (a clean column test). Gym ex/in-battle, gender, invasion-confirmed, quest title/target, and station time-window gates (inactive/upcoming) stay residual. |
| B | **DNF is a superset narrow; `secondaryFilter` stays and finalizes.** Correctness can't regress from an imperfect translation; only under-return is a bug. |
| C | **Poisoning rule:** any active match-all toggle or gap category ⇒ backend returns `[]` (match-all). |
| D | **Three pure per-type backend modules** under `server/src/filters/fort/`, not one class. |
| E | **Observability log** shows examined → DNF-dropped → returned → residual-dropped → final, to expose the DNF gap per query. |
| F | Both changes extend the **open PRs** (#385 Golbat, #1228 ReactMap); no new PRs. |


## 11. Post-review dead-surface cleanup (2026-07-17)

The dual-PR review audited every `ApiFortDnfFilter` field against what consumers send. Removed
(structurally unusable by ReactMap, no other reader):

- `incident_style` (+ `FortLookupIncident.Style`) — ReactMap has no style concept.
- `incident_pokemon` — deliberately rejected consumer-side (confirmed-slot semantics under-return;
  `a` keys expand to `incident_character` instead). `FortLookupIncident.Slot1PokemonId/Form` stay —
  `/api/pokestop/available` reads them.
- `contest_total_entries` (+ `FortLookup.ContestTotalEntries` and the per-save showcase-rankings JSON
  parse) — no entry-count filter exists in the UI.

Kept though currently unsent — **future-sendable**: `power_up_level`, `team_id`, `available_slots`.
In `onlyAllGyms` mode `secondaryFilter` DOES narrow by team/slot/power-up keys, so the current
poison-to-match-all for that mode could be replaced by real clauses using these fields (follow-up
optimization). Also from review: `stationed_gmax:false` now symmetric (matches gmax-less stations);
top-level `filters` doc clarified (omitted/empty array = match-all; empty *inner* lists match nothing).
