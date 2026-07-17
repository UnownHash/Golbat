# Maintained Fort Availability Index — Design Spec

- **Date:** 2026-07-17
- **Status:** Approved design → planning
- **Repo:** Golbat only (`feat/fort-scan-map-data`, worktree `~/GolandProjects/Golbat-wt/pokestop-available-api`, PR #385)
- **Author:** James Berry (with Claude)
- **Extends:** the fort availability endpoints (`/api/{pokestop,gym,station}/available` + combined `/api/fort/available`).

## 1. Problem

The availability endpoints answer "which filter options exist on any resident fort right now?" — distinct active lures, showcases, invasions (pokestop), raids (gym), and battles (station). Today each build does a full `fortLookupCache.Range` over **every** fort. On a large instance (~1.7M forts) that is ~600 ms, fired ~once/minute (session-init + the `availableRefreshSeconds` TTL). The output is tiny (a few hundred distinct options), so we scan 1.7M entries to produce ~400 keys.

Quests already avoid the scan — they are served from a maintained running aggregate (`questConditionCount`, updated incrementally via `reconcileFortQuestConditions`). This spec applies the same "maintained, not scanned" principle to the remaining five aggregates, eliminating the per-request full-fort walk entirely.

## 2. Why a different mechanism than quests

Quests use **reconcile** (a per-fort contribution tracker + exact counts + decrement) because a quest can be **retracted** mid-life — cleared by `RemoveQuestsWithinGeofence`, replaced by a next-day scan, or swapped by an event — while its nominal `quest_expiry` (≈ daily) is still hours away. Reconcile handles retraction; a monotonic index cannot.

The five aggregates here are different: **an entry's disappearance is always signalled by its own expiry timestamp passing.** A lure is gone exactly when `LureExpireTimestamp` passes; a raid at `RaidEndTimestamp`; an incident at its `ExpireTimestamp`; a battle at `BattleEndTimestamp`. There is no geofence-clear or early-removal routine for any of them. That property lets us use a far simpler structure than reconcile:

> **Max-expiry index:** per distinct option value, store the latest expiry seen. On read, emit the value iff `maxExpiry > now`.

No per-fort tracker, no decrement, no eviction/clear handling, and self-healing in both directions (see §7).

## 3. Architecture

Five package-level maps, one per aggregate, each `*xsync.Map[<OptionKey>, int64]` mapping an option value to its **max seen expiry** (unix seconds):

| Aggregate | Map | Option key | Expiry field | Write hook (fn, lock domain) |
|---|---|---|---|---|
| Pokestop lure | `lureExpiry` | `int16` lure id | `LureExpireTimestamp` | `updatePokestopLookup` (pokestop) |
| Pokestop showcase | `showcaseExpiry` | `{PokemonId int16, Form int16, TypeId int8}` | `ShowcaseExpiry` | `updatePokestopLookup` (pokestop) |
| Pokestop invasion | `invasionExpiry` | `{Character int16, DisplayType int16, Confirmed bool, Slot1PokemonId int16, Slot1Form int16}` | incident `ExpireTimestamp` | `updatePokestopIncidentLookup` (incident) |
| Gym raid | `raidExpiry` | `{RaidLevel int8, PokemonId int16, Form int16}` | `RaidEndTimestamp` | `updateGymLookup` (gym) |
| Station battle | `battleExpiry` | `{BattleLevel int8, PokemonId int16, Form int16}` | `BattleEndTimestamp` | `updateStationLookupWithBattles` (station) |

The option keys are exactly the existing `Api*Available` structs **minus `Count`** (§8) — same fields consumers already receive.

**Concurrency is trivial:** each map has a single writer lock-domain (lures/showcases from the pokestop domain, invasions from the incident domain, raids from the gym domain, battles from the station domain — none shared), and every write is one atomic `Compute` that keeps the larger expiry. No cross-map invariant, so no lock-order reasoning. Reads (`Range`) run concurrently with writes as `xsync.Map` allows.

### 3.1 The observe primitive

```go
// observeExpiry records value as available until at least expiry. Ignores
// already-expired observations (never inserts a dead key). Keeping the LARGER
// expiry means a still-active fort refreshes the option's lifetime.
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
```

### 3.2 Write hooks

Each hook fires inside the existing update function, using the same `now` the function already computes, e.g. in `updateGymLookup`:

```go
if fl.RaidLevel > 0 {
	observeExpiry(raidExpiry, raidKey{fl.RaidLevel, fl.RaidPokemonId, fl.RaidPokemonForm}, fl.RaidEndTimestamp, now)
}
```

Invasions observe **per incident** inside `updatePokestopIncidentLookup` (that function already builds the `FortLookupIncident` with `ExpireTimestamp`). Battles observe **per battle** inside `updateStationLookupWithBattles`. Lures and showcases observe once per pokestop in `updatePokestopLookup`.

These hooks also fire during preload (`preload.go` calls the same update functions), so the maps warm up at startup — no cold-start gap beyond the preload window that already exists.

### 3.3 Read side (prune-on-read)

Each `GetAvailable<X>` ranges its map, emits live keys, and deletes the ones whose expiry has passed (so the map stays bounded by *currently-distinct* options, not all-time):

```go
func readRaidAvailable(now int64) []ApiGymRaidAvailable {
	out := []ApiGymRaidAvailable{}
	raidExpiry.Range(func(k raidKey, exp int64) bool {
		if exp > now {
			out = append(out, ApiGymRaidAvailable{RaidLevel: k.level, PokemonId: k.id, Form: k.form})
			return true
		}
		// Conditional prune: delete ONLY if still expired. A blind Delete could
		// race a concurrent observe that just refreshed this key to a future
		// expiry and wrongly drop the live option. Compute re-checks under lock.
		raidExpiry.Compute(k, func(cur int64, loaded bool) (int64, xsync.ComputeOp) {
			if loaded && cur <= now {
				return 0, xsync.DeleteOp
			}
			return cur, xsync.CancelOp
		})
		return true
	})
	return out
}
```

- `GetAvailableGyms` → `{raids: readRaidAvailable(now)}`
- `GetAvailableStations` → `{battles: readBattleAvailable(now)}`
- `GetAvailablePokestops` → `{quests: GetAvailableQuestConditions(), lures: …, showcases: …, invasions: …}` (quests unchanged)
- `GetAvailableForts` → assembles the three from the same reads — **no `fortLookupCache.Range` anywhere**

## 4. What is removed

- The `fortLookupCache.Range` full-fort walk in `GetAvailablePokestops`, `GetAvailableGyms`, `GetAvailableStations`, and `GetAvailableForts`.
- The `gymAvailAcc` / `pokestopAvailAcc` / `stationAvailAcc` accumulators (they existed only to tally the scan).
- The `verifyQuestAggregate` cross-check and its FortLookup quest-reward tally (it compared the reconcile map to the scan; with no scan there is nothing to compare — see §7 caveat).
- The `Count` field on every `Api*Available` struct (§8).

Quests (`questConditionCount` / `reconcileFortQuestConditions` / `GetAvailableQuestConditions`) are **unchanged**.

## 5. Data flow

```
fort save / incident save / preload
        │  (existing update fn, same `now`)
        ▼
  observeExpiry(map, optionKey, expiry, now)   ── atomic max, per-key
        │
        ▼
  <aggregate>Expiry map  (option → maxExpiry)
        ▲
        │  GetAvailable<X>(now): Range → emit exp>now, Delete exp<=now
        ▼
  /api/{type}/available  and  /api/fort/available   (no scan)
```

## 6. Concurrency & correctness notes

- Single-writer-domain per map ⇒ writes never race each other on a key beyond `Compute`'s own atomicity.
- Use the strong `Map.Range` (each key visited at most once), **not** `RangeRelaxed` (which may visit a key more than once → a duplicate availability entry). `xsync/v4 v4.5.0` documents `Range` as safe to modify during iteration, including deletion — so prune-on-read during `Range` is supported.
- Prune-on-read must delete **conditionally** (`Compute` with a delete-if-still-`<= now` predicate, §3.3), never a blind `Delete`: a blind delete could race a concurrent `observe` that just refreshed the key to a future expiry and wrongly drop a live option. The conditional re-check runs under the key's lock, so a refreshed key survives.
- No eviction hook needed: an evicted fort's option remains valid until its expiry passes (the option genuinely is still active until then), then prunes on the next read.

## 7. Accepted trade-offs (self-healing only, no sweep)

Per the design decision, there is **no periodic reconciliation sweep**. The consequences, all bounded/cosmetic:

1. **Over-report on mid-life replacement.** An egg key lingers after it hatches (until the raid's own `RaidEndTimestamp`); a grunt key after it's swapped (until the old incident's `ExpireTimestamp`). Capped at the entry's own expiry (~30–45 min), self-healing, and the option almost always still exists on another fort. Documented, not fought.
2. **No quest drift net.** `verifyQuestAggregate` is removed. It was Debug-level and its own comment calls divergences "benign, transient"; the reconcile map is otherwise rebuilt across the daily clear+rescan cycle. Acceptable.
3. **Rare stuck under-report.** If the *only* fort holding some option never re-saves, that option could be missing from the list. Extremely rare (would need a unique option on a fort that stops being scanned), cosmetic (a filter option absent), and self-heals on any re-save. No mitigation.
4. **Counts gone** (§8).

## 8. Counts

The `count` field on the availability responses is **unused by consumers** — ReactMap's `gymAvailableMapper` / `stationAvailableMapper` / `pokestopAvailableMapper` build a distinct key set and never read `count`. Max-expiry does not produce counts. So the `Count` field is **dropped** from `ApiPokestopLureAvailable`, `ApiPokestopShowcaseAvailable`, `ApiPokestopInvasionAvailable`, `ApiGymRaidAvailable`, and `ApiStationBattleAvailable`. This is a visible response-shape change but harmless (no consumer reads it). Pokemon availability is untouched — its count feeds rarity and stays a maintained count.

## 9. Testing

Node-golden equivalent is Go unit tests (`_test.go`), matching the existing availability test style:

- **`observeExpiry`**: keeps the larger expiry; ignores `expiry <= now`; distinct keys independent.
- **Each read fn**: emits keys with `exp > now`; prunes and omits `exp <= now`; empty map → empty slice (not nil, marshals `[]`).
- **Each write hook**: a synthetic fort/incident/battle save (drive the existing update fn or the hook directly) inserts the expected key with the expected expiry; a level-0/lure-0/expired entry inserts nothing.
- **`GetAvailableForts`**: assembles the three sections from seeded maps; parity with the per-type reads over the same maps.
- **No-scan assertion**: the read path performs no `fortLookupCache.Range` (structural — enforced by the accumulators being gone).

## 10. Non-goals

- Quests stay on reconcile (they require retraction; max-expiry cannot express it).
- No periodic reconciliation sweep (self-healing only — §7).
- Pokemon availability unchanged (count is load-bearing for rarity).
- ReactMap unchanged — response shapes are identical except the dropped `count`, which ReactMap never reads.
- Gym team/slot availability (already removed) and `station_active` (already shipped) are out of scope.

## 11. Task decomposition (for the plan)

Naturally splits into independent, individually-reviewable tasks:

1. `observeExpiry` primitive + the five map declarations + init.
2. Pokestop aggregates (lures, showcases, invasions): hooks in `updatePokestopLookup` + `updatePokestopIncidentLookup`; `GetAvailablePokestops` reads maps; drop `pokestopAvailAcc`, the scan, and `verifyQuestAggregate`.
3. Gym raids: hook in `updateGymLookup`; `GetAvailableGyms` reads map; drop `gymAvailAcc`.
4. Station battles: hook in `updateStationLookupWithBattles`; `GetAvailableStations` reads map; drop `stationAvailAcc`.
5. `GetAvailableForts` assembles from the three (no range); drop `Count` fields across the response structs.

Each task ends green (`go build -tags go_json ./...` + `go test ./decoder/`), with the availability endpoints returning the same key sets they do today (minus counts), served from the maps.
